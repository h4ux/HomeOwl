package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestDeviceKey(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPhone","kind":"phone","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	x := a.checkins.List()[0]
	if _, err := a.approveCheckIn(map[string]any{"id": x.ID, "name": "Alex's iPhone", "nd_profile": "abc123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	key := a.deviceKey(x)
	if len(key) != 32 || key != a.deviceKey(x) || key == a.deviceKey(CheckIn{ID: "other"}) {
		t.Fatalf("key %q", key)
	}
	// the Home Screen icon's link carries it
	r := httptest.NewRequest("GET", "http://homeowl.lan:8555/api/hi/profile?p=ios", nil)
	r.RemoteAddr = "192.168.1.40:51234"
	w := httptest.NewRecorder()
	a.hiProfile(w, r)
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "<string>http://homeowl.lan:8555/hi?k="+key+"</string>") {
		t.Fatalf("icon link lacks the key:\n%s", body)
	}
	// a wrong key moves nothing
	c := map[string]any{"mac": "aa:bb:cc:00:00:41", "user_id": "u41", "hostname": "iPhone"}
	a.keyFollow(strings.Repeat("0", 32), c, "aa:bb:cc:00:00:41")
	a.keyFollow("short", c, "aa:bb:cc:00:00:41")
	if a.checkins.List()[0].MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("moved without the key")
	}
	// while the device's old address is online (the fake console lists .40), a key opened elsewhere moves nothing
	a.keyFollow(key, c, "aa:bb:cc:00:00:41")
	if a.checkins.List()[0].MAC != "aa:bb:cc:00:00:40" {
		t.Fatal("moved while the old address is online")
	}
	// the old address gone: the check-in, the person and the approval follow
	a.checkins.Edit(x.ID, func(y *CheckIn) { y.MAC = "aa:bb:cc:00:00:39" })
	a.keyFollow(key, c, "aa:bb:cc:00:00:41")
	y, ok := a.checkins.Registered("aa:bb:cc:00:00:41")
	if !ok || y.ID != x.ID || y.UserID != "u41" {
		t.Fatalf("not moved: %+v", a.checkins.List())
	}
	found := false
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			if p.ID == alex && d.Kind == "mac" && d.Key == "aa:bb:cc:00:00:41" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the new address should be Alex's")
	}
	// another approved device's address is never taken over
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("second check-in failed")
	}
	for _, z := range a.checkins.List() {
		if z.Status == "pending" {
			if _, err := a.approveCheckIn(map[string]any{"id": z.ID, "name": "Alex's iPad"}, "admin"); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.keyFollow(key, map[string]any{"mac": "aa:bb:cc:00:00:40"}, "aa:bb:cc:00:00:40")
	if z, _ := a.checkins.Registered("aa:bb:cc:00:00:40"); z.Name != "Alex's iPad" {
		t.Fatalf("took another device's address: %+v", z)
	}
	time.Sleep(50 * time.Millisecond) // the UniFi rename runs in the background
}
