package unifi

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Firewall allow rules for one device (server/issues.go): "let these home networks reach the printer". Each one is
// narrow (one destination address, the ports the problem needs), created new — never an edit of an existing rule —
// and undone by deleting it. Zone-based consoles (UniFi Network 9+) get a policy, classic ones a LAN In rule with a
// port group.

var (
	portsRE  = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$`)
	fwNameRE = regexp.MustCompile(`[^\w .,:()'-]`)
	fwProtos = map[string]bool{"tcp": true, "udp": true, "tcp_udp": true, "all": true}
)

// FwName keeps a rule name short and plain.
func FwName(s string) string {
	s = strings.TrimSpace(fwNameRE.ReplaceAllString(s, ""))
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func (a Action) strs(k string) []string {
	var out []string
	switch v := a[k].(type) {
	case []string:
		out = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// fwTarget checks the common parts: one private IPv4 address, the protocol, the ports ("" = any).
func fwTarget(a Action) (ip, proto, ports string, err error) {
	v4 := net.ParseIP(a.s("ip")).To4()
	if v4 == nil || !v4.IsPrivate() {
		return "", "", "", aerr("the device needs a home-network IPv4 address")
	}
	proto, ports = a.s("protocol"), a.s("ports")
	if !fwProtos[proto] {
		return "", "", "", aerr("unknown protocol %q", proto)
	}
	if ports != "" && (!portsRE.MatchString(ports) || proto == "all") {
		return "", "", "", aerr("bad ports %q", ports)
	}
	return v4.String(), proto, ports, nil
}

func fwWhat(proto, ports string) string {
	if ports == "" {
		return "everything"
	}
	return strings.ToUpper(strings.ReplaceAll(proto, "_", "/")) + " " + ports
}

func (c *Client) planFwPolicy(a Action) (*plan, error) {
	ip, proto, ports, err := fwTarget(a)
	if err != nil {
		return nil, err
	}
	src, dst, nets := a.s("src_zone"), a.s("dst_zone"), a.strs("network_ids")
	if !idRE.MatchString(src) || !idRE.MatchString(dst) || len(nets) == 0 {
		return nil, aerr("pick the networks that should reach the device")
	}
	for _, n := range nets {
		if !idRE.MatchString(n) {
			return nil, aerr("bad network id")
		}
	}
	name := FwName(orStr(a.s("name"), "HomeOwl: allow "+ip))
	dest := map[string]any{"zone_id": dst, "matching_target": "IP", "ips": []string{ip}, "port_matching_type": "ANY",
		"match_opposite_ips": false, "match_opposite_ports": false}
	if ports != "" {
		dest["port_matching_type"], dest["port"] = "SPECIFIC", ports
	}
	body := map[string]any{"name": name, "description": "Added by HomeOwl", "enabled": true, "action": "ALLOW", "ip_version": "IPV4",
		"protocol": proto, "create_allow_respond": true, "logging": false, "connection_state_type": "ALL", "connection_states": []string{},
		"match_ip_sec": false, "match_opposite_protocol": false, "icmp_typename": "ANY", "icmp_v6_typename": "ANY",
		"schedule": map[string]any{"mode": "ALWAYS"},
		"source": map[string]any{"zone_id": src, "matching_target": "NETWORK", "network_ids": nets, "port_matching_type": "ANY",
			"match_opposite_ports": false, "match_opposite_networks": false, "match_mac": false},
		"destination": dest}
	return &plan{req: req{"POST", c.SitePath("firewall-policies", true), body}, Before: map[string]any{"policy": nil},
		After: map[string]any{"allow": orStr(a.s("from"), "home") + " → " + ip + " (" + fwWhat(proto, ports) + ")"}, DeleteCreated: true, Target: name}, nil
}

func (c *Client) planFwPortGroup(a Action) (*plan, error) {
	ports := a.s("ports")
	if !portsRE.MatchString(ports) {
		return nil, aerr("bad ports %q", ports)
	}
	var members []string
	for _, p := range strings.Split(ports, ",") {
		members = append(members, strings.ReplaceAll(p, "-", ":")) // classic port groups write ranges as 1:2
	}
	name := FwName(orStr(a.s("name"), "HomeOwl ports"))
	return &plan{req: req{"POST", c.SitePath("rest/firewallgroup", false), map[string]any{"name": name, "group_type": "port-group", "group_members": members}},
		Before: map[string]any{"port group": nil}, After: map[string]any{"port group": ports}, DeleteCreated: true, Target: name}, nil
}

func (c *Client) planFwRule(a Action) (*plan, error) {
	ip, proto, ports, err := fwTarget(a)
	if err != nil {
		return nil, err
	}
	net, group := a.s("network_id"), a.s("port_group")
	if !idRE.MatchString(net) || (group != "" && !idRE.MatchString(group)) || (ports != "" && group == "") {
		return nil, aerr("pick the network that should reach the device")
	}
	path := c.SitePath("rest/firewallrule", false)
	st, d := c.Get(path)
	if st != 200 {
		return nil, aerr("couldn't read the firewall rules: %s", ErrText(d))
	}
	idx := 2000 // user rules sit in 2000–2999, before UniFi's own (isolation) rules
	for _, r := range List(d) {
		if r["ruleset"] == "LAN_IN" {
			if n, ok := r["rule_index"].(float64); ok && int(n) >= idx && n < 3000 {
				idx = int(n) + 1
			}
		}
	}
	if idx >= 3000 {
		return nil, aerr("no room for another LAN In rule")
	}
	name := FwName(orStr(a.s("name"), "HomeOwl: allow "+ip))
	groups := []string{}
	if group != "" {
		groups = append(groups, group)
	}
	body := map[string]any{"name": name, "enabled": true, "action": "accept", "ruleset": "LAN_IN", "rule_index": idx, "protocol": proto,
		"protocol_match_excepted": false, "logging": false, "state_established": false, "state_invalid": false, "state_new": false,
		"state_related": false, "ipsec": "", "src_firewallgroup_ids": []string{}, "src_mac_address": "", "src_address": "",
		"src_networkconf_id": net, "src_networkconf_type": "NETv4", "dst_firewallgroup_ids": groups, "dst_address": ip,
		"dst_networkconf_type": "ADDRv4", "dst_port": ""}
	return &plan{req: req{"POST", path, body}, Before: map[string]any{"rule": nil},
		After:         map[string]any{"allow": orStr(a.s("from"), "home") + " → " + ip + " (" + fwWhat(proto, ports) + ")", "rule index": fmt.Sprint(idx)},
		DeleteCreated: true, Target: name}, nil
}
