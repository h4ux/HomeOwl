package server

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestSetupLostRule(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		last, online time.Time
		want         bool
	}{
		{time.Time{}, now, false},                                   // never worked: not "stopped"
		{now.Add(-time.Hour), now, false},                           // working
		{now.Add(-8 * time.Hour), now.Add(-time.Minute), true},      // at home for hours, no queries
		{now.Add(-8 * time.Hour), time.Time{}, false},               // just switched off
		{now.Add(-8 * time.Hour), now.Add(-7 * time.Hour), false},   // left soon after
		{now.Add(-80 * time.Hour), time.Time{}, true},               // three days of nothing
		{now.Add(-20 * time.Hour), now.Add(-2 * time.Minute), true}, // online now, quiet since yesterday
	} {
		if got := setupLost(c.last, c.online, now); got != c.want {
			t.Errorf("last %v online %v: %v, want %v", now.Sub(c.last), now.Sub(c.online), got, c.want)
		}
	}
}

func TestSetupWatch(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	id := a.checkins.List()[0].ID
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPad", "nd_profile": "abc123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	seen := map[string]time.Time{} // profile → last query under the device's name
	latest := now.Add(-time.Minute)
	ndSeen = func(_ *App, nd, _ string) (analytics.NDSeen, error) { return analytics.NDSeen{Last: seen[nd]}, nil }
	ndLatest = func(*App, string) (time.Time, error) { return latest, nil }
	t.Cleanup(func() {
		ndSeen = func(a *App, nd, name string) (analytics.NDSeen, error) { return a.An.NDDeviceSeen(nd, name) }
		ndLatest = func(a *App, nd string) (time.Time, error) { return a.An.NDProfileLatest(nd) }
	})
	count := func(kind string) int {
		n := 0
		for _, al := range a.Alerts.List() {
			if al.Kind == kind {
				n++
			}
		}
		return n
	}
	online := []map[string]any{{"mac": "aa:bb:cc:00:00:40"}}

	// it works
	seen["abc123"] = now.Add(-10 * time.Minute)
	a.online.mark(online, now)
	a.setupWatch(now)
	if x, _ := a.checkins.Get(id); x.SetupSeen == 0 || x.SetupLost != 0 {
		t.Fatalf("working: %+v", x)
	}
	// hours later it's on the home Wi-Fi, but nothing under its name: stopped, one alert, on Home
	later := now.Add(8 * time.Hour)
	latest = later.Add(-time.Minute)
	a.online.mark(online, later)
	a.setupWatch(later)
	a.setupWatch(later)
	if count("setup_lost") != 1 || len(a.setupAttention()) != 1 {
		t.Fatalf("lost: %d alerts, attention %v", count("setup_lost"), a.setupAttention())
	}
	// NextDNS logs that stopped arriving don't make devices "stopped"
	x, _ := a.checkins.Get(id)
	a.checkins.Edit(id, func(c *CheckIn) { c.SetupLost = 0 })
	latest = later.Add(-5 * time.Hour)
	a.setupWatch(later)
	if y, _ := a.checkins.Get(id); y.SetupLost != 0 {
		t.Fatal("stale logs should not raise the alert")
	}
	a.checkins.Edit(id, func(c *CheckIn) { c.SetupLost = x.SetupLost })
	latest = later.Add(-time.Minute)
	// queries again: cleared, with a "working again" alert
	seen["abc123"] = later.Add(time.Minute)
	a.setupWatch(later.Add(2 * time.Minute))
	if count("setup_back") != 1 || len(a.setupAttention()) != 0 {
		t.Fatalf("back: %d, attention %v", count("setup_back"), a.setupAttention())
	}
	// alerts off: still tracked, no alert
	_ = a.alertsSettings(map[string]any{"setup": false})
	a.online.mark(online, later.Add(10*time.Hour))
	latest = later.Add(10 * time.Hour)
	a.setupWatch(later.Add(10 * time.Hour))
	if count("setup_lost") != 1 || len(a.setupAttention()) != 1 {
		t.Fatalf("alerts off: %d alerts, attention %v", count("setup_lost"), a.setupAttention())
	}

	// a new NextDNS profile: the installed one keeps its name in People until the device uses the new one
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "def456"}); err != nil {
		t.Fatal(err)
	}
	links := func() map[string]bool {
		l := map[string]bool{}
		for _, p := range a.An.People() {
			for _, d := range p.Devices {
				l[d.Kind+" "+d.Key] = true
			}
		}
		return l
	}
	if l := links(); !l["ndev abc123/Alex's iPad"] || !l["ndev def456/Alex's iPad"] {
		t.Fatalf("both names while the update waits: %v", l)
	}
	if x, _ := a.checkins.Get(id); x.ProfileND != "abc123" {
		t.Fatalf("installed profile: %+v", x)
	}
	seen["def456"] = time.Now().Add(time.Minute)
	a.setupWatch(time.Now().Add(2 * time.Minute))
	if x, _ := a.checkins.Get(id); x.ProfileND != "def456" {
		t.Fatalf("the update should count as installed: %+v", x)
	}
	if l := links(); l["ndev abc123/Alex's iPad"] || !l["ndev def456/Alex's iPad"] {
		t.Fatalf("the old name should go: %v", l)
	}

	// coverage: Alex's iPad is listed under Alex
	cov := a.setupCoverage()["people"].(map[string]any)[alex].([]map[string]any)
	if len(cov) != 1 || cov[0]["name"] != "Alex's iPad" {
		t.Fatalf("coverage: %v", cov)
	}
}
