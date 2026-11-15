package unifi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Changes are only possible through this allow-list. Each change is previewed first, logged with who made
// it and the previous values, and can be undone.
var restFields = map[string][]string{
	"wlanconf": {"enabled", "pmf_mode", "wpa3_support", "wpa3_transition", "hide_ssid", "l2_isolation", "fast_roaming_enabled",
		"bss_transition", "uapsd_enabled", "mac_filter_enabled", "minrate_setting_preference", "minrate_ng_enabled",
		"minrate_ng_data_rate_kbps", "minrate_na_enabled", "minrate_na_data_rate_kbps", "is_guest"},
	"portforward": {"enabled", "log"},
	"networkconf": {"network_isolation_enabled", "internet_access_enabled", "mdns_enabled", "igmp_snooping", "dhcpd_dns_enabled"},
	"user":        {"name", "note", "noted", "use_fixedip", "fixed_ip", "network_id", "virtual_network_override_enabled", "virtual_network_override_id"},
}
var settingFields = map[string][]string{
	"usg": {"upnp_enabled", "geo_ip_filtering_enabled"}, "ips": {"ips_mode"},
	"mgmt": {"x_ssh_enabled", "x_ssh_auth_password_enabled", "auto_upgrade", "led_enabled"}, "dpi": {"enabled"}, "doh": {"state"},
	// the guest portal, pointed at HomeOwl for the setup Wi-Fi (portal.go in the server)
	"guest_access": {"portal_enabled", "auth", "custom_ip", "redirect_enabled"},
}
var radioFields = []string{"channel", "ht", "tx_power_mode", "tx_power", "min_rssi_enabled", "min_rssi"}
var devCmds = []string{"restart", "set-locate", "unset-locate", "upgrade", "force-provision", "adopt", "power-cycle", "speedtest"}
var staCmds = []string{"block-sta", "unblock-sta", "kick-sta"}
var undoCmd = map[string]string{"block-sta": "unblock-sta", "unblock-sta": "block-sta", "set-locate": "unset-locate"}
var macRE = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type ActionError struct{ Msg string }

func (e ActionError) Error() string { return e.Msg }
func aerr(f string, a ...any) error { return ActionError{fmt.Sprintf(f, a...)} }

// Action is what the UI / MCP asks for.
type Action map[string]any

func (a Action) s(k string) string {
	v, _ := a[k].(string)
	if v == "" && a[k] != nil {
		if f, ok := a[k].(float64); ok {
			return fmt.Sprint(int64(f))
		}
	}
	return v
}

func (a Action) fields() map[string]any {
	f, _ := a["fields"].(map[string]any)
	return f
}

type req struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body"`
}

type plan struct {
	req
	Before        map[string]any
	After         map[string]any
	Undo          *req
	DeleteCreated bool
	Target        string
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func checkFields(f map[string]any, allowed []string) error {
	if len(f) == 0 {
		return aerr("no fields given")
	}
	var bad []string
	for k := range f {
		if !contains(allowed, k) {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return aerr("field(s) not allowed: %s", strings.Join(bad, ", "))
	}
	return nil
}

func first(d any) map[string]any {
	l := List(d)
	if len(l) == 0 {
		return nil
	}
	return l[0]
}

func pick(m map[string]any, f map[string]any) map[string]any {
	out := map[string]any{}
	for k := range f {
		out[k] = m[k]
	}
	return out
}

func (c *Client) plan(a Action) (*plan, error) {
	switch a.s("kind") {
	case "rest":
		res, id, f := a.s("resource"), a.s("id"), a.fields()
		if _, ok := restFields[res]; !ok || !idRE.MatchString(id) {
			return nil, aerr("unsupported resource or bad id")
		}
		if err := checkFields(f, restFields[res]); err != nil {
			return nil, err
		}
		path := c.SitePath("rest/"+res+"/"+id, false)
		st, d := c.Get(path)
		cur := first(d)
		if st != 200 || cur == nil {
			return nil, aerr("could not read current %s %s: %s", res, id, ErrText(d))
		}
		before := pick(cur, f)
		return &plan{req: req{"PUT", path, f}, Before: before, After: f, Undo: &req{"PUT", path, before}, Target: label(cur, id)}, nil
	case "setting":
		key, f := a.s("key"), a.fields()
		if _, ok := settingFields[key]; !ok {
			return nil, aerr("unsupported setting")
		}
		if err := checkFields(f, settingFields[key]); err != nil {
			return nil, err
		}
		_, d := c.Get(c.SitePath("rest/setting", false))
		var cur map[string]any
		for _, x := range List(d) {
			if x["key"] == key {
				cur = x
			}
		}
		if cur == nil {
			return nil, aerr("setting '%s' not found on this console", key)
		}
		path := c.SitePath(fmt.Sprintf("rest/setting/%s/%v", key, cur["_id"]), false)
		before := pick(cur, f)
		return &plan{req: req{"PUT", path, f}, Before: before, After: f, Undo: &req{"PUT", path, before}, Target: "setting: " + key}, nil
	case "radio":
		did, radio, f := a.s("device_id"), a.s("radio"), a.fields()
		if !idRE.MatchString(did) || radio == "" {
			return nil, aerr("bad device/radio")
		}
		if err := checkFields(f, radioFields); err != nil {
			return nil, err
		}
		path := c.SitePath("rest/device/"+did, false)
		_, d := c.Get(path)
		cur := first(d)
		rt, ok := cur["radio_table"].([]any)
		if cur == nil || !ok {
			return nil, aerr("could not read the AP's radio settings")
		}
		oldB, _ := json.Marshal(rt)
		var newRT []map[string]any
		_ = json.Unmarshal(oldB, &newRT)
		var oldRT []any
		_ = json.Unmarshal(oldB, &oldRT)
		var entry map[string]any
		for _, r := range newRT {
			if r["name"] == radio || r["radio"] == radio {
				entry = r
			}
		}
		if entry == nil {
			return nil, aerr("radio %s not found", radio)
		}
		before, after := map[string]any{}, map[string]any{}
		for k, v := range f {
			if _, isStr := entry[k].(string); isStr {
				if _, vs := v.(string); !vs {
					v = fmt.Sprint(v)
				}
			}
			before[fmt.Sprintf("%v.%s", entry["radio"], k)] = entry[k]
			after[fmt.Sprintf("%v.%s", entry["radio"], k)] = v
			entry[k] = v
		}
		return &plan{req: req{"PUT", path, map[string]any{"radio_table": newRT}}, Before: before, After: after,
			Undo: &req{"PUT", path, map[string]any{"radio_table": oldRT}}, Target: label(cur, did)}, nil
	case "devcmd":
		cmd, mac := a.s("cmd"), strings.ToLower(a.s("mac"))
		if !contains(devCmds, cmd) {
			return nil, aerr("unsupported device command")
		}
		body := map[string]any{"cmd": cmd}
		if cmd != "speedtest" {
			if !macRE.MatchString(mac) {
				return nil, aerr("bad MAC")
			}
			body["mac"] = mac
		}
		if cmd == "power-cycle" {
			pi, ok := a["port_idx"].(float64)
			if !ok {
				return nil, aerr("port_idx required")
			}
			body["port_idx"] = int(pi)
		}
		p := &plan{req: req{"POST", c.SitePath("cmd/devmgr", false), body}, Before: map[string]any{}, After: map[string]any{"command": cmd}, Target: orStr(a.s("label"), mac, "gateway")}
		if u, ok := undoCmd[cmd]; ok {
			ub := map[string]any{}
			for k, v := range body {
				ub[k] = v
			}
			ub["cmd"] = u
			p.Undo = &req{"POST", p.Path, ub}
		}
		return p, nil
	case "guestauth": // HomeOwl's guest portal lets a device on the guest Wi-Fi online (UniFi's "authorize-guest")
		mac := strings.ToLower(a.s("mac"))
		minutes, err := strconv.Atoi(orStr(a.s("minutes"), "480"))
		if !macRE.MatchString(mac) || err != nil || minutes < 1 || minutes > 525600 {
			return nil, aerr("bad guest")
		}
		path := c.SitePath("cmd/stamgr", false)
		return &plan{req: req{"POST", path, map[string]any{"cmd": "authorize-guest", "mac": mac, "minutes": minutes}}, Before: map[string]any{},
			After: map[string]any{"online for": fmt.Sprintf("%d minutes", minutes)}, Undo: &req{"POST", path, map[string]any{"cmd": "unauthorize-guest", "mac": mac}},
			Target: orStr(a.s("label"), mac)}, nil
	case "stacmd":
		cmd, mac := a.s("cmd"), strings.ToLower(a.s("mac"))
		if !contains(staCmds, cmd) || !macRE.MatchString(mac) {
			return nil, aerr("unsupported client command or bad MAC")
		}
		body := map[string]any{"cmd": cmd, "mac": mac}
		p := &plan{req: req{"POST", c.SitePath("cmd/stamgr", false), body}, Before: map[string]any{}, After: map[string]any{"command": cmd}, Target: orStr(a.s("label"), mac)}
		if u, ok := undoCmd[cmd]; ok {
			p.Undo = &req{"POST", p.Path, map[string]any{"cmd": u, "mac": mac}}
		}
		return p, nil
	case "guestpass":
		id, pw := a.s("wlan_id"), a.s("password")
		if !idRE.MatchString(id) {
			return nil, aerr("bad Wi-Fi id")
		}
		if pw == "" {
			pw = NewGuestPassword()
		}
		if err := checkPassphrase(pw); err != nil {
			return nil, err
		}
		path := c.SitePath("rest/wlanconf/"+id, false)
		st, d := c.Get(path)
		cur := first(d)
		if st != 200 || cur == nil {
			return nil, aerr("could not read Wi-Fi %s: %s", id, ErrText(d))
		}
		if !c.IsGuestWLAN(cur) {
			return nil, aerr("only guest Wi-Fi passwords can be changed here")
		}
		if sec, _ := cur["security"].(string); sec != "wpapsk" {
			return nil, aerr("this Wi-Fi has no shared password (security: %v)", cur["security"])
		}
		old, _ := cur["x_passphrase"].(string)
		if old == "" {
			return nil, aerr("could not read the current password")
		}
		// the passwords themselves stay out of the preview and the change log
		return &plan{req: req{"PUT", path, map[string]any{"x_passphrase": pw}},
			Before: map[string]any{"password": fmt.Sprintf("(hidden, %d characters)", len(old))},
			After:  map[string]any{"password": fmt.Sprintf("(new, %d characters)", len(pw))},
			Undo:   &req{"PUT", path, map[string]any{"x_passphrase": old}}, Target: "Wi-Fi " + label(cur, id)}, nil
	case "route":
		_, ipn, err := net.ParseCIDR(a.s("network"))
		hop := net.ParseIP(a.s("nexthop"))
		if err != nil || hop == nil {
			return nil, aerr("bad network or next hop")
		}
		name := regexp.MustCompile(`[^\w .()-]`).ReplaceAllString(orStr(a.s("name"), "Route to "+ipn.String()), "")
		if len(name) > 48 {
			name = name[:48]
		}
		body := map[string]any{"name": name, "enabled": true, "type": "static-route", "static-route_type": "nexthop-route",
			"static-route_network": ipn.String(), "static-route_nexthop": hop.String(), "static-route_distance": 1}
		return &plan{req: req{"POST", c.SitePath("rest/routing", false), body}, Before: map[string]any{"route": nil},
			After: map[string]any{"route": ipn.String() + " via " + hop.String()}, DeleteCreated: true, Target: name}, nil
	case "setupnet": // HomeOwl's setup Wi-Fi: its network, then the Wi-Fi (setupwifi.go)
		return c.planSetupNet(a)
	case "setupwlan":
		return c.planSetupWLAN(a)
	case "dnsrecord": // a local DNS record on the console (UniFi Network 8.2+): name → a home address
		name, ip := strings.TrimSuffix(strings.ToLower(a.s("name")), "."), net.ParseIP(a.s("ip")).To4()
		if !hostRE.MatchString(name) || ip == nil || !ip.IsPrivate() {
			return nil, aerr("a DNS record needs a name like wifi.home.arpa and a home-network IPv4 address")
		}
		path := c.SitePath("static-dns", true)
		st, d := c.Get(path)
		if st != 200 {
			return nil, aerr("this console has no local DNS records (they need UniFi Network 8.2 or newer): %s", ErrText(d))
		}
		for _, r := range List(d) {
			if !strings.EqualFold(fmt.Sprint(r["key"]), name) {
				continue
			}
			id, _ := r["_id"].(string)
			if !idRE.MatchString(id) {
				return nil, aerr("unexpected DNS record on the console")
			}
			if r["value"] == ip.String() && r["record_type"] == "A" && r["enabled"] != false {
				return nil, aerr("%s already points to %s", name, ip)
			}
			upd := map[string]any{}
			for k, v := range r {
				upd[k] = v
			}
			upd["value"], upd["record_type"], upd["enabled"] = ip.String(), "A", true
			return &plan{req: req{"PUT", path + "/" + id, upd}, Before: map[string]any{"record": fmt.Sprintf("%s → %v", name, r["value"])},
				After: map[string]any{"record": name + " → " + ip.String()}, Undo: &req{"PUT", path + "/" + id, r}, Target: name}, nil
		}
		body := map[string]any{"key": name, "value": ip.String(), "record_type": "A", "enabled": true, "ttl": 0, "port": 0, "priority": 0, "weight": 0}
		return &plan{req: req{"POST", path, body}, Before: map[string]any{"record": nil}, After: map[string]any{"record": name + " → " + ip.String()},
			DeleteCreated: true, Target: name}, nil
	case "fwpolicy": // zone-based firewall (UniFi Network 9+): let some networks reach one device (issues.go)
		return c.planFwPolicy(a)
	case "fwportgroup": // classic firewall: the ports such a rule allows
		return c.planFwPortGroup(a)
	case "fwrule": // classic firewall: let a network reach one device
		return c.planFwRule(a)
	}
	return nil, aerr("unknown action kind")
}

var hostRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$`)

// IsGuestWLAN: UniFi's guest flag, a guest network behind it, or picked by an admin.
func (c *Client) IsGuestWLAN(w map[string]any) bool {
	if g, _ := w["is_guest"].(bool); g {
		return true
	}
	id, _ := w["_id"].(string)
	if c.GuestSSIDs != nil && id != "" && contains(c.GuestSSIDs(), id) {
		return true
	}
	if nid, _ := w["networkconf_id"].(string); nid != "" && idRE.MatchString(nid) {
		if st, d := c.Get(c.SitePath("rest/networkconf/"+nid, false)); st == 200 {
			if n := first(d); n != nil && n["purpose"] == "guest" {
				return true
			}
		}
	}
	return false
}

// checkPassphrase: WPA personal passwords are 8–63 printable ASCII characters.
func checkPassphrase(pw string) error {
	if len(pw) < 8 || len(pw) > 63 {
		return aerr("the password must be 8 to 63 characters")
	}
	for _, r := range pw {
		if r < 0x20 || r > 0x7e {
			return aerr("use plain letters, digits and symbols (no accents or emoji) in Wi-Fi passwords")
		}
	}
	return nil
}

// NewGuestPassword makes an easy-to-type password: three groups of four letters/digits without look-alikes.
func NewGuestPassword() string {
	const alpha = "abcdefghjkmnpqrstuvwxyz23456789"
	out := make([]byte, 0, 14)
	for i := 0; i < 12; i++ {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alpha))))
		if err != nil {
			panic(err)
		}
		out = append(out, alpha[n.Int64()])
	}
	return string(out)
}

func label(m map[string]any, fallback string) string {
	for _, k := range []string{"name", "hostname", "mac"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

func orStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Change is one entry of the change log.
type Change struct {
	ID          int            `json:"id"`
	Time        string         `json:"time"`
	Host        string         `json:"host"`
	By          string         `json:"by"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Target      string         `json:"target"`
	Description string         `json:"description"`
	Before      map[string]any `json:"before"`
	After       map[string]any `json:"after"`
	Status      int            `json:"status"`
	OK          bool           `json:"ok"`
	Error       string         `json:"error,omitempty"`
	Undoable    bool           `json:"undoable"`
	Undone      bool           `json:"undone"`
	Created     string         `json:"created,omitempty"` // the id of what it created (a firewall port group the next rule uses)
	undo        *req
}

// Changes runs actions and remembers them for undo. Write mode is a session-wide lock the UI toggles.
type Changes struct {
	mu       sync.Mutex
	list     []*Change
	logPath  string
	undoPath string
	Write    bool
	ReadOnly bool
}

func NewChanges(dataDir string) *Changes {
	ch := &Changes{logPath: filepath.Join(dataDir, "changes-log.jsonl"), undoPath: filepath.Join(dataDir, "changes-undo.json")}
	ch.load()
	return ch
}

// The change log survives restarts (updates restart HomeOwl), and so does the way to undo each change — kept apart
// from the log, readable only by HomeOwl, and never for a change whose undo would hold a password (those can be
// undone until HomeOwl restarts).
const keepChanges = 2000

type undoFile struct {
	Undo   map[int]*req `json:"undo"`
	Undone []int        `json:"undone,omitempty"`
}

var undoSecretRE = regexp.MustCompile(`(?i)pass|secret|psk|key|token`)

func holdsSecret(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, y := range x {
			if undoSecretRE.MatchString(k) || holdsSecret(y) {
				return true
			}
		}
	case []any:
		for _, y := range x {
			if holdsSecret(y) {
				return true
			}
		}
	}
	return false
}

var createdRE = regexp.MustCompile(`^map\[_id:([0-9a-fA-F-]{8,64}) `)

func (ch *Changes) load() {
	b, err := os.ReadFile(ch.logPath)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > keepChanges {
		lines = lines[len(lines)-keepChanges:]
	}
	var uf undoFile
	if ub, err := os.ReadFile(ch.undoPath); err == nil {
		_ = json.Unmarshal(ub, &uf)
	}
	undone := map[int]bool{}
	for _, id := range uf.Undone {
		undone[id] = true
	}
	for _, l := range lines {
		var c Change
		if json.Unmarshal([]byte(l), &c) != nil || c.ID == 0 {
			continue
		}
		c.undo = uf.Undo[c.ID]
		c.Undone = undone[c.ID]
		if m := createdRE.FindStringSubmatch(c.Error); m != nil && !c.OK && c.Status > 200 && c.Status < 300 && c.Method == "POST" && c.undo == nil {
			// before 0.1.82 a "201 Created" counted as refused, though UniFi made it: logged with the new object as the error
			c.OK, c.Error, c.Created, c.undo = true, "", m[1], &req{"DELETE", c.Path + "/" + m[1], nil}
		}
		c.Undoable = c.undo != nil
		ch.list = append(ch.list, &c)
	}
}

// saveUndo writes the undo file (call with ch.mu held).
func (ch *Changes) saveUndoLocked() {
	uf := undoFile{Undo: map[int]*req{}}
	for _, c := range ch.list {
		if c.undo != nil && !holdsSecret(c.undo.Body) {
			uf.Undo[c.ID] = c.undo
		}
		if c.Undone {
			uf.Undone = append(uf.Undone, c.ID)
		}
	}
	b, _ := json.Marshal(uf)
	tmp := ch.undoPath + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, ch.undoPath)
	}
}

func (ch *Changes) List() []Change {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	out := make([]Change, len(ch.list))
	for i, c := range ch.list {
		out[i] = *c
	}
	return out
}

func (ch *Changes) logLine(c *Change) {
	b, _ := json.Marshal(c)
	_ = os.MkdirAll(filepath.Dir(ch.logPath), 0o700)
	f, err := os.OpenFile(ch.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}

// Preview is what a dry run returns.
type Preview struct {
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Before      map[string]any `json:"before"`
	After       map[string]any `json:"after"`
	Target      string         `json:"target"`
	Undoable    bool           `json:"undoable"`
	Description string         `json:"description"`
}

// Run previews (dryRun) or applies an allow-listed action. requireUnlock=false is used by the MCP server,
// whose own mode setting is the permission.
func (ch *Changes) Run(c *Client, a Action, dryRun bool, who string, requireUnlock bool) (map[string]any, error) {
	p, err := c.plan(a)
	if err != nil {
		return nil, err
	}
	pub := Preview{p.Method, p.Path, p.Before, p.After, p.Target, p.Undo != nil || p.DeleteCreated, a.s("description")}
	if dryRun {
		return map[string]any{"preview": pub}, nil
	}
	if ch.ReadOnly {
		return nil, aerr("HomeOwl was started in read-only mode")
	}
	if requireUnlock && !ch.Write {
		return nil, aerr("change mode is locked - unlock it in the sidebar first")
	}
	st, d := c.Request(p.Method, p.Path, p.Body)
	ok := st >= 200 && st < 300 // the v2 API answers a new object with 201 Created
	if m, isMap := d.(map[string]any); isMap {
		if meta, _ := m["meta"].(map[string]any); meta != nil && meta["rc"] == "error" {
			ok = false
		}
	}
	undo := p.Undo
	if p.DeleteCreated && ok {
		f := first(d)
		if m, isMap := d.(map[string]any); f == nil && isMap { // the v2 API answers with the object itself
			f = m
		}
		if f != nil && f["_id"] != nil {
			undo = &req{"DELETE", fmt.Sprintf("%s/%v", p.Path, f["_id"]), nil}
		}
	}
	ch2 := ch.record(c, who, pub, st, ok, d, undo)
	if p.DeleteCreated && ok && undo != nil {
		ch2.Created = path.Base(undo.Path)
	}
	return map[string]any{"result": ch2}, nil
}

// Raw performs any request (MCP "god mode") and logs it.
func (ch *Changes) Raw(c *Client, method, path string, body any, who string) (int, any) {
	st, d := c.Request(method, path, body)
	if method != "GET" {
		ch.record(c, who, Preview{Method: method, Path: path, Target: path, Description: "raw API request (MCP full access)",
			Before: map[string]any{}, After: map[string]any{"body": body}}, st, st >= 200 && st < 300, d, nil)
	}
	return st, d
}

func (ch *Changes) record(c *Client, who string, pub Preview, st int, ok bool, d any, undo *req) Change {
	return ch.recordAt(c.Host(), who, pub, st, ok, d, undo)
}

func (ch *Changes) recordAt(host, who string, pub Preview, st int, ok bool, d any, undo *req) Change {
	ch.mu.Lock()
	id := 1
	if n := len(ch.list); n > 0 {
		id = ch.list[n-1].ID + 1
	}
	e := &Change{ID: id, Time: time.Now().Format("2006-01-02 15:04:05"), Host: host, By: who,
		Method: pub.Method, Path: pub.Path, Target: pub.Target, Description: pub.Description, Before: pub.Before, After: pub.After,
		Status: st, OK: ok}
	if !ok {
		e.Error = ErrText(d)
	} else {
		e.undo = undo
	}
	e.Undoable = e.undo != nil
	ch.list = append(ch.list, e)
	if len(ch.list) > keepChanges {
		ch.list = ch.list[len(ch.list)-keepChanges:]
	}
	if e.undo != nil && ch.undoPath != "" {
		ch.saveUndoLocked()
	}
	out := *e
	ch.mu.Unlock()
	ch.logLine(e)
	return out
}

func (ch *Changes) Undo(c *Client, id int, who string, requireUnlock bool) error {
	if ch.ReadOnly || (requireUnlock && !ch.Write) {
		return aerr("change mode is locked")
	}
	ch.mu.Lock()
	var e *Change
	for _, x := range ch.list {
		if x.ID == id {
			e = x
		}
	}
	if e == nil || e.undo == nil || e.Undone {
		ch.mu.Unlock()
		return aerr("nothing to undo for that change")
	}
	if e.Host != "" && e.Host != c.Host() {
		ch.mu.Unlock()
		return aerr("that change was made on another console (%s)", e.Host)
	}
	u := e.undo
	ch.mu.Unlock()
	st, d := c.Request(u.Method, u.Path, u.Body)
	ok := st >= 200 && st < 300
	if ok {
		ch.mu.Lock()
		e.Undone = true
		if ch.undoPath != "" {
			ch.saveUndoLocked()
		}
		ch.mu.Unlock()
	}
	ch.record(c, who, Preview{Method: u.Method, Path: u.Path, Target: e.Target, Description: fmt.Sprintf("Undo of change #%d", id),
		Before: e.After, After: e.Before}, st, ok, d, nil)
	if !ok {
		return aerr("undo failed: %s", ErrText(d))
	}
	return nil
}

var ErrNotConnected = errors.New("not connected to a UniFi console")

// External runs a write to another UniFi app (e.g. Access) under the same rules as Run: read-only mode and the
// change lock are honoured, and it is logged with who did it. It can't be undone.
func (ch *Changes) External(host, who string, pub Preview, requireUnlock bool, do func() (int, any)) (Change, error) {
	if ch.ReadOnly {
		return Change{}, aerr("HomeOwl was started in read-only mode")
	}
	if requireUnlock && !ch.Write {
		return Change{}, aerr("change mode is locked - unlock it in the sidebar first")
	}
	st, d := do()
	return ch.recordAt(host, who, pub, st, st >= 200 && st < 300, d, nil), nil
}
