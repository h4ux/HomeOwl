package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCreateSetupWiFi(t *testing.T) {
	resetWlans()
	t.Cleanup(resetWlans)
	var mu sync.Mutex
	nets := []map[string]any{
		{"_id": "n1", "name": "Default", "purpose": "corporate", "attr_hidden_id": "LAN", "ip_subnet": "192.168.1.1/24", "networkgroup": "LAN",
			"dhcpd_enabled": true, "dhcpd_start": "192.168.1.10", "domain_name": "example.lan", "ipv6_interface_type": "pd", "site_id": "s1", "igmp_snooping": true},
		{"_id": "n2", "name": "Example IoT", "purpose": "corporate", "vlan_enabled": true, "vlan": 20, "ip_subnet": "192.168.20.1/24"}}
	wlans := []map[string]any{{"_id": "w1", "name": "Example Home", "security": "wpapsk", "wpa_mode": "wpa2", "wpa3_support": true, "x_passphrase": "example-pass-123",
		"x_iapp_key": "secret-iapp", "private_preshared_keys": []any{map[string]any{"password": "ppsk-1"}}, "mac_filter_enabled": true, "mac_filter_list": []any{"00:11:22:00:00:01"},
		"usergroup_id": "ug1", "ap_group_ids": []any{"ag1"}, "wlan_bands": []any{"2g", "5g"}, "networkconf_id": "n1", "enabled": true}}
	var posted []map[string]any
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		out := func(l []map[string]any) {
			b, _ := json.Marshal(map[string]any{"meta": map[string]any{"rc": "ok"}, "data": l})
			_, _ = w.Write(b)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/networkconf") && r.Method == "POST":
			var m map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &m)
			posted = append(posted, m)
			m["_id"] = "n9"
			nets = append(nets, m)
			out([]map[string]any{m})
		case strings.HasSuffix(r.URL.Path, "/rest/networkconf"):
			out(nets)
		case strings.HasSuffix(r.URL.Path, "/rest/wlanconf") && r.Method == "POST":
			var m map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &m)
			posted = append(posted, m)
			m["_id"] = "w9"
			wlans = append(wlans, m)
			out([]map[string]any{m})
		case strings.HasSuffix(r.URL.Path, "/rest/wlanconf"):
			out(wlans)
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
	body := map[string]any{"ssid": "Home Setup", "password": "setup-pass-42", "vlan": float64(30), "network": "HomeOwl Setup"}

	// bad input is refused before anything is sent
	for _, bad := range []map[string]any{
		{"ssid": "Home Setup", "password": "short", "vlan": float64(30)},
		{"ssid": "Example Home", "password": "setup-pass-42", "vlan": float64(30)},
		{"ssid": "Home Setup", "password": "setup-pass-42", "vlan": float64(20)},
	} {
		if _, err := a.setupWiFiAPI("preview", bad, "admin"); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	pv, err := a.setupWiFiAPI("preview", body, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(pv); strings.Contains(string(b), "setup-pass-42") || !strings.Contains(string(b), "192.168.30.0/24") {
		t.Fatalf("preview: %s", b)
	}
	// locked change mode: nothing is created
	if _, err := a.setupWiFiAPI("apply", body, "admin"); err == nil || len(posted) != 0 {
		t.Fatalf("change mode locked: %v %d", err, len(posted))
	}
	a.Changes.Write = true
	if _, err := a.setupWiFiAPI("apply", body, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 2 {
		t.Fatalf("two creates: %d", len(posted))
	}
	n, wl := posted[0], posted[1]
	if n["name"] != "HomeOwl Setup" || n["vlan"] != float64(30) || n["ip_subnet"] != "192.168.30.1/24" || n["internet_access_enabled"] != false ||
		n["dhcpd_start"] != "192.168.30.10" || n["domain_name"] != nil || n["ipv6_interface_type"] != "none" || n["site_id"] != nil || n["igmp_snooping"] != true {
		t.Fatalf("network body: %v", n)
	}
	wb, _ := json.Marshal(wl)
	for _, leak := range []string{"example-pass-123", "secret-iapp", "ppsk-1", "00:11:22:00:00:01"} {
		if strings.Contains(string(wb), leak) {
			t.Fatalf("%q copied into the setup Wi-Fi: %s", leak, wb)
		}
	}
	if wl["name"] != "Home Setup" || wl["x_passphrase"] != "setup-pass-42" || wl["networkconf_id"] != "n9" || wl["usergroup_id"] != "ug1" ||
		wl["wpa3_support"] != true || wl["mac_filter_enabled"] != false {
		t.Fatalf("Wi-Fi body: %s", wb)
	}
	if a.Cfg.Get().Onboard.SSID != "Home Setup" {
		t.Fatal("it becomes HomeOwl's setup Wi-Fi")
	}
	n2 := 0
	for _, c := range a.Changes.List() {
		cb, _ := json.Marshal(c)
		if strings.Contains(string(cb), "setup-pass-42") {
			t.Fatalf("the password is in the change log: %s", cb)
		}
		if strings.HasPrefix(c.Description, "Setup Wi-Fi") && c.Undoable {
			n2++
		}
	}
	if n2 != 2 {
		t.Fatalf("both creates logged with undo: %d", n2)
	}
}
