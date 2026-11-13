package server

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

const testWG = `[Interface]
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 10.8.0.2/32
DNS = 192.168.1.1

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
AllowedIPs = 0.0.0.0/0
Endpoint = vpn.example.com:51820`

func TestProfileTypes(t *testing.T) {
	a := checkinApp(t)
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	id := a.checkins.List()[0].ID
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's iPad", "nd_profile": "abc123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	get := func(q string) string {
		r := httptest.NewRequest("GET", "http://homeowl.lan:8555/api/hi/profile"+q, nil)
		r.RemoteAddr = "192.168.1.40:51234"
		w := httptest.NewRecorder()
		a.hiProfile(w, r)
		b, _ := io.ReadAll(w.Body)
		if w.Code != 200 {
			t.Fatalf("profile: %d %s", w.Code, b)
		}
		dec := xml.NewDecoder(strings.NewReader(string(b)))
		for {
			if _, err := dec.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("profile isn't valid XML: %v\n%s", err, b)
				}
				break
			}
		}
		return string(b)
	}
	before := get("?p=ios")

	// bad input is refused
	for _, body := range []map[string]any{
		{"name": ""},
		{"name": "Kids", "vpn": map[string]any{"type": "pptp"}},
		{"name": "Kids", "vpn": map[string]any{"type": "ikev2", "server": "bad host!"}},
	} {
		if _, err := a.saveDevType(body); err == nil {
			t.Errorf("accepted %v", body)
		}
	}
	// a type with the home Wi-Fi (and the enterprise one, which can't go in) and IKEv2 that connects when away
	r, err := a.saveDevType(map[string]any{"name": "Family", "wifi": []any{"w1", "w3"}, "people": []any{alex}, "secret": "shared-secret-789",
		"vpn": map[string]any{"type": "ikev2", "name": "Home VPN", "server": "vpn.example.com", "on_demand": true}})
	if err != nil {
		t.Fatal(err)
	}
	tid := r["id"].(string)
	view, _ := a.devTypesView()
	vb, _ := json.Marshal(view)
	if strings.Contains(string(vb), "shared-secret-789") || strings.Contains(string(vb), "example-pass-123") || !strings.Contains(string(vb), `"secret_set":true`) {
		t.Fatalf("secrets must not go to the browser: %s", vb)
	}
	if a.Sec.Get(vpnSecretSlot(tid)) != "shared-secret-789" {
		t.Fatal("the secret should be in the secrets store")
	}
	// the device's page: what's included, and an update is ready
	_, out := hi(a, "GET", "192.168.1.40", "", nil)
	reg := out["registered"].(map[string]any)
	ob, _ := json.Marshal(reg)
	if reg["update"] != true || !strings.Contains(string(ob), `"wifi":["Example Home"]`) || !strings.Contains(string(ob), `"on_demand":true`) || strings.Contains(string(ob), "example-pass") {
		t.Fatalf("device page: %s", ob)
	}
	prof := get("?p=ios")
	for _, want := range []string{"com.apple.wifi.managed", "<string>Example Home</string>", "<string>example-pass-123</string>", "com.apple.vpn.managed",
		"<string>IKEv2</string>", "<string>shared-secret-789</string>", "<key>OnDemandRules</key>", "<string>Disconnect</string>", "com.apple.dnsSettings.managed", "com.apple.webClip.managed"} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile lacks %q", want)
		}
	}
	if strings.Contains(prof, ".wifi.w3") || strings.Contains(prof, "guest-pass-456") || !strings.Contains(prof, "<string>Example Work</string>") || strings.Contains(prof, "SSIDMatch</key><array><string>Example Guests") {
		t.Error("enterprise Wi-Fi can't go in, and the guest network isn't home")
	}
	if strings.Contains(prof, "contains no passwords") {
		t.Error("the description must not promise no passwords")
	}
	if prof == before {
		t.Fatal("the profile didn't change")
	}
	if _, out := hi(a, "GET", "192.168.1.40", "", nil); out["registered"].(map[string]any)["update"] != false {
		t.Fatal("just downloaded: nothing to update")
	}
	// a new Wi-Fi password in UniFi would change the fingerprint; so does another VPN server
	h := a.setupHash(a.checkins.List()[0])
	if _, err := a.saveDevType(map[string]any{"id": tid, "name": "Family", "wifi": []any{"w1"}, "people": []any{alex},
		"vpn": map[string]any{"type": "ikev2", "name": "Home VPN", "server": "vpn2.example.com", "on_demand": true}}); err != nil {
		t.Fatal(err)
	}
	if a.setupHash(a.checkins.List()[0]) == h || a.Sec.Get(vpnSecretSlot(tid)) != "shared-secret-789" {
		t.Fatal("the change should show as an update, and an empty secret keeps the saved one")
	}

	// WireGuard needs the device's own config
	if _, err := a.saveDevType(map[string]any{"id": tid, "name": "Family", "people": []any{alex}, "vpn": map[string]any{"type": "wireguard", "on_demand": true}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(""), "com.apple.vpn.managed") {
		t.Fatal("no WireGuard payload without the device's config")
	}
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "abc123", "wg": "not a config"}); err == nil {
		t.Fatal("a bad WireGuard config was accepted")
	}
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "abc123", "wg": testWG}); err != nil {
		t.Fatal(err)
	}
	mac := get("?p=mac")
	for _, want := range []string{"<string>com.wireguard.macos</string>", "<key>WgQuickConfig</key>", "<string>vpn.example.com:51820</string>"} {
		if !strings.Contains(mac, want) {
			t.Errorf("WireGuard profile lacks %q", want)
		}
	}
	if strings.Contains(mac, "webClip") {
		t.Error("no Home Screen icon on a Mac")
	}
	cv, _ := json.Marshal(a.checkinView())
	if strings.Contains(string(cv), "yAnz5TF") {
		t.Fatal("the WireGuard key must not go to the browser")
	}

	// a device can opt out of its person's type, and a type can be removed (with its secrets)
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "abc123", "type_id": typeNone}); err != nil {
		t.Fatal(err)
	}
	if p := get(""); strings.Contains(p, "wifi.managed") || strings.Contains(p, "vpn.managed") {
		t.Fatal("type \"none\" should leave Wi-Fi and VPN out")
	}
	if err := a.deleteDevType(tid); err != nil || a.Sec.Get(vpnSecretSlot(tid)) != "" || len(a.devTypes.List()) != 0 {
		t.Fatalf("delete: %v", err)
	}
	// a person is in one type at a time
	r1, _ := a.saveDevType(map[string]any{"name": "One", "people": []any{alex}})
	if _, err := a.saveDevType(map[string]any{"name": "Two", "people": []any{alex}}); err != nil {
		t.Fatal(err)
	}
	if one, _ := a.devTypes.Get(r1["id"].(string)); len(one.People) != 0 {
		t.Fatalf("Alex should have moved: %+v", one)
	}

	// the Home Screen icon: the check-in page (default), HomeOwl itself, or none
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "abc123", "type_id": ""}); err != nil {
		t.Fatal(err)
	}
	two := a.devTypes.List()[1]
	if _, err := a.saveDevType(map[string]any{"id": two.ID, "name": "Two", "people": []any{alex}, "icon": "desktop"}); err == nil {
		t.Fatal("unknown icon choice accepted")
	}
	if p := get("?p=ios"); !strings.Contains(p, "<string>http://homeowl.lan:8555/hi</string>") {
		t.Fatal("the default icon opens the check-in page")
	}
	h0 := a.setupHash(a.checkins.List()[0])
	if _, err := a.saveDevType(map[string]any{"id": two.ID, "name": "Two", "people": []any{alex}, "icon": "app"}); err != nil {
		t.Fatal(err)
	}
	if a.setupHash(a.checkins.List()[0]) == h0 {
		t.Fatal("another icon should show as an update")
	}
	app := get("?p=ios")
	if !strings.Contains(app, "<string>http://homeowl.lan:"+strconv.Itoa(a.Cfg.Get().LAN.Port)+"/</string>") || strings.Contains(app, "/hi</string>") ||
		!strings.Contains(app, "opens HomeOwl") || !strings.Contains(app, "<key>FullScreen</key><false/>") {
		t.Fatalf("app icon:\n%s", app)
	}
	if _, err := a.saveDevType(map[string]any{"id": two.ID, "name": "Two", "people": []any{alex}, "icon": "none"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get("?p=ios"), "webClip") {
		t.Fatal("no icon wanted")
	}
	if _, out := hi(a, "GET", "192.168.1.40", "", nil); out["registered"].(map[string]any)["includes"].(map[string]any)["icon"] != "none" {
		t.Fatal("the device's page should know there's no icon")
	}
}
