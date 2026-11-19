package server

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

// A checked-in phone whose private Wi-Fi address rotates is followed by its specific host name, and only then.
func TestFollowRotated(t *testing.T) {
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
	old := map[string]any{"mac": "aa:bb:cc:00:00:40", "hostname": "Alexs-iPhone"}
	a.followRotated([]map[string]any{old}, now) // learns its host name
	if x, _ := a.checkins.Get(id); x.Host != "Alexs-iPhone" {
		t.Fatalf("host not remembered: %+v", x)
	}
	newer := map[string]any{"mac": "6e:bb:cc:00:00:41", "hostname": "Alexs-iPhone"}
	steps := []struct {
		why  string
		list []map[string]any
	}{
		{"old address still online", []map[string]any{old, newer}},
		{"two devices with the name", []map[string]any{newer, {"mac": "6e:bb:cc:00:00:42", "hostname": "alexs-iphone"}}},
		{"a real address isn't a rotation", []map[string]any{{"mac": "00:11:22:00:00:41", "hostname": "Alexs-iPhone"}}},
		{"wired", []map[string]any{{"mac": "6e:bb:cc:00:00:43", "hostname": "Alexs-iPhone", "is_wired": true}}},
		{"another name", []map[string]any{{"mac": "6e:bb:cc:00:00:44", "hostname": "Sams-iPhone"}}},
	}
	for _, s := range steps {
		a.followRotated(s.list, now)
		if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" {
			t.Fatalf("%s: moved to %s", s.why, x.MAC)
		}
	}
	// a generic name says nothing
	a.checkins.Edit(id, func(y *CheckIn) { y.Host = "iPhone" })
	a.followRotated([]map[string]any{{"mac": "6e:bb:cc:00:00:45", "hostname": "iPhone"}}, now)
	if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("followed a generic host name")
	}
	// too long ago
	a.checkins.Edit(id, func(y *CheckIn) { y.Host, y.HostAt = "Alexs-iPhone", now.Add(-30*24*time.Hour).Unix() })
	a.followRotated([]map[string]any{newer}, now)
	if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("followed a name last seen a month ago")
	}
	a.checkins.Edit(id, func(y *CheckIn) { y.HostAt = now.Add(-time.Hour).Unix() })
	a.followRotated([]map[string]any{newer}, now)
	x, _ := a.checkins.Get(id)
	if x.MAC != "6e:bb:cc:00:00:41" {
		t.Fatalf("not followed: %+v", x)
	}
	if _, ok := a.checkins.Registered("6e:bb:cc:00:00:41"); !ok {
		t.Fatal("the new address isn't a registered device, so device approval would block it")
	}
	found := false
	for _, al := range a.Alerts.List() {
		found = found || al.Kind == "rotated"
	}
	if !found {
		t.Fatal("no alert for the admins")
	}
}
