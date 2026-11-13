package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
)

func checkinApp(t *testing.T) *App {
	t.Helper()
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/wlanconf"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[
				{"_id":"w1","name":"Example Home","security":"wpapsk","x_passphrase":"example-pass-123","enabled":true},
				{"_id":"w2","name":"Example Guests","security":"wpapsk","x_passphrase":"guest-pass-456","is_guest":true,"enabled":true},
				{"_id":"w3","name":"Example Work","security":"wpaeap","enabled":true}]}`))
		case strings.HasSuffix(r.URL.Path, "/stat/sta"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[
				{"ip":"192.168.1.40","mac":"AA:BB:CC:00:00:40","hostname":"ipad","oui":"Apple","user_id":"u40","network":"Default"},
				{"ip":"192.168.1.60","mac":"aa:bb:cc:00:00:60","is_guest":true}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(console.Close)
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
	return a
}

func hi(a *App, method, ip, body string, hdr map[string]string) (int, map[string]any) {
	r := httptest.NewRequest(method, "http://homeowl.lan:8555/api/hi", strings.NewReader(body))
	r.RemoteAddr = ip + ":51234"
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	a.hiAPI(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestDeviceCheckIn(t *testing.T) {
	a := checkinApp(t)
	alex, err := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]int{"127.0.0.1": 400, "203.0.113.5": 403, "192.168.1.60": 403, "192.168.1.99": 404} {
		if code, out := hi(a, "GET", ip, "", nil); code != want {
			t.Errorf("GET from %s: %d %v, want %d", ip, code, out, want)
		}
	}
	code, out := hi(a, "GET", "192.168.1.40", "", nil)
	if code != 200 || out["device"].(map[string]any)["mac"] != "aa:bb:cc:00:00:40" || len(out["people"].([]any)) != 1 {
		t.Fatalf("GET: %d %v", code, out)
	}
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"x"}`, map[string]string{"Origin": "http://evil.example"}); code != 403 {
		t.Fatalf("cross-site POST: %d", code)
	}
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"","person_id":"`+alex+`"}`, nil); code != 400 {
		t.Fatalf("empty name: %d", code)
	}
	if code, out := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPad","kind":"tablet","person_id":"`+alex+`"}`, nil); code != 200 {
		t.Fatalf("POST: %d %v", code, out)
	}
	// a second answer from the same device replaces the first; someone new is allowed
	if code, out := hi(a, "POST", "192.168.1.40", `{"name":"Sam's iPad\u0007","kind":"rocket","person_name":"Sam"}`, nil); code != 200 {
		t.Fatalf("POST again: %d %v", code, out)
	}
	items := a.checkins.List()
	if len(items) != 1 || items[0].Name != "Sam's iPad" || items[0].Kind != "other" || items[0].UserID != "u40" || items[0].PersonName != "Sam" {
		t.Fatalf("queue: %+v", items)
	}
	// typing the name of someone already in People picks that person
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Tablet","person_name":"alex"}`, nil); code != 200 || a.checkins.List()[0].PersonID != alex {
		t.Fatalf("typed name should match Alex: %+v", a.checkins.List())
	}
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Sam's iPad","kind":"other","person_name":"Sam"}`, nil); code != 200 {
		t.Fatal("POST failed")
	}
	items = a.checkins.List()
	if _, err := a.approveCheckIn(map[string]any{"id": items[0].ID}, "alex"); err != nil {
		t.Fatal(err)
	}
	var sam string
	for _, p := range a.An.People() {
		if p.Name == "Sam" && len(p.Devices) == 1 && p.Devices[0].Key == "aa:bb:cc:00:00:40" {
			sam = p.ID
		}
	}
	if sam == "" {
		t.Fatalf("approving should create Sam with the iPad: %+v", a.An.People())
	}
	if _, err := a.approveCheckIn(map[string]any{"id": items[0].ID}, "alex"); err == nil {
		t.Fatal("a check-in can be approved only once")
	}
	if a.checkins.Pending() != 0 {
		t.Fatal("nothing should be pending")
	}
	// the admin can approve the name without giving the device to anyone
	_ = a.checkins.Add(CheckIn{MAC: "aa:bb:cc:00:00:41", Name: "TV", PersonID: alex})
	for _, x := range a.checkins.List() {
		if x.Status == "pending" {
			if _, err := a.approveCheckIn(map[string]any{"id": x.ID, "person_id": "-"}, "alex"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range a.An.People() {
		for _, d := range p.Devices {
			if d.Key == "aa:bb:cc:00:00:41" {
				t.Fatalf("“Nobody” still gave the TV to %s", p.Name)
			}
		}
	}
	// at most 10 check-ins an hour from one address (4 sent above)
	last := 0
	for i := 0; i < 7; i++ {
		last, _ = hi(a, "POST", "192.168.1.40", `{"name":"Tablet","person_id":"`+alex+`"}`, nil)
	}
	if last != 429 {
		t.Fatalf("rate limit: last status %d, want 429", last)
	}
}

// From another device, plain HTTP is sent to HTTPS — except the check-in page and what it needs.
func TestCheckInPageOverPlainHTTP(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	a.cert = tls.Certificate{Certificate: [][]byte{{1}}}
	for path, wantRedirect := range map[string]bool{"/hi": false, "/hi.js": false, "/style.css": false, "/": true, "/app.js": true, "/api/status": true} {
		r := httptest.NewRequest("GET", "http://192.168.1.10:8555"+path, nil)
		r.RemoteAddr = "192.168.1.40:50000"
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		if got := w.Code == http.StatusFound; got != wantRedirect {
			t.Errorf("%s: status %d, redirect %v, want %v", path, w.Code, got, wantRedirect)
		}
		if path == "/hi" && !strings.Contains(w.Body.String(), "Hi HomeOwl") {
			t.Errorf("/hi should serve the check-in page")
		}
	}
}
