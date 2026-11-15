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
}
