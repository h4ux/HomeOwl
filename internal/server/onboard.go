package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/config"
)

// The setup Wi-Fi (Settings › Device profiles): a network (open, or with its own password) whose portal opens the check-in page and that has
// no internet of its own. A device on it may check in (other guest networks may not), waits there for an admin's
// approval, and gets its profile. The profile joins the home Wi-Fi by itself and lists the setup Wi-Fi as "don't
// join by itself", so the device moves over and doesn't come back. Android and Windows get the home Wi-Fi's name
// and password on their page instead, and are told to forget the setup Wi-Fi.

// onSetupWiFi: is this UniFi client on the setup Wi-Fi?
func (a *App) onSetupWiFi(c map[string]any) bool {
	ssid := a.Cfg.Get().Onboard.SSID
	return ssid != "" && c != nil && strings.EqualFold(str(c, "essid"), ssid)
}

func (a *App) saveOnboard(body map[string]any) (map[string]any, error) {
	ssid := strings.TrimSpace(s(body, "ssid"))
	if utf8.RuneCountInString(ssid) > 32 || strings.ContainsAny(ssid, "\x00\r\n") {
		return nil, errors.New("a Wi-Fi name is up to 32 characters")
	}
	if ssid != "" {
		for _, t := range a.devTypes.List() {
			if all, err := a.wlans(); err == nil {
				for _, w := range wifiFor(all, t.WiFi, false) {
					if strings.EqualFold(w.SSID, ssid) {
						return nil, errors.New(ssid + " is a network the profiles join (type " + t.Name + "); the setup Wi-Fi has to be another one")
					}
				}
			}
		}
	}
	if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.SSID = ssid }); err != nil {
		return nil, err
	}
	return a.devTypesView()
}

// checkinAlert tells the admins a device is waiting (with the macOS/ntfy/webhook channels they chose).
func (a *App) checkinAlert(x CheckIn) {
	who := x.PersonName
	if who == "" && x.PersonID != "" && a.An.Open() {
		for _, p := range a.An.People() {
			if p.ID == x.PersonID {
				who = p.Name
			}
		}
	}
	body := "Approving names it “" + x.Name + "” in UniFi and lets it on the network — one approval, here or in Devices › New devices."
	if who != "" {
		body = "For " + who + ". " + body
	}
	if x.Onboard {
		body += " It's on the setup Wi-Fi and waits there until you approve it."
	}
	a.Alerts.Add(alerts.Alert{Kind: "checkin", Level: "info", Title: x.Name + " is waiting for approval", Body: body,
		Data: map[string]any{"mac": x.MAC, "name": x.Name, "checkin": x.ID}})
}

// hiWiFi gives an approved device the home Wi-Fi networks its profile type has, with their passwords: for Android
// and Windows, which take no profile. Same rules as the profile itself (the device, on the home or setup network,
// approved).
func (a *App) hiWiFi(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "use GET")
		return
	}
	c, mac, ok := a.hiDevice(w, r)
	if !ok {
		return
	}
	x, ok := a.checkins.Registered(mac)
	if !ok {
		fail(w, 404, "This device isn't approved yet.")
		return
	}
	t, has := a.typeFor(x)
	nets := []map[string]any{}
	if has && len(t.WiFi) > 0 {
		all, err := a.wlans()
		if err != nil {
			fail(w, 503, "Couldn't read the Wi-Fi settings — try again in a minute.")
			return
		}
		for _, n := range wifiFor(all, t.WiFi, false) {
			sec := "WPA"
			if n.Enc == "None" {
				sec = "nopass"
			}
			nets = append(nets, map[string]any{"ssid": n.SSID, "password": n.Password, "security": sec, "hidden": n.Hidden})
		}
	}
	if len(nets) > 0 && x.Onboard && a.onSetupWiFi(c) { // it's about to move to the home Wi-Fi: follow it there
		a.fixedDownloaded(x, c, []wifiNet{{SSID: str(nets[0], "ssid")}})
	}
	res := map[string]any{"networks": nets}
	if ssid := a.Cfg.Get().Onboard.SSID; x.Onboard && ssid != "" {
		res["leave"] = ssid
	}
	send(w, 200, res)
}
