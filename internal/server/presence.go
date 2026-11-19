package server

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/presence"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Who's home: a person is home while one of their tagged devices (usually a phone) is connected to the home
// network. HomeOwl looks at UniFi's connected clients once a minute; nothing leaves this computer.

var macRE = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

func newPresence(dataDir string) *presence.Tracker {
	return presence.Open(filepath.Join(dataDir, "presence.json"))
}

func presenceOptions(c config.Presence) presence.Options {
	away := c.AwayMinutes
	if away <= 0 {
		away = 10
	}
	days := c.Days
	if days <= 0 {
		days = 90
	}
	return presence.Options{Grace: time.Duration(away) * time.Minute, History: !c.NoHistory, Days: days}
}

// presencePeople: everyone with at least one tagged device.
func (a *App) presencePeople() []presence.Person {
	tags := a.Cfg.Get().Presence.Devices
	if len(tags) == 0 {
		return nil
	}
	byPerson := map[string][]string{}
	for mac, pid := range tags {
		byPerson[pid] = append(byPerson[pid], mac)
	}
	var out []presence.Person
	for _, p := range a.An.People() {
		if macs := byPerson[p.ID]; len(macs) > 0 && p.Kind != "group" { // a group (the TV, servers) is never "home"
			out = append(out, presence.Person{ID: p.ID, Name: p.Name, Devices: macs})
		}
	}
	return out
}

func (a *App) presenceLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	defer func() { _ = a.presence.Save() }()
	for {
		a.presenceTick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.presenceKick:
		}
	}
}

// presenceTick reads the clients online once a minute: for Who's home and for the new-device alert.
func (a *App) presenceTick() {
	people := a.presencePeople()
	if (len(people) == 0 && a.Cfg.Get().Alerts.NewDevice == "off" && !a.fixedPending() && !a.Cfg.Get().Onboard.Approve) || !a.UniFi.Connected() {
		return
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("stat/sta", false))
	if st != 200 {
		return // can't see the network: say nothing rather than send everyone away
	}
	list := unifi.List(d)
	a.online.mark(list, time.Now())
	if a.fixedPending() {
		a.followFixed(list) // before the new-device alert: a phone that switched to its real address isn't new
	}
	a.followRotated(list, time.Now()) // a checked-in device back with a new private address isn't new either
	a.approvalTick(list)              // blocks first-time devices on the home Wi-Fi until an admin approves them
	a.watchNewDevices(list)
	if len(people) == 0 {
		return
	}
	online := map[string]string{}
	for _, c := range list {
		mac, _ := c["mac"].(string)
		if mac == "" {
			continue
		}
		name, _ := c["name"].(string)
		if name == "" {
			name, _ = c["hostname"].(string)
		}
		online[strings.ToLower(mac)] = name
	}
	a.presence.Observe(time.Now(), online, people, presenceOptions(a.Cfg.Get().Presence))
	if err := a.presence.Save(); err != nil {
		log.Printf("presence: %v", err)
	}
}

func (a *App) kickPresence() {
	select {
	case a.presenceKick <- struct{}{}:
	default:
	}
}

// presenceView is the Who's home page.
func (a *App) presenceView(admin bool, days int) map[string]any {
	c := a.Cfg.Get().Presence
	o := presenceOptions(c)
	if days < 1 || days > 365 {
		days = 1
	}
	now := a.presence.Now()
	tags := map[string][]string{}
	for mac, pid := range c.Devices {
		tags[pid] = append(tags[pid], mac)
	}
	people := []map[string]any{}
	for _, p := range a.An.People() {
		if p.Kind == "group" {
			continue
		}
		m := map[string]any{"id": p.ID, "name": p.Name, "color": p.Color, "devices": orEmpty(tags[p.ID])}
		if st, ok := now[p.ID]; ok {
			m["state"] = st
		}
		people = append(people, m)
	}
	out := map[string]any{"people": people, "history": o.History, "days": days, "away_minutes": int(o.Grace.Minutes()), "keep_days": o.Days,
		"connected": a.UniFi.Connected()}
	if o.History {
		out["events"] = a.presence.Since(time.Now().AddDate(0, 0, -days).UnixMilli())
	}
	if admin {
		out["admin"] = true
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// presenceTag gives a device to a person for presence ("" person: untag). The device also becomes theirs in
// Analytics, so their usage is counted too.
func (a *App) presenceTag(body map[string]any) error {
	mac, pid := strings.ToLower(s(body, "mac")), s(body, "person")
	if !macRE.MatchString(mac) {
		return errors.New("that isn't a device address (MAC)")
	}
	if pid != "" {
		found := false
		for _, p := range a.An.People() {
			found = found || (p.ID == pid && p.Kind != "group")
		}
		if !found {
			return errors.New("no such person; add them under Family › People & groups first")
		}
	}
	if err := a.Cfg.Update(func(c *config.Config) {
		if c.Presence.Devices == nil {
			c.Presence.Devices = map[string]string{}
		}
		if pid == "" {
			delete(c.Presence.Devices, mac)
		} else {
			c.Presence.Devices[mac] = pid
		}
	}); err != nil {
		return err
	}
	if pid == "" {
		a.presence.Forget(mac)
	} else {
		// the device's earlier addresses are theirs too: Analytics and the history follow
		for _, m := range append([]string{mac}, a.deviceAddresses(mac)...) {
			if err := a.An.AssignDevice(pid, "mac", m); err != nil {
				log.Printf("presence: assigning %s: %v", m, err)
			}
		}
		if n, err := a.presenceRepair(pid, time.Now()); err != nil {
			log.Printf("presence: rebuilding the history: %v", err)
		} else if n > 0 {
			log.Printf("presence: rebuilt %d arrivals and departures from the device's past connections", n)
		}
	}
	a.kickPresence()
	return nil
}

// deviceAddresses: the other addresses the device at this address had: the ones HomeOwl followed it from, and those
// of check-ins merged into it.
func (a *App) deviceAddresses(mac string) []string {
	x, ok := a.checkins.Registered(mac)
	if !ok {
		return nil
	}
	var out []string
	add := func(m string) {
		if m = strings.ToLower(m); m != "" && m != mac && !contains(out, m) {
			out = append(out, m)
		}
	}
	for _, m := range x.OldMACs {
		add(m)
	}
	for _, y := range a.checkins.List() {
		if y.Status == "merged" && y.MergedInto == x.ID {
			add(y.MAC)
			for _, m := range y.OldMACs {
				add(m)
			}
		}
	}
	return out
}

// presenceRepair rewrites someone's Who's home history from UniFi's record of when their devices were connected,
// over the time that record covers: a device tagged today fills in the days it went untagged, and a device's
// earlier addresses count too. Only the time before the last sync, minus the away time, is touched.
func (a *App) presenceRepair(pid string, now time.Time) (int, error) {
	pc := a.Cfg.Get().Presence
	if pc.NoHistory || !a.An.Open() {
		return 0, nil
	}
	var name string
	for _, p := range a.An.People() {
		if p.ID == pid {
			name = p.Name
		}
	}
	names := map[string]string{}
	var macs []string
	for m, p := range pc.Devices {
		if p != pid {
			continue
		}
		for _, x := range append([]string{m}, a.deviceAddresses(m)...) {
			if _, ok := names[x]; !ok {
				macs = append(macs, x)
				names[x] = m
			}
		}
	}
	if name == "" || len(macs) == 0 {
		return 0, nil
	}
	o := presenceOptions(pc)
	spans, first, upto, err := a.An.ConnSpans(macs, now.AddDate(0, 0, -o.Days))
	if err != nil || upto.IsZero() {
		return 0, err
	}
	devName := map[string]string{}
	for _, x := range a.checkins.List() {
		if x.Status == "approved" {
			devName[x.MAC] = x.Name
		}
	}
	var l []presence.Span
	for _, s := range spans {
		cur := names[s.MAC] // the address it has now names it
		l = append(l, presence.Span{Device: cur, DeviceName: orStr(devName[cur], cur), Start: s.Start.UnixMilli(), Stop: s.Stop.UnixMilli()})
	}
	from := max(first.UnixMilli(), now.AddDate(0, 0, -o.Days).UnixMilli())
	to := min(upto.UnixMilli(), now.Add(-o.Grace).UnixMilli())
	n := a.presence.Rebuild(pid, name, l, o.Grace, from, to)
	_ = a.presence.Save()
	return n, nil
}

// movePresenceTag: a device got a new address (fixedmac.go, merge.go); Who's home follows it there.
func (a *App) movePresenceTag(from, to string) {
	pid := a.Cfg.Get().Presence.Devices[from]
	if pid == "" || from == to || to == "" {
		return
	}
	_ = a.Cfg.Update(func(c *config.Config) {
		delete(c.Presence.Devices, from)
		c.Presence.Devices[to] = pid
	})
	a.presence.Forget(from)
	a.kickPresence()
}

func (a *App) presenceSettings(body map[string]any) error {
	return a.Cfg.Update(func(c *config.Config) {
		if has(body, "history") {
			c.Presence.NoHistory = !b(body, "history")
		}
		if d, ok := n(body, "keep_days"); ok {
			c.Presence.Days = min(max(d, 7), 365)
		}
		if m, ok := n(body, "away_minutes"); ok {
			c.Presence.AwayMinutes = min(max(m, 2), 120)
		}
	})
}
