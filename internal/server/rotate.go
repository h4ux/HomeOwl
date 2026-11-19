package server

import (
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
)

// Rotating private addresses. iOS 18 and macOS 15 can rotate a phone's private Wi-Fi address on a network (about every
// two weeks), and a phone whose profile didn't turn that off (no home Wi-Fi in it, or the owner chose Rotating) then
// comes back as a stranger: Who's home loses it, its person and name stay on the old address, and with device
// approval on it's blocked — and a blocked phone can't even open its own page to say who it is.
//
// So HomeOwl remembers the host name each checked-in device's address had online. When a new private address
// appears with that same, specific host name ("Alexs-iPhone", not just "iPhone") while the old address is gone, and
// exactly one checked-in device fits, it's that device: the check-in, its person and type move over, and the admins
// get an alert naming the old and new address (block it from the device list if it isn't). A device whose old
// address is still online, or that last showed that name more than three weeks ago, isn't followed.

const rotateFollowFor = 21 * 24 * time.Hour

// host names too common to say which device it is
var genericHostRE = regexp.MustCompile(`(?i)^(i?phone|ipad|ipod|imac|mac|macbook(-?(air|pro))?|mac-?(mini|studio|pro)|apple-?(tv|watch)|android(-[0-9a-f]+)?|galaxy[-\w]*|pixel[-\w]*|localhost|unknown|esp[-_][0-9a-f]+|[0-9a-f]{12})$`)

func specificHost(h string) bool {
	h = strings.TrimSpace(h)
	return len(h) >= 4 && !genericHostRE.MatchString(h)
}

// followRotated runs with the presence check (once a minute), before device approval and the new-device alert.
func (a *App) followRotated(online []map[string]any, now time.Time) {
	onNow := map[string]bool{}
	hosts := map[string]int{} // how many devices online have each host name
	for _, c := range online {
		onNow[strings.ToLower(str(c, "mac"))] = true
		if h := strings.ToLower(str(c, "hostname")); h != "" {
			hosts[h]++
		}
	}
	list := a.checkins.List()
	byMAC := map[string]CheckIn{}
	for _, x := range list {
		if x.Status == "approved" {
			byMAC[x.MAC] = x
		}
	}
	for _, c := range online { // remember each checked-in device's host name
		mac, h := strings.ToLower(str(c, "mac")), str(c, "hostname")
		x, ok := byMAC[mac]
		if !ok || h == "" || (x.Host == h && now.Unix()-x.HostAt < 3600) {
			continue
		}
		a.checkins.Edit(x.ID, func(y *CheckIn) { y.Host, y.HostAt = h, now.Unix() })
	}
	for _, c := range online {
		mac, h := strings.ToLower(str(c, "mac")), str(c, "hostname")
		if _, known := byMAC[mac]; known || !privateMACAddr(mac) || c["is_wired"] == true || !specificHost(h) || hosts[strings.ToLower(h)] != 1 {
			continue
		}
		var fit []CheckIn
		for _, x := range list {
			if x.Status == "approved" && x.MAC != mac && privateMACAddr(x.MAC) && !onNow[x.MAC] && strings.EqualFold(x.Host, h) &&
				now.Sub(time.Unix(x.HostAt, 0)) <= rotateFollowFor {
				fit = append(fit, x)
			}
		}
		if len(fit) != 1 {
			continue
		}
		x, old := fit[0], fit[0].MAC
		a.fixedMove(x, mac)
		a.checkins.Edit(x.ID, func(y *CheckIn) {
			y.Host, y.HostAt = h, now.Unix()
			if uid := orStr(str(c, "user_id"), str(c, "_id")); uid != "" {
				y.UserID = uid
			}
		})
		if err := a.approvalSettle(mac, x.Name, "rotated address"); err != nil {
			log.Printf("rotated address: letting %s on: %v", x.Name, err)
		}
		log.Printf("rotated address: %s came back with a new private Wi-Fi address (was %s)", x.Name, old)
		a.Alerts.Add(alerts.Alert{Kind: "rotated", Level: "info", Title: x.Name + " has a new Wi-Fi address",
			Body: "Its private Wi-Fi address changed (" + old + " → " + mac + "), with the same name " + h + ", so HomeOwl kept it as " +
				x.Name + ". If that isn't it, block " + mac + " in the device list. To stop the changes, set Private Wi-Fi Address to Off or Fixed for this network on the device.",
			Data: map[string]any{"mac": mac, "name": x.Name}})
		moved, _ := a.checkins.Get(x.ID)
		go func() {
			if err := a.checkinRename(moved, "rotated address"); err != nil {
				log.Printf("rotated address: naming %s in UniFi: %v", x.Name, err)
			}
		}()
		byMAC[mac] = moved
	}
}
