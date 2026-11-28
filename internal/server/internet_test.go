package server

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/netq"
)

func TestSpeedTestSchedule(t *testing.T) {
	loc := time.UTC
	day := func(d, h, m int) time.Time { return time.Date(2026, 3, d, h, m, 0, 0, loc) }
	daily := config.Internet{SpeedTest: "daily", Hour: 4}
	cases := []struct {
		c         config.Internet
		last, now time.Time
		due       bool
	}{
		{daily, time.Time{}, day(2, 4, 0), true},
		{daily, day(2, 4, 1), day(2, 4, 30), false}, // ran this morning
		{daily, day(1, 4, 1), day(2, 4, 5), true},
		{daily, day(1, 4, 1), day(2, 5, 0), false}, // not the hour
		{config.Internet{SpeedTest: "6h"}, day(2, 1, 0), day(2, 6, 0), false},
		{config.Internet{SpeedTest: "6h"}, day(2, 1, 0), day(2, 7, 0), true},
		{config.Internet{}, time.Time{}, day(2, 4, 0), false},
	}
	for i, c := range cases {
		if got := speedTestDue(c.c, c.last, c.now); got != c.due {
			t.Errorf("case %d: due = %v, want %v", i, got, c.due)
		}
	}
	if n := nextSpeedTest(daily, day(2, 4, 1), day(2, 9, 0)); n != day(3, 4, 0).UnixMilli() {
		t.Errorf("next daily = %v", time.UnixMilli(n).UTC())
	}
	if n := nextSpeedTest(daily, day(1, 4, 1), day(2, 2, 0)); n != day(2, 4, 0).UnixMilli() {
		t.Errorf("next daily later today = %v", time.UnixMilli(n).UTC())
	}
	if n := nextSpeedTest(config.Internet{}, time.Time{}, day(2, 2, 0)); n != 0 {
		t.Errorf("no schedule: next = %d", n)
	}
}

func TestConsoleHost(t *testing.T) {
	for in, want := range map[string]string{"192.168.1.1": "192.168.1.1", "https://192.168.1.1:443/": "192.168.1.1", " unifi.example.com ": "unifi.example.com", "": ""} {
		if got := consoleHost(in); got != want {
			t.Errorf("consoleHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInternetSettingsAndAlerts(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.internetSave(map[string]any{"speed_test": "hourly"}); err == nil {
		t.Error("an unknown schedule was accepted")
	}
	if err := a.internetSave(map[string]any{"speed_test": "daily", "hour": float64(25)}); err == nil {
		t.Error("hour 25 was accepted")
	}
	if err := a.internetSave(map[string]any{"readings": true, "speed_test": "daily", "hour": float64(3), "plan_down": float64(500), "plan_up": "50", "alerts": false}); err != nil {
		t.Fatal(err)
	}
	c := a.Cfg.Get()
	if c.Internet.Off || c.Internet.SpeedTest != "daily" || c.Internet.Hour != 3 || c.Internet.PlanDown != 500 || c.Internet.PlanUp != 50 || c.Alerts.Internet != "off" {
		t.Fatalf("saved %+v, alerts %q", c.Internet, c.Alerts.Internet)
	}
	// alerts off: nothing added
	a.internetEvent(netq.Event{Kind: "down", Outage: netq.Outage{Start: time.Now().UnixMilli(), Cause: "isp"}})
	if len(a.Alerts.List()) != 0 {
		t.Fatal("an alert was added while internet alerts are off")
	}
	if err := a.internetSave(map[string]any{"alerts": true}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-7 * time.Minute).UnixMilli()
	a.internetEvent(netq.Event{Kind: "down", Outage: netq.Outage{Start: start, Cause: "home"}})
	a.internetEvent(netq.Event{Kind: "up", Outage: netq.Outage{Start: start, End: time.Now().UnixMilli(), Cause: "home"}})
	l := a.Alerts.List()
	if len(l) != 2 {
		t.Fatalf("want 2 alerts, got %d", len(l))
	}
	kinds := map[string]string{}
	for _, x := range l {
		kinds[x.Kind] = x.Body
	}
	if kinds["internet_up"] != "It was down for 7 minutes (inside the home)." || kinds["internet_down"] == "" {
		t.Fatalf("alerts = %+v", kinds)
	}

	// the page works without UniFi, and only admins see the settings
	v := a.internetView(false, 7)
	if v["settings"] != nil || v["connected"] != false || v["days"] != 7 {
		t.Fatalf("viewer's page: %+v", v)
	}
	if v := a.internetView(true, 1); v["settings"] == nil {
		t.Fatal("admin's page has no settings")
	}
	if verdict, _ := v["verdict"].(netq.Verdict); verdict.Level != "info" {
		t.Fatalf("no readings yet: verdict %+v", verdict)
	}
}

func TestHomeGatewayFallsBackToConsole(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if gw, _ := a.homeGateway(); gw != "" {
		t.Fatalf("no console set: gateway %q", gw)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Host = "https://192.168.1.1" })
	if gw, _ := a.homeGateway(); gw != "192.168.1.1" {
		t.Fatalf("gateway %q, want the console's address", gw)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Host = "203.0.113.10" }) // a public address isn't the home's gateway
	if gw, _ := a.homeGateway(); gw != "" {
		t.Fatalf("public console address used as the gateway: %q", gw)
	}
}
