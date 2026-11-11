package server

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// A device never seen before raises one alert; known devices, repeats and (by default) guest Wi-Fi don't.
func TestNewDeviceAlert(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	a.Alerts.Send = nil
	a.seen.add([]string{"b0:bb:cc:00:00:01"}, 1) // seeded: what UniFi already knew
	online := []map[string]any{
		{"mac": "B0:BB:CC:00:00:01", "name": "Old laptop"},
		{"mac": "b0:bb:cc:00:00:02", "hostname": "Kindle", "oui": "Amazon", "ip": "192.168.1.50", "network": "Main"},
		{"mac": "b0:bb:cc:00:00:03", "hostname": "Visitor", "is_guest": true},
	}
	a.watchNewDevices(online)
	a.watchNewDevices(online)
	l := a.Alerts.List()
	if len(l) != 1 || l[0].Title != "New device: Kindle" || l[0].Data["mac"] != "b0:bb:cc:00:00:02" || l[0].Body != "Amazon · 192.168.1.50 · Main" {
		t.Fatalf("alerts: %+v", l)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Alerts.NewDevice = "all" })
	a.watchNewDevices(append(online, map[string]any{"mac": "02:00:00:00:00:04", "is_guest": true}))
	if l := a.Alerts.List(); len(l) != 2 || l[0].Body != "private address" || l[0].Title != "New device: Unknown device" {
		t.Fatalf("guest device with 'all': %+v", l[0])
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Alerts.NewDevice = "off" })
	a.watchNewDevices([]map[string]any{{"mac": "b0:bb:cc:00:00:09"}})
	if len(a.Alerts.List()) != 2 {
		t.Fatal("off means off")
	}
}

func TestGuestRotationSchedule(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, loc) // a Wednesday
	w := config.Guest{Rotate: "weekly", RotateDay: 1, RotateHour: 4}
	if got := lastRotation(w, now); !got.Equal(time.Date(2026, 11, 9, 4, 0, 0, 0, loc)) {
		t.Fatalf("weekly last %v", got)
	}
	if got := nextRotation(w, now); !got.Equal(time.Date(2026, 11, 16, 4, 0, 0, 0, loc)) {
		t.Fatalf("weekly next %v", got)
	}
	w.RotateDay = 3 // Wednesday 04:00 already passed today
	if got := lastRotation(w, now); !got.Equal(time.Date(2026, 11, 11, 4, 0, 0, 0, loc)) {
		t.Fatalf("same day %v", got)
	}
	m := config.Guest{Rotate: "monthly", RotateDay: 15, RotateHour: 3}
	if got := lastRotation(m, now); !got.Equal(time.Date(2026, 10, 15, 3, 0, 0, 0, loc)) {
		t.Fatalf("monthly last %v", got)
	}
	if got := nextRotation(m, now); !got.Equal(time.Date(2026, 11, 15, 3, 0, 0, 0, loc)) {
		t.Fatalf("monthly next %v", got)
	}
	if !lastRotation(config.Guest{}, now).IsZero() {
		t.Fatal("no schedule")
	}
}
