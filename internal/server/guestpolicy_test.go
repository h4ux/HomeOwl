package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

func TestGuestPolicy(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/wlanconf"):
			// the home Wi-Fi, UniFi's guest Wi-Fi, and one an admin picks as a guest Wi-Fi
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"w1","name":"Example Home","networkconf_id":"n1"},
				{"_id":"w2","name":"Example Guests","is_guest":true,"networkconf_id":"n2"},{"_id":"w3","name":"Visitors","networkconf_id":"n1"}]}`))
		case strings.HasSuffix(r.URL.Path, "/rest/user"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/cmd/stamgr"):
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			cmds = append(cmds, fmt.Sprint(m["cmd"], " ", m["mac"], m["macs"]))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer console.Close()
	forgetGuestNets()
	defer forgetGuestNets()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	if err := a.pickGuestSSIDs(map[string]any{"ids": []any{"w3"}}); err != nil {
		t.Fatal(err)
	}
	got := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), cmds...) }

	home := map[string]any{"mac": "00:11:22:00:00:01", "hostname": "family-laptop", "essid": "Example Home", "network_id": "n1"}
	visitor := map[string]any{"mac": "00:11:22:00:00:02", "hostname": "visitor-phone", "essid": "Visitors", "network_id": "n1"}
	guest := map[string]any{"mac": "00:11:22:00:00:03", "hostname": "guest-tablet", "essid": "Example Guests", "network_id": "n2"}
	moved := map[string]any{"mac": "00:11:22:00:00:04", "hostname": "moved-phone", "essid": "Example Home", "network_id": "n2"} // HomeOwl put it on the guest network

	// guests never wait for approval: UniFi's guest Wi-Fi, a picked one, and the guest network
	if !a.gated(home) || a.gated(visitor) || a.gated(guest) || a.gated(moved) {
		t.Fatalf("gated: home %v visitor %v guest %v moved %v", a.gated(home), a.gated(visitor), a.gated(guest), a.gated(moved))
	}

	// guest alerts on, home alerts off
	if err := a.guestPolicySave(map[string]any{"alert": true}); err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Alerts.NewDevice = "off" })
	a.watchNewDevices(nil) // the first time only remembers what UniFi knows
	a.watchNewDevices([]map[string]any{home, visitor})
	l := a.Alerts.List()
	if len(l) != 1 || l[0].Kind != "guest_joined" || !strings.Contains(l[0].Title, "visitor-phone") {
		t.Fatalf("want one guest alert, got %+v", l)
	}
	// "all" from older settings means guests too; switching guests off turns it back to home only
	_ = a.Cfg.Update(func(c *config.Config) { c.Alerts.NewDevice = "all"; c.Guest.Alert = false })
	if !guestAlertsOn(a.Cfg.Get()) {
		t.Fatal(`"all" should include guests`)
	}
	if err := a.guestPolicySave(map[string]any{"alert": false}); err != nil {
		t.Fatal(err)
	}
	if c := a.Cfg.Get(); guestAlertsOn(c) || c.Alerts.NewDevice != "" {
		t.Fatalf("guest alerts off: new_device %q", c.Alerts.NewDevice)
	}

	// removing guests not seen for a while
	a.guestTick([]map[string]any{visitor, guest, home})
	if n := len(a.guests.list()); n != 2 {
		t.Fatalf("remembered %d guests, want 2", n)
	}
	if err := a.guestPolicySave(map[string]any{"forget_days": float64(-1)}); err == nil {
		t.Fatal("negative days accepted")
	}
	if err := a.guestPolicySave(map[string]any{"forget_days": float64(30)}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if names, err := a.forgetStaleGuests(now, "test"); err != nil || len(names) != 0 {
		t.Fatalf("just seen: removed %v (%v)", names, err)
	}
	// both last seen 40 days ago; the visitor's tablet is tagged in Who's home, so it stays
	a.guests.mu.Lock()
	for k, x := range a.guests.m {
		x.Last = now.AddDate(0, 0, -40).UnixMilli()
		a.guests.m[k] = x
	}
	a.guests.mu.Unlock()
	_ = a.Cfg.Update(func(c *config.Config) { c.Presence.Devices = map[string]string{"00:11:22:00:00:02": "p1"} })
	names, err := a.forgetStaleGuests(now, "test")
	if err != nil || len(names) != 1 || names[0] != "guest-tablet" {
		t.Fatalf("removed %v (%v)", names, err)
	}
	if c := got(); len(c) != 1 || c[0] != "forget-sta <nil> [00:11:22:00:00:03]" {
		t.Fatalf("UniFi commands = %v", c)
	}
	v := a.guestPolicyView()
	devs, _ := v["devices"].([]forgetCand)
	if len(devs) != 1 || devs[0].Reason != "tagged in Who's home" {
		t.Fatalf("left: %+v", devs)
	}
	logged := false
	for _, ch := range a.Changes.List() {
		logged = logged || (ch.After["command"] == "forget-sta" && ch.By == "test" && ch.OK)
	}
	if !logged {
		t.Fatalf("the removal isn't in the change log: %+v", a.Changes.List())
	}

	// seen on a home network: not a guest any more
	a.guestTick([]map[string]any{{"mac": "00:11:22:00:00:02", "hostname": "visitor-phone", "essid": "Example Home", "network_id": "n1"}})
	if n := len(a.guests.list()); n != 0 {
		t.Fatalf("a device on the home Wi-Fi is still listed as a guest (%d)", n)
	}
}
