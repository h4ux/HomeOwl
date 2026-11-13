package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/unifi"
)

// Profile types (Settings › Device profiles): named sets of settings — Wi-Fi networks and a VPN — that the device
// setup profile carries besides NextDNS (devprofile.go). A type is attached to people; a device gets its person's
// type unless an admin picked another one for it. Wi-Fi passwords are read from UniFi when the device downloads its
// profile; VPN passwords, shared secrets and WireGuard configs live in the secrets store and never go back to the
// browser.

type VPNConf struct {
	Type     string `json:"type"` // ikev2 | l2tp | wireguard
	Name     string `json:"name,omitempty"`
	Server   string `json:"server,omitempty"`
	RemoteID string `json:"remote_id,omitempty"` // IKEv2
	LocalID  string `json:"local_id,omitempty"`  // IKEv2
	Auth     string `json:"auth,omitempty"`      // IKEv2: "secret" (shared secret) or "eap" (user name and password)
	User     string `json:"user,omitempty"`      // IKEv2 EAP, L2TP
	OnDemand bool   `json:"on_demand,omitempty"` // connect by itself when not on the home Wi-Fi
}

type ProfileType struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	WiFi   []string `json:"wifi,omitempty"` // UniFi wlanconf _id
	VPN    *VPNConf `json:"vpn,omitempty"`
	People []string `json:"people,omitempty"`
}

type profileTypes struct {
	mu   sync.Mutex
	path string
}

func newProfileTypes(dir string) *profileTypes {
	return &profileTypes{path: filepath.Join(dir, "device-types.json")}
}

func (s *profileTypes) List() []ProfileType {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *profileTypes) load() []ProfileType {
	var l []ProfileType
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	if l == nil {
		l = []ProfileType{}
	}
	return l
}

func (s *profileTypes) save(l []ProfileType) error {
	b, _ := json.MarshalIndent(l, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

func (s *profileTypes) Get(id string) (ProfileType, bool) {
	for _, t := range s.List() {
		if t.ID == id {
			return t, true
		}
	}
	return ProfileType{}, false
}

// the secrets of a type and of a device (secrets store slots)
func vpnSecretSlot(id string) string   { return "devtype-" + id + "-secret" }
func vpnPasswordSlot(id string) string { return "devtype-" + id + "-password" }
func deviceWGSlot(id string) string    { return "device-wg-" + id }

const typeNone = "-" // a device that gets no profile type, whatever its person's

// typeFor is the device's profile type: the one an admin picked for it, else its person's.
func (a *App) typeFor(x CheckIn) (ProfileType, bool) {
	if x.TypeID == typeNone {
		return ProfileType{}, false
	}
	l := a.devTypes.List()
	if x.TypeID != "" {
		for _, t := range l {
			if t.ID == x.TypeID {
				return t, true
			}
		}
	}
	for _, t := range l {
		for _, p := range t.People {
			if p != "" && p == x.PersonID {
				return t, true
			}
		}
	}
	return ProfileType{}, false
}

// ---- what goes in the profile

type wifiNet struct {
	ID, SSID, Enc, Password string
	Hidden                  bool
}

type vpnPayload struct {
	Conf             VPNConf
	Secret, Password string
	WG               string   // the device's own wg-quick config
	HomeSSIDs        []string // for "connect when away"
}

type profileExtras struct {
	Clip *webClip
	Mac  bool
	WiFi []wifiNet
	VPN  *vpnPayload
}

// wlans reads the Wi-Fi networks from UniFi (cached a minute: the profile, the status and the update check all
// want them).
func (a *App) wlans() ([]map[string]any, error) {
	wlanCache.Lock()
	defer wlanCache.Unlock()
	if time.Since(wlanCache.at) < time.Minute && wlanCache.list != nil {
		return wlanCache.list, nil
	}
	if !a.UniFi.Connected() {
		return nil, errors.New("not connected to UniFi")
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/wlanconf", false))
	if st != 200 {
		return nil, errors.New("couldn't read the Wi-Fi settings: " + unifi.ErrText(d))
	}
	wlanCache.list, wlanCache.at = unifi.List(d), time.Now()
	return wlanCache.list, nil
}

var wlanCache struct {
	sync.Mutex
	list []map[string]any
	at   time.Time
}

// wifiFor turns UniFi networks into profile entries. Enterprise (802.1X) networks have no shared password and are
// left out.
func wifiFor(all []map[string]any, ids []string) []wifiNet {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []wifiNet
	for _, w := range all {
		id := str(w, "_id")
		if !want[id] {
			continue
		}
		n := wifiNet{ID: id, SSID: str(w, "name")}
		n.Hidden, _ = w["hide_ssid"].(bool)
		switch str(w, "security") {
		case "open":
			n.Enc = "None"
		case "wpapsk":
			n.Enc, n.Password = "Any", str(w, "x_passphrase")
		default:
			continue
		}
		out = append(out, n)
	}
	return out
}

// extrasFor collects the device's Wi-Fi and VPN settings. ok is false when UniFi couldn't be read (so the caller
// can't tell whether anything changed).
func (a *App) extrasFor(x CheckIn) (profileExtras, bool) {
	var ex profileExtras
	t, has := a.typeFor(x)
	if !has {
		return ex, true
	}
	ok := true
	var all []map[string]any
	if len(t.WiFi) > 0 || t.VPN != nil && t.VPN.OnDemand {
		var err error
		if all, err = a.wlans(); err != nil {
			ok = false
		}
	}
	ex.WiFi = wifiFor(all, t.WiFi)
	if t.VPN != nil {
		v := &vpnPayload{Conf: *t.VPN, Secret: a.Sec.Get(vpnSecretSlot(t.ID)), Password: a.Sec.Get(vpnPasswordSlot(t.ID))}
		if v.Conf.Type == "wireguard" {
			v.WG = a.Sec.Get(deviceWGSlot(x.ID))
		}
		for _, w := range all {
			if en, ok := w["enabled"].(bool); (!ok || en) && !a.UniFi.IsGuestWLAN(w) {
				v.HomeSSIDs = append(v.HomeSSIDs, str(w, "name"))
			}
		}
		sort.Strings(v.HomeSSIDs)
		if v.Conf.Type != "wireguard" || v.WG != "" { // WireGuard needs the device's own config
			ex.VPN = v
		}
	}
	return ex, ok
}

// setupHash fingerprints what the device's profile holds, so a change (new NextDNS profile, other Wi-Fi, a new
// Wi-Fi password, VPN settings) shows "update ready". "" when it can't be told.
func (a *App) setupHash(x CheckIn) string {
	ex, ok := a.extrasFor(x)
	if !ok {
		return ""
	}
	h := sha256.New()
	_ = json.NewEncoder(h).Encode([]any{x.NDProfile, x.Name, ex.WiFi, ex.VPN})
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// setupUpdate: has the setup changed since the device last downloaded it?
func (a *App) setupUpdate(x CheckIn) bool {
	if x.ProfileHash != "" {
		if h := a.setupHash(x); h != "" {
			return h != x.ProfileHash
		}
	}
	return x.ProfileND != "" && x.ProfileND != x.NDProfile
}

// includes is what the device's page says the setup holds.
func (a *App) includes(x CheckIn) map[string]any {
	out := map[string]any{}
	t, has := a.typeFor(x)
	if !has {
		return out
	}
	out["type"] = t.Name
	if all, err := a.wlans(); err == nil {
		var names []string
		for _, w := range wifiFor(all, t.WiFi) {
			names = append(names, w.SSID)
		}
		out["wifi"] = names
	}
	if t.VPN != nil {
		v := map[string]any{"name": orDefault(t.VPN.Name, "Home VPN"), "type": t.VPN.Type, "on_demand": t.VPN.OnDemand}
		if t.VPN.Type == "wireguard" {
			v["app"] = "WireGuard"
			v["ready"] = a.Sec.Get(deviceWGSlot(x.ID)) != ""
		}
		out["vpn"] = v
	}
	return out
}

// ---- payloads (plist fragments; see mobileConfig)

type plistW struct{ b *bytes.Buffer }

func (p plistW) str(k, v string) {
	p.b.WriteString("<key>" + k + "</key><string>")
	_ = xml.EscapeText(p.b, []byte(v))
	p.b.WriteString("</string>\n")
}
func (p plistW) boolean(k string, v bool) {
	p.b.WriteString("<key>" + k + "</key>" + map[bool]string{true: "<true/>", false: "<false/>"}[v] + "\n")
}
func (p plistW) integer(k, v string) {
	p.b.WriteString("<key>" + k + "</key><integer>" + v + "</integer>\n")
}
func (p plistW) data(k string, v []byte) {
	p.b.WriteString("<key>" + k + "</key><data>" + base64.StdEncoding.EncodeToString(v) + "</data>\n")
}
func (p plistW) head(base, kind, typ, name, desc string) {
	p.str("PayloadDescription", desc)
	p.str("PayloadDisplayName", name)
	p.str("PayloadIdentifier", base+"."+kind)
	p.str("PayloadType", typ)
	p.str("PayloadUUID", stableUUID(base+"."+kind))
	p.integer("PayloadVersion", "1")
}

func wifiPayload(p plistW, base string, w wifiNet) {
	p.b.WriteString("<dict>\n")
	p.boolean("AutoJoin", true)
	p.str("EncryptionType", w.Enc)
	p.boolean("HIDDEN_NETWORK", w.Hidden)
	if w.Password != "" {
		p.str("Password", w.Password)
	}
	p.head(base, "wifi."+w.ID, "com.apple.wifi.managed", "Wi-Fi · "+w.SSID, "Joins "+w.SSID+" by itself.")
	p.str("SSID_STR", w.SSID)
	p.b.WriteString("</dict>\n")
}

// onDemand: off on the home Wi-Fi, on everywhere else.
func onDemand(p plistW, home []string) {
	p.integer("OnDemandEnabled", "1")
	p.b.WriteString("<key>OnDemandRules</key><array>\n")
	if len(home) > 0 {
		p.b.WriteString("<dict>\n")
		p.str("Action", "Disconnect")
		p.str("InterfaceTypeMatch", "WiFi")
		p.b.WriteString("<key>SSIDMatch</key><array>")
		for _, s := range home {
			p.b.WriteString("<string>")
			_ = xml.EscapeText(p.b, []byte(s))
			p.b.WriteString("</string>")
		}
		p.b.WriteString("</array>\n</dict>\n")
	}
	p.b.WriteString("<dict>\n")
	p.str("Action", "Connect")
	p.b.WriteString("</dict>\n</array>\n")
}

var wgEndpointRE = regexp.MustCompile(`(?mi)^\s*Endpoint\s*=\s*(\S+)`)

func vpnPayloadXML(p plistW, base string, v *vpnPayload, mac bool) {
	c := v.Conf
	name := orDefault(c.Name, "Home VPN")
	p.b.WriteString("<dict>\n")
	p.head(base, "vpn", "com.apple.vpn.managed", "VPN · "+name, "The "+name+" VPN.")
	p.str("UserDefinedName", name)
	switch c.Type {
	case "ikev2":
		p.str("VPNType", "IKEv2")
		p.b.WriteString("<key>IKEv2</key><dict>\n")
		p.str("RemoteAddress", c.Server)
		p.str("RemoteIdentifier", orDefault(c.RemoteID, c.Server))
		p.str("LocalIdentifier", c.LocalID)
		if c.Auth == "eap" {
			p.str("AuthenticationMethod", "None")
			p.integer("ExtendedAuthEnabled", "1")
			p.str("AuthName", c.User)
			p.str("AuthPassword", v.Password)
		} else {
			p.str("AuthenticationMethod", "SharedSecret")
			p.str("SharedSecret", v.Secret)
		}
		if c.OnDemand {
			onDemand(p, v.HomeSSIDs)
		}
		p.b.WriteString("</dict>\n")
	case "l2tp":
		p.str("VPNType", "L2TP")
		p.b.WriteString("<key>PPP</key><dict>\n")
		p.str("CommRemoteAddress", c.Server)
		p.str("AuthName", c.User)
		p.str("AuthPassword", v.Password)
		p.b.WriteString("</dict>\n<key>IPSec</key><dict>\n")
		p.str("AuthenticationMethod", "SharedSecret")
		p.data("SharedSecret", []byte(v.Secret))
		p.b.WriteString("</dict>\n")
	case "wireguard": // the WireGuard app's own format
		p.str("VPNType", "VPN")
		p.str("VPNSubType", map[bool]string{true: "com.wireguard.macos", false: "com.wireguard.ios"}[mac])
		p.b.WriteString("<key>VendorConfig</key><dict>\n")
		p.str("WgQuickConfig", v.WG)
		p.b.WriteString("</dict>\n<key>VPN</key><dict>\n")
		ep := ""
		if m := wgEndpointRE.FindStringSubmatch(v.WG); m != nil {
			ep = m[1]
		}
		p.str("RemoteAddress", ep)
		p.str("AuthenticationMethod", "Password")
		if c.OnDemand {
			onDemand(p, v.HomeSSIDs)
		}
		p.b.WriteString("</dict>\n")
	}
	p.b.WriteString("</dict>\n")
}

// ---- Settings › Device profiles

func (a *App) devTypesView() (map[string]any, error) {
	types := []map[string]any{}
	for _, t := range a.devTypes.List() {
		types = append(types, map[string]any{"id": t.ID, "name": t.Name, "wifi": t.WiFi, "vpn": t.VPN, "people": t.People,
			"secret_set": a.Sec.Get(vpnSecretSlot(t.ID)) != "", "password_set": a.Sec.Get(vpnPasswordSlot(t.ID)) != ""})
	}
	wl := []map[string]any{}
	all, err := a.wlans()
	for _, w := range all {
		sec := str(w, "security")
		wl = append(wl, map[string]any{"id": str(w, "_id"), "ssid": str(w, "name"), "guest": a.UniFi.IsGuestWLAN(w),
			"usable": sec == "open" || sec == "wpapsk", "security": sec}) // never the password
	}
	res := map[string]any{"types": types, "wlans": wl}
	if err != nil {
		res["wlan_error"] = err.Error()
	}
	return res, nil
}

var vpnHostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// saveDevType adds or changes a type. Secrets: "" keeps the saved one, "-" removes it.
func (a *App) saveDevType(body map[string]any) (map[string]any, error) {
	t := ProfileType{ID: s(body, "id"), Name: strings.TrimSpace(s(body, "name"))}
	if t.Name == "" || len(t.Name) > 40 {
		return nil, errors.New("give the type a name (up to 40 characters)")
	}
	for _, k := range []string{"wifi", "people"} {
		l, _ := body[k].([]any)
		for _, v := range l {
			if id, _ := v.(string); id != "" && len(id) < 64 {
				if k == "wifi" {
					t.WiFi = append(t.WiFi, id)
				} else {
					t.People = append(t.People, id)
				}
			}
		}
	}
	if v, ok := body["vpn"].(map[string]any); ok && s(v, "type") != "" {
		c := VPNConf{Type: s(v, "type"), Name: strings.TrimSpace(s(v, "name")), Server: strings.TrimSpace(s(v, "server")), RemoteID: strings.TrimSpace(s(v, "remote_id")),
			LocalID: strings.TrimSpace(s(v, "local_id")), Auth: s(v, "auth"), User: strings.TrimSpace(s(v, "user")), OnDemand: b(v, "on_demand")}
		switch c.Type {
		case "ikev2", "l2tp":
			if !vpnHostRE.MatchString(c.Server) && net.ParseIP(c.Server) == nil {
				return nil, errors.New("the VPN server should be a host name or an IP address")
			}
		case "wireguard":
			c.Server = ""
		default:
			return nil, errors.New("pick IKEv2, L2TP or WireGuard")
		}
		if c.Type == "ikev2" && c.Auth != "eap" {
			c.Auth = "secret"
		}
		if c.Type == "l2tp" {
			c.OnDemand = false // iOS connects L2TP only by hand
		}
		if len(c.Name) > 40 || len(c.User) > 128 || len(c.RemoteID) > 253 || len(c.LocalID) > 253 {
			return nil, errors.New("a VPN field is too long")
		}
		t.VPN = &c
	}
	a.devTypes.mu.Lock()
	defer a.devTypes.mu.Unlock()
	l := a.devTypes.load()
	if t.ID == "" {
		var r [5]byte
		_, _ = rand.Read(r[:])
		t.ID = hex.EncodeToString(r[:])
		l = append(l, t)
	} else {
		found := false
		for i := range l {
			if l[i].ID == t.ID {
				l[i], found = t, true
			}
		}
		if !found {
			return nil, errors.New("that profile type is gone")
		}
	}
	// a person has one type: attaching them here takes them off the others
	for i := range l {
		if l[i].ID == t.ID {
			continue
		}
		var keep []string
		for _, p := range l[i].People {
			if !contains(t.People, p) {
				keep = append(keep, p)
			}
		}
		l[i].People = keep
	}
	for slot, k := range map[string]string{vpnSecretSlot(t.ID): "secret", vpnPasswordSlot(t.ID): "password"} {
		switch v := s(body, k); {
		case v == "":
		case v == "-" || t.VPN == nil:
			a.Sec.Delete(slot)
		default:
			if len(v) > 512 {
				return nil, errors.New("that " + k + " is too long")
			}
			if err := a.Sec.Set(slot, v); err != nil {
				return nil, err
			}
		}
	}
	if t.VPN == nil {
		a.Sec.Delete(vpnSecretSlot(t.ID))
		a.Sec.Delete(vpnPasswordSlot(t.ID))
	}
	if err := a.devTypes.save(l); err != nil {
		return nil, err
	}
	return map[string]any{"id": t.ID}, nil
}

func (a *App) deleteDevType(id string) error {
	a.devTypes.mu.Lock()
	defer a.devTypes.mu.Unlock()
	l := a.devTypes.load()
	var keep []ProfileType
	for _, t := range l {
		if t.ID != id {
			keep = append(keep, t)
		}
	}
	if len(keep) == len(l) {
		return errors.New("that profile type is gone")
	}
	a.Sec.Delete(vpnSecretSlot(id))
	a.Sec.Delete(vpnPasswordSlot(id))
	if keep == nil {
		keep = []ProfileType{}
	}
	return a.devTypes.save(keep)
}

var wgPrivRE = regexp.MustCompile(`(?mi)^\s*PrivateKey\s*=\s*[A-Za-z0-9+/]{42,43}=\s*$`)

// checkWG makes sure a pasted WireGuard config is one: an interface with a private key and a peer with an endpoint.
func checkWG(conf string) error {
	if len(conf) > 4096 {
		return errors.New("that WireGuard config is too long")
	}
	if !strings.Contains(conf, "[Interface]") || !strings.Contains(conf, "[Peer]") || !wgPrivRE.MatchString(conf) || !wgEndpointRE.MatchString(conf) {
		return errors.New("that isn't a WireGuard config: it needs [Interface] with a PrivateKey and [Peer] with an Endpoint")
	}
	return nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
