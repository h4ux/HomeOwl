package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// A visitor on the home Wi-Fi goes on the guest network (the per-client override), by hand or when nobody decided
// in time; "back to home network" undoes it; a checked-in device is never moved. More time pushes the block back.
func TestGuestMove(t *testing.T) {
	var mu sync.Mutex
	var puts []map[string]any
	var cmds []string
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(p, "/rest/wlanconf"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"w1","name":"Example Home","enabled":true,"networkconf_id":"n1"},
				{"_id":"w2","name":"Example Guests","is_guest":true,"enabled":true,"networkconf_id":"n2"}]}`))
		case strings.HasSuffix(p, "/rest/networkconf/n2"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"n2","name":"Guests","purpose":"guest"}]}`))
		case strings.HasSuffix(p, "/stat/sta"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"ip":"192.168.1.41","mac":"aa:bb:cc:00:00:41","hostname":"iPhone","user_id":"u41","essid":"Example Home"}]}`))
		case strings.HasSuffix(p, "/rest/user/u41"):
			if r.Method == "PUT" {
				b, _ := io.ReadAll(r.Body)
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				mu.Lock()
				puts = append(puts, m)
				mu.Unlock()
			}
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"u41","mac":"aa:bb:cc:00:00:41","virtual_network_override_enabled":false}]}`))
		case strings.HasSuffix(p, "/rest/user"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"u41","mac":"aa:bb:cc:00:00:41"}]}`))
		case strings.HasSuffix(p, "/cmd/stamgr"):
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			cmds = append(cmds, m["cmd"].(string))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer console.Close()
	resetWlans()
	guestNetCache.Lock()
	guestNetCache.at = time.Time{}
	guestNetCache.Unlock()
	t.Cleanup(func() {
		guestNetCache.Lock()
		guestNetCache.at = time.Time{}
		guestNetCache.Unlock()
	})
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.Approve, c.Onboard.GraceMinutes = true, 60 })
	mac := "aa:bb:cc:00:00:41"
	now := time.Now()
	setGrace := func(at time.Time) {
		a.approval.mu.Lock()
		st := a.approval.load()
		st.Seeded, st.SeedRecent = now.Unix(), true
		st.Approved["aa:bb:cc:00:00:01"] = now.Unix()
		st.Grace = map[string]pendingDevice{mac: {MAC: mac, Hostname: "iPhone", At: at.Unix()}}
		delete(st.Approved, mac)
		_ = a.approval.save(st)
		a.approval.mu.Unlock()
	}

	// more time: a day after it would have been blocked
	setGrace(now.Add(-30 * time.Minute))
	if _, err := a.approvalAPI("extend", map[string]any{"mac": mac, "minutes": 1440.0}, "admin"); err != nil {
		t.Fatal(err)
	}
	g := a.approval.load().Grace[mac]
	if want := now.Add(30*time.Minute + 24*time.Hour).Unix(); g.Until < want-5 || g.Until > want+5 {
		t.Fatalf("until %v, want about %v", g.Until, want)
	}
	if v := a.approvalView(); v["guest_net"] != "Guests" {
		t.Fatalf("guest network: %v", v["guest_net"])
	}

	// nobody decided, and undecided devices go to the guest network
	if _, err := a.approvalAPI("when_due", map[string]any{"value": "guest"}, "admin"); err != nil {
		t.Fatal(err)
	}
	setGrace(now.Add(-2 * time.Hour))
	a.approvalTick([]map[string]any{{"mac": mac, "hostname": "iPhone", "essid": "Example Home"}})
	st := a.approval.load()
	if _, ok := st.Guests[mac]; !ok || st.Approved[mac] == 0 || len(st.Blocked) != 0 {
		t.Fatalf("not moved: %+v", st)
	}
	mu.Lock()
	if len(puts) != 1 || puts[0]["virtual_network_override_enabled"] != true || puts[0]["virtual_network_override_id"] != "n2" ||
		strings.Join(cmds, ",") != "kick-sta" {
		t.Fatalf("UniFi: %v %v", puts, cmds)
	}
	mu.Unlock()

	// back home
	if _, err := a.approvalAPI("unguest", map[string]any{"mac": mac}, "admin"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(puts) != 2 || puts[1]["virtual_network_override_enabled"] != false {
		t.Fatalf("undo: %v", puts)
	}
	mu.Unlock()
	if _, ok := a.approval.load().Guests[mac]; ok {
		t.Fatal("still listed as a guest")
	}

	// a family device is never moved
	a.checkins.Add(CheckIn{Name: "Sam's Phone", MAC: mac})
	id := a.checkins.List()[0].ID
	a.checkins.Edit(id, func(c *CheckIn) { c.Status = "approved" })
	if err := a.moveToGuest(mac, "admin"); err == nil {
		t.Fatal("moved a checked-in device")
	}
}
