package server

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/h4ux/homeowl/internal/unifi"
)

// Wi-Fi sign-in check (Settings › Device profiles): a read-only look at what the UniFi console offers for telling
// devices apart by the Wi-Fi login they use instead of their MAC — private passwords per person (PPSK) or
// WPA2-Enterprise with UniFi's RADIUS server — and for an onboarding network with a captive portal. It only reads
// (GET), and it never returns a password, a secret or a login name: just which settings exist, whether they're on,
// and counts.

var identKeyRE = regexp.MustCompile(`(?i)1x|identity|ppsk|preshared|psk_|radius|eap|^user$|username`)

type identCheck struct {
	Version  string           `json:"version,omitempty"`
	WLANs    []map[string]any `json:"wlans"`
	PPSK     map[string]any   `json:"ppsk"`
	RADIUS   map[string]any   `json:"radius"`
	Portal   map[string]any   `json:"portal"`
	Clients  map[string]any   `json:"clients"`
	Networks int              `json:"networks"`
	Verdict  []map[string]any `json:"verdict"`
	Errors   []string         `json:"errors,omitempty"`
}

func (a *App) wifiIdentCheck() (*identCheck, error) {
	if !a.UniFi.Connected() {
		return nil, errors.New("not connected to UniFi")
	}
	r := &identCheck{WLANs: []map[string]any{}, PPSK: map[string]any{}, RADIUS: map[string]any{}, Portal: map[string]any{}, Clients: map[string]any{}}
	get := func(name, path string) []map[string]any {
		st, d := a.UniFi.Get(a.UniFi.SitePath(path, false))
		if st != 200 {
			r.Errors = append(r.Errors, name+": "+unifi.ErrText(d))
			return nil
		}
		return unifi.List(d)
	}
	if si := get("version", "stat/sysinfo"); len(si) > 0 {
		r.Version = str(si[0], "version")
	}
	nets := get("networks", "rest/networkconf")
	netName := map[string]string{}
	for _, n := range nets {
		netName[str(n, "_id")] = str(n, "name")
		if str(n, "purpose") != "wan" && str(n, "purpose") != "remote-user-vpn" && str(n, "purpose") != "site-vpn" {
			r.Networks++
		}
	}

	// Wi-Fi networks: security, PPSK and RADIUS settings (never the passwords)
	ppskSeen, ppskOn, ppskKeys, enterprise := false, 0, 0, 0
	ppskNets := map[string]bool{}
	ppskSSID := map[string]bool{}
	for _, w := range get("wifi", "rest/wlanconf") {
		sec := str(w, "security")
		row := map[string]any{"name": str(w, "name"), "security": sec, "wpa_mode": str(w, "wpa_mode"), "enabled": w["enabled"] != false,
			"guest": a.UniFi.IsGuestWLAN(w), "network": netName[str(w, "networkconf_id")]}
		if v, ok := w["wpa3_support"].(bool); ok {
			row["wpa3"] = v
		}
		if v, ok := w["wpa3_transition"].(bool); ok {
			row["wpa3_transition"] = v
		}
		for k, v := range w {
			lk := strings.ToLower(k)
			if !strings.Contains(lk, "preshared") && !strings.Contains(lk, "ppsk") {
				continue
			}
			ppskSeen = true
			switch x := v.(type) {
			case bool:
				if x {
					row["ppsk"] = true
					ppskSSID[str(w, "name")] = true
					ppskOn++
				}
			case []any:
				row["ppsk_keys"] = len(x)
				ppskKeys += len(x)
				for _, e := range x {
					if m, ok := e.(map[string]any); ok {
						if id := str(m, "networkconf_id"); id != "" {
							ppskNets[id] = true
						}
					}
				}
			}
		}
		if sec == "wpaeap" {
			enterprise++
			row["radius_profile"] = str(w, "radiusprofile_id") != ""
		}
		if w["hotspot2conf_id"] != nil || w["is_guest"] == true {
			row["portal_capable"] = true
		}
		r.WLANs = append(r.WLANs, row)
	}
	sort.Slice(r.WLANs, func(i, j int) bool { return str(r.WLANs[i], "name") < str(r.WLANs[j], "name") })
	r.PPSK = map[string]any{"offered": ppskSeen, "networks_on": ppskOn, "keys": ppskKeys, "key_vlans": len(ppskNets)}

	// RADIUS: the built-in server, its user accounts (a count), RADIUS profiles
	var radiusOn any
	guest := map[string]any{}
	for _, s := range get("settings", "rest/setting") {
		switch str(s, "key") {
		case "radius":
			radiusOn = s["enabled"] == true
			r.RADIUS["accounting"] = s["accounting_enabled"] == true
		case "guest_access":
			guest = s
		}
	}
	r.RADIUS["server_on"] = radiusOn
	accts := get("RADIUS users", "rest/account")
	r.RADIUS["users"] = len(accts)
	vlans := 0
	for _, u := range accts {
		if str(u, "vlan") != "" || u["vlan"] != nil {
			vlans++
		}
	}
	r.RADIUS["users_with_vlan"] = vlans
	r.RADIUS["profiles"] = len(get("RADIUS profiles", "rest/radiusprofile"))
	r.RADIUS["enterprise_wifi"] = enterprise

	// guest portal: on, how guests sign in, an external portal set (no passwords or vouchers)
	r.Portal = map[string]any{"enabled": guest["portal_enabled"] == true, "auth": str(guest, "auth"),
		"external": str(guest, "custom_ip") != "" || str(guest, "portal_customized_url") != "" || strings.Contains(str(guest, "auth"), "custom"),
		"redirect": guest["redirect_enabled"] == true, "allowed_subnets": len(anyList(guest["allowed_subnet"])) + len(anyList(guest["restricted_subnet"]))}
	for _, k := range []string{"custom_ip", "auth", "expire", "portal_use_hostname", "portal_hostname"} {
		if _, ok := guest[k]; ok {
			r.Portal["has_"+k] = true
		}
	}

	// clients: which identity fields UniFi records, and on how many clients (names only, never the values)
	fields := map[string]int{}
	cls := get("clients", "stat/sta")
	wifiN, private, onPPSK := 0, 0, 0
	ppskOnly, others := map[string]bool{}, map[string]bool{}
	for _, c := range cls {
		if c["is_wired"] != true {
			set := others
			if ppskSSID[str(c, "essid")] {
				onPPSK++
				set = ppskOnly
			}
			for k, v := range c {
				if v != nil && v != "" {
					set[k] = true
				}
			}
			wifiN++
			if privateMACAddr(str(c, "mac")) {
				private++
			}
		}
		for k, v := range c {
			if identKeyRE.MatchString(k) && v != nil && v != "" && v != false {
				fields[k]++
			}
		}
	}
	// field names (never values) that only devices on a private-password network have: where UniFi would keep the key
	extra := []string{}
	for k := range ppskOnly {
		if !others[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	r.Clients = map[string]any{"wifi": wifiN, "private_mac": private, "identity_fields": fields, "on_ppsk": onPPSK, "ppsk_only_fields": extra}

	r.Verdict = identVerdict(r, fields)
	return r, nil
}

// keyish picks a field name that looks like it holds which key or password a device used.
func keyish(names []string) string {
	for _, k := range names {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "psk") || strings.Contains(lk, "pass") {
			return k
		}
	}
	return ""
}

func anyList(v any) []any { l, _ := v.([]any); return l }

func privateMACAddr(mac string) bool {
	return len(mac) >= 2 && strings.ContainsRune("26ae", rune(strings.ToLower(mac)[1]))
}

// identVerdict sums it up: what works on this console, and what each way would need.
func identVerdict(r *identCheck, fields map[string]int) []map[string]any {
	v := []map[string]any{}
	say := func(topic, state, text string) {
		v = append(v, map[string]any{"topic": topic, "state": state, "text": text})
	}
	idf := func(sub ...string) (string, int) {
		for k, n := range fields {
			for _, s := range sub {
				if strings.Contains(strings.ToLower(k), s) {
					return k, n
				}
			}
		}
		return "", 0
	}
	if n := r.Clients["private_mac"].(int); n > 0 {
		say("Private addresses", "info", strconv.Itoa(n)+" of "+strconv.Itoa(r.Clients["wifi"].(int))+" Wi-Fi devices use a private (changing) Wi-Fi address — the reason a login is surer than the MAC.")
	}
	switch {
	case r.PPSK["offered"] != true:
		say("PPSK", "bad", "This console's Wi-Fi settings have no private passwords (PPSK). They need a newer UniFi Network version and a WPA2 network.")
	case r.PPSK["networks_on"].(int) == 0:
		say("PPSK", "ok", "Private passwords (PPSK) are offered but not used on any Wi-Fi network yet.")
	default:
		say("PPSK", "ok", "Private passwords are on for "+strconv.Itoa(r.PPSK["networks_on"].(int))+" network(s), "+strconv.Itoa(r.PPSK["keys"].(int))+" password(s).")
	}
	if r.PPSK["offered"] == true {
		if k, n := idf("ppsk", "preshared", "psk_"); k != "" {
			say("PPSK: whose password", "ok", "UniFi records which private password a device used (field “"+k+"”, on "+strconv.Itoa(n)+" devices now), so a password per person names the device's owner.")
		} else if r.PPSK["networks_on"].(int) > 0 && r.Clients["on_ppsk"].(int) == 0 {
			say("PPSK: whose password", "info", "No device is on the private-password network right now, so it can't be seen yet whether UniFi records which password a device used. Join it from one phone and check again.")
		} else if ex := r.Clients["ppsk_only_fields"].([]string); r.PPSK["networks_on"].(int) > 0 && keyish(ex) != "" {
			say("PPSK: whose password", "ok", "UniFi seems to record which private password a device used (field “"+keyish(ex)+"”, only on devices using a private password), so a password per person can name the device's owner.")
		} else if r.PPSK["networks_on"].(int) > 0 {
			say("PPSK: whose password", "warn", strconv.Itoa(r.Clients["on_ppsk"].(int))+" device(s) are on the private-password network, but none shows which password it used, so HomeOwl could only tell people apart by the network each password leads to: one VLAN per person.")
		} else {
			say("PPSK: whose password", "info", "Can't tell yet whether UniFi records which password a device used — no device uses a private password. One test password on one device would show it.")
		}
	}
	switch r.RADIUS["server_on"] {
	case true:
		say("RADIUS server", "ok", "UniFi's built-in RADIUS server is on, with "+strconv.Itoa(r.RADIUS["users"].(int))+" user(s).")
	case false:
		say("RADIUS server", "ok", "UniFi's built-in RADIUS server is available but off. Turning it on and adding a user per person is a console change.")
	default:
		say("RADIUS server", "bad", "No built-in RADIUS server setting found on this console.")
	}
	if k, n := idf("1x", "identity"); k != "" {
		say("Enterprise: whose login", "ok", "UniFi shows each Enterprise device's login (field “"+k+"”, on "+strconv.Itoa(n)+" devices now), so a login per person names the device's owner.")
	} else if r.RADIUS["enterprise_wifi"].(int) > 0 {
		say("Enterprise: whose login", "warn", "There's an Enterprise (802.1X) Wi-Fi network, but no device shows its login. If devices are connected to it, UniFi doesn't report the login here.")
	} else {
		say("Enterprise: whose login", "info", "No Enterprise (802.1X) Wi-Fi network yet, so it can't be seen whether UniFi shows each device's login. A test network with one user would show it.")
	}
	if r.Portal["enabled"] == true {
		state, t := "ok", "The guest portal is on (sign-in: "+orDefault(str(r.Portal, "auth"), "none")+")."
		if r.Portal["external"] == true {
			t += " An external portal server is set."
		} else {
			t += " It isn't sent to an external portal (HomeOwl) yet."
		}
		say("Captive portal", state, t)
	} else if r.Portal["has_custom_ip"] == true || r.Portal["has_auth"] == true {
		say("Captive portal", "ok", "The guest portal is available but off. An onboarding network would turn it on for that network only, pointed at HomeOwl.")
	} else {
		say("Captive portal", "bad", "No guest portal settings found.")
	}
	return v
}
