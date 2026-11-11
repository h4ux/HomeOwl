package server

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mdns"
)

// Easy names on the home network (Settings › Access): HomeOwl answers mDNS for e.g. hi.local and wifi.local,
// and a visit by that name gets only its own small page — the check-in page or the guest Wi-Fi card — over plain HTTP.

// pages each name may serve; anything else on that name goes back to its page
var namePaths = map[string]map[string]bool{
	"checkin": plainHTTPOK,
	"guest":   {"/wifi": true, "/wifi.js": true, "/qr.js": true, "/style.css": true, "/icon.png": true, "/api/wifi": true},
}
var nameHome = map[string]string{"checkin": "/hi", "guest": "/wifi"}

// ApplyNames starts or stops the mDNS answers and the port-80 listener to match the settings.
func (a *App) ApplyNames() {
	c := a.Cfg.Get()
	var list []string
	if c.LAN.Enabled { // the names point other devices here, so they need home-network access
		for _, n := range []string{c.Names.Checkin, c.Names.Guest} {
			if n != "" {
				list = append(list, n)
			}
		}
	}
	a.names.Set(list)
	want := c.Names.Port80 && c.LAN.Enabled && len(list) > 0
	a.mu.Lock()
	defer a.mu.Unlock()
	if !want {
		if a.port80 != nil {
			go func(s *http.Server) { _ = s.Close() }(a.port80)
		}
		a.port80, a.port80Err = nil, ""
		return
	}
	if a.port80 != nil {
		return
	}
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		a.port80Err = "Port 80 is busy or not allowed here (" + err.Error() + "). The names still work with :" + strconv.Itoa(c.LAN.Port) + "."
		return
	}
	srv := &http.Server{Handler: a.port80Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	a.port80, a.port80Err = srv, ""
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("port 80: %v", err)
		}
	}()
}

// port80Handler serves the easy names; any other visit is sent to HomeOwl's real port.
func (a *App) port80Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.nameHost(r) != "" {
			a.handler.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !a.hostAllowed(r) {
			http.Error(w, "Unexpected host name.", http.StatusMisdirectedRequest)
			return
		}
		scheme := "http"
		if a.Cfg.Get().LAN.TLS {
			scheme = "https"
		}
		http.Redirect(w, r, scheme+"://"+net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(a.Cfg.Get().LAN.Port))+"/", http.StatusFound)
	})
}

// nameHost: "checkin" or "guest" when the request came in by one of the easy names, else "".
func (a *App) nameHost(r *http.Request) string {
	h := strings.ToLower(r.Host)
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(h, ".") + "."
	c := a.Cfg.Get()
	if !c.LAN.Enabled {
		return ""
	}
	if c.Names.Checkin != "" && h == mdns.FQDN(c.Names.Checkin) {
		return "checkin"
	}
	if c.Names.Guest != "" && h == mdns.FQDN(c.Names.Guest) {
		return "guest"
	}
	return ""
}

func (a *App) serveName(kind string, w http.ResponseWriter, r *http.Request, h http.Handler) {
	if r.URL.Path == "/" {
		r.URL.Path = nameHome[kind]
	}
	if !namePaths[kind][r.URL.Path] {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	h.ServeHTTP(w, r)
}

// wifiAPI: the guest networks' names and passwords, for the card at http://<guest name>.local only.
func (a *App) wifiAPI(w http.ResponseWriter, r *http.Request) {
	if a.nameHost(r) != "guest" {
		fail(w, 404, "not found")
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "not allowed")
		return
	}
	res, err := a.guestWiFi(false)
	if err != nil {
		fail(w, 503, "The guest Wi-Fi isn't available right now.")
		return
	}
	nets := []map[string]any{}
	for _, n := range res["networks"].([]map[string]any) {
		if en, _ := n["enabled"].(bool); en && n["security"] != "" {
			nets = append(nets, map[string]any{"ssid": n["ssid"], "password": n["password"], "security": n["security"], "hidden": n["hidden"]})
		}
	}
	send(w, 200, map[string]any{"networks": nets, "qr_theme": res["qr_theme"]})
}

// namesView is the Settings › Access part: the names, the links and whether they work.
func (a *App) namesView() map[string]any {
	c := a.Cfg.Get()
	ifaces, err := a.names.Status()
	a.mu.Lock()
	p80, p80err := a.port80 != nil, a.port80Err
	a.mu.Unlock()
	link := func(n string) any {
		if n == "" {
			return nil
		}
		u := "http://" + strings.TrimSuffix(mdns.FQDN(n), ".")
		if !p80 {
			u += ":" + strconv.Itoa(c.LAN.Port)
		}
		return u
	}
	return map[string]any{"checkin": c.Names.Checkin, "guest": c.Names.Guest, "port80": c.Names.Port80,
		"checkin_url": link(c.Names.Checkin), "guest_url": link(c.Names.Guest), "lan": c.LAN.Enabled,
		"interfaces": ifaces, "error": nilIfEmpty(err), "port80_error": nilIfEmpty(p80err), "port80_on": p80}
}

func parseNames(m map[string]any, old config.Names) (config.Names, error) {
	n := old
	clean := func(k string) (string, error) {
		v := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(s(m, k)), "."), ".local")
		if v != "" && !mdns.ValidLabel(v) {
			return "", errors.New("a name is one word: letters, numbers and dashes (like “hi” or “guest-wifi”)")
		}
		return v, nil
	}
	var err error
	if has(m, "checkin") {
		if n.Checkin, err = clean("checkin"); err != nil {
			return old, err
		}
	}
	if has(m, "guest") {
		if n.Guest, err = clean("guest"); err != nil {
			return old, err
		}
	}
	if has(m, "port80") {
		n.Port80 = b(m, "port80")
	}
	if n.Checkin != "" && n.Checkin == n.Guest {
		return old, errors.New("the check-in page and the guest Wi-Fi card need different names")
	}
	for _, own := range ownHostLabels() {
		if n.Checkin == own || n.Guest == own {
			return old, errors.New("“" + own + "” is already this computer's own name — pick another")
		}
	}
	return n, nil
}

// this computer's own .local name(s), which the system already answers for
func ownHostLabels() []string {
	names, _ := localNames()
	var out []string
	for _, n := range names {
		if l := strings.TrimSuffix(strings.ToLower(n), ".local"); l != n || !strings.Contains(n, ".") {
			out = append(out, l)
		}
	}
	return out
}
