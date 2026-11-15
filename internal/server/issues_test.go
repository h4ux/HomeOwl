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

func TestDeviceIssueFix(t *testing.T) {
	orig := probe
	defer func() { probe = orig }()
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
			case strings.HasSuffix(p, "/stat/sta"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"mac":"00:11:22:00:00:20","ip":"192.168.6.20","network_id":"iot","hostname":"office-printer"}]}`))
			case strings.HasSuffix(p, "/rest/networkconf"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"lan","name":"Home","purpose":"corporate","ip_subnet":"192.168.1.1/24","attr_hidden_id":"LAN"},
					{"_id":"iot","name":"IoT","purpose":"corporate","ip_subnet":"192.168.6.1/24"},{"_id":"wan","name":"Internet","purpose":"wan"}]}`))
			case strings.HasSuffix(p, "/firewall/zone"):
				if !zoned {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`[{"_id":"zi","name":"Internal","network_ids":["lan"]},{"_id":"zt","name":"IoT","network_ids":["iot"]}]`))
			case strings.HasSuffix(p, "/firewall-policies"):
				if r.Method == "POST" {
					_, _ = w.Write([]byte(`{"_id":"pol1"}`))
					return
				}
				_, _ = w.Write([]byte(`[]`))
			case strings.HasSuffix(p, "/rest/firewallgroup"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"grp1"}]}`))
			case strings.HasSuffix(p, "/rest/firewallrule"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"rule1","ruleset":"LAN_IN","rule_index":2000}]}`))
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
		answers := false
		probe = func(ip string, ports []int) map[int]bool { return map[int]bool{631: answers} }
		body := map[string]any{"mac": "00:11:22:00:00:20", "issue": "print"}
		v, err := a.issueAPI("check", body, "192.168.1.50", "admin")
		if err != nil {
			t.Fatal(err)
		}
		if l, _ := v["fix"].([]any); len(l) != map[bool]int{true: 1, false: 2}[zoned] || v["answers"] != false {
			t.Fatalf("zoned=%v: the fix: %v", zoned, v)
		}
		if f, _ := v["from"].([]string); len(f) != 1 || f[0] != "lan" {
			t.Fatalf("from the viewer's network: %v", v["from"])
		}
		if _, err := a.issueAPI("fix", body, "192.168.1.50", "admin"); err == nil {
			t.Fatal("change mode is locked")
		}
		a.Changes.Write = true
		answers = true
		res, err := a.issueAPI("fix", body, "192.168.1.50", "admin")
		if err != nil || res["answers"] != true {
			t.Fatalf("zoned=%v: fixed: %v %v", zoned, res, err)
		}
		mu.Lock()
		if zoned {
			pol := posts["firewall-policies"]
			src, _ := pol["source"].(map[string]any)
			dst, _ := pol["destination"].(map[string]any)
			if pol["action"] != "ALLOW" || src["zone_id"] != "zi" || dst["zone_id"] != "zt" || dst["matching_target_type"] != "SPECIFIC" || dst["port"] != "631,9100,80,443,8080" ||
				dst["ips"].([]any)[0] != "192.168.6.20" {
				t.Fatalf("policy: %v", pol)
			}
		} else {
			rule, grp := posts["firewallrule"], posts["firewallgroup"]
			if rule["action"] != "accept" || rule["src_networkconf_id"] != "lan" || rule["dst_address"] != "192.168.6.20" ||
				rule["dst_firewallgroup_ids"].([]any)[0] != "grp1" || rule["rule_index"] != float64(2001) || grp["group_type"] != "port-group" {
				t.Fatalf("rule %v group %v", rule, grp)
			}
		}
		mu.Unlock()
		// every part can be undone (deleted)
		for _, c := range a.Changes.List() {
			if !c.Undoable {
				t.Fatalf("not undoable: %+v", c)
			}
		}
		console.Close()
	}
}
