package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWifiIdentCheck(t *testing.T) {
	var methods []string
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(p, "/stat/sysinfo"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"version":"9.1.0"}]}`))
		case strings.HasSuffix(p, "/rest/networkconf"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"n1","name":"Default","purpose":"corporate"},{"_id":"n2","name":"Kids","purpose":"corporate","vlan":21},{"_id":"n3","purpose":"wan"}]}`))
		case strings.HasSuffix(p, "/rest/wlanconf"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[
				{"_id":"w1","name":"Example Home","security":"wpapsk","wpa_mode":"wpa2","x_passphrase":"example-pass-123","enabled":true,"networkconf_id":"n1",
				 "private_preshared_keys_enabled":true,"private_preshared_keys":[{"password":"ppsk-secret-1","networkconf_id":"n1"},{"password":"ppsk-secret-2","networkconf_id":"n2"}]},
				{"_id":"w2","name":"Example Work","security":"wpaeap","radiusprofile_id":"r1","enabled":true}]}`))
		case strings.HasSuffix(p, "/rest/setting"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"key":"radius","enabled":true,"x_secret":"radius-secret-9"},
				{"key":"guest_access","portal_enabled":true,"auth":"custom","custom_ip":"192.168.1.5","x_password":"guest-pass-7"}]}`))
		case strings.HasSuffix(p, "/rest/account"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"name":"alex","x_password":"user-pass-1","vlan":21}]}`))
		case strings.HasSuffix(p, "/rest/radiusprofile"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"name":"Default"}]}`))
		case strings.HasSuffix(p, "/stat/sta"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[
				{"mac":"00:bb:cc:00:00:01","1x_identity":"alex","is_wired":false},
				{"mac":"12:bb:cc:00:00:02","is_wired":false},
				{"mac":"aa:bb:cc:00:00:03","is_wired":true}]}`))
		default:
			http.NotFound(w, r)
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
	methods = nil
	r, err := a.wifiIdentCheck()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range methods {
		if m != "GET" {
			t.Fatalf("the check must only read: %v", methods)
		}
	}
	b, _ := json.Marshal(r)
	out := string(b)
	for _, secret := range []string{"example-pass-123", "ppsk-secret", "radius-secret-9", "guest-pass-7", "user-pass-1", `"alex"`, "192.168.1.5"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q must not leave the check: %s", secret, out)
		}
	}
	if r.Version != "9.1.0" || r.PPSK["networks_on"] != 1 || r.PPSK["keys"] != 2 || r.PPSK["key_vlans"] != 2 || r.RADIUS["server_on"] != true ||
		r.RADIUS["users"] != 1 || r.RADIUS["enterprise_wifi"] != 1 || r.Portal["external"] != true || r.Clients["private_mac"] != 1 || r.Clients["wifi"] != 2 {
		t.Fatalf("summary: %s", out)
	}
	if !strings.Contains(out, "1x_identity") || !strings.Contains(out, "names the device's owner") {
		t.Fatalf("verdict: %s", out)
	}
}
