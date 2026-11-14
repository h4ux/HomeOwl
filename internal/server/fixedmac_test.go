package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestFixedMAC(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	id := a.checkins.List()[0].ID
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPad"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveDevType(map[string]any{"name": "Family", "wifi": []any{"w1"}, "people": []any{alex}}); err != nil {
		t.Fatal(err)
	}
	get := func() string {
		r := httptest.NewRequest("GET", "http://homeowl.lan:8555/api/hi/profile?p=ios", nil)
		r.RemoteAddr = "192.168.1.40:51234"
		w := httptest.NewRecorder()
		a.hiProfile(w, r)
		b, _ := io.ReadAll(w.Body)
		if w.Code != 200 {
			t.Fatalf("profile: %d %s", w.Code, b)
		}
		return string(b)
	}
	if !strings.Contains(get(), "<key>DisableAssociationMACRandomization</key><true/>") {
		t.Fatal("by default the home Wi-Fi goes in with the real address")
	}
	x, _ := a.checkins.Get(id)
	if x.FixedFrom != "aa:bb:cc:00:00:40" || x.FixedHost != "ipad" || x.Fixed || !a.fixedPending() {
		t.Fatalf("download with a private address should wait for the real one: %+v", x)
	}
	if st := a.setupStatus(x); st["mac"] != "waiting" {
		t.Fatalf("status: %v", st)
	}

	real := map[string]any{"mac": "00:11:22:00:00:40", "hostname": "IPad", "ip": "192.168.1.41"}
	// still on its old address: not installed yet
	a.followFixed([]map[string]any{{"mac": "aa:bb:cc:00:00:40", "hostname": "ipad"}, real})
	// two devices with the same name: don't guess
	a.followFixed([]map[string]any{real, {"mac": "00:11:22:00:00:99", "hostname": "ipad"}})
	// a private address or a wired one isn't it
	a.followFixed([]map[string]any{{"mac": "02:11:22:00:00:40", "hostname": "ipad"}, {"mac": "00:11:22:00:00:98", "hostname": "ipad", "is_wired": true}})
	if x, _ := a.checkins.Get(id); x.MAC != "aa:bb:cc:00:00:40" || x.Fixed {
		t.Fatalf("moved too early: %+v", x)
	}
	a.followFixed([]map[string]any{real})
	x, _ = a.checkins.Get(id)
	if x.MAC != "00:11:22:00:00:40" || !x.Fixed || x.FixedFrom != "" || a.fixedPending() {
		t.Fatalf("should follow the device to its real address: %+v", x)
	}
	if st := a.setupStatus(x); st["mac"] != "fixed" {
		t.Fatalf("status: %v", st)
	}
	if y, ok := a.checkins.Registered("00:11:22:00:00:40"); !ok || y.ID != id {
		t.Fatal("the real address should be the registered device")
	}
	owned := false
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			if p.ID == alex && d.Kind == "mac" && d.Key == "00:11:22:00:00:40" {
				owned = true
			}
		}
	}
	if !owned {
		t.Fatal("the real address should be Alex's")
	}

	// a type can keep private addresses: no key in the profile
	tid := a.devTypes.List()[0].ID
	if _, err := a.saveDevType(map[string]any{"id": tid, "name": "Family", "wifi": []any{"w1"}, "people": []any{alex}, "private_mac": true}); err != nil {
		t.Fatal(err)
	}
	a.checkins.Edit(id, func(y *CheckIn) { y.MAC = "aa:bb:cc:00:00:40" }) // the fake console still shows the old address
	if strings.Contains(get(), "DisableAssociationMACRandomization") {
		t.Fatal("private addresses kept: no real-address key")
	}
}
