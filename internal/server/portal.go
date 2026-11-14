package server

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// The guest portal for the setup Wi-Fi. UniFi has one guest portal per site, so HomeOwl becomes it ("External portal
// server" = HomeOwl's address, on port 80):
//   - a device on the setup Wi-Fi (its own password, guest portal on) lands on the check-in page, and is never let
//     online: before sign-in a guest device reaches only the portal, so it has HomeOwl and nothing else;
//   - a device on the guest Wi-Fi lands on HomeOwl's sign-in page, which works as the portal did before (a Connect
//     button, or the portal's own password) and lets it online through UniFi for as long as the portal did.
// Turning it on is two previewed, logged, undoable console changes; undo puts the old portal back.

// portalPlan reads the console and works out the two changes, or why it can't.
func (a *App) portalPlan() (acts []unifi.Action, conf *config.Portal, notes []string, err error) {
	c := a.Cfg.Get()
	if !a.UniFi.Connected() {
		return nil, nil, nil, errors.New("connect UniFi first")
	}
	ssid := c.Onboard.SSID
	if ssid == "" {
		return nil, nil, nil, errors.New("pick or create the setup Wi-Fi first")
	}
	all, err := a.wlans()
	if err != nil {
		return nil, nil, nil, err
	}
	var setup map[string]any
	for _, w := range all {
		if strings.EqualFold(str(w, "name"), ssid) {
			setup = w
		}
	}
	if setup == nil {
		return nil, nil, nil, errors.New("there's no Wi-Fi called " + ssid + " on the console")
	}
	if str(setup, "security") != "wpapsk" {
		return nil, nil, nil, errors.New(ssid + " has no password: give it one first, so only people you tell can join it")
	}
	a.mu.Lock()
	p80 := a.port80 != nil
	a.mu.Unlock()
	if !p80 {
		return nil, nil, nil, errors.New("UniFi sends portal visits to port 80, and HomeOwl isn't answering there: turn on short links in Settings › Access")
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/setting", false))
	if st != 200 {
		return nil, nil, nil, errors.New("couldn't read the console's guest portal settings")
	}
	var g map[string]any
	for _, x := range unifi.List(d) {
		if x["key"] == "guest_access" {
			g = x
		}
	}
	if g == nil {
		return nil, nil, nil, errors.New("this console has no guest portal settings")
	}
	ip := a.portalIP()
	if ip == "" {
		return nil, nil, nil, errors.New("HomeOwl has no home network address for the portal")
	}
	mode, why := portalMode(g, ip)
	if mode == "" {
		return nil, nil, nil, errors.New(why)
	}
	minutes := 480
	if n, err := strconv.Atoi(strings.TrimSpace(str(g, "expire"))); err == nil && n > 0 {
		minutes = n
	} else if f, ok := g["expire"].(float64); ok && f > 0 {
		minutes = int(f)
	}
	conf = &config.Portal{Mode: mode, IP: ip, Minutes: minutes}
	acts = []unifi.Action{
		{"kind": "setting", "key": "guest_access", "fields": map[string]any{"portal_enabled": true, "auth": "custom", "custom_ip": ip, "redirect_enabled": false},
			"description": "Setup Wi-Fi: the guest portal is HomeOwl (" + ip + ")"},
		{"kind": "rest", "resource": "wlanconf", "id": str(setup, "_id"), "fields": map[string]any{"is_guest": true},
			"description": "Setup Wi-Fi: guest portal on for " + ssid},
	}
	if g["portal_enabled"] != true {
		notes = append(notes, "The guest portal is off now. Once it's on, guest Wi-Fi networks show HomeOwl's sign-in page with a Connect button before going online.")
	} else if mode == "password" {
		notes = append(notes, "Guest Wi-Fi visitors still sign in with the portal's password: HomeOwl's page asks for it and checks it with UniFi's.")
	} else {
		notes = append(notes, "Guest Wi-Fi visitors still just tap Connect, now on HomeOwl's page.")
	}
	notes = append(notes, "Devices on "+ssid+" are never let online: they reach HomeOwl's check-in page and nothing else. Undo puts the old portal back.")
	return acts, conf, notes, nil
}

// portalMode: how guest Wi-Fi visitors signed in before, as HomeOwl's page will copy it ("" + why when it can't).
func portalMode(g map[string]any, ip string) (string, string) {
	if g["portal_enabled"] != true {
		return "none", ""
	}
	switch auth := str(g, "auth"); auth {
	case "", "none":
		return "none", ""
	case "password":
		return "password", ""
	case "hotspot":
		if g["voucher_enabled"] == true || g["payment_enabled"] == true || g["radius_enabled"] == true || g["facebook_enabled"] == true || g["google_enabled"] == true {
			return "", "The guest portal uses vouchers, payment or another sign-in that HomeOwl can't take over. Switch the guest Wi-Fi's portal to a password or no sign-in in UniFi first, or keep the setup Wi-Fi without a portal."
		}
		if g["password_enabled"] == true {
			return "password", ""
		}
		return "none", ""
	case "custom":
		if c := str(g, "custom_ip"); c != "" && c != ip {
			return "", "The guest portal already goes to another server (" + c + "). HomeOwl won't take it over."
		}
		return "none", ""
	default:
		return "", "The guest portal signs in with " + auth + ", which HomeOwl can't take over."
	}
}

// portalIP: HomeOwl's address on the home network (not on the setup network), where UniFi sends portal visits.
func (a *App) portalIP() string {
	_, sn := a.setupSubnet()
	for _, ip := range portalIPs() {
		if ip.To4() != nil && ip.IsPrivate() && (sn == nil || !sn.Contains(ip)) {
			return ip.String()
		}
	}
	return ""
}

var portalIPs = func() []net.IP { _, ips := localNames(); return ips } // tests set their own

// portalVisit: UniFi sent a device to http://<HomeOwl>/guest/s/<site>/?… — the setup Wi-Fi goes to the check-in
// page, the guest Wi-Fi to HomeOwl's sign-in page.
func (a *App) portalVisit(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.Get().Onboard.Portal != nil {
		if c, err := a.clientByIP(remoteIP(r)); err == nil && c != nil && c["is_guest"] == true && !a.onSetupWiFi(c) {
			u := "/welcome"
			if back := r.URL.Query().Get("url"); back != "" {
				u += "?url=" + url.QueryEscape(back)
			}
			http.Redirect(w, r, u, http.StatusFound)
			return
		}
	}
	http.Redirect(w, r, "/hi", http.StatusFound)
}

type portalLimit struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

var welcomeHits portalLimit

func (l *portalLimit) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	var recent []time.Time
	for _, t := range l.hits[ip] {
		if time.Since(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		l.hits[ip] = recent
		return false
	}
	l.hits[ip] = append(recent, time.Now())
	return true
}

// welcomeAPI: HomeOwl's sign-in page for the guest Wi-Fi (no session: the guest's own device).
func (a *App) welcomeAPI(w http.ResponseWriter, r *http.Request) {
	pc := a.Cfg.Get().Onboard.Portal
	if pc == nil {
		fail(w, 404, "not found")
		return
	}
	ip := remoteIP(r)
	c, err := a.clientByIP(ip)
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	if c == nil || c["is_guest"] != true || a.onSetupWiFi(c) {
		fail(w, 403, "This page is for the guest Wi-Fi.")
		return
	}
	if r.Method == http.MethodGet {
		send(w, 200, map[string]any{"ssid": str(c, "essid"), "password": pc.Mode == "password", "online": c["authorized"] == true})
		return
	}
	if r.Method != http.MethodPost || !sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "not allowed")
		return
	}
	if !welcomeHits.allow(ip) {
		fail(w, 429, "Too many tries — wait a few minutes.")
		return
	}
	if pc.Mode == "password" {
		want := a.portalPassword()
		got := s(readJSON(r), "password")
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
			fail(w, 403, "That's not the password.")
			return
		}
	}
	act := unifi.Action{"kind": "guestauth", "mac": strings.ToLower(str(c, "mac")), "minutes": strconv.Itoa(pc.Minutes),
		"label": orStr(str(c, "hostname"), str(c, "mac")), "description": "Guest portal: " + orStr(str(c, "essid"), "guest Wi-Fi") + " online"}
	res, err := a.Changes.Run(a.UniFi, act, false, "guest portal", false)
	if err != nil {
		fail(w, 502, "The network didn't let this device online — try again.")
		return
	}
	if ch, ok := res["result"].(unifi.Change); ok && !ch.OK {
		fail(w, 502, "The network didn't let this device online — try again.")
		return
	}
	send(w, 200, map[string]any{"ok": true})
}

// portalPassword: the guest portal's own password, read from UniFi when needed (never stored or sent out).
func (a *App) portalPassword() string {
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/setting", false))
	if st != 200 {
		return ""
	}
	for _, x := range unifi.List(d) {
		if x["key"] == "guest_access" {
			return str(x, "x_password")
		}
	}
	return ""
}

// portalAPI: "preview" the changes, "apply" them (change mode unlocked, like any console change from the UI).
func (a *App) portalAPI(op, by string) (map[string]any, error) {
	acts, conf, notes, err := a.portalPlan()
	if err != nil {
		return nil, err
	}
	switch op {
	case "preview":
		var steps []any
		for _, x := range acts {
			p, err := a.Changes.Run(a.UniFi, x, true, by, false)
			if err != nil {
				return nil, err
			}
			steps = append(steps, p["preview"])
		}
		return map[string]any{"steps": steps, "notes": notes, "ip": conf.IP}, nil
	case "apply":
		for i, x := range acts {
			res, err := a.Changes.Run(a.UniFi, x, false, by, true)
			if err == nil {
				if ch, ok := res["result"].(unifi.Change); ok && !ch.OK {
					err = errors.New(ch.Error)
				}
			}
			if err != nil {
				if i > 0 {
					return nil, errors.New("the guest portal now points at HomeOwl, but the setup Wi-Fi's portal wasn't turned on: " + err.Error() + " (undo it in the change log)")
				}
				return nil, err
			}
		}
		if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.Portal = conf }); err != nil {
			return nil, err
		}
		a.Cache.Invalidate()
		wlanCache.Lock()
		wlanCache.list = nil
		wlanCache.Unlock()
		return a.devTypesView()
	}
	return nil, errors.New("not found")
}
