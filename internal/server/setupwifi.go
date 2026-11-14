package server

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Creating the setup Wi-Fi on the console (Settings › Device profiles › Setup Wi-Fi › Create it): a network with
// no internet on its own VLAN, and a Wi-Fi with its own password on it. Without a portal (the site's one guest
// portal stays with the guest Wi-Fi): people open the check-in page by its address or QR code. Preview first; Apply
// runs both allow-listed actions (logged, undo deletes them) and makes it the setup Wi-Fi in HomeOwl.

func setupActions(body map[string]any, netID string) (unifi.Action, unifi.Action) {
	ssid := strings.TrimSpace(s(body, "ssid"))
	netName := strings.TrimSpace(s(body, "network"))
	if netName == "" {
		netName = "HomeOwl Setup"
	}
	vlan := s(body, "vlan")
	if v, ok := body["vlan"].(float64); ok {
		vlan = strconv.Itoa(int(v))
	}
	internet, what := "on", " (with internet)"
	if v, ok := body["internet"].(bool); ok && !v {
		internet, what = "off", " (no internet)"
	}
	return unifi.Action{"kind": "setupnet", "name": netName, "vlan": vlan, "internet": internet, "description": "Setup Wi-Fi: network " + netName + what},
		unifi.Action{"kind": "setupwlan", "ssid": ssid, "password": s(body, "password"), "network_id": netID, "description": "Setup Wi-Fi: " + ssid}
}

// setupPassword: 12 easy-to-type characters (no 0/O, 1/l/I).
func setupPassword() string {
	const abc = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(abc))))
		b[i] = abc[n.Int64()]
	}
	return string(b[:4]) + "-" + string(b[4:8]) + "-" + string(b[8:])
}

// setupLinks: the check-in page by IP address — what a device on the setup Wi-Fi (no names) can open. An address
// of HomeOwl's inside the setup network comes first: phones reach it directly, even when the network is isolated
// or has no internet (then a phone sends anything outside its own network over mobile data).
func (a *App) setupLinks() []string {
	var in, out []string
	_, subnet := a.setupSubnet()
	for _, l := range a.checkinLinks() {
		h := strings.TrimPrefix(l, "http://")
		if i := strings.IndexAny(h, ":/"); i > 0 {
			h = h[:i]
		}
		if ip := net.ParseIP(h); ip != nil && ip.To4() != nil && ip.IsPrivate() {
			if subnet != nil && subnet.Contains(ip) {
				in = append(in, l)
			} else {
				out = append(out, l)
			}
		}
	}
	return append(in, out...)
}

// setupSubnet: the setup Wi-Fi's network on the console (from the Wi-Fi's network) and its subnet.
func (a *App) setupSubnet() (map[string]any, *net.IPNet) {
	ssid := a.Cfg.Get().Onboard.SSID
	if ssid == "" || !a.UniFi.Connected() {
		return nil, nil
	}
	all, err := a.wlans()
	if err != nil {
		return nil, nil
	}
	for _, w := range all {
		if !strings.EqualFold(str(w, "name"), ssid) {
			continue
		}
		id := str(w, "networkconf_id")
		if id == "" {
			return nil, nil
		}
		st, d := a.UniFi.Get(a.UniFi.SitePath("rest/networkconf/"+url.PathEscape(id), false))
		l := unifi.List(d)
		if len(l) == 0 {
			return nil, nil
		}
		n := l[0]
		if st != 200 {
			return nil, nil
		}
		_, sn, _ := net.ParseCIDR(str(n, "ip_subnet"))
		return n, sn
	}
	return nil, nil
}

// setupNetView: what Settings shows about the setup Wi-Fi's network: internet, isolation (missing on consoles
// with zone-based firewall) and whether HomeOwl has an address of its own on it.
func (a *App) setupNetView() map[string]any {
	n, sn := a.setupSubnet()
	if n == nil {
		return nil
	}
	v := map[string]any{"id": str(n, "_id"), "name": str(n, "name"), "vlan": n["vlan"], "internet": n["internet_access_enabled"] != false}
	if sn != nil {
		v["subnet"] = sn.String()
		_, ips := localNames()
		for _, ip := range ips {
			if sn.Contains(ip) {
				v["homeowl_ip"] = ip.String()
			}
		}
	}
	if iso, ok := n["network_isolation_enabled"].(bool); ok {
		v["isolated"] = iso
	}
	return v
}

func (a *App) setupWiFiAPI(op string, body map[string]any, by string) (map[string]any, error) {
	if !a.UniFi.Connected() {
		return nil, errors.New("connect UniFi first")
	}
	switch op {
	case "suggest":
		return map[string]any{"password": setupPassword(), "vlan": 30, "network": "HomeOwl Setup"}, nil
	case "preview":
		na, wa := setupActions(body, "new")
		p1, err := a.Changes.Run(a.UniFi, na, true, by, false)
		if err != nil {
			return nil, err
		}
		p2, err := a.Changes.Run(a.UniFi, wa, true, by, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"steps": []any{p1["preview"], p2["preview"]}}, nil
	case "apply":
		na, _ := setupActions(body, "new")
		r1, err := a.Changes.Run(a.UniFi, na, false, by, true) // like any console change from the UI: change mode unlocked
		if err != nil {
			return nil, err
		}
		if ch, _ := r1["result"].(unifi.Change); !ch.OK {
			return nil, errors.New("UniFi didn't create the network: " + ch.Error)
		}
		st, d := a.UniFi.Get(a.UniFi.SitePath("rest/networkconf", false))
		netID := ""
		if st == 200 {
			for _, n := range unifi.List(d) {
				if strings.EqualFold(str(n, "name"), str(na, "name")) {
					netID = str(n, "_id")
				}
			}
		}
		if netID == "" {
			return nil, errors.New("the network was created, but HomeOwl can't find it to put the Wi-Fi on (undo it in the change log, or add the Wi-Fi in UniFi)")
		}
		_, wa := setupActions(body, netID)
		r2, err := a.Changes.Run(a.UniFi, wa, false, by, true)
		if err != nil {
			return nil, errors.New("the network was created, but the Wi-Fi wasn't: " + err.Error())
		}
		if ch, _ := r2["result"].(unifi.Change); !ch.OK {
			return nil, errors.New("the network was created, but UniFi didn't create the Wi-Fi: " + ch.Error)
		}
		ssid := str(wa, "ssid")
		if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.SSID = ssid }); err != nil {
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
