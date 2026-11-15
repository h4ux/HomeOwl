package server

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestMergeCheckins(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.An.OpenDB(); err != nil {
		t.Fatal(err)
	}
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	add := func(mac, name string, at int64) string {
		_ = a.checkins.Add(CheckIn{MAC: mac, Name: name, PersonID: alex, PersonName: "Alex"})
		var id string
		for _, x := range a.checkins.List() {
			if x.MAC == mac && x.Status == "pending" {
				id = x.ID
			}
		}
		_ = a.checkins.Decide(id, "approved", "admin")
		a.checkins.Edit(id, func(y *CheckIn) { y.Time, y.NDProfile, y.SetupLost, y.SetupSeen = at, "abc123", at, at })
		_ = a.An.AssignDevice(alex, "mac", mac)
		return id
	}
	now := time.Now().Unix()
	old := add("02:00:00:00:00:01", "Alex's iPhone", now-86400)
	cur := add("06:00:00:00:00:02", "alex's iphone ", now-3600)
	add("0a:00:00:00:00:03", "Alex's iPad", now-7200)
	// approval on, the current address not approved yet
	st := a.approval.load()
	st.Approved = map[string]int64{"0a:00:00:00:00:03": now}
	_ = a.approval.save(st)

	d := a.duplicateAttention()
	if len(d) != 1 || d[0]["count"] != 2 {
		t.Fatalf("one device checked in twice: %v", d)
	}
	if l := a.setupAttention(); len(l) != 2 { // the iPad and the newest iPhone; the older iPhone is the merge question
		t.Fatalf("setup attention without the older copy: %v", l)
	}
	if err := a.mergeCheckins(cur, []string{cur}, "admin"); err == nil {
		t.Fatal("nothing to merge")
	}
	if err := a.mergeCheckins(cur, []string{old}, "admin"); err != nil {
		t.Fatal(err)
	}
	if x, _ := a.checkins.Get(old); x.Status != "merged" || x.MergedInto != cur {
		t.Fatalf("old one merged: %+v", x)
	}
	if _, ok := a.checkins.Registered("02:00:00:00:00:01"); ok {
		t.Fatal("the old address isn't a checked-in device any more")
	}
	if _, ok := a.archive.load()["02:00:00:00:00:01"]; !ok {
		t.Fatal("the old address is archived")
	}
	if a.approval.load().Approved["06:00:00:00:00:02"] == 0 {
		t.Fatal("the kept address is approved")
	}
	for _, p := range a.An.People() {
		for _, dv := range p.Devices {
			if dv.Kind == "mac" && dv.Key == "02:00:00:00:00:01" {
				t.Fatal("the old address left Alex")
			}
		}
	}
	if len(a.duplicateAttention()) != 0 {
		t.Fatal("nothing left to merge")
	}
	// Who's home follows the old address's tag to the kept one
	if a.Cfg.Get().Presence.Devices["06:00:00:00:00:02"] != "" {
		t.Fatal("not tagged yet")
	}
}

func TestMainDevice(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.An.OpenDB(); err != nil {
		t.Fatal(err)
	}
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	_ = a.checkins.Add(CheckIn{MAC: "02:00:00:00:00:01", Name: "Alex's iPhone", PersonID: alex, Main: true})
	x := a.checkins.List()[0]
	if _, err := a.approveCheckIn(map[string]any{"id": x.ID}, "admin"); err != nil {
		t.Fatal(err)
	}
	if a.Cfg.Get().Presence.Devices["02:00:00:00:00:01"] != alex {
		t.Fatalf("main device tagged for Who's home: %v", a.Cfg.Get().Presence.Devices)
	}
	// the admin can say no
	_ = a.checkins.Add(CheckIn{MAC: "02:00:00:00:00:02", Name: "Alex's iPad", PersonID: alex, Main: true})
	for _, y := range a.checkins.List() {
		if y.Status == "pending" {
			_, _ = a.approveCheckIn(map[string]any{"id": y.ID, "main": false}, "admin")
		}
	}
	if a.Cfg.Get().Presence.Devices["02:00:00:00:00:02"] != "" {
		t.Fatal("not the main device")
	}
	// a new address (real Wi-Fi address, or a merge): the tag follows
	x, _ = a.checkins.Get(x.ID)
	a.fixedMove(x, "00:11:22:00:00:09")
	if d := a.Cfg.Get().Presence.Devices; d["00:11:22:00:00:09"] != alex || d["02:00:00:00:00:01"] != "" {
		t.Fatalf("tag moved: %v", d)
	}
}
