package unifi

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The setup Wi-Fi (HomeOwl's onboarding network), created through two allow-listed actions:
//   - "setupnet": a new network (VLAN) with no internet, modelled on the console's own Default network so the body
//     has every field this UniFi version expects; only the name, VLAN, subnet, DHCP range and internet are set.
//   - "setupwlan": a new Wi-Fi on it, modelled on the home Wi-Fi (same security mode, bands, AP groups), with its
//     own name and password; no MAC filter, schedule, private keys or RADIUS are copied.
// Both are creates: the change log's undo deletes what was made.

// keys never copied from the template: identity, secrets and per-object state
func cloneBody(src map[string]any, drop func(k string) bool) map[string]any {
	out := map[string]any{}
	for k, v := range src {
		if k == "_id" || k == "site_id" || k == "external_id" || strings.HasPrefix(k, "attr_") || strings.HasPrefix(k, "x_") || drop(k) {
			continue
		}
		out[k] = v
	}
	return out
}

// SetupSubnet picks the setup network's subnet for a VLAN: 192.168.<vlan>.0/24 (or 10.<hi>.<lo>.0/24 above 255).
func SetupSubnet(vlan int) (gw string, n *net.IPNet) {
	ip := net.IPv4(192, 168, byte(vlan), 1)
	if vlan > 255 {
		ip = net.IPv4(10, byte(vlan>>8), byte(vlan&255), 1)
	}
	_, n, _ = net.ParseCIDR(ip.String() + "/24")
	return ip.String(), n
}

func (c *Client) planSetupNet(a Action) (*plan, error) {
	name := strings.TrimSpace(a.s("name"))
	vlan, err := strconv.Atoi(a.s("vlan"))
	if err != nil || vlan < 2 || vlan > 4009 {
		return nil, aerr("pick a VLAN between 2 and 4009")
	}
	if name == "" || utf8.RuneCountInString(name) > 32 {
		return nil, aerr("give the network a name (up to 32 characters)")
	}
	st, d := c.Get(c.SitePath("rest/networkconf", false))
	if st != 200 {
		return nil, aerr("could not read the networks: %s", ErrText(d))
	}
	gw, subnet := SetupSubnet(vlan)
	var tmpl map[string]any
	for _, n := range List(d) {
		if strings.EqualFold(fmt.Sprint(n["name"]), name) {
			return nil, aerr("there's already a network called %s", name)
		}
		if v := fmt.Sprint(n["vlan"]); n["vlan_enabled"] == true && v == strconv.Itoa(vlan) {
			return nil, aerr("VLAN %d is already used by %v", vlan, n["name"])
		}
		if s, _ := n["ip_subnet"].(string); s != "" {
			if _, other, err := net.ParseCIDR(s); err == nil && (other.Contains(subnet.IP) || subnet.Contains(other.IP)) {
				return nil, aerr("%s overlaps %v (%s): pick another VLAN", subnet, n["name"], s)
			}
		}
		if n["purpose"] == "corporate" && (tmpl == nil || n["attr_hidden_id"] == "LAN") {
			tmpl = n
		}
	}
	if tmpl == nil {
		return nil, aerr("no home network to model the setup network on")
	}
	body := cloneBody(tmpl, func(k string) bool {
		return strings.HasPrefix(k, "dhcpd_") || strings.HasPrefix(k, "ipv6_") || strings.HasPrefix(k, "dhcpdv6_") ||
			strings.HasPrefix(k, "nat_") || k == "domain_name" || k == "dhcp_relay_enabled" || k == "dhcpguard_enabled"
	})
	ip := subnet.IP.To4()
	at := func(last byte) string { return net.IPv4(ip[0], ip[1], ip[2], last).String() }
	set := map[string]any{"name": name, "purpose": "corporate", "enabled": true, "vlan_enabled": true, "vlan": vlan,
		"ip_subnet": gw + "/24", "dhcpd_enabled": true, "dhcpd_start": at(10), "dhcpd_stop": at(250), "dhcpd_leasetime": 3600,
		"internet_access_enabled": false, "network_isolation_enabled": false, "mdns_enabled": false, "ipv6_interface_type": "none"}
	for k, v := range set {
		body[k] = v
	}
	return &plan{req: req{"POST", c.SitePath("rest/networkconf", false), body}, Before: map[string]any{"network": nil},
		After: map[string]any{"network": name, "vlan": vlan, "subnet": subnet.String(), "internet": "off"}, DeleteCreated: true, Target: name}, nil
}

func (c *Client) planSetupWLAN(a Action) (*plan, error) {
	ssid, pw, netID := strings.TrimSpace(a.s("ssid")), a.s("password"), a.s("network_id")
	if ssid == "" || utf8.RuneCountInString(ssid) > 32 {
		return nil, aerr("give the Wi-Fi a name (up to 32 characters)")
	}
	if err := checkPassphrase(pw); err != nil {
		return nil, err
	}
	if netID != "new" && !idRE.MatchString(netID) {
		return nil, aerr("bad network id")
	}
	st, d := c.Get(c.SitePath("rest/wlanconf", false))
	if st != 200 {
		return nil, aerr("could not read the Wi-Fi networks: %s", ErrText(d))
	}
	var tmpl map[string]any
	for _, w := range List(d) {
		if strings.EqualFold(fmt.Sprint(w["name"]), ssid) {
			return nil, aerr("there's already a Wi-Fi called %s", ssid)
		}
		if w["security"] == "wpapsk" && !c.IsGuestWLAN(w) && w["enabled"] != false && tmpl == nil {
			tmpl = w
		}
	}
	if tmpl == nil {
		return nil, aerr("no home Wi-Fi with a password to model the setup Wi-Fi on")
	}
	body := cloneBody(tmpl, func(k string) bool {
		return strings.Contains(k, "preshared") || strings.Contains(k, "ppsk") || strings.HasPrefix(k, "mac_filter") ||
			strings.HasPrefix(k, "schedule") || strings.HasPrefix(k, "radius") || strings.HasPrefix(k, "hotspot") ||
			k == "name_combine_enabled" || k == "name_combine_suffix" || k == "iapp_key" || k == "sae_psk" || k == "sae_groups_custom"
	})
	for k, v := range map[string]any{"name": ssid, "x_passphrase": pw, "networkconf_id": netID, "enabled": true, "is_guest": false,
		"hide_ssid": false, "mac_filter_enabled": false, "schedule_enabled": false, "radius_mac_auth_enabled": false} {
		body[k] = v
	}
	mode := fmt.Sprint(tmpl["wpa_mode"])
	if tmpl["wpa3_support"] == true {
		mode = "WPA3"
		if tmpl["wpa3_transition"] == true {
			mode = "WPA2/WPA3"
		}
	}
	return &plan{req: req{"POST", c.SitePath("rest/wlanconf", false), body}, Before: map[string]any{"wifi": nil},
		After:         map[string]any{"wifi": ssid, "security": mode, "password": fmt.Sprintf("(set, %d characters)", len(pw)), "modelled on": tmpl["name"]},
		DeleteCreated: true, Target: "Wi-Fi " + ssid}, nil
}
