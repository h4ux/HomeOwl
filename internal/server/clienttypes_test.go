package server

import "testing"

func TestClientTypes(t *testing.T) {
	ct := newClientTypes(t.TempDir())
	if err := ct.Set("AA:BB:CC:00:00:01", "tv", false); err != nil {
		t.Fatal(err)
	}
	if ct.All()["aa:bb:cc:00:00:01"] != "tv" {
		t.Fatalf("saved: %v", ct.All())
	}
	if err := ct.Set("aa:bb:cc:00:00:01", "phone", true); err != nil || ct.All()["aa:bb:cc:00:00:01"] != "tv" {
		t.Fatal("keep=true must not replace an admin's choice")
	}
	for _, bad := range [][2]string{{"not-a-mac", "tv"}, {"aa:bb:cc:00:00:02", "rocket"}, {"aa:bb:cc:00:00:02", "<b>"}} {
		if ct.Set(bad[0], bad[1], false) == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if err := ct.Set("aa:bb:cc:00:00:01", "", false); err != nil || len(ct.All()) != 0 {
		t.Fatalf("clearing: %v %v", err, ct.All())
	}
}
