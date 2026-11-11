package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/config"
)

// http://hi.local and http://wifi.local only serve their own page, over plain HTTP; the guest password API answers
// on the guest name only.
func TestEasyNames(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	a.cert = tls.Certificate{Certificate: [][]byte{{1}}}
	if err := a.Cfg.Update(func(c *config.Config) { c.Names = config.Names{Checkin: "hi", Guest: "wifi"} }); err != nil {
		t.Fatal(err)
	}
	get := func(url string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", url, nil)
		r.RemoteAddr = "192.168.1.40:50000"
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		return w
	}
	if w := get("http://hi.local/"); w.Code != 200 || !strings.Contains(w.Body.String(), "Hi HomeOwl") {
		t.Fatalf("hi.local/: %d", w.Code)
	}
	if w := get("http://HI.local:8555/app.js"); w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("hi.local must not serve the app: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := get("http://wifi.local/"); w.Code != 200 || !strings.Contains(w.Body.String(), "Guest Wi-Fi") {
		t.Fatalf("wifi.local/: %d", w.Code)
	}
	if w := get("http://wifi.local/api/status"); w.Code != http.StatusFound {
		t.Fatalf("wifi.local must not reach the API: %d", w.Code)
	}
	if w := get("http://wifi.local/api/wifi"); w.Code != 503 { // not connected to UniFi in the test
		t.Fatalf("wifi.local/api/wifi: %d", w.Code)
	}
	if w := get("http://192.168.1.10:8555/api/wifi"); w.Code != 503 { // by IP from the home network: the fallback link
		t.Fatalf("the guest card by IP: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "http://192.168.1.10:8555/api/wifi", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	if w := httptest.NewRecorder(); func() int { a.handler.ServeHTTP(w, r); return w.Code }() != 404 {
		t.Fatalf("the guest password must not be served outside the home network")
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Names.Guest = "" })
	if w := get("http://192.168.1.10:8555/api/wifi"); w.Code != 404 {
		t.Fatalf("the guest card is off without a name: %d", w.Code)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Names.Guest = "wifi" })
	if w := get("http://hi.local/api/wifi"); w.Code != http.StatusFound {
		t.Fatalf("hi.local/api/wifi: %d", w.Code)
	}
	if _, err := parseNames(map[string]any{"checkin": "x", "guest": "X.local"}, config.Names{}); err == nil {
		t.Fatal("the same name twice must fail")
	}
	if _, err := parseNames(map[string]any{"checkin": "two words"}, config.Names{}); err == nil {
		t.Fatal("a bad name must fail")
	}
	if n, err := parseNames(map[string]any{"checkin": "Hi.local", "guest": "", "port80": true}, config.Names{Guest: "wifi"}); err != nil || n.Checkin != "hi" || n.Guest != "" || n.Short != "" {
		t.Fatalf("parse: %+v %v", n, err)
	}
}

// On port 80, http://<IP>/hi and /wifi are served directly; anything else goes to HomeOwl's real port.
func TestShortLinks(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Names.Guest = "wifi" })
	h := a.port80Handler()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://192.168.1.10"+path, nil)
		r.RemoteAddr = "192.168.1.40:50000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("/hi"); w.Code != 200 || !strings.Contains(w.Body.String(), "Hi HomeOwl") {
		t.Fatalf("/hi: %d", w.Code)
	}
	if w := get("/wifi"); w.Code != 200 || !strings.Contains(w.Body.String(), "Guest Wi-Fi") {
		t.Fatalf("/wifi: %d", w.Code)
	}
	if w := get("/app.js"); w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), ":8555/") {
		t.Fatalf("/app.js must go to the real port: %d %s", w.Code, w.Header().Get("Location"))
	}
}
