package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/h4ux/homeowl/internal/cloudflare"
	"github.com/h4ux/homeowl/internal/config"
)

// fakeCF is a tiny Cloudflare: one zone, DNS records and the redirect entry-point ruleset.
type fakeCF struct {
	mu    sync.Mutex
	recs  []map[string]any
	rs    string
	rules []map[string]any
	n     int
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v}) }
	if r.Header.Get("Authorization") != "Bearer good-token" {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 1000, "message": "Invalid API Token"}}})
		return
	}
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	p := r.URL.Path
	f.n++
	id := fmt.Sprint("id", f.n)
	switch {
	case p == "/user/tokens/verify":
		ok(map[string]any{"status": "active"})
	case p == "/zones":
		ok([]map[string]any{{"id": "z1", "name": "example.com"}})
	case p == "/zones/z1/dns_records" && r.Method == "GET":
		out := []map[string]any{}
		for _, x := range f.recs {
			if x["name"] == r.URL.Query().Get("name") {
				out = append(out, x)
			}
		}
		ok(out)
	case p == "/zones/z1/dns_records" && r.Method == "POST":
		body["id"] = id
		f.recs = append(f.recs, body)
		ok(body)
	case strings.HasPrefix(p, "/zones/z1/dns_records/") && r.Method == "DELETE":
		l := f.recs[:0]
		for _, x := range f.recs {
			if x["id"] != strings.TrimPrefix(p, "/zones/z1/dns_records/") {
				l = append(l, x)
			}
		}
		f.recs = l
		ok(map[string]any{"id": id})
	case p == "/zones/z1/rulesets/phases/"+cloudflare.Phase+"/entrypoint" && r.Method == "GET":
		if f.rs == "" {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 10003, "message": "not found"}}})
			return
		}
		ok(map[string]any{"id": f.rs, "rules": f.rules})
	case p == "/zones/z1/rulesets/phases/"+cloudflare.Phase+"/entrypoint" && r.Method == "PUT":
		f.rs = "rs1"
		f.rules = nil
		for _, x := range body["rules"].([]any) {
			m := x.(map[string]any)
			m["id"] = fmt.Sprint("r", len(f.rules))
			f.rules = append(f.rules, m)
		}
		ok(map[string]any{"id": f.rs, "rules": f.rules})
	case p == "/zones/z1/rulesets/rs1/rules" && r.Method == "POST":
		body["id"] = id
		f.rules = append(f.rules, body)
		ok(map[string]any{"id": f.rs, "rules": f.rules})
	case strings.HasPrefix(p, "/zones/z1/rulesets/rs1/rules/"):
		rid := strings.TrimPrefix(p, "/zones/z1/rulesets/rs1/rules/")
		l := f.rules[:0]
		for _, x := range f.rules {
			if x["id"] == rid {
				if r.Method == "DELETE" {
					continue
				}
				body["id"] = rid
				x = body
			}
			l = append(l, x)
		}
		f.rules = l
		ok(map[string]any{"id": f.rs, "rules": f.rules})
	default:
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false})
	}
}

func (f *fakeCF) summary() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var l []string
	for _, x := range f.recs {
		l = append(l, fmt.Sprint("dns ", x["name"]))
	}
	for _, x := range f.rules {
		ap, _ := json.Marshal(x["action_parameters"])
		l = append(l, fmt.Sprintf("rule %v %v %s", x["ref"], x["expression"], ap))
	}
	return strings.Join(l, "\n")
}

func TestCloudflareLinks(t *testing.T) {
	f := &fakeCF{recs: []map[string]any{{"id": "mine", "name": "www.example.com", "type": "A", "content": "203.0.113.9"}}, rs: "rs1",
		rules: []map[string]any{{"id": "other", "ref": "users_rule", "expression": "true", "action": "redirect"}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	oldAPI, oldHome, oldIP := cloudflare.API, cfHome, homeIP
	defer func() { cloudflare.API, cfHome, homeIP = oldAPI, oldHome, oldIP }()
	cloudflare.API = srv.URL
	wan := []string{"203.0.113.7"}
	cfHome = func(context.Context) []string { return wan }
	ip := "192.168.1.10"
	homeIP = func() string { return ip }

	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) {
		c.LAN.Enabled, c.LAN.Port = true, 8555
		c.Names.Guest = "wifi"
	})
	if _, err := a.cloudflareAPI("cloudflare/connect", "alex", map[string]any{"token": "bad"}); err == nil {
		t.Fatal("a bad token must be refused")
	}
	if _, err := a.cloudflareAPI("cloudflare/connect", "alex", map[string]any{"token": "good-token"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.cloudflareAPI("cloudflare/settings", "alex", map[string]any{"zone_id": "z1", "wifi": "wifi", "hi": "hi"}); err != nil {
		t.Fatal(err)
	}
	got := f.summary()
	for _, want := range []string{"dns www.example.com", "dns wifi.example.com", "dns hi.example.com", "rule users_rule true",
		`rule homeowl_wifi (http.host eq "wifi.example.com" and ip.src in {203.0.113.7})`, `"value":"http://192.168.1.10:8555/wifi"`,
		`"value":"http://192.168.1.10:8555/hi"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if st := a.cfLoad(); len(st.Errors) > 0 {
		t.Fatal(st.Errors)
	}
	if a.cfDue() {
		t.Fatal("nothing changed")
	}
	ip, wan = "192.168.1.11", []string{"203.0.113.8"} // new addresses at home and on the internet
	if !a.cfDue() {
		t.Fatal("new addresses must be synced")
	}
	a.syncCloudflare("test")
	got = f.summary()
	if !strings.Contains(got, "203.0.113.8") || !strings.Contains(got, "192.168.1.11:8555/wifi") || strings.Contains(got, "192.168.1.10") {
		t.Fatalf("rules must follow the addresses:\n%s", got)
	}
	if v := a.cloudflareView(); len(v["links"].([]map[string]string)) != 2 {
		t.Fatalf("links: %v", v["links"])
	}
	// turning it off removes only what HomeOwl made
	if _, err := a.cloudflareAPI("cloudflare/disconnect", "alex", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.summary(); got != "dns www.example.com\nrule users_rule true null" {
		t.Fatalf("after disconnect:\n%s", got)
	}
	if a.Sec.Get(cfSlot) != "" {
		t.Fatal("the token must be forgotten")
	}
}
