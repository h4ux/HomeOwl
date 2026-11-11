package server

import (
	"errors"
	"regexp"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

var wlanIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// guestWiFi returns the guest networks with their passwords (for the join QR code and the printed card).
// Passwords of other Wi-Fi networks never leave the server; admins also get the list of all SSIDs to pick from.
func (a *App) guestWiFi(admin bool) (map[string]any, error) {
	if !a.UniFi.Connected() {
		return nil, errors.New("not connected to UniFi")
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/wlanconf", false))
	if st != 200 {
		return nil, errors.New("couldn't read the Wi-Fi settings: " + unifi.ErrText(d))
	}
	picked := map[string]bool{}
	for _, id := range a.Cfg.Get().Guest.SSIDs {
		picked[id] = true
	}
	guests, all := []map[string]any{}, []map[string]any{}
	for _, w := range unifi.List(d) {
		id, _ := w["_id"].(string)
		name, _ := w["name"].(string)
		isGuest := a.UniFi.IsGuestWLAN(w)
		all = append(all, map[string]any{"id": id, "ssid": name, "guest": isGuest, "picked": picked[id], "auto": isGuest && !picked[id]})
		if !isGuest {
			continue
		}
		sec, _ := w["security"].(string)
		pw, _ := w["x_passphrase"].(string)
		qrSec := "nopass"
		if sec == "wpapsk" {
			qrSec = "WPA"
			if w3, _ := w["wpa3_support"].(bool); w3 {
				if tr, _ := w["wpa3_transition"].(bool); !tr {
					qrSec = "SAE" // WPA3 only
				}
			}
		} else if sec != "open" {
			qrSec = "" // enterprise: no shared password, no join code
			pw = ""
		}
		hidden, _ := w["hide_ssid"].(bool)
		enabled, ok := w["enabled"].(bool)
		guests = append(guests, map[string]any{"id": id, "ssid": name, "security": qrSec, "password": pw, "hidden": hidden,
			"enabled": !ok || enabled, "can_rotate": sec == "wpapsk"})
	}
	res := map[string]any{"networks": guests, "qr_theme": orDefault(a.Cfg.Get().Guest.QRTheme, "rounded")}
	if admin {
		res["all"] = all
	}
	return res, nil
}

func (a *App) pickGuestSSIDs(body map[string]any) error {
	raw, _ := body["ids"].([]any)
	ids := []string{}
	for _, v := range raw {
		if s, ok := v.(string); ok && wlanIDRE.MatchString(s) && len(ids) < 32 {
			ids = append(ids, s)
		}
	}
	return a.Cfg.Update(func(c *config.Config) { c.Guest.SSIDs = ids })
}

var qrThemes = map[string]bool{"classic": true, "rounded": true, "blue": true, "night": true, "fridge": true}

// setQRTheme saves the style of the guest join code (admins).
func (a *App) setQRTheme(body map[string]any) (any, error) {
	t := s(body, "theme")
	if !qrThemes[t] {
		return nil, errors.New("unknown code style")
	}
	if err := a.Cfg.Update(func(c *config.Config) { c.Guest.QRTheme = t }); err != nil {
		return nil, err
	}
	return map[string]any{"qr_theme": t}, nil
}
