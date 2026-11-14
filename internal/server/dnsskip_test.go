package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestSkipWiFi(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	id := a.checkins.List()[0].ID
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPad", "nd_profile": "abc123"}, "admin"); err != nil {
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
	dns := func(p string) string { // the DNS payload only
		i := strings.Index(p, "<key>DNSSettings</key>")
		j := strings.Index(p[i:], "com.apple.dnsSettings.managed")
		return p[i : i+j]
	}
	// the starting list, before anything is saved: in-flight networks first, then on everywhere else
	d := dns(get())
	if !strings.Contains(d, "<string>Disconnect</string>\n<key>SSIDMatch</key><array><string>BAWi-Fi</string>") ||
		!strings.HasSuffix(strings.TrimSpace(d[:strings.LastIndex(d, "</array>")]), "<string>Connect</string>\n</dict>") {
		t.Fatalf("starting list rules:\n%s", d)
	}
	// the global list, edited
	if _, err := a.saveSkipWiFi(map[string]any{"ssids": []any{" Example Air ", "Example Air", ""}}); err != nil {
		t.Fatal(err)
	}
	if d := dns(get()); !strings.Contains(d, "<array><string>Example Air</string></array>") || strings.Contains(d, "BAWi-Fi") {
		t.Fatalf("edited list:\n%s", d)
	}
	if _, err := a.saveSkipWiFi(map[string]any{"ssids": []any{strings.Repeat("x", 33)}}); err == nil {
		t.Fatal("a name over 32 characters was accepted")
	}
	// none: no rules at all (as before this existed)
	if _, err := a.saveSkipWiFi(map[string]any{"ssids": []any{}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(), "OnDemandRules") {
		t.Fatal("an empty list means no rules")
	}
	// a type adds its own, and can keep NextDNS to the home Wi-Fi and mobile data
	if _, err := a.saveDevType(map[string]any{"name": "Adults", "people": []any{alex}, "skip_wifi": []any{"Example Hotel"}, "other_wifi": "off"}); err != nil {
		t.Fatal(err)
	}
	h := a.setupHash(a.checkins.List()[0])
	d = dns(get())
	order := []string{"<string>Disconnect</string>\n<key>SSIDMatch</key><array><string>Example Hotel</string>",
		"<string>Connect</string>\n<key>SSIDMatch</key><array><string>Example Home</string>",
		"<string>Disconnect</string>\n<key>InterfaceTypeMatch</key><string>WiFi</string>"}
	at := 0
	for _, o := range order {
		i := strings.Index(d[at:], o)
		if i < 0 {
			t.Fatalf("missing or out of order %q:\n%s", o, d)
		}
		at += i
	}
	if strings.Contains(d, "Example Guests") {
		t.Fatal("the guest network isn't home")
	}
	// the reset brings back the starting list, and the profile shows an update
	if _, err := a.saveSkipWiFi(map[string]any{"reset": true}); err != nil {
		t.Fatal(err)
	}
	if a.setupHash(a.checkins.List()[0]) == h || !strings.Contains(dns(get()), "BAWi-Fi") {
		t.Fatal("reset: the starting list again, as an update")
	}
}
