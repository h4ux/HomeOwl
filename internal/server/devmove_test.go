package server

import (
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestDeviceMove(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex", Devices: []analytics.PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:40"}, {Kind: "ndev", Key: "abc123/Alex's Mac"}}})
	srv, _ := a.An.SavePerson(analytics.Person{Name: "Servers", Kind: "group"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's Mac","kind":"laptop","person_id":"`+alex+`","main":true}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	if _, err := a.deviceMove(map[string]any{"person": srv, "keys": []any{map[string]any{"kind": "mac", "key": "aa:bb:cc:00:00:40"}, map[string]any{"kind": "ndev", "key": "abc123/Alex's Mac"}}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range a.An.People() {
		if p.ID == alex && len(p.Devices) != 0 || p.ID == srv && len(p.Devices) != 2 {
			t.Fatalf("not moved: %+v", p)
		}
	}
	if x := a.checkins.List()[0]; x.PersonID != srv || x.PersonName != "Servers" || x.Main {
		t.Fatalf("check-in didn't follow: %+v", x)
	}
	if _, err := a.deviceMove(map[string]any{"person": "nope", "keys": []any{map[string]any{"kind": "mac", "key": "aa:bb:cc:00:00:40"}}}); err == nil {
		t.Fatal("moved to nobody")
	}
}
