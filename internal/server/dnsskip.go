package server

import (
	"encoding/xml"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/h4ux/homeowl/internal/config"
)

// Wi-Fi networks where the profile's NextDNS stands down. A plane's (or hotel's) sign-in page appears by answering
// ordinary DNS; encrypted DNS can't reach NextDNS before signing in, so the page never loads. The DNS payload's
// on-demand rules (iOS 14+, macOS 11+) say "not on these networks" (Disconnect + SSIDMatch, exact names), then
// "everywhere else" (Connect). A global list (Settings › Device profiles) starts with common in-flight networks;
// a profile type can add its own, and can also keep NextDNS to the home Wi-Fi and mobile data only.

// defaultSkipWiFi: the starting list — in-flight Wi-Fi names checked against the airline's own pages (British
// Airways: "BAWi-Fi", britishairways.com › Onboard Wi-Fi). Add others in Settings once you've seen their exact name.
var defaultSkipWiFi = []string{"BAWi-Fi"}

const maxSkipWiFi = 100

// skipWiFiGlobal is the global list: the starting list until it's been saved once (an empty saved list is none).
func skipWiFiGlobal(c config.Config) []string {
	if c.Onboard.SkipWiFi == nil {
		return defaultSkipWiFi
	}
	return c.Onboard.SkipWiFi
}

// cleanSSIDs trims, drops empty and repeated names, and checks each is a possible Wi-Fi name.
func cleanSSIDs(v any) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range anyArr(v) {
		ssid, _ := x.(string)
		ssid = strings.TrimSpace(ssid)
		if ssid == "" || seen[ssid] {
			continue
		}
		if utf8.RuneCountInString(ssid) > 32 || strings.ContainsAny(ssid, "\x00\r\n\t") {
			return nil, errors.New("“" + ssid + "” isn't a Wi-Fi name (up to 32 characters)")
		}
		seen[ssid] = true
		out = append(out, ssid)
	}
	if len(out) > maxSkipWiFi {
		return nil, errors.New("up to 100 networks")
	}
	return out, nil
}

// saveSkipWiFi saves the global list ("reset" brings back the starting list).
func (a *App) saveSkipWiFi(body map[string]any) (map[string]any, error) {
	var l []string
	if !b(body, "reset") {
		var err error
		if l, err = cleanSSIDs(body["ssids"]); err != nil {
			return nil, err
		}
	}
	if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.SkipWiFi = l }); err != nil {
		return nil, err
	}
	return a.devTypesView()
}

// dnsRules is what the device's DNS payload gets: networks to stand down on, and — with "home Wi-Fi and mobile
// data only" — the home networks it stays on.
type dnsRules struct {
	Skip     []string // Disconnect on these
	HomeOnly bool     // on Wi-Fi, only the Home networks
	Home     []string
}

func (r dnsRules) empty() bool { return len(r.Skip) == 0 && !r.HomeOnly }

func (a *App) dnsRulesFor(t ProfileType, has bool, all []map[string]any) dnsRules {
	r := dnsRules{}
	seen := map[string]bool{}
	add := func(l []string) {
		for _, s := range l {
			if !seen[s] {
				seen[s] = true
				r.Skip = append(r.Skip, s)
			}
		}
	}
	add(skipWiFiGlobal(a.Cfg.Get()))
	if has {
		add(t.SkipWiFi)
		if t.OtherWiFi == "off" {
			r.HomeOnly = true
			for _, w := range all {
				if en, ok := w["enabled"].(bool); (!ok || en) && !a.UniFi.IsGuestWLAN(w) && !seen[str(w, "name")] {
					r.Home = append(r.Home, str(w, "name"))
				}
			}
			sort.Strings(r.Home)
		}
	}
	return r
}

// onDemandXML writes the rules: first the networks to skip, then (home only) the home Wi-Fi on, other Wi-Fi off,
// and last "on" for everything else (mobile data, Ethernet, other Wi-Fi).
func onDemandXML(p plistW, r dnsRules) {
	ssids := func(l []string) {
		p.b.WriteString("<key>SSIDMatch</key><array>")
		for _, s := range l {
			p.b.WriteString("<string>")
			_ = xml.EscapeText(p.b, []byte(s))
			p.b.WriteString("</string>")
		}
		p.b.WriteString("</array>\n")
	}
	p.b.WriteString("<key>OnDemandRules</key><array>\n")
	if len(r.Skip) > 0 {
		p.b.WriteString("<dict>\n")
		p.str("Action", "Disconnect")
		ssids(r.Skip)
		p.b.WriteString("</dict>\n")
	}
	if r.HomeOnly {
		if len(r.Home) > 0 {
			p.b.WriteString("<dict>\n")
			p.str("Action", "Connect")
			ssids(r.Home)
			p.b.WriteString("</dict>\n")
		}
		p.b.WriteString("<dict>\n")
		p.str("Action", "Disconnect")
		p.str("InterfaceTypeMatch", "WiFi")
		p.b.WriteString("</dict>\n")
	}
	p.b.WriteString("<dict>\n")
	p.str("Action", "Connect")
	p.b.WriteString("</dict>\n</array>\n")
}
