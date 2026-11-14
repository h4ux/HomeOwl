package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Archived devices: the same phone or laptop shows up once per Wi-Fi address it ever used (private addresses
// change), and UniFi keeps them all. Archiving hides an old address from HomeOwl's lists (Clients, Who's home, the
// device picker) — nothing is deleted: UniFi, the logs and the history keep it, and it can be restored. An archived
// address that comes back online shows again. HomeOwl suggests what to archive: older copies of a device that is
// now on a newer address (same UniFi name, or the same specific host name), private addresses unused for months, and
// anything unseen for half a year.

const staleDays = 90     // a private address unused this long has most likely been replaced
const staleAnyDays = 180 // any device unseen this long is most likely gone

type archivedDevice struct {
	MAC    string `json:"mac"`
	Name   string `json:"name,omitempty"`
	At     int64  `json:"at"`
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type archiveStore struct {
	mu   sync.Mutex
	path string
}

func newArchiveStore(dir string) *archiveStore {
	return &archiveStore{path: filepath.Join(dir, "archived-devices.json")}
}

func (s *archiveStore) load() map[string]archivedDevice {
	m := map[string]archivedDevice{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *archiveStore) save(m map[string]archivedDevice) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

// MACs: the archived addresses, for the browser to leave out of its lists.
func (s *archiveStore) MACs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for mac := range s.load() {
		out = append(out, mac)
	}
	sort.Strings(out)
	return out
}

// names phones and laptops share out of the box: not enough to call two addresses the same device
var genericHost = regexp.MustCompile(`(?i)^(iphone|ipad|ipod|android(-[0-9a-f]+)?|galaxy[-\w]*|localhost|unknown|espressif|esp[-_]?\w*|macbook(-(pro|air))?|imac|windows|desktop-\w+|laptop-\w+|pixel[-\w]*|watch|apple-watch|homepod|appletv|apple-tv|chromecast|amazon-\w+|echo[-\w]*|wlan0|-)?$`)

// privateMAC: a locally administered (randomised) address.
func privateMAC(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	b, err := strconv.ParseUint(mac[:2], 16, 8)
	return err == nil && b&2 != 0
}

type archiveCand struct {
	MAC        string `json:"mac"`
	Name       string `json:"name"`
	Vendor     string `json:"vendor,omitempty"`
	LastSeen   int64  `json:"last_seen,omitempty"`
	Reason     string `json:"reason"`
	Kind       string `json:"kind"`                  // duplicate | stale
	KeptMAC    string `json:"kept_mac,omitempty"`    // the newer address of the same device
	KeptOnline bool   `json:"kept_online,omitempty"` //
	PersonID   string `json:"person_id,omitempty"`   // the old address belongs to this person, the newer one to nobody yet
	PersonName string `json:"person_name,omitempty"` //
}

type knownDev struct {
	mac, name, host, vendor string
	last                    int64
	online, fixed, blocked  bool
}

// archiveSuggestions works out what to suggest from UniFi's known and online clients.
func (a *App) archiveSuggestions(known, online []map[string]any, archived map[string]archivedDevice, owner map[string][2]string, now time.Time) []archiveCand {
	on := map[string]bool{}
	devs := map[string]*knownDev{}
	add := func(c map[string]any, live bool) {
		mac := strings.ToLower(str(c, "mac"))
		if mac == "" {
			return
		}
		d := devs[mac]
		if d == nil {
			d = &knownDev{mac: mac}
			devs[mac] = d
		}
		d.name = orStr(d.name, str(c, "name"))
		d.host = orStr(d.host, str(c, "hostname"))
		d.vendor = orStr(d.vendor, str(c, "oui"))
		if v, ok := c["last_seen"].(float64); ok && int64(v) > d.last {
			d.last = int64(v)
		}
		if b, _ := c["use_fixedip"].(bool); b {
			d.fixed = true
		}
		if b, _ := c["blocked"].(bool); b {
			d.blocked = true
		}
		if live {
			d.online, on[mac] = true, true
			d.last = now.Unix()
		}
	}
	for _, c := range known {
		add(c, false)
	}
	for _, c := range online {
		add(c, true)
	}
	key := func(d *knownDev) string {
		if d.name != "" {
			return "n:" + strings.ToLower(strings.TrimSpace(d.name))
		}
		if h := strings.TrimSpace(d.host); h != "" && !genericHost.MatchString(h) {
			return "h:" + strings.ToLower(h)
		}
		return ""
	}
	groups := map[string][]*knownDev{}
	for _, d := range devs {
		if _, gone := archived[d.mac]; gone {
			continue
		}
		if k := key(d); k != "" {
			groups[k] = append(groups[k], d)
		}
	}
	var out []archiveCand
	seen := map[string]bool{}
	label := func(d *knownDev) string { return orStr(d.name, d.host, d.vendor, d.mac) }
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(i, j int) bool {
			if g[i].online != g[j].online {
				return g[i].online
			}
			return g[i].last > g[j].last
		})
		kept := g[0]
		for _, d := range g[1:] {
			// still around, or something set on purpose: leave it
			if d.online || d.fixed || d.blocked || now.Unix()-d.last < 86400 {
				continue
			}
			// a host name match between different makers isn't the same device
			if d.name == "" && d.vendor != "" && kept.vendor != "" && d.vendor != kept.vendor && !privateMAC(d.mac) && !privateMAC(kept.mac) {
				continue
			}
			c := archiveCand{MAC: d.mac, Name: label(d), Vendor: d.vendor, LastSeen: d.last, Kind: "duplicate", KeptMAC: kept.mac, KeptOnline: kept.online,
				Reason: "An older address of " + label(kept) + ", which is now on " + kept.mac + "."}
			if o, ok := owner[d.mac]; ok {
				if _, keptOwned := owner[kept.mac]; !keptOwned {
					c.PersonID, c.PersonName = o[0], o[1]
				}
			}
			out = append(out, c)
			seen[d.mac] = true
		}
	}
	for _, d := range devs {
		if _, gone := archived[d.mac]; gone || seen[d.mac] || d.online || d.fixed || d.blocked || d.last == 0 {
			continue
		}
		days := (now.Unix() - d.last) / 86400
		var why string
		switch {
		case privateMAC(d.mac) && days >= staleDays:
			why = "A private (changing) address not seen for " + strconv.FormatInt(days, 10) + " days: the device has most likely moved on to another one."
		case days >= staleAnyDays:
			why = "Not seen for " + strconv.FormatInt(days, 10) + " days: most likely gone."
		default:
			continue
		}
		c := archiveCand{MAC: d.mac, Name: label(d), Vendor: d.vendor, LastSeen: d.last, Kind: "stale", Reason: why}
		if o, owned := owner[d.mac]; owned {
			c.Reason += " It stays linked to " + o[1] + "."
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "duplicate"
		}
		return out[i].LastSeen < out[j].LastSeen
	})
	return out
}

// owners: MAC → [person id, name] for people's UniFi devices.
func (a *App) macOwners() map[string][2]string {
	m := map[string][2]string{}
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			if d.Kind == "mac" {
				m[strings.ToLower(d.Key)] = [2]string{p.ID, p.Name}
			}
		}
	}
	return m
}

func (a *App) archiveView() (map[string]any, error) {
	a.archive.mu.Lock()
	archived := a.archive.load()
	a.archive.mu.Unlock()
	list := make([]archivedDevice, 0, len(archived))
	for _, d := range archived {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].At > list[j].At })
	res := map[string]any{"archived": list, "suggestions": []archiveCand{}}
	if !a.UniFi.Connected() {
		return res, nil
	}
	st1, d1 := a.UniFi.Get(a.UniFi.SitePath("rest/user", false))
	st2, d2 := a.UniFi.Get(a.UniFi.SitePath("stat/sta", false))
	if st1 != 200 || st2 != 200 {
		res["error"] = "UniFi didn't return its client list"
		return res, nil
	}
	if s := a.archiveSuggestions(unifi.List(d1), unifi.List(d2), archived, a.macOwners(), time.Now()); s != nil {
		res["suggestions"] = s
	}
	return res, nil
}

// archiveAPI: "add" {macs, link} archives (link: the newer address joins the old one's person), "restore" {macs}.
func (a *App) archiveAPI(op string, body map[string]any, by string) (map[string]any, error) {
	var macs []string
	if l, ok := body["macs"].([]any); ok {
		for _, v := range l {
			if m, _ := v.(string); macRE.MatchString(strings.ToLower(m)) {
				macs = append(macs, strings.ToLower(m))
			}
		}
	}
	if len(macs) == 0 {
		return nil, errors.New("pick a device")
	}
	switch op {
	case "add":
		var cands map[string]archiveCand
		if b(body, "link") {
			v, _ := a.archiveView()
			cands = map[string]archiveCand{}
			if l, ok := v["suggestions"].([]archiveCand); ok {
				for _, c := range l {
					cands[c.MAC] = c
				}
			}
		}
		names := map[string]string{}
		if l, ok := body["names"].(map[string]any); ok {
			for k, v := range l {
				if s, _ := v.(string); s != "" && len(s) <= 80 {
					names[strings.ToLower(k)] = s
				}
			}
		}
		a.archive.mu.Lock()
		m := a.archive.load()
		for _, mac := range macs {
			m[mac] = archivedDevice{MAC: mac, Name: names[mac], At: time.Now().Unix(), By: by, Reason: s(body, "reason")}
		}
		err := a.archive.save(m)
		a.archive.mu.Unlock()
		if err != nil {
			return nil, err
		}
		for _, mac := range macs {
			if c, ok := cands[mac]; ok && c.PersonID != "" && c.KeptMAC != "" {
				_ = a.An.AssignDevice(c.PersonID, "mac", c.KeptMAC)
			}
		}
	case "restore":
		a.archive.mu.Lock()
		m := a.archive.load()
		for _, mac := range macs {
			delete(m, mac)
		}
		err := a.archive.save(m)
		a.archive.mu.Unlock()
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("not found")
	}
	return a.archiveView()
}

// archiveLoop: every few hours, an alert when there's a pile of old addresses to archive — at most once a week,
// and again sooner only if the pile has doubled.
func (a *App) archiveLoop(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if a.UniFi.Connected() {
			a.archiveNudge(time.Now())
		}
	}
}

func (a *App) archiveNudge(now time.Time) {
	v, _ := a.archiveView()
	l, _ := v["suggestions"].([]archiveCand)
	if len(l) < 3 {
		return
	}
	path := filepath.Join(filepath.Dir(a.archive.path), "archive-nudge.json")
	var last struct {
		At int64 `json:"at"`
		N  int   `json:"n"`
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &last)
	}
	if now.Unix()-last.At < 7*86400 && len(l) < 2*last.N {
		return
	}
	dup := 0
	for _, c := range l {
		if c.Kind == "duplicate" {
			dup++
		}
	}
	a.Alerts.Add(alerts.Alert{Kind: "archive", Level: "info", Title: fmt.Sprintf("%d old device addresses to archive", len(l)),
		Body: fmt.Sprintf("%d are older copies of devices now on a newer address; the rest haven't been seen for months. Network › Clients › Review hides them from HomeOwl's lists (nothing is deleted).", dup),
		Data: map[string]any{"count": len(l)}})
	last.At, last.N = now.Unix(), len(l)
	b, _ := json.Marshal(last)
	_ = os.WriteFile(path, b, 0o600)
}
