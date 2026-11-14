package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/h4ux/homeowl/internal/config"
)

func TestPortalMode(t *testing.T) {
	for _, c := range []struct {
		g    map[string]any
		want string
	}{
		{map[string]any{"portal_enabled": false, "auth": "hotspot", "voucher_enabled": true}, "none"}, // off: nobody signs in yet
		{map[string]any{"portal_enabled": true, "auth": "none"}, "none"},
		{map[string]any{"portal_enabled": true, "auth": "password"}, "password"},
		{map[string]any{"portal_enabled": true, "auth": "hotspot", "password_enabled": true}, "password"},
		{map[string]any{"portal_enabled": true, "auth": "hotspot", "password_enabled": true, "voucher_enabled": true}, ""},
		{map[string]any{"portal_enabled": true, "auth": "custom", "custom_ip": "192.168.1.99"}, ""},
		{map[string]any{"portal_enabled": true, "auth": "custom", "custom_ip": "192.168.1.10"}, "none"},
		{map[string]any{"portal_enabled": true, "auth": "radius"}, ""},
	} {
		if got, _ := portalMode(c.g, "192.168.1.10"); got != c.want {
			t.Errorf("%v: %q, want %q", c.g, got, c.want)
		}
	}
}

func TestGuestPortal(t *testing.T) {
	resetWlans()
	t.Cleanup(resetWlans)
	old := portalIPs
	portalIPs = func() []net.IP { return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.10")} }
	t.Cleanup(func() { portalIPs = old })
	var mu sync.Mutex
	var sent []string
	guest := map[string]any{"_id": "g1", "key": "guest_access", "portal_enabled": true, "auth": "hotspot", "password_enabled": true, "x_password": "portal-pass-1", "expire": "240"}
	wlans := `[{"_id":"w1","name":"Example Home","security":"wpapsk","x_passphrase":"example-pass-123","enabled":true},
		{"_id":"w2","name":"Example Guests","security":"wpapsk","x_passphrase":"guest-pass-456","is_guest":true,"enabled":true},
		{"_id":"w4","name":"Home Setup","security":"wpapsk","x_passphrase":"setup-pass-42","enabled":true}]`
	sta := `[{"ip":"192.168.2.21","mac":"aa:bb:cc:00:00:21","hostname":"guest-phone","essid":"Example Guests","is_guest":true},
		{"ip":"192.168.30.20","mac":"aa:bb:cc:00:00:30","hostname":"new-phone","essid":"Home Setup","is_guest":true}]`
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		ok := func(data string) { _, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":` + data + `}`)) }
		p := r.URL.Path
		if r.Method != "GET" {
			b, _ := io.ReadAll(r.Body)
			sent = append(sent, r.Method+" "+p[strings.LastIndex(p, "/api/"):]+" "+string(b))
		}
		switch {
		case strings.HasSuffix(p, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(p, "/rest/setting"):
			b, _ := json.Marshal([]any{guest})
			ok(string(b))
		case strings.Contains(p, "/rest/setting/guest_access/"):
			b, _ := json.Marshal([]any{guest})
			ok(string(b))
		case strings.HasSuffix(p, "/rest/wlanconf"):
			ok(wlans)
		case strings.HasSuffix(p, "/rest/wlanconf/w4"):
			ok(`[{"_id":"w4","name":"Home Setup","is_guest":false}]`)
		case strings.HasSuffix(p, "/stat/sta"):
			ok(sta)
		default:
			ok(`[]`)
		}
	}))
	defer console.Close()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.SSID = "Home Setup" })

	if _, err := a.portalAPI("preview", "admin"); err == nil || !strings.Contains(err.Error(), "port 80") {
		t.Fatalf("needs HomeOwl on port 80: %v", err)
	}
	a.mu.Lock()
	a.port80 = &http.Server{}
	a.mu.Unlock()
	pv, err := a.portalAPI("preview", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(pv); !strings.Contains(string(b), `"custom_ip":"192.168.1.10"`) || strings.Contains(string(b), "portal-pass-1") {
		t.Fatalf("preview: %s", b)
	}
	if _, err := a.portalAPI("apply", "admin"); err == nil {
		t.Fatal("change mode locked")
	}
	a.Changes.Write = true
	if _, err := a.portalAPI("apply", "admin"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := strings.Join(sent, "\n")
	mu.Unlock()
	if !strings.Contains(got, `"auth":"custom"`) || !strings.Contains(got, `"custom_ip":"192.168.1.10"`) || !strings.Contains(got, `/rest/wlanconf/w4 {"is_guest":true}`) {
		t.Fatalf("console changes: %s", got)
	}
	if pc := a.Cfg.Get().Onboard.Portal; pc == nil || pc.Mode != "password" || pc.Minutes != 240 {
		t.Fatalf("portal config: %+v", pc)
	}

	// UniFi's portal visits: the setup Wi-Fi goes to the check-in page, the guest Wi-Fi to the sign-in page
	visit := func(ip string) string {
		r := httptest.NewRequest("GET", "http://192.168.1.10/guest/s/default/?id=x&url=http%3A%2F%2Fexample.com%2F", nil)
		r.RemoteAddr = ip + ":5000"
		w := httptest.NewRecorder()
		a.portalVisit(w, r)
		return w.Header().Get("Location")
	}
	if l := visit("192.168.30.20"); l != "/hi" {
		t.Fatalf("setup Wi-Fi: %s", l)
	}
	if l := visit("192.168.2.21"); !strings.HasPrefix(l, "/welcome?url=") {
		t.Fatalf("guest Wi-Fi: %s", l)
	}
	welcome := func(ip, body string) (int, string) {
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r := httptest.NewRequest(method, "http://192.168.1.10/api/welcome", strings.NewReader(body))
		r.RemoteAddr = ip + ":5000"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.welcomeAPI(w, r)
		return w.Code, w.Body.String()
	}
	if code, _ := welcome("192.168.30.20", ""); code != 403 {
		t.Fatal("the setup Wi-Fi never signs in to go online")
	}
	if code, b := welcome("192.168.2.21", ""); code != 200 || !strings.Contains(b, `"password":true`) || strings.Contains(b, "portal-pass") {
		t.Fatalf("sign-in page: %d %s", code, b)
	}
	if code, _ := welcome("192.168.2.21", `{"password":"wrong"}`); code != 403 {
		t.Fatal("wrong password")
	}
	if code, b := welcome("192.168.2.21", `{"password":"portal-pass-1"}`); code != 200 {
		t.Fatalf("right password: %d %s", code, b)
	}
	mu.Lock()
	got = strings.Join(sent, "\n")
	mu.Unlock()
	if !strings.Contains(got, `"cmd":"authorize-guest"`) || !strings.Contains(got, `"mac":"aa:bb:cc:00:00:21"`) || !strings.Contains(got, `"minutes":240`) {
		t.Fatalf("authorize: %s", got)
	}
	for _, c := range a.Changes.List() {
		if b, _ := json.Marshal(c); strings.Contains(string(b), "portal-pass-1") {
			t.Fatal("the portal password is in the change log")
		}
	}
}
