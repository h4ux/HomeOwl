package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/analytics"
)

// Watching device setups (devprofile.go). A device that used its NextDNS setup and then stops — the profile was
// removed, Private DNS was switched off — raises an alert and shows on Home until it works again. A device that
// was simply off doesn't: HomeOwl calls it stopped only when the device was on the home network for hours without
// a query under its name, or when nothing came for three days.

const (
	setupLostOnline = 6 * time.Hour  // on the home Wi-Fi this long after its last query: the setup isn't in use
	setupLostQuiet  = 72 * time.Hour // nothing at all for this long
	ndLogsFresh     = 2 * time.Hour  // NextDNS logs must be arriving, or every device would look stopped
)

// onlineMACs remembers when each client was last online (presence.go reads the clients once a minute).
type onlineMACs struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (o *onlineMACs) mark(list []map[string]any, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]time.Time{}
	}
	for _, c := range list {
		if mac := strings.ToLower(str(c, "mac")); mac != "" {
			o.m[mac] = now
		}
	}
}

func (o *onlineMACs) at(mac string) time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m[strings.ToLower(mac)]
}

// The NextDNS log lookups (tests swap them).
var (
	ndSeen   = func(a *App, nd, name string) (analytics.NDSeen, error) { return a.An.NDDeviceSeen(nd, name) }
	ndLatest = func(a *App, nd string) (time.Time, error) { return a.An.NDProfileLatest(nd) }
)

// setupSeen is the device's last query under its name: in its NextDNS profile, or in the one it still has
// installed while an update waits. cur is the last one in the profile it should use.
func (a *App) setupSeen(x CheckIn) (last, cur time.Time, ip string, day int, err error) {
	// the names NextDNS may know it by: its name now, the one in the profile it installed, and those of the
	// check-ins merged into it (the profile on the device may be one of theirs)
	type src struct{ nd, name string }
	var srcs []src
	seenSrc := map[src]bool{}
	add := func(nd, name string) {
		k := src{nd, name}
		if nd != "" && name != "" && !seenSrc[k] {
			seenSrc[k] = true
			srcs = append(srcs, k)
		}
	}
	from := func(y CheckIn) {
		for _, n := range a.ndNameList(y) {
			add(x.NDProfile, n)
			add(y.ProfileND, n)
		}
	}
	from(x)
	for _, y := range a.checkins.List() {
		if y.Status == "merged" && y.MergedInto == x.ID {
			from(y)
		}
	}
	ok := false
	for _, k := range srcs {
		s, e := ndSeen(a, k.nd, k.name)
		if e != nil {
			err = e
			continue
		}
		ok = true
		day += s.Day
		if s.Last.After(last) {
			last, ip = s.Last, s.ClientIP
		}
		if k.nd == x.NDProfile && s.Last.After(cur) {
			cur = s.Last
		}
	}
	if ok {
		err = nil
	}
	return
}

// setupLost decides whether a setup that worked has stopped.
func setupLost(last, online, now time.Time) bool {
	if last.IsZero() {
		return false // never worked: "not seen", not "stopped"
	}
	if now.Sub(last) >= setupLostQuiet {
		return true
	}
	return online.Sub(last) >= setupLostOnline && now.Sub(online) < 30*time.Minute
}

func (a *App) setupWatchLoop(ctx context.Context) {
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.setupWatch(time.Now())
		t.Reset(15 * time.Minute)
	}
}

// setupWatch looks at every approved device with a setup: remembers when it last worked, notices an installed
// update, and raises (or clears) "stopped using its setup".
func (a *App) setupWatch(now time.Time) {
	if !a.An.Open() { // without NextDNS logs nothing is ever seen, so nothing is "stopped" either
		return
	}
	alertsOn := a.Cfg.Get().Alerts.Setup != "off"
	fresh := map[string]bool{}
	logsFresh := func(nd string) bool {
		if v, ok := fresh[nd]; ok {
			return v
		}
		t, err := ndLatest(a, nd)
		fresh[nd] = err == nil && !t.IsZero() && now.Sub(t) < ndLogsFresh
		return fresh[nd]
	}
	for _, x := range a.checkins.List() {
		if x.Status != "approved" || x.NDProfile == "" {
			continue
		}
		last, cur, _, _, err := a.setupSeen(x)
		if err != nil {
			continue
		}
		prev := x.SetupSeen
		installed := x.ProfileND != "" && x.ProfileND != x.NDProfile && !cur.IsZero() && cur.Unix() > x.SetupChanged
		if installed { // the device uses the new profile now: the old NextDNS name can go
			_ = a.An.UnassignDevice("ndev", x.ProfileND+"/"+x.Name)
			_ = a.An.UnassignDevice("ndev", x.ProfileND+"/"+orStr(x.ProfileName, a.ndName(x)))
		}
		back := x.SetupLost > 0 && !last.IsZero() && last.Unix() > prev
		lost := x.SetupLost == 0 && logsFresh(x.NDProfile) && setupLost(last, a.online.at(x.MAC), now)
		if !last.IsZero() && last.Unix() > prev || installed || back || lost {
			a.checkins.Edit(x.ID, func(c *CheckIn) {
				if !last.IsZero() && last.Unix() > c.SetupSeen {
					c.SetupSeen = last.Unix()
				}
				if installed {
					c.ProfileND = c.NDProfile
				}
				if back {
					c.SetupLost = 0
				}
				if lost {
					c.SetupLost = now.Unix()
				}
			})
		}
		data := map[string]any{"id": x.ID, "mac": x.MAC, "name": x.Name, "person": x.PersonName}
		switch {
		case lost && alertsOn:
			body := "NextDNS hasn't logged anything under its name since " + last.Local().Format("Mon 2 Jan 15:04")
			if now.Sub(last) < setupLostQuiet {
				body += ", though it has been on the home network"
			}
			a.Alerts.Add(alerts.Alert{Kind: "setup_lost", Level: "warn", Title: x.Name + " stopped using its setup",
				Body: body + ". The profile may have been removed, or Private DNS switched off. Open the check-in page on the device to set it up again.", Data: data})
		case back && alertsOn:
			a.Alerts.Add(alerts.Alert{Kind: "setup_back", Title: x.Name + " is using its setup again", Body: "NextDNS sees it by name again.", Data: data})
		}
	}
}

// setupAttention lists the devices whose setup stopped (Home's "needs you").
func (a *App) setupAttention() []map[string]any {
	out := []map[string]any{}
	older := map[string]bool{} // the same device checked in twice: Home asks to merge them instead
	for _, g := range a.checkinDuplicates() {
		for _, x := range g.List[1:] {
			older[x.ID] = true
		}
	}
	for _, x := range a.checkins.List() {
		if x.Status == "approved" && x.NDProfile != "" && x.SetupLost > 0 && !older[x.ID] {
			out = append(out, map[string]any{"id": x.ID, "name": x.Name, "person": x.PersonName, "since": x.SetupSeen})
		}
	}
	return out
}

// setupCoverage: for each person (not groups), their devices and whether each is set up — so it's easy to see
// which device escapes NextDNS. Devices come from People (by address) and from approved check-ins.
func (a *App) setupCoverage() map[string]any {
	byMac := map[string]CheckIn{}
	for _, x := range a.checkins.List() {
		if x.Status == "approved" {
			byMac[x.MAC] = x
		}
	}
	people := map[string]any{}
	if !a.An.Open() {
		return map[string]any{"people": people}
	}
	for _, p := range a.An.People() {
		if p.Kind == "group" {
			continue
		}
		seen := map[string]bool{}
		devs := []map[string]any{}
		add := func(mac string) {
			if mac == "" || seen[mac] {
				return
			}
			seen[mac] = true
			d := map[string]any{"mac": mac, "state": "no_checkin"}
			if x, ok := byMac[mac]; ok {
				d["id"], d["name"], d["kind"], d["state"] = x.ID, x.Name, x.Kind, "none"
				if x.NDProfile != "" {
					d["state"] = a.setupStatus(x)["state"]
				}
			}
			devs = append(devs, d)
		}
		for _, d := range p.Devices {
			if d.Kind == "mac" {
				add(strings.ToLower(d.Key))
			}
		}
		for _, x := range byMac {
			if x.PersonID == p.ID {
				add(x.MAC)
			}
		}
		people[p.ID] = devs
	}
	return map[string]any{"people": people}
}
