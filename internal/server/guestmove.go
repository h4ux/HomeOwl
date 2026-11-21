package server

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Guests on the home Wi-Fi. Visitors often get the home Wi-Fi's password from a family member's iPhone (password
// sharing) and never see the guest Wi-Fi. HomeOwl can put such a device on the guest network without it changing
// Wi-Fi: UniFi's per-client "virtual network override" puts it on the guest network's VLAN on the same Wi-Fi, so it
// gets the internet and the guest network's rules, and nothing of the home network. It's a logged UniFi change with
// undo; the device is disconnected once so it reconnects there. "Back to home network" undoes it.

var guestNetCache struct {
	sync.Mutex
	at       time.Time
	id, name string
}

// guestNet: the network of the guest Wi-Fi (UniFi's own guest networks, or one picked on the Guest Wi-Fi page).
func (a *App) guestNet() (id, name string) {
	guestNetCache.Lock()
	defer guestNetCache.Unlock()
	if time.Since(guestNetCache.at) < 10*time.Minute {
		return guestNetCache.id, guestNetCache.name
	}
	if !a.UniFi.Connected() {
		return "", ""
	}
	guestNetCache.at = time.Now()
	guestNetCache.id, guestNetCache.name = "", ""
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/wlanconf", false))
	if st != 200 {
		return "", ""
	}
	for _, w := range unifi.List(d) {
		if w["enabled"] == false || !a.UniFi.IsGuestWLAN(w) {
			continue
		}
		nid := str(w, "networkconf_id")
		if nid == "" {
			continue
		}
		name := str(w, "name")
		if st, d := a.UniFi.Get(a.UniFi.SitePath("rest/networkconf/"+nid, false)); st == 200 {
			if l := unifi.List(d); len(l) > 0 {
				name = orStr(str(l[0], "name"), name)
			}
		}
		guestNetCache.id, guestNetCache.name = nid, name
		break
	}
	return guestNetCache.id, guestNetCache.name
}

func (a *App) guestNetID() string   { id, _ := a.guestNet(); return id }
func (a *App) guestNetName() string { _, n := a.guestNet(); return n }

// modelLabel: "iPhone 16 Pro" for a model identifier HomeOwl knows, else the identifier.
func modelLabel(id string) string {
	if id == "" {
		return ""
	}
	return orStr(modelName(id), id)
}

// unifiUser: the device's UniFi client record id (and name).
func (a *App) unifiUser(mac string) (id, name string) {
	if list, err := a.onlineClients(time.Minute); err == nil {
		for _, c := range list {
			if strings.EqualFold(str(c, "mac"), mac) {
				id, name = str(c, "user_id"), str(c, "name")
			}
		}
	}
	if id == "" {
		if st, d := a.UniFi.Get(a.UniFi.SitePath("rest/user", false)); st == 200 {
			for _, c := range unifi.List(d) {
				if strings.EqualFold(str(c, "mac"), mac) {
					id, name = str(c, "_id"), str(c, "name")
				}
			}
		}
	}
	return id, name
}

func (a *App) runOK(act unifi.Action, by string) error {
	res, err := a.Changes.Run(a.UniFi, act, false, by, false)
	if err != nil {
		return err
	}
	if ch, ok := res["result"].(unifi.Change); ok && !ch.OK {
		return errors.New(ch.Error)
	}
	return nil
}

// moveToGuest puts a new device on the guest network and lets it on there (it's no longer waiting for approval).
func (a *App) moveToGuest(mac, by string) error {
	if !macRE.MatchString(mac) {
		return errors.New("that isn't a device address (MAC)")
	}
	if x, ok := a.checkins.Registered(mac); ok {
		return errors.New("it's checked in as " + x.Name + ", a family device")
	}
	nid, nname := a.guestNet()
	if nid == "" {
		return errors.New("no guest network found in UniFi")
	}
	uid, uname := a.unifiUser(mac)
	if uid == "" {
		return errors.New("UniFi doesn't list it as a known client yet")
	}
	label := orStr(uname, mac)
	act := unifi.Action{"kind": "rest", "resource": "user", "id": uid,
		"fields":      map[string]any{"virtual_network_override_enabled": true, "virtual_network_override_id": nid},
		"description": "Guest on the home Wi-Fi: " + label + " goes on the " + nname + " network"}
	if err := a.runOK(act, by); err != nil {
		return err
	}
	as := a.approval
	as.mu.Lock()
	st := as.load()
	_, blocked := st.Blocked[mac]
	if g, ok := st.Grace[mac]; ok {
		label = orStr(uname, g.Name, g.Hostname, mac)
	}
	delete(st.Grace, mac)
	delete(st.Blocked, mac)
	if st.Seeded > 0 {
		st.Approved[mac] = time.Now().Unix()
	}
	if st.Guests == nil {
		st.Guests = map[string]guestDevice{}
	}
	st.Guests[mac] = guestDevice{MAC: mac, Name: label, At: time.Now().Unix(), Network: nname, By: by}
	err := as.save(st)
	as.mu.Unlock()
	if err != nil {
		return err
	}
	if blocked {
		_ = a.runOK(unifi.Action{"kind": "stacmd", "cmd": "unblock-sta", "mac": mac, "description": "Guest on the home Wi-Fi: let " + label + " on"}, by)
	}
	// reconnect, so it gets an address on the guest network
	_ = a.runOK(unifi.Action{"kind": "stacmd", "cmd": "kick-sta", "mac": mac, "description": "Guest on the home Wi-Fi: reconnect " + label}, by)
	a.Alerts.Add(alerts.Alert{Kind: "guest_moved", Level: "info", Title: label + " is on the guest network",
		Body: "It joined the home Wi-Fi, and now gets the internet through " + nname + " only. Devices › New devices › Guests can move it back.",
		Data: map[string]any{"mac": mac, "name": label}})
	return nil
}

// moveFromGuest puts it back on the network of the Wi-Fi it joined (the override comes off).
func (a *App) moveFromGuest(mac, by string) error {
	as := a.approval
	as.mu.Lock()
	g, ok := as.load().Guests[mac]
	as.mu.Unlock()
	if !ok {
		return errors.New("HomeOwl didn't put it on the guest network")
	}
	uid, _ := a.unifiUser(mac)
	if uid == "" {
		return errors.New("UniFi doesn't list it any more")
	}
	act := unifi.Action{"kind": "rest", "resource": "user", "id": uid, "fields": map[string]any{"virtual_network_override_enabled": false},
		"description": "Guest on the home Wi-Fi: " + orStr(g.Name, mac) + " back on the home network"}
	if err := a.runOK(act, by); err != nil {
		return err
	}
	as.mu.Lock()
	st := as.load()
	delete(st.Guests, mac)
	err := as.save(st)
	as.mu.Unlock()
	_ = a.runOK(unifi.Action{"kind": "stacmd", "cmd": "kick-sta", "mac": mac, "description": "Reconnect " + orStr(g.Name, mac)}, by)
	return err
}
