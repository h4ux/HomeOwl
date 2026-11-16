package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDetectModel(t *testing.T) {
	a := checkinApp(t)
	asked := make(chan string, 4)
	old := deviceModel
	defer func() { deviceModel = old; modelAsked.Lock(); modelAsked.at = nil; modelAsked.Unlock() }()
	deviceModel = func(_ context.Context, ip string) string { asked <- ip; return "iPhone18,1" }
	modelAsked.Lock()
	modelAsked.at = nil
	modelAsked.Unlock()
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPhone","kind":"phone","person_name":"Alex"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	select {
	case ip := <-asked:
		if ip != "192.168.1.40" {
			t.Fatalf("asked %s", ip)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the device wasn't asked for its model")
	}
	var x CheckIn
	for i := 0; i < 50; i++ {
		if x = a.checkins.List()[0]; x.DetModel != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if x.DetModel != "iPhone18,1" {
		t.Fatalf("found model: %+v", x)
	}
	// approved with the dialog's "Automatic": the profile uses the found model
	if _, err := a.approveCheckIn(map[string]any{"id": x.ID, "name": "Alex's iPhone", "nd_profile": "abc123", "nd_model": ""}, "admin"); err != nil {
		t.Fatal(err)
	}
	_, out := hi(a, "GET", "192.168.1.40", "", nil)
	if nd, _ := out["registered"].(map[string]any)["nextdns"].(map[string]any); nd == nil {
		t.Fatalf("setup: %v", out)
	}
	if got := appleDoH(a.checkins.List()[0]); !strings.HasSuffix(got, "/Apple%20iPhone18%2C1") {
		t.Fatalf("address %s", got)
	}
	// asked at most once every 10 minutes
	hi(a, "GET", "192.168.1.40", "", nil)
	select {
	case <-asked:
		t.Fatal("asked again too soon")
	case <-time.After(100 * time.Millisecond):
	}
	// what the device says must be a model identifier
	deviceModel = func(context.Context, string) string { return "evil/../x" }
	modelAsked.Lock()
	modelAsked.at = nil
	modelAsked.Unlock()
	a.detectModel("aa:bb:cc:00:00:40", "192.168.1.40", true)
	if m := a.checkins.List()[0].DetModel; m != "iPhone18,1" {
		t.Fatalf("junk replaced it: %q", m)
	}
}
