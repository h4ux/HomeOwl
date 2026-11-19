package server

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

// A phone that installed its profile on the setup Wi-Fi and comes to the home Wi-Fi as "iPhone" is recognised when
// it's the only one waiting and the only stranger that joined since; otherwise the approval list says who it
// probably is, and "It's …" moves it there. Its old addresses are remembered and suggested for archiving.
func TestNewDeviceFollowsCheckIn(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPhone","kind":"phone","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	id := a.checkins.List()[0].ID
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPhone"}, "admin"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	wait := func(at time.Time) {
		a.checkins.Edit(id, func(y *CheckIn) {
			y.MAC, y.Onboard, y.FixedFrom, y.FixedHost, y.FixedAt = "aa:bb:cc:00:00:40", true, "aa:bb:cc:00:00:40", "", at.Unix()
		})
	}
	wait(now.Add(-5 * time.Minute))
	joined := float64(now.Add(-time.Minute).Unix())
	stranger := map[string]any{"mac": "6e:bb:cc:00:00:41", "hostname": "iPhone", "first_seen": joined}

	// two strangers joined since: can't tell
	a.followFixed([]map[string]any{stranger, {"mac": "6e:bb:cc:00:00:42", "hostname": "iPhone", "first_seen": joined}})
	if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("guessed between two")
	}
	// one that was there before the profile was installed isn't it
	a.followFixed([]map[string]any{{"mac": "6e:bb:cc:00:00:43", "first_seen": float64(now.Add(-time.Hour).Unix())}})
	if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("followed a device that came before")
	}
	// the only one: followed, old address remembered
	a.followFixed([]map[string]any{stranger})
	x, _ := a.checkins.Get(id)
	if x.MAC != "6e:bb:cc:00:00:41" || len(x.OldMACs) != 1 || x.OldMACs[0] != "aa:bb:cc:00:00:40" || x.FixedFrom != "" {
		t.Fatalf("not followed: %+v", x)
	}
	if l := a.deviceAddresses("6e:bb:cc:00:00:41"); len(l) != 1 || l[0] != "aa:bb:cc:00:00:40" {
		t.Fatalf("addresses: %v", l)
	}
	// the old address is suggested for archiving as an older copy
	known := []map[string]any{{"mac": "aa:bb:cc:00:00:40", "last_seen": float64(now.Add(-3 * 24 * time.Hour).Unix())}}
	found := false
	for _, c := range a.archiveSuggestions(known, []map[string]any{stranger}, nil, nil, now) {
		found = found || (c.MAC == "aa:bb:cc:00:00:40" && c.Kind == "duplicate" && c.KeptMAC == "6e:bb:cc:00:00:41")
	}
	if !found {
		t.Fatal("old address not suggested")
	}

	// waiting again, but the new one joined three hours later: not followed, only suggested
	wait(now.Add(-4 * time.Hour))
	late := map[string]any{"mac": "6e:bb:cc:00:00:44", "hostname": "iPhone", "first_seen": joined}
	a.followFixed([]map[string]any{late})
	if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("followed after two hours")
	}
	l := a.likelyDevice("6e:bb:cc:00:00:44", "iPhone", int64(joined), now)
	if len(l) != 1 || l[0]["id"] != id {
		t.Fatalf("likely: %v", l)
	}
	if _, err := a.approvalAPI("same", map[string]any{"mac": "6e:bb:cc:00:00:44", "id": id}, "admin"); err != nil {
		t.Fatal(err)
	}
	if x, _ := a.checkins.Get(id); x.MAC != "6e:bb:cc:00:00:44" {
		t.Fatalf("same: %+v", x)
	}
	// a ghost: a nameless private address nobody owns, gone for weeks
	ghost := []map[string]any{{"mac": "6e:bb:cc:00:00:99", "last_seen": float64(now.Add(-20 * 24 * time.Hour).Unix())}}
	found = false
	for _, c := range a.archiveSuggestions(ghost, nil, nil, nil, now) {
		found = found || (c.MAC == "6e:bb:cc:00:00:99" && c.Kind == "stale")
	}
	if !found {
		t.Fatal("ghost not suggested")
	}
}
