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
		if now.Sub(time.Unix(x.FixedAt, 0)) > fixedFollowFor || (x.FixedHost == "" && !x.Onboard) {
			// no host name to recognise it by, or it never came back: stop looking (it can check in again)
			a.checkins.Edit(x.ID, func(y *CheckIn) { y.FixedFrom, y.FixedHost, y.FixedAt = "", "", 0 })
			continue
		}
		if onNow[x.FixedFrom] && !x.Onboard { // still on its old address: not installed yet (or not reconnected)
			continue // (from the setup Wi-Fi the old entry can linger after it left; a new one elsewhere is the move)
		}
		var found []string
		for _, c := range online {
			mac := strings.ToLower(str(c, "mac"))
			if mac == "" || mac == x.FixedFrom || (privateMACAddr(mac) && !x.Onboard) || c["is_wired"] == true || c["is_guest"] == true || a.onSetupWiFi(c) ||
				x.FixedHost == "" || !strings.EqualFold(str(c, "hostname"), x.FixedHost) {
				continue
			}
			if y, taken := a.checkins.Registered(mac); taken && y.ID != x.ID {
				continue
			}
			found = append(found, mac)
		}
		if len(found) == 0 && x.Onboard {
			found = a.onboardArrival(x, online, now)
		}
		if len(found) != 1 { // none yet, or two devices with the same name: wait rather than guess
			continue
		}
		a.fixedMove(x, found[0])
		if err := a.approvalSettle(found[0], x.Name, "check-in"); err != nil {
			log.Printf("check-in: letting %s on: %v", x.Name, err)
		}
	}
}

// onboardArrival: a device that installed its profile on the setup Wi-Fi comes to the home Wi-Fi with a new address,
// and its host name ("iPhone") often says nothing. If it's the only device waiting for that move (in the last two
// hours), and exactly one stranger joined the home Wi-Fi since, that's it.
func (a *App) onboardArrival(x CheckIn, online []map[string]any, now time.Time) []string {
	if now.Sub(time.Unix(x.FixedAt, 0)) > 2*time.Hour {
		return nil
	}
	for _, y := range a.checkins.List() {
		if y.ID != x.ID && y.Status == "approved" && y.Onboard && y.FixedFrom != "" && now.Sub(time.Unix(y.FixedAt, 0)) <= 2*time.Hour {
			return nil // two waiting: can't tell which is which
		}
	}
	var found []string
	for _, c := range online {
		mac := strings.ToLower(str(c, "mac"))
		if mac == "" || mac == x.FixedFrom || c["is_wired"] == true || c["is_guest"] == true || a.onSetupWiFi(c) {
			continue
		}
		if _, known := a.checkins.Registered(mac); known {
			continue
		}
		first, ok := a.firstOnHome(mac, c)
		if !ok || first < x.FixedAt-120 {
			continue
		}
		found = append(found, mac)
	}
	return found
}

// firstOnHome: when this address first joined, from device approval's grace list or UniFi's first-seen time.
func (a *App) firstOnHome(mac string, c map[string]any) (int64, bool) {
	a.approval.mu.Lock()
	st := a.approval.load()
	a.approval.mu.Unlock()
	if st.Approved[mac] > 0 {
		return 0, false // approved before: not a new arrival
	}
	if g, ok := st.Grace[mac]; ok {
		return g.At, true
	}
	if d, ok := st.Blocked[mac]; ok {
		return d.At, true
	}
	if v, ok := c["first_seen"].(float64); ok && v > 0 {
		return int64(v), true
	}
	return 0, false
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
		if y.MAC != "" && y.MAC != mac && !contains(y.OldMACs, y.MAC) {
			y.OldMACs = append(y.OldMACs, y.MAC)
			if len(y.OldMACs) > 20 {
				y.OldMACs = y.OldMACs[len(y.OldMACs)-20:]
			}
		}
		y.MAC, y.Fixed, y.FixedFrom, y.FixedHost, y.FixedAt = mac, !privateMACAddr(mac), "", "", 0
	})
	a.movePresenceTag(x.MAC, mac)
	log.Printf("check-in: %s moved to its home Wi-Fi address", x.Name)
}
