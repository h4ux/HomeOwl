package server

import (
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/unifi"
)

// Device issues (the client window's "Issues"): pick what doesn't work — the printer doesn't print, AirPlay to the
// TV doesn't, file sharing doesn't — and HomeOwl looks at why and offers the fix. The usual cause is a device on its
// own network (IoT) that the home network can see (its name is announced across) but can't reach (the firewall
// blocks it). The fix is one narrow allow rule: these home networks → this device's address, only the ports the
// problem needs. It's previewed, logged and undone like every console change.

type deviceIssue struct {
	ID, Label, Proto, Ports string
	Probe                   []int // ports HomeOwl tries, to show whether the device answers
	Tip                     string
}

var deviceIssues = []deviceIssue{
	{"print", "Printing or scanning doesn't work", "tcp", "631,9100,80,443,8080", []int{631, 9100, 80, 443},
		"If the printer doesn't show up at all, turn on Multicast DNS for both networks (UniFi › Settings › Networks › Multicast DNS)."},
	{"airplay", "AirPlay doesn't work", "all", "", []int{7000, 7100},
		"If it doesn't show in the AirPlay list at all, turn on Multicast DNS for both networks (UniFi › Settings › Networks › Multicast DNS)."},
	{"cast", "Casting (Chromecast, Google TV) doesn't work", "all", "", []int{8008, 8009, 8443},
		"If it doesn't show as a cast target, turn on Multicast DNS for both networks (UniFi › Settings › Networks › Multicast DNS)."},
	{"share", "File sharing doesn't work", "tcp", "445,139,548", []int{445, 548}, ""},
	{"web", "Its app or web page doesn't open", "tcp", "80,443,8080,8443", []int{80, 443, 8080, 8443}, ""},
	{"other", "Something else at home can't reach it", "all", "", []int{80, 443}, ""},
}

func issueByID(id string) (deviceIssue, bool) {
	for _, x := range deviceIssues {
		if x.ID == id {
			return x, true
		}
	}
	return deviceIssue{}, false
}

type issueNet struct {
	ID, Name, Subnet string
	sn               *net.IPNet
	lan              bool // the console's default LAN
}

// issueNets: the console's local networks (not WAN or VPN).
func (a *App) issueNets() ([]issueNet, error) {
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/networkconf", false))
	if st != 200 {
		return nil, errors.New("couldn't read the networks from UniFi")
	}
	var out []issueNet
	for _, n := range unifi.List(d) {
		if p := str(n, "purpose"); p != "corporate" && p != "guest" && p != "" {
			continue
		}
		_, sn, err := net.ParseCIDR(str(n, "ip_subnet"))
		if err != nil {
			continue
		}
		out = append(out, issueNet{ID: str(n, "_id"), Name: str(n, "name"), Subnet: sn.String(), sn: sn, lan: str(n, "attr_hidden_id") == "LAN"})
	}
	return out, nil
}

// issueDevice: the device's name, address and network now (online), or its fixed address.
func (a *App) issueDevice(mac string) (name, ip, netID string, err error) {
	if list, err := a.onlineClients(staFresh); err == nil {
		for _, c := range list {
			if strings.EqualFold(str(c, "mac"), mac) {
				return clientName(c), str(c, "ip"), str(c, "network_id"), nil
			}
		}
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/user", false))
	if st == 200 {
		for _, c := range unifi.List(d) {
			if strings.EqualFold(str(c, "mac"), mac) && c["use_fixedip"] == true && str(c, "fixed_ip") != "" {
				return clientName(c), str(c, "fixed_ip"), str(c, "network_id"), nil
			}
		}
	}
	return "", "", "", errors.New("the device isn't online and has no fixed address, so there's nothing to reach — turn it on first")
}

// probe: which of these ports answer from HomeOwl's computer (on the home network).
var probe = func(ip string, ports []int) map[int]bool {
	out := map[int]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(p)), 1500*time.Millisecond)
			if err == nil {
				_ = c.Close()
			}
			mu.Lock()
			out[p] = err == nil
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}

type issueStep struct {
	OK   *bool  `json:"ok"` // nil: just information
	Text string `json:"text"`
}

func yes(b bool) *bool { return &b }

// issuePlan works out the diagnosis and the fix's actions.
func (a *App) issuePlan(mac, issueID string, from []string, viewer string) (map[string]any, []unifi.Action, error) {
	if !a.UniFi.Connected() {
		return nil, nil, errors.New("connect UniFi first")
	}
	is, ok := issueByID(issueID)
	if !ok {
		return nil, nil, errors.New("pick what doesn't work")
	}
	mac = strings.ToLower(mac)
	if !macRE.MatchString(mac) {
		return nil, nil, errors.New("that isn't a device address (MAC)")
	}
	name, ip, netID, err := a.issueDevice(mac)
	if err != nil {
		return nil, nil, err
	}
	nets, err := a.issueNets()
	if err != nil {
		return nil, nil, err
	}
	var dev *issueNet
	pip := net.ParseIP(ip)
	for i, n := range nets {
		if n.ID == netID || (netID == "" && pip != nil && n.sn.Contains(pip)) {
			dev = &nets[i]
		}
	}
	if dev == nil || pip == nil {
		return nil, nil, errors.New("couldn't tell which network " + name + " is on")
	}
	var steps []issueStep
	steps = append(steps, issueStep{nil, name + " is on " + dev.Name + " (" + ip + ")."})
	// where the request comes from: the viewer's network, and the default LAN
	choices := []map[string]any{}
	vip := net.ParseIP(viewer)
	defaults := map[string]bool{}
	for _, n := range nets {
		if n.ID == dev.ID {
			continue
		}
		if (vip != nil && n.sn.Contains(vip)) || n.lan {
			defaults[n.ID] = true
		}
		choices = append(choices, map[string]any{"id": n.ID, "name": n.Name, "subnet": n.Subnet})
	}
	if from == nil {
		for id := range defaults {
			from = append(from, id)
		}
		sort.Strings(from)
	}
	picked := map[string]issueNet{}
	for _, id := range from {
		for _, n := range nets {
			if n.ID == id && n.ID != dev.ID {
				picked[id] = n
			}
		}
	}
	view := map[string]any{"device": map[string]any{"name": name, "ip": ip, "network": dev.Name, "network_id": dev.ID}, "issue": is.ID,
		"label": is.Label, "from": from, "choices": choices, "tip": is.Tip}
	if vip != nil && dev.sn.Contains(vip) {
		steps = append(steps, issueStep{nil, "You're on " + dev.Name + " too, so no firewall is in between for you. If it still fails, restart the device, and check it's on and awake."})
	}
	// does it answer from HomeOwl's computer (which sits on the home network)?
	hp := probe(ip, is.Probe)
	open := []string{}
	for _, p := range is.Probe {
		if hp[p] {
			open = append(open, strconv.Itoa(p))
		}
	}
	if len(open) > 0 {
		steps = append(steps, issueStep{yes(true), "It answers HomeOwl on port " + strings.Join(open, ", ") + "."})
	} else {
		steps = append(steps, issueStep{yes(false), "It doesn't answer HomeOwl on port " + joinInts(is.Probe) + ": something blocks the way, or it's asleep."})
	}
	view["answers"] = len(open) > 0
	if len(picked) == 0 {
		view["steps"] = steps
		return view, nil, nil
	}
	// the firewall: zone-based (Network 9+) or classic
	zoneOf, zoned := a.fwZones()
	label := FwLabel(is, name)
	var acts []unifi.Action
	fromNames := func(ns []issueNet) string {
		var l []string
		for _, n := range ns {
			l = append(l, n.Name)
		}
		return strings.Join(l, ", ")
	}
	var pickedList []issueNet
	for _, n := range picked {
		pickedList = append(pickedList, n)
	}
	sort.Slice(pickedList, func(i, j int) bool { return pickedList[i].Name < pickedList[j].Name })
	if zoned {
		dz := zoneOf(dev.ID)
		if dz == nil {
			return nil, nil, errors.New(dev.Name + " isn't in a firewall zone")
		}
		if exists, err := a.fwPolicyExists(ip, label); err == nil && exists {
			steps = append(steps, issueStep{yes(true), "HomeOwl's rule for this is already there (Settings › Policy Engine). The problem is somewhere else."})
			view["steps"] = steps
			return view, nil, nil
		}
		steps = append(steps, issueStep{nil, "Your console uses zone-based firewall policies. " + dev.Name + " is in the " + str(dz, "name") + " zone."})
		byZone := map[string][]issueNet{}
		for _, n := range pickedList {
			z := zoneOf(n.ID)
			if z == nil {
				continue
			}
			byZone[str(z, "_id")] = append(byZone[str(z, "_id")], n)
		}
		for zid, ns := range byZone {
			ids := []string{}
			for _, n := range ns {
				ids = append(ids, n.ID)
			}
			acts = append(acts, unifi.Action{"kind": "fwpolicy", "src_zone": zid, "dst_zone": str(dz, "_id"), "network_ids": ids, "ip": ip,
				"protocol": is.Proto, "ports": is.Ports, "name": label, "from": fromNames(ns),
				"description": "Device issue: let " + fromNames(ns) + " reach " + name + " (" + is.Label + ")"})
		}
	} else {
		steps = append(steps, issueStep{nil, "Your console uses classic firewall rules (LAN In)."})
		if is.Ports != "" {
			acts = append(acts, unifi.Action{"kind": "fwportgroup", "ports": is.Ports, "name": "HomeOwl " + is.ID + " ports",
				"description": "Device issue: the ports " + is.Label + " needs"})
		}
		for _, n := range pickedList {
			act := unifi.Action{"kind": "fwrule", "network_id": n.ID, "ip": ip, "protocol": is.Proto, "ports": is.Ports, "name": label, "from": n.Name,
				"description": "Device issue: let " + n.Name + " reach " + name + " (" + is.Label + ")"}
			if is.Ports != "" {
				act["port_group"] = portGroupPending
			}
			acts = append(acts, act)
		}
	}
	if len(acts) == 0 {
		return nil, nil, errors.New("couldn't find the firewall zones of those networks")
	}
	if len(open) == 0 {
		steps = append(steps, issueStep{nil, "The fix: let " + fromNames(pickedList) + " reach " + name + " — only its address, " + fwWhatIssue(is) + ". The rest of " + dev.Name + " stays separated."})
	} else {
		steps = append(steps, issueStep{nil, "It already answers on some ports. The fix below still makes sure " + fromNames(pickedList) + " can reach it for " + strings.ToLower(is.Label[:1]) + is.Label[1:] + "."})
	}
	view["steps"] = steps
	return view, acts, nil
}

const portGroupPending = "new-port-group" // the classic rule's port group, created just before it

// FwLabel: the rule's name, so it's recognisable in UniFi and HomeOwl can tell it's there.
func FwLabel(is deviceIssue, device string) string {
	return unifi.FwName("HomeOwl: " + is.ID + " " + device)
}

func fwWhatIssue(is deviceIssue) string {
	if is.Ports == "" {
		return "any port"
	}
	return "ports " + strings.ReplaceAll(is.Ports, ",", ", ")
}

func joinInts(l []int) string {
	var s []string
	for _, x := range l {
		s = append(s, strconv.Itoa(x))
	}
	return strings.Join(s, ", ")
}

func (a *App) fwPolicyExists(ip, name string) (bool, error) {
	st, d := a.UniFi.Get(a.UniFi.SitePath("firewall-policies", true))
	if st != 200 {
		return false, errors.New("couldn't read the firewall policies")
	}
	for _, p := range unifi.List(d) {
		if str(p, "name") == name && p["enabled"] != false {
			return true, nil
		}
	}
	return false, nil
}

// fwZones: the console's firewall zones by network (zoned false: a classic firewall).
func (a *App) fwZones() (zoneOf func(netID string) map[string]any, zoned bool) {
	st, d := a.UniFi.Get(a.UniFi.SitePath("firewall/zone", true))
	zones := unifi.List(d)
	return func(netID string) map[string]any {
		for _, z := range zones {
			if l, ok := z["network_ids"].([]any); ok {
				for _, x := range l {
					if x == netID {
						return z
					}
				}
			}
		}
		return nil
	}, st == 200 && len(zones) > 0
}

// fwPreview: what each firewall action would do, without doing it.
func (a *App) fwPreview(acts []unifi.Action, by string) ([]any, error) {
	var pv []any
	for _, x := range acts {
		p, err := a.Changes.Run(a.UniFi, x, true, by, false)
		if err != nil {
			return nil, err
		}
		pv = append(pv, p["preview"])
	}
	return pv, nil
}

// fwApply runs a firewall fix (a classic port group first, then the rules that use it), each one logged and undoable.
func (a *App) fwApply(acts []unifi.Action, by string) ([]any, error) {
	if len(acts) == 0 {
		return nil, errors.New("nothing to fix")
	}
	group := ""
	var done []any
	for _, x := range acts {
		if x["port_group"] == portGroupPending {
			x["port_group"] = group
		}
		res, err := a.Changes.Run(a.UniFi, x, false, by, true)
		if err == nil {
			if ch, ok := res["result"].(unifi.Change); ok {
				if !ch.OK {
					err = errors.New(ch.Error)
				} else {
					done = append(done, ch)
					if x["kind"] == "fwportgroup" {
						group = ch.Created
					}
				}
			}
		}
		if err != nil {
			if len(done) > 0 {
				return nil, errors.New("UniFi refused part of the fix (" + err.Error() + "); what was added is in the change log, where it can be undone")
			}
			return nil, errors.New("UniFi refused the fix: " + err.Error())
		}
	}
	return done, nil
}

// issueAPI: "check" (diagnosis + preview of the fix) and "fix" (apply it, change mode unlocked like any UI change).
func (a *App) issueAPI(op string, body map[string]any, viewer, by string) (map[string]any, error) {
	var from []string
	if l, ok := body["from"].([]any); ok {
		from = []string{}
		for _, v := range l {
			if s, _ := v.(string); s != "" {
				from = append(from, s)
			}
		}
	}
	view, acts, err := a.issuePlan(s(body, "mac"), s(body, "issue"), from, viewer)
	if err != nil {
		return nil, err
	}
	switch op {
	case "check":
		pv, err := a.fwPreview(acts, by)
		if err != nil {
			return nil, err
		}
		view["fix"] = pv
		return view, nil
	case "fix":
		done, err := a.fwApply(acts, by)
		if err != nil {
			return nil, err
		}
		time.Sleep(1500 * time.Millisecond) // the console applies it in a moment
		is, _ := issueByID(s(body, "issue"))
		after := probe(str(view["device"].(map[string]any), "ip"), is.Probe)
		ok := false
		for _, v := range after {
			ok = ok || v
		}
		return map[string]any{"done": done, "answers": ok}, nil
	}
	return nil, errors.New("not found")
}
