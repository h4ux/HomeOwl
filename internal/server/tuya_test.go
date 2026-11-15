package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/h4ux/homeowl/internal/tuya"
)

func TestTuyaCloud(t *testing.T) {
	a := checkinApp(t)
	var cmds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/v1.0/token":
			_, _ = w.Write([]byte(`{"success":true,"result":{"access_token":"tok","expire_time":7200}}`))
		case strings.HasSuffix(r.URL.Path, "/associated-users/devices"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"has_more":false,"devices":[{"id":"bf00000000000001","name":"Desk plug","category":"cz","online":true,"local_key":"0123456789abcdef",
				"status":[{"code":"switch_1","value":false},{"code":"cur_power","value":125},{"code":"cur_voltage","value":2301}]}]}}`))
		case strings.HasSuffix(r.URL.Path, "/specification"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"status":[{"code":"cur_power","type":"Integer","values":"{\"unit\":\"W\",\"scale\":1}"},{"code":"cur_voltage","type":"Integer","values":"{\"unit\":\"V\",\"scale\":1}"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/commands"):
			cmds = append(cmds, string(b))
			_, _ = w.Write([]byte(`{"success":true,"result":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tuya.Regions["test"] = srv.URL
	defer delete(tuya.Regions, "test")

	v, err := a.tuyaAPI(context.Background(), "settings", map[string]any{"mode": "cloud", "region": "test", "access_id": "cid", "access_secret": "sec"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	devs := v["devices"].([]map[string]any)
	if len(devs) != 1 || devs[0]["name"] != "Desk plug" || devs[0]["kind"] != "Plug" || devs[0]["online"] != true {
		t.Fatalf("devices: %v", v)
	}
	sw := devs[0]["switches"].([]map[string]any)
	if len(sw) != 1 || sw[0]["code"] != "switch_1" || sw[0]["label"] != "Power" {
		t.Fatalf("switches: %v", sw)
	}
	_ = a.tuyaSync() // units arrive after the first sync
	rd := a.tuyaView(true)["devices"].([]map[string]any)[0]["readings"].([]map[string]any)
	if len(rd) != 2 || rd[0]["value"] != "12.5" || rd[0]["unit"] != "W" || rd[1]["value"] != "230.1" {
		t.Fatalf("readings: %v", rd)
	}
	if a.Sec.Get(tuyaKeySlot("bf00000000000001")) != "0123456789abcdef" {
		t.Fatal("the local key is kept for local mode")
	}
	if v := a.tuyaView(false); v["cloud_ready"] != nil || v["found"] != nil {
		t.Fatalf("viewers see devices only: %v", v)
	}
	if err := a.tuyaSwitch("bf00000000000001", "switch_9", true, "admin"); err == nil {
		t.Fatal("no such switch")
	}
	if err := a.tuyaSwitch("bf00000000000001", "switch_1", true, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || !strings.Contains(cmds[0], `"value":true`) {
		t.Fatalf("command: %v", cmds)
	}
	logged := false
	for _, c := range a.Changes.List() {
		if strings.Contains(c.Description, "Turn on Desk plug") && c.OK {
			logged = true
		}
	}
	if !logged {
		t.Fatal("in History › Changes")
	}
	if sw := a.tuyaView(true)["devices"].([]map[string]any)[0]["switches"].([]map[string]any); sw[0]["on"] != true {
		t.Fatalf("shows on: %v", sw)
	}
	if _, err := a.tuyaAPI(context.Background(), "device", map[string]any{"id": "x"}, "admin"); err == nil {
		t.Fatal("bad id")
	}
}

func TestTuyaFirewall(t *testing.T) {
	orig := ownIPs
	defer func() { ownIPs = orig }()
	ownIPs = func() []net.IP { return []net.IP{net.ParseIP("192.168.1.5").To4()} }
	for _, zoned := range []bool{true, false} {
		var mu sync.Mutex
		posts := map[string]map[string]any{}
		console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if r.Method == "POST" {
				b, _ := io.ReadAll(r.Body)
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				mu.Lock()
				posts[p[strings.LastIndex(p, "/")+1:]] = m
				mu.Unlock()
			}
			switch {
			case strings.HasSuffix(p, "/integration/v1/info"):
				_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
			case strings.HasSuffix(p, "/rest/networkconf"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"lan","name":"Home","purpose":"corporate","ip_subnet":"192.168.1.1/24","attr_hidden_id":"LAN"},
					{"_id":"iot","name":"IoT","purpose":"corporate","ip_subnet":"192.168.2.1/24"}]}`))
			case strings.HasSuffix(p, "/firewall/zone"):
				if !zoned {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`[{"_id":"zi","name":"Internal","network_ids":["lan"]},{"_id":"zt","name":"IoT","network_ids":["iot"]}]`))
			case strings.HasSuffix(p, "/firewall-policies"):
				if r.Method == "POST" {
					w.WriteHeader(201)
					_, _ = w.Write([]byte(`{"_id":"pol1"}`))
					return
				}
				_, _ = w.Write([]byte(`[]`))
			case strings.HasSuffix(p, "/rest/firewallgroup"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"grp1"}]}`))
			case strings.HasSuffix(p, "/rest/firewallrule"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"rule1","ruleset":"LAN_IN","rule_index":2000}]}`))
			case strings.HasSuffix(p, "/stat/sta"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
			default:
				http.NotFound(w, r)
			}
		}))
		a, err := New(t.TempDir(), false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
			t.Fatal(err)
		}
		// a scan of both networks found nothing: only the other network is flagged
		a.tuya.Nets = []string{"192.168.1.0/24", "192.168.2.0/24"}
		if u := a.tuyaUnreached(nil); len(u) != 1 || u[0]["network"] != "192.168.2.0/24" {
			t.Fatalf("unreached: %v", u)
		}
		if u := a.tuyaUnreached([]map[string]any{{"ip": "192.168.2.40"}}); len(u) != 0 {
			t.Fatalf("something answered there: %v", u)
		}
		if _, err := a.tuyaAPI(context.Background(), "firewall-check", map[string]any{"network": "192.168.1.0/24"}, "admin"); err == nil {
			t.Fatal("HomeOwl's own network needs no rule")
		}
		v, err := a.tuyaAPI(context.Background(), "firewall-check", map[string]any{"network": "192.168.2.0/24"}, "admin")
		if err != nil {
			t.Fatal(err)
		}
		if l, _ := v["fix"].([]any); len(l) != map[bool]int{true: 1, false: 2}[zoned] || v["from"] != "Home" {
			t.Fatalf("zoned=%v: preview %v", zoned, v)
		}
		if _, err := a.tuyaAPI(context.Background(), "firewall-fix", map[string]any{"network": "192.168.2.0/24"}, "admin"); err == nil {
			t.Fatal("change mode is locked")
		}
		a.Changes.Write = true
		if _, err := a.tuyaAPI(context.Background(), "firewall-fix", map[string]any{"network": "192.168.2.0/24"}, "admin"); err != nil {
			t.Fatalf("zoned=%v: %v", zoned, err)
		}
		mu.Lock()
		if zoned {
			pol := posts["firewall-policies"]
			src, _ := pol["source"].(map[string]any)
			dst, _ := pol["destination"].(map[string]any)
			if pol["protocol"] != "tcp" || src["zone_id"] != "zi" || src["network_ids"].([]any)[0] != "lan" || dst["zone_id"] != "zt" ||
				dst["port"] != "6668" || dst["ips"].([]any)[0] != "192.168.2.0/24" {
				t.Fatalf("policy: %v", pol)
			}
		} else {
			rule := posts["firewallrule"]
			if rule["src_networkconf_id"] != "lan" || rule["dst_address"] != "192.168.2.0/24" || rule["dst_firewallgroup_ids"].([]any)[0] != "grp1" {
				t.Fatalf("rule: %v", rule)
			}
		}
		mu.Unlock()
		for _, c := range a.Changes.List() {
			if !c.Undoable {
				t.Fatalf("not undoable: %+v", c)
			}
		}
		console.Close()
	}
}
