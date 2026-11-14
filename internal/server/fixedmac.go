package server

import (
	"log"
	"strings"
	"time"
)

// Real Wi-Fi addresses. A profile type's Wi-Fi networks go into the device's profile with "use the real address
// here" (DisableAssociationMACRandomization), so phones stop changing their address on the home Wi-Fi and the
// owner a check-in gave them sticks. Installing it changes the address once: the device comes back with its real
// one. HomeOwl follows that move — the same host name, a real (not private) address, the old one gone — and
// carries the check-in, the person and the device type over to it.

const fixedFollowFor = 14 * 24 * time.Hour // stop looking after this long

// fixedDownloaded notes a download that will move the device to another address: its real one (on a network with
// the real-address setting), or — from the setup Wi-Fi — the one it gets on the home Wi-Fi (onboard.go).
func (a *App) fixedDownloaded(x CheckIn, c map[string]any, wifi []wifiNet) {
	fixed, home := false, false
	for _, w := range wifi {
		fixed = fixed || w.Fixed
		home = home || !w.NoJoin
	}
	moves := x.Onboard && home && a.onSetupWiFi(c) // the setup Wi-Fi's address is never the home one
	if !fixed && !moves {
		return
	}
	mac, host := strings.ToLower(str(c, "mac")), str(c, "hostname")
	a.checkins.Edit(x.ID, func(y *CheckIn) {
		if !privateMACAddr(mac) && !moves { // already its real address: nothing will move
			y.Fixed, y.FixedFrom, y.FixedHost, y.FixedAt = true, "", "", 0
			return
		}
		y.Fixed, y.FixedFrom, y.FixedHost, y.FixedAt = false, mac, host, time.Now().Unix()
	})
}

// fixedPending: is some device still expected to come back with its real address?
func (a *App) fixedPending() bool {
	for _, x := range a.checkins.List() {
		if x.FixedFrom != "" && x.Status == "approved" {
			return true
		}
	}
	return false
}

// followFixed looks at the clients online now for devices that came back with their real address.
func (a *App) followFixed(online []map[string]any) {
	now := time.Now()
	onNow := map[string]bool{}
	for _, c := range online {
		onNow[strings.ToLower(str(c, "mac"))] = true
	}
	for _, x := range a.checkins.List() {
		if x.FixedFrom == "" || x.Status != "approved" {
			continue
		}
		if now.Sub(time.Unix(x.FixedAt, 0)) > fixedFollowFor || x.FixedHost == "" {
			// no host name to recognise it by, or it never came back: stop looking (it can check in again)
			a.checkins.Edit(x.ID, func(y *CheckIn) { y.FixedFrom, y.FixedHost, y.FixedAt = "", "", 0 })
			continue
		}
		if onNow[x.FixedFrom] { // still on its old address: not installed yet (or not reconnected)
			continue
		}
		var found []string
		for _, c := range online {
			mac := strings.ToLower(str(c, "mac"))
			if mac == "" || mac == x.FixedFrom || (privateMACAddr(mac) && !x.Onboard) || c["is_wired"] == true || c["is_guest"] == true ||
				!strings.EqualFold(str(c, "hostname"), x.FixedHost) {
				continue
			}
			if y, taken := a.checkins.Registered(mac); taken && y.ID != x.ID {
				continue
			}
			found = append(found, mac)
		}
		if len(found) != 1 { // none yet, or two devices with the same name: wait rather than guess
			continue
		}
		a.fixedMove(x, found[0])
	}
}

// fixedMove carries the check-in over to the device's real address.
func (a *App) fixedMove(x CheckIn, mac string) {
	if x.PersonID != "" && a.An.Open() {
		if err := a.An.AssignDevice(x.PersonID, "mac", mac); err != nil {
			log.Printf("real Wi-Fi address: couldn't give %s to its person: %v", x.Name, err)
			return
		}
	}
	if k := checkinType[x.Kind]; k != "" {
		_ = a.cliTypes.Set(mac, k, true)
	}
	a.checkins.Edit(x.ID, func(y *CheckIn) {
		y.MAC, y.Fixed, y.FixedFrom, y.FixedHost, y.FixedAt = mac, !privateMACAddr(mac), "", "", 0
	})
	log.Printf("check-in: %s moved to its home Wi-Fi address", x.Name)
}
