package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

// The check-in page learns that the device runs Tailscale (its DNS may skip the profile) and the names NextDNS
// knows it by; its security policy lets it ask test.nextdns.io, and no other page can.
func TestTailscaleDNSHint(t *testing.T) {
	a := checkinApp(t)
	old := tailscalePeerAt
	defer func() { tailscalePeerAt = old }()
	peer := ""
	tailscalePeerAt = func(_ *App, ip string) (string, bool) { return "alex-mac", ip == peer }
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's Mac","kind":"laptop","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	x := a.checkins.List()[0]
	if _, err := a.approveCheckIn(map[string]any{"id": x.ID, "name": "Alex's Mac", "nd_profile": "abc123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	_, out := hi(a, "GET", "192.168.1.40", "", nil)
	if out["tailscale"] != nil {
		t.Fatalf("not a peer: %v", out["tailscale"])
	}
	nd, _ := out["registered"].(map[string]any)["nextdns"].(map[string]any)
	if nd["name"] != "Alex's Mac" || len(nd["names"].([]any)) == 0 {
		t.Fatalf("names: %v", nd)
	}
	peer = "192.168.1.40"
	if _, out = hi(a, "GET", "192.168.1.40", "", nil); out["tailscale"] != true {
		t.Fatal("a Tailscale peer isn't flagged")
	}
	h := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for path, want := range map[string]bool{"/hi": true, "/": false, "/api/hi": false} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if got := strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self' https://test.nextdns.io;"); got != want {
			t.Errorf("%s: test.nextdns.io allowed %v, want %v", path, got, want)
		}
	}
}
