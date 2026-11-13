package server

import (
	"encoding/xml"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

func TestDeviceSetupProfile(t *testing.T) {
	a := checkinApp(t)
	alex, err := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	get := func(ip string, q ...string) (int, string, string) {
		r := httptest.NewRequest("GET", "http://homeowl.lan:8555/api/hi/profile"+strings.Join(q, ""), nil)
		r.RemoteAddr = ip + ":51234"
		w := httptest.NewRecorder()
		a.hiProfile(w, r)
		b, _ := io.ReadAll(w.Body)
		return w.Code, w.Header().Get("Content-Type"), string(b)
	}
	if code, _, _ := get("192.168.1.40"); code != 404 {
		t.Fatalf("before check-in: %d", code)
	}
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	if code, _, _ := get("192.168.1.40"); code != 404 {
		t.Fatalf("still pending: %d", code)
	}
	id := a.checkins.List()[0].ID
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPad", "nd_profile": "../evil"}, "admin"); err == nil {
		t.Fatal("a bad NextDNS profile ID should be refused")
	}
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPad", "nd_profile": "abc123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if a.cliTypes.All()["aa:bb:cc:00:00:40"] != "tablet" {
		t.Errorf("the check-in's kind should set the icon: %v", a.cliTypes.All())
	}
	// the device's page now offers the setup
	_, out := hi(a, "GET", "192.168.1.40", "", nil)
	reg, _ := out["registered"].(map[string]any)
	nd, _ := reg["nextdns"].(map[string]any)
	if reg["person"] != "Alex" || nd["android"] != "Alexs--iPad-abc123.dns.nextdns.io" || nd["doh"] != "https://dns.nextdns.io/abc123/Alex%27s%20iPad" {
		t.Fatalf("setup: %v", out["registered"])
	}
	// the NextDNS name belongs to Alex too, so logs from anywhere are hers
	found := false
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			if p.ID == alex && d.Kind == "ndev" && d.Key == "abc123/Alex's iPad" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("ndev link missing: %+v", a.An.People())
	}
	// only that device gets its profile; a guest or an unknown address doesn't
	for ip, want := range map[string]int{"192.168.1.60": 403, "192.168.1.99": 404, "203.0.113.5": 403} {
		if code, _, _ := get(ip); code != want {
			t.Errorf("profile from %s: %d, want %d", ip, code, want)
		}
	}
	code, ct, body := get("192.168.1.40")
	if code != 200 || ct != "application/x-apple-aspen-config" {
		t.Fatalf("profile: %d %s", code, ct)
	}
	dec := xml.NewDecoder(strings.NewReader(body)) // the plist must be well-formed XML
	for {
		if _, err := dec.Token(); err != nil {
			if err != io.EOF {
				t.Fatalf("profile isn't valid XML: %v", err)
			}
			break
		}
	}
	for _, want := range []string{"<string>https://dns.nextdns.io/abc123/Alex%27s%20iPad</string>", "com.apple.dnsSettings.managed",
		"io.github.h4ux.homeowl.device.aabbcc000040", "<key>PayloadRemovalDisallowed</key><false/>"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "webClip") {
		t.Error("no Home Screen icon unless it's an iPhone or iPad (macOS doesn't take one)")
	}
	_, _, ios := get("192.168.1.40", "?p=ios")
	for _, want := range []string{"com.apple.webClip.managed", "<string>http://homeowl.lan:8555/hi</string>", "<key>Icon</key><data>iVBOR", "<key>IsRemovable</key><true/>"} {
		if !strings.Contains(ios, want) {
			t.Errorf("iOS profile lacks %q", want)
		}
	}
	if regexp.MustCompile(`(?i)<key>[a-z]*(password|token|secret)`).MatchString(body) {
		t.Error("the profile must not carry secrets")
	}
	if a.checkins.List()[0].ProfileAt == 0 {
		t.Error("the download should be recorded")
	}
	// an admin moves the device to another NextDNS profile: its page says an update is ready, People follow
	view, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "def456"})
	if err != nil {
		t.Fatal(err)
	}
	if st := view.(map[string]any)["setup"].(map[string]any)[id].(map[string]any); st["update"] != true || st["state"] != "unknown" {
		t.Fatalf("status after the change: %v", st)
	}
	if _, out := hi(a, "GET", "192.168.1.40", "", nil); out["registered"].(map[string]any)["update"] != true {
		t.Fatalf("the device's page should offer the update: %v", out["registered"])
	}
	links := map[string]bool{}
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			links[d.Kind+" "+d.Key] = true
		}
	}
	if !links["ndev abc123/Alex's iPad"] || !links["ndev def456/Alex's iPad"] {
		t.Fatalf("both NextDNS names until the device installs the new one: %v", links)
	}
	if code, _, body := get("192.168.1.40"); code != 200 || !strings.Contains(body, "dns.nextdns.io/def456/") {
		t.Fatalf("new profile download: %d", code)
	}
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			if d.Key == "abc123/Alex's iPad" {
				t.Fatal("the old NextDNS name should go with the new download")
			}
		}
	}
	if _, out := hi(a, "GET", "192.168.1.40", "", nil); out["registered"].(map[string]any)["update"] != false {
		t.Fatal("after installing the new one there's nothing to update")
	}
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "../x"}); err == nil {
		t.Fatal("bad profile accepted")
	}
	if !regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-5[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`).MatchString(stableUUID("x")) || stableUUID("x") != stableUUID("x") {
		t.Errorf("uuid %s", stableUUID("x"))
	}
}

func TestPersonNDProfile(t *testing.T) {
	p := analytics.Person{Devices: []analytics.PersonDevice{{Kind: "mac", Key: "aa"}, {Kind: "ndev", Key: "bbb222/Phone"}, {Kind: "ndev", Key: "aaa111/TV"}, {Kind: "ndev", Key: "bbb222/Mac"}}}
	if got := personNDProfile(p); got != "bbb222" {
		t.Errorf("most devices: %q", got)
	}
	p.Devices = append(p.Devices, analytics.PersonDevice{Kind: "profile", Key: "ccc333"})
	if got := personNDProfile(p); got != "ccc333" {
		t.Errorf("whole profile wins: %q", got)
	}
	if ndDeviceLabel("  ") != "" || ndDeviceLabel("Kid's Phone (old)") != "Kids--Phone--old" {
		t.Errorf("label %q", ndDeviceLabel("Kid's Phone (old)"))
	}
}
