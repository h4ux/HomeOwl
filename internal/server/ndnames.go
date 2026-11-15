package server

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

// NextDNS knows a device by the name in the profile it installed. That can differ from HomeOwl's name for it: a
// profile from before the device was renamed, another spelling, a second install. The device's own page lists
// the names NextDNS sees that look like it, and "this is me" links one (CheckIn.NDNames).

const ndCandidateWindow = 7 * 24 * time.Hour

// the device names in a NextDNS profile's logs (tests swap it)
var ndNames = func(a *App, nd string, since time.Time) ([]analytics.NDName, error) {
	return a.An.NDDeviceNames(nd, since)
}

// ndWanted is every name this device may have in NextDNS, reduced for comparing (analytics.SameDeviceName).
func (a *App) ndWanted(x CheckIn) map[string]bool {
	w := map[string]bool{}
	add := func(y CheckIn) {
		for _, n := range append([]string{y.Name, y.ProfileName}, y.NDNames...) {
			if k := analytics.SameDeviceName(n); k != "" {
				w[k] = true
			}
		}
	}
	add(x)
	for _, y := range a.checkins.List() {
		if y.Status == "merged" && y.MergedInto == x.ID {
			add(y)
		}
	}
	return w
}

// ndNamesIn: the device names NextDNS logged in the device's profiles since a time, newest first per name.
func (a *App) ndNamesIn(x CheckIn, since time.Time) []analytics.NDName {
	if !a.An.Open() {
		return nil
	}
	best := map[string]analytics.NDName{}
	for _, p := range ndProfilesOf(x) {
		l, err := ndNames(a, p, since)
		if err != nil {
			continue
		}
		for _, n := range l {
			o, ok := best[n.Name]
			if ok && o.Last.After(n.Last) {
				o.Day += n.Day
				best[n.Name] = o
				continue
			}
			n.Day += o.Day
			best[n.Name] = n
		}
	}
	out := make([]analytics.NDName, 0, len(best))
	for _, n := range best {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// ndMatched: the exact names NextDNS logs this device under (for queries by name).
func (a *App) ndMatched(x CheckIn) []string {
	want := a.ndWanted(x)
	out := []string{}
	for _, n := range a.ndNamesIn(x, time.Now().Add(-30*24*time.Hour)) {
		if want[analytics.SameDeviceName(n.Name)] {
			out = append(out, n.Name)
		}
	}
	if len(out) == 0 && x.Name != "" {
		out = append(out, x.Name)
	}
	return out
}

// ndCandidates: names NextDNS saw lately in the device's profiles that share a word with it and that no other
// device has.
func (a *App) ndCandidates(x CheckIn) []map[string]any {
	want := a.ndWanted(x)
	others := map[string]bool{}
	for _, y := range a.checkins.List() {
		if y.Status == "approved" && y.ID != x.ID && y.MAC != x.MAC {
			for k := range a.ndWanted(y) {
				others[k] = true
			}
		}
	}
	words := map[string]bool{}
	for _, n := range append([]string{x.Name, x.ProfileName}, x.NDNames...) {
		for _, w := range nameWords(n) {
			words[w] = true
		}
	}
	type cand struct {
		n      analytics.NDName
		shared int
	}
	var l []cand
	for _, n := range a.ndNamesIn(x, time.Now().Add(-ndCandidateWindow)) {
		k := analytics.SameDeviceName(n.Name)
		if want[k] || others[k] {
			continue
		}
		shared := 0
		for _, w := range nameWords(n.Name) {
			if words[w] {
				shared++
			}
		}
		if shared > 0 {
			l = append(l, cand{n, shared})
		}
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].shared > l[j].shared })
	out := []map[string]any{}
	for i, c := range l {
		if i == 6 {
			break
		}
		out = append(out, map[string]any{"name": c.n.Name, "last": c.n.Last.Unix(), "day": c.n.Day})
	}
	return out
}

// linkNDName links (op "link") or unlinks ("unlink") a NextDNS device name to a check-in. Linking only takes a
// name from ndCandidates, and puts it under the device's person so Analytics follows it.
func (a *App) linkNDName(x CheckIn, name, op string) error {
	name = strings.TrimSpace(name)
	switch op {
	case "link":
		ok := false
		for _, c := range a.ndCandidates(x) {
			if c["name"] == name {
				ok = true
			}
		}
		if !ok {
			return errors.New("NextDNS hasn't seen that name lately for this device's profile")
		}
		a.checkins.Edit(x.ID, func(c *CheckIn) { c.NDNames = append(c.NDNames, name) })
		if x.PersonID != "" && a.An.Open() {
			for _, p := range ndProfilesOf(x) {
				_ = a.An.AssignDevice(x.PersonID, "ndev", p+"/"+name)
			}
		}
	case "unlink":
		found := false
		a.checkins.Edit(x.ID, func(c *CheckIn) {
			l := c.NDNames[:0]
			for _, n := range c.NDNames {
				if n == name {
					found = true
					continue
				}
				l = append(l, n)
			}
			c.NDNames = l
		})
		if !found {
			return errors.New("that name isn't linked to this device")
		}
		if a.An.Open() {
			for _, p := range ndProfilesOf(x) {
				_ = a.An.UnassignDevice("ndev", p+"/"+name)
			}
		}
	default:
		return errors.New("unknown action")
	}
	a.setupWatch(time.Now()) // "stopped" clears now when the linked name is in use
	return nil
}

// hiNDName: POST /api/hi/ndname {name, op} from the device's own page.
func (a *App) hiNDName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "not allowed")
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
	if !a.checkins.allow(remoteIP(r)) {
		fail(w, 429, "That's a lot of requests — try again later.")
		return
	}
	body := readJSON(r)
	if err := a.linkNDName(x, s(body, "name"), s(body, "op")); err != nil {
		fail(w, 400, err.Error())
		return
	}
	x, _ = a.checkins.Get(x.ID)
	send(w, 200, map[string]any{"mine": a.myDevice(x, c)})
}
