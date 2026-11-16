package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
)

// Device keys. A phone's private Wi-Fi address can change (iOS "Rotating", a new network, a reset), and the
// check-in page knows a device only by its address — so an approved iPhone could open its own Home Screen icon and
// be asked to check in again. The icon in the device's profile therefore opens /hi?k=<key>: a key only that
// device's profile carries (an HMAC of its check-in under HomeOwl's local secret, so nothing new is stored and it
// stays the same across downloads). Opened from a new address, the key moves the device's check-in there — the
// person, its icon, Who's home, its approval and its UniFi name follow — instead of asking again.

// deviceKey is the check-in's key ("" when the local secret isn't there).
func (a *App) deviceKey(x CheckIn) string {
	k := fingerprintKey(a.DataDir)
	if len(k) == 0 || x.ID == "" {
		return ""
	}
	m := hmac.New(sha256.New, k)
	m.Write([]byte("hi-device-key/" + x.ID))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// checkinByKey finds the approved check-in a key belongs to.
func (a *App) checkinByKey(key string) (CheckIn, bool) {
	if len(key) != 32 {
		return CheckIn{}, false
	}
	for _, x := range a.checkins.List() {
		if x.Status == "approved" && hmac.Equal([]byte(a.deviceKey(x)), []byte(key)) {
			return x, true
		}
	}
	return CheckIn{}, false
}

// keyFollow: the device opened its page with its key from address mac (client c). If that's a new address and no
// other approved device has it, the check-in moves there.
func (a *App) keyFollow(key string, c map[string]any, mac string) {
	x, ok := a.checkinByKey(key)
	if !ok || mac == "" || x.MAC == mac {
		return
	}
	if y, taken := a.checkins.Registered(mac); taken && y.ID != x.ID {
		return
	}
	// a device is in one place at a time: while its old address is still online, the key opened elsewhere (a shared
	// bookmark) moves nothing
	if list, err := a.onlineClients(staFresh); err != nil || findClientMAC(list, x.MAC) {
		return
	}
	old := x.MAC
	a.fixedMove(x, mac)
	uid := orStr(str(c, "user_id"), str(c, "_id"))
	a.checkins.Edit(x.ID, func(y *CheckIn) {
		if uid != "" {
			y.UserID = uid
		}
	})
	if err := a.approvalSettle(mac, x.Name, "device key"); err != nil {
		log.Printf("device key: letting %s on: %v", x.Name, err)
	}
	log.Printf("device key: %s came back with another Wi-Fi address (was %s)", x.Name, old)
	moved, _ := a.checkins.Get(x.ID)
	go func() {
		if err := a.checkinRename(moved, "device key"); err != nil {
			log.Printf("device key: naming %s in UniFi: %v", x.Name, err)
		}
	}()
}

func findClientMAC(list []map[string]any, mac string) bool {
	for _, c := range list {
		if mac != "" && strings.EqualFold(str(c, "mac"), mac) {
			return true
		}
	}
	return false
}
