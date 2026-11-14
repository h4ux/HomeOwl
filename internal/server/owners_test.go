package server

import (
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestOwnersAPI(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if _, err := a.ownersAPI("link", map[string]any{"mac": "not-a-mac", "person_id": alex}); err == nil {
		t.Fatal("bad MAC accepted")
	}
	if _, err := a.ownersAPI("link", map[string]any{"mac": "AA:BB:CC:00:00:70", "person_id": alex}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			found = found || p.ID == alex && d.Kind == "mac" && d.Key == "aa:bb:cc:00:00:70"
		}
	}
	if !found {
		t.Fatalf("not linked: %+v", a.An.People())
	}
	if _, err := a.ownersAPI("dismiss", map[string]any{"mac": "aa:bb:cc:00:00:71", "all": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ownersAPI("dismiss", map[string]any{"mac": "aa:bb:cc:00:00:72", "person_id": alex}); err != nil {
		t.Fatal(err)
	}
	skip := a.ownerDismissed().skip()
	if !skip("aa:bb:cc:00:00:71", "anyone") || !skip("aa:bb:cc:00:00:72", alex) || skip("aa:bb:cc:00:00:72", "someone-else") {
		t.Fatal("dismissals")
	}
	if v, err := a.ownersView(); err != nil || v["suggestions"] == nil {
		t.Fatalf("%v %v", v, err)
	}
}
