// Package history keeps snapshots of the UniFi configuration (whenever it changes) and explains what changed
// between two of them. Only configuration is kept — no counters, clients' traffic or events — and secrets stay
// redacted (length plus a short fingerprint, so a changed password still shows up as a change).
package history

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Section is one kind of configuration object in the collected bundle.
type Section struct {
	Key, Title, Source, Name string
	Keep                     []string // when set, only these fields are kept (objects that mix config and live data)
}

var Sections = []Section{
	{Key: "networks", Title: "Networks & VLANs", Source: "legacy", Name: "networks"},
	{Key: "wlans", Title: "Wi-Fi", Source: "legacy", Name: "wlans"},
	{Key: "wlan_groups", Title: "Wi-Fi groups", Source: "legacy", Name: "wlan_groups"},
	{Key: "firewall_rules", Title: "Firewall rules", Source: "legacy", Name: "firewall_rules"},
	{Key: "firewall_groups", Title: "Firewall groups", Source: "legacy", Name: "firewall_groups"},
	{Key: "firewall_policies", Title: "Firewall policies", Source: "v2", Name: "firewall_policies"},
	{Key: "firewall_zones", Title: "Firewall zones", Source: "v2", Name: "firewall_zones"},
	{Key: "traffic_rules", Title: "Traffic rules", Source: "v2", Name: "traffic_rules"},
	{Key: "traffic_routes", Title: "Traffic routes", Source: "v2", Name: "traffic_routes"},
	{Key: "nat_rules", Title: "NAT rules", Source: "v2", Name: "nat_rules"},
	{Key: "port_forwards", Title: "Port forwards", Source: "legacy", Name: "port_forwards"},
	{Key: "routes", Title: "Static routes", Source: "legacy", Name: "routes"},
	{Key: "port_profiles", Title: "Switch port profiles", Source: "legacy", Name: "port_profiles"},
	{Key: "dynamic_dns", Title: "Dynamic DNS", Source: "legacy", Name: "dynamic_dns"},
	{Key: "radius_profiles", Title: "RADIUS profiles", Source: "legacy", Name: "radius_profiles"},
	{Key: "settings", Title: "Site settings", Source: "legacy", Name: "settings"},
	{Key: "clients", Title: "Named clients & fixed IPs", Source: "legacy", Name: "known_clients",
		Keep: []string{"mac", "name", "note", "noted", "use_fixedip", "fixed_ip", "network_id", "blocked", "usergroup_id",
			"virtual_network_override_enabled", "virtual_network_override_id", "local_dns_record_enabled", "local_dns_record"}},
	{Key: "devices", Title: "UniFi devices", Source: "legacy", Name: "devices",
		Keep: []string{"mac", "name", "model", "version", "port_overrides", "radio_table", "led_override", "led_override_color",
			"led_override_color_brightness", "config_network", "mgmt_network_id", "outlet_overrides", "lcm_brightness_override",
			"stp_priority", "jumboframe_enabled", "flowctrl_enabled", "snmp_contact", "snmp_location", "ethernet_overrides"}},
}

// fields that change on their own and say nothing about the configuration
var volatile = regexp.MustCompile(`^(last_seen|first_seen|uptime|_uptime|time|timestamp|datetime|last_connection|stat|stats|` +
	`rx_bytes|tx_bytes|rx_packets|tx_packets|num_sta|satisfaction|state|status|status_message|hostname|ip|` +
	`cu_total|cu_self_rx|cu_self_tx|tx_retries|bss_.*|.*-r)$`)

// radio and port lists are matched by these keys, not by position
var listKeys = []string{"port_idx", "radio", "name", "_id", "id", "key"}

type Snapshot struct {
	Time     int64                                `json:"time"`
	Host     string                               `json:"host"`
	Site     string                               `json:"site"`
	Sections map[string]map[string]map[string]any `json:"sections"` // section → object id → object
}

type Entry struct {
	File    string   `json:"file"`
	Time    int64    `json:"time"`
	Hash    string   `json:"hash"`
	Changes int      `json:"changes"` // vs the snapshot before
	Areas   []string `json:"areas,omitempty"`
}

type Store struct {
	mu      sync.Mutex
	dir     string
	Checked int64 // last time a collection was compared
	max     int
}

func New(dir string) *Store { return &Store{dir: dir, max: 1000} }

func label(o map[string]any) string {
	for _, k := range []string{"name", "key", "description", "mac", "_id", "id"} {
		if s, ok := o[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func objID(o map[string]any) string {
	for _, k := range []string{"_id", "id", "key", "mac"} {
		if s, ok := o[k].(string); ok && s != "" {
			return s
		}
	}
	b, _ := json.Marshal(o)
	h := sha256.Sum256(b)
	return "h" + hex.EncodeToString(h[:6])
}

func clean(v any, keep []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if keep != nil {
				found := false
				for _, kk := range keep {
					found = found || kk == k
				}
				if !found {
					continue
				}
			} else if volatile.MatchString(k) {
				continue
			}
			out[k] = clean(x, nil)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = clean(x, nil)
		}
		return out
	}
	return v
}

// Extract takes the configuration out of a collected bundle. Sections that weren't collected are left out
// (rather than looking deleted).
func Extract(bundle map[string]any) Snapshot {
	s := Snapshot{Time: time.Now().Unix(), Sections: map[string]map[string]map[string]any{}}
	if meta, ok := bundle["meta"].(map[string]any); ok {
		s.Host, _ = meta["host"].(string)
		if site, ok := meta["site"].(map[string]any); ok {
			s.Site, _ = site["name"].(string)
		}
	}
	for _, sec := range Sections {
		src, _ := bundle[sec.Source].(map[string]any)
		raw, ok := src[sec.Name].([]any)
		if !ok {
			if m, isMap := src[sec.Name].(map[string]any); isMap { // some v2 answers wrap the list
				for _, v := range m {
					if l, isList := v.([]any); isList {
						raw, ok = l, true
					}
				}
			}
		}
		if !ok {
			continue
		}
		objs := map[string]map[string]any{}
		for _, x := range raw {
			o, _ := x.(map[string]any)
			if o == nil {
				continue
			}
			c, _ := clean(o, sec.Keep).(map[string]any)
			if sec.Key == "clients" && c["name"] == nil && c["note"] == nil && c["use_fixedip"] != true && c["blocked"] != true {
				continue // only clients someone configured
			}
			objs[objID(o)] = c
		}
		s.Sections[sec.Key] = objs
	}
	return s
}

func (s Snapshot) hash() string {
	b, _ := json.Marshal(s.Sections) // map keys are sorted, so this is canonical
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (st *Store) index() []Entry {
	var l []Entry
	if b, err := os.ReadFile(filepath.Join(st.dir, "index.json")); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (st *Store) saveIndex(l []Entry) error {
	b, _ := json.MarshalIndent(l, "", " ")
	return config.WriteFileAtomic(filepath.Join(st.dir, "index.json"), b, 0o600)
}

func (st *Store) List() []Entry {
	st.mu.Lock()
	defer st.mu.Unlock()
	l := st.index()
	if l == nil {
		l = []Entry{}
	}
	return l
}

var fileRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9]{1,4})?\.json\.gz$`)

func (st *Store) Load(file string) (Snapshot, error) {
	var s Snapshot
	if !fileRE.MatchString(file) {
		return s, errors.New("bad snapshot name")
	}
	f, err := os.Open(filepath.Join(st.dir, file))
	if err != nil {
		return s, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return s, err
	}
	err = json.NewDecoder(gz).Decode(&s)
	return s, err
}

// Record stores the bundle's configuration if it differs from the last snapshot. Sections missing from this
// collection are carried over from the last snapshot. It returns the new entry, or nil when nothing changed.
func (st *Store) Record(bundle map[string]any) (*Entry, error) {
	snap := Extract(bundle)
	if len(snap.Sections) == 0 {
		return nil, nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Checked = snap.Time
	l := st.index()
	var prev *Snapshot
	if len(l) > 0 {
		if p, err := st.Load(l[len(l)-1].File); err == nil {
			prev = &p
			for k, v := range p.Sections {
				if _, ok := snap.Sections[k]; !ok {
					snap.Sections[k] = v
				}
			}
		}
	}
	h := snap.hash()
	if len(l) > 0 && l[len(l)-1].Hash == h {
		return nil, nil
	}
	base := time.Unix(snap.Time, 0).UTC().Format("20060102-150405")
	e := Entry{File: base + ".json.gz", Time: snap.Time, Hash: h}
	for n := 2; len(l) > 0 && strings.HasPrefix(l[len(l)-1].File, base) && n < 1000; n++ {
		if _, err := os.Stat(filepath.Join(st.dir, e.File)); err != nil {
			break
		}
		e.File = fmt.Sprintf("%s-%d.json.gz", base, n)
	}
	if prev != nil {
		ch := Diff(*prev, snap)
		e.Changes = len(ch)
		seen := map[string]bool{}
		for _, c := range ch {
			if !seen[c.Area] {
				seen[c.Area] = true
				e.Areas = append(e.Areas, c.Area)
			}
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(snap); err != nil {
		return nil, err
	}
	_ = gz.Close()
	if err := config.WriteFileAtomic(filepath.Join(st.dir, e.File), buf.Bytes(), 0o600); err != nil {
		return nil, err
	}
	l = append(l, e)
	for len(l) > st.max {
		_ = os.Remove(filepath.Join(st.dir, l[0].File))
		l = l[1:]
	}
	return &e, st.saveIndex(l)
}

// ------------------------------------------------------------------ diff

type Field struct {
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type Change struct {
	Area   string  `json:"area"` // section title
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"` // added | removed | changed
	Fields []Field `json:"fields,omitempty"`
}

// flatten turns an object into path → value, matching list items by a stable key where there is one.
func flatten(prefix string, v any, out map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		if r, ok := t["__redacted__"]; ok && r == true {
			out[prefix] = fmt.Sprintf("(secret, %v characters%s)", t["length"], fp(t))
			return
		}
		for k, x := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flatten(p, x, out)
		}
	case []any:
		allScalar := true
		for _, x := range t {
			if _, isMap := x.(map[string]any); isMap {
				allScalar = false
			}
		}
		if allScalar {
			b, _ := json.Marshal(t)
			out[prefix] = string(b)
			return
		}
		for i, x := range t {
			key := fmt.Sprint(i)
			if m, ok := x.(map[string]any); ok {
				for _, k := range listKeys {
					if v, ok := m[k]; ok && v != nil && v != "" {
						key = fmt.Sprintf("%s=%v", k, v)
						break
					}
				}
			}
			flatten(prefix+"["+key+"]", x, out)
		}
	default:
		out[prefix] = v
	}
}

func fp(t map[string]any) string {
	if f, ok := t["fp"].(string); ok && f != "" {
		return ", #" + f
	}
	return ""
}

func eq(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// Diff lists what changed from a to b. Sections present in only one of them are skipped.
func Diff(a, b Snapshot) []Change {
	var out []Change
	for _, sec := range Sections {
		sa, okA := a.Sections[sec.Key]
		sb, okB := b.Sections[sec.Key]
		if !okA || !okB {
			continue
		}
		ids := map[string]bool{}
		for id := range sa {
			ids[id] = true
		}
		for id := range sb {
			ids[id] = true
		}
		keys := make([]string, 0, len(ids))
		for id := range ids {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			oa, inA := sa[id]
			ob, inB := sb[id]
			switch {
			case !inA:
				out = append(out, Change{Area: sec.Title, ID: id, Name: label(ob), Kind: "added"})
			case !inB:
				out = append(out, Change{Area: sec.Title, ID: id, Name: label(oa), Kind: "removed"})
			default:
				fa, fb := map[string]any{}, map[string]any{}
				flatten("", oa, fa)
				flatten("", ob, fb)
				var fields []Field
				for p := range union(fa, fb) {
					if !eq(fa[p], fb[p]) {
						fields = append(fields, Field{Path: p, Before: fa[p], After: fb[p]})
					}
				}
				if len(fields) > 0 {
					sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
					name := label(ob)
					if sec.Key == "settings" {
						name = strings.ReplaceAll(name, "_", " ")
					}
					out = append(out, Change{Area: sec.Title, ID: id, Name: name, Kind: "changed", Fields: fields})
				}
			}
		}
	}
	return out
}

func union(a, b map[string]any) map[string]bool {
	u := map[string]bool{}
	for k := range a {
		u[k] = true
	}
	for k := range b {
		u[k] = true
	}
	return u
}

// At returns the newest snapshot entry at or before t (or the oldest one).
func At(l []Entry, t int64) (Entry, bool) {
	if len(l) == 0 {
		return Entry{}, false
	}
	best := l[0]
	for _, e := range l {
		if e.Time <= t {
			best = e
		}
	}
	return best, true
}
