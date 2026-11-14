package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/config"
)

func TestOnboard(t *testing.T) {
	resetWlans()
	t.Cleanup(resetWlans)
	sta := `[{"ip":"192.168.2.20","mac":"aa:bb:cc:00:00:20","hostname":"alexs-phone","oui":"Apple","essid":"Home Setup","is_guest":true},
		{"ip":"192.168.2.21","mac":"aa:bb:cc:00:00:21","hostname":"guest-phone","essid":"Example Guests","is_guest":true}]`
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/wlanconf"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[
				{"_id":"w1","name":"Example Home","security":"wpapsk","x_passphrase":"example-pass-123","enabled":true},
				{"_id":"w2","name":"Example Guests","security":"wpapsk","x_passphrase":"guest-pass-456","is_guest":true,"enabled":true},
				{"_id":"w3","name":"Example IoT","security":"wpapsk","x_passphrase":"iot-pass-789","enabled":true},
				{"_id":"w4","name":"Home Setup","security":"open","is_guest":true,"enabled":true},
				{"_id":"w5","name":"Example Work","security":"wpaeap","enabled":true}]}`))
		case strings.HasSuffix(r.URL.Path, "/stat/sta"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":` + sta + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer console.Close()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.An.OpenDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if _, err := a.saveDevType(map[string]any{"name": "Family", "wifi": []any{"w1"}, "people": []any{alex}}); err != nil {
		t.Fatal(err)
	}

	// without a setup Wi-Fi, guest networks can't check in
	if code, _ := hi(a, "GET", "192.168.2.20", "", nil); code != 403 {
		t.Fatalf("guest check-in: %d", code)
	}
	if _, err := a.saveOnboard(map[string]any{"ssid": "Example Home"}); err == nil {
		t.Fatal("a network the profiles join can't be the setup Wi-Fi")
	}
	if _, err := a.saveOnboard(map[string]any{"ssid": "Home Setup"}); err != nil {
		t.Fatal(err)
	}
	code, out := hi(a, "GET", "192.168.2.20", "", nil)
	if code != 200 || out["setup_wifi"] != "Home Setup" {
		t.Fatalf("setup Wi-Fi check-in: %d %v", code, out)
	}
	if code, _ := hi(a, "GET", "192.168.2.21", "", nil); code != 403 {
		t.Fatal("other guest networks still can't")
	}
	if code, _ := hi(a, "POST", "192.168.2.20", `{"name":"Alex's Phone","kind":"phone","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	x := a.checkins.List()[0]
	if !x.Onboard {
		t.Fatal("the check-in should remember it came from the setup Wi-Fi")
	}
	found := false
	for _, al := range a.Alerts.List() {
		if al.Kind == "checkin" && strings.Contains(al.Title, "Alex's Phone") {
			found = true
		}
	}
	if !found {
		t.Fatal("admins get an alert to approve it")
	}
	// not approved yet: no Wi-Fi details
	get := func(path string) (int, string) {
		r := httptest.NewRequest("GET", "http://192.168.1.10:8555"+path, nil)
		r.RemoteAddr = "192.168.2.20:51234"
		w := httptest.NewRecorder()
		if strings.HasPrefix(path, "/api/hi/wifi") {
			a.hiWiFi(w, r)
		} else {
			a.hiProfile(w, r)
		}
		b, _ := io.ReadAll(w.Body)
		return w.Code, string(b)
	}
	if code, body := get("/api/hi/wifi"); code != 404 || strings.Contains(body, "example-pass") {
		t.Fatalf("before approval: %d %s", code, body)
	}
	if _, err := a.approveCheckIn(map[string]any{"id": x.ID, "name": "Alex's Phone"}, "admin"); err != nil {
		t.Fatal(err)
	}

	// the profile: the home Wi-Fi joins by itself, the setup Wi-Fi never does
	code, prof := get("/api/hi/profile?p=ios")
	if code != 200 {
		t.Fatalf("profile: %d %s", code, prof)
	}
	setup := prof[strings.Index(prof, "<string>Home Setup</string>")-600:]
	if !strings.Contains(prof, "<string>example-pass-123</string>") || !strings.Contains(setup, "<key>AutoJoin</key><false/>") {
		t.Fatalf("profile Wi-Fi entries:\n%s", prof)
	}
	if strings.Contains(prof, "Example IoT") || strings.Contains(prof, "iot-pass") {
		t.Fatal("other networks are left out unless the type says so")
	}
	x, _ = a.checkins.Get(x.ID)
	if x.FixedFrom != "aa:bb:cc:00:00:20" {
		t.Fatalf("the download should wait for it on the home Wi-Fi: %+v", x)
	}
	if st := a.setupStatus(x); st["mac"] != "joining" {
		t.Fatalf("status: %v", st)
	}
	// it shows up on the home Wi-Fi (a private address there is fine: it came from the setup Wi-Fi)
	a.followFixed([]map[string]any{{"mac": "06:bb:cc:00:00:20", "hostname": "alexs-phone", "essid": "Example Home"}})
	x, _ = a.checkins.Get(x.ID)
	if x.MAC != "06:bb:cc:00:00:20" || x.FixedFrom != "" || x.Fixed {
		t.Fatalf("should follow it to the home Wi-Fi: %+v", x)
	}

	// "don't join our other networks": open ones go in as no-join; password networks stay out (no password in the
	// profile to pick them by hand with, and no prompt); enterprise left out
	tid := a.devTypes.List()[0].ID
	if _, err := a.saveDevType(map[string]any{"id": tid, "name": "Family", "wifi": []any{"w1"}, "people": []any{alex}, "no_other_wifi": true}); err != nil {
		t.Fatal(err)
	}
	ex, _ := a.extrasFor(x)
	var names []string
	for _, w := range ex.WiFi {
		if w.NoJoin {
			if (w.Enc == "None") != (w.Password == "") {
				t.Fatalf("%s: a password network carries its password, an open one none (%s)", w.SSID, w.Enc)
			}
			names = append(names, w.SSID)
		}
	}
	if strings.Join(names, ",") != "Home Setup" { // Example Guests and Example IoT have passwords: left out
		t.Fatalf("no-join networks: %v", names)
	}
	inc, _ := json.Marshal(a.includes(x))
	if !strings.Contains(string(inc), `"no_join":["Home Setup"]`) {
		t.Fatalf("includes: %s", inc)
	}

	// Android on the setup Wi-Fi: the home Wi-Fi's details, and what to forget
	a.checkins.Edit(x.ID, func(y *CheckIn) { y.MAC = "aa:bb:cc:00:00:20" })
	code, body := get("/api/hi/wifi")
	if code != 200 || !strings.Contains(body, `"password":"example-pass-123"`) || !strings.Contains(body, `"leave":"Home Setup"`) || strings.Contains(body, "iot-pass") {
		t.Fatalf("home Wi-Fi for Android: %d %s", code, body)
	}
	// a setup Wi-Fi with a password: the no-join entry matches its security and carries the password (no prompt)
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.SSID = "Example IoT" })
	ex, _ = a.extrasFor(x)
	for _, w := range ex.WiFi {
		if w.ID == "setup" && (w.SSID != "Example IoT" || w.Enc != "Any" || w.Password != "iot-pass-789" || !w.NoJoin) {
			t.Fatalf("setup Wi-Fi with a password: %+v", w)
		}
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.SSID = "" })
	if code, _ := hi(a, "GET", "192.168.2.20", "", nil); code != 403 {
		t.Fatal("setup Wi-Fi off: guest networks can't check in again")
	}
}

func TestPortalRedirect(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []http.Handler{a.port80Handler(), a.guard(http.NotFoundHandler())} {
		r := httptest.NewRequest("GET", "http://192.168.1.10/guest/s/default/?ap=00:11:22:00:00:01&id=aa:bb:cc:00:00:20&url=http://captive.apple.com/", nil)
		r.RemoteAddr = "192.168.2.20:51234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 302 || w.Header().Get("Location") != "/hi" {
			t.Fatalf("the UniFi portal should land on the check-in page: %d %v", w.Code, w.Header())
		}
	}
}
