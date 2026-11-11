package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/config"
)

// A fake NextDNS with two accounts (keys) and three profiles: the home DNS names go into every profile, follow
// the address, leave other rewrites alone, and are removed when turned off.
func TestHomeDNS(t *testing.T) {
	var mu sync.Mutex
	rw := map[string][]map[string]string{"p1": {{"id": "x1", "name": "printer.home.arpa", "content": "192.168.1.5"}}, "p2": nil, "p3": nil}
	owner := map[string]string{"key-one": "p1 p2", "key-two": "p3"}
	next := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		profs, ok := owner[r.Header.Get("X-Api-Key")]
		if !ok {
			w.WriteHeader(401)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) == 1 {
			var d []map[string]string
			for _, p := range strings.Fields(profs) {
				d = append(d, map[string]string{"id": p, "name": "Profile " + p})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": d})
			return
		}
		p := parts[1]
		if !strings.Contains(profs, p) {
			w.WriteHeader(404)
			return
		}
		switch {
		case r.Method == "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rw[p]})
		case r.Method == "POST":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			next++
			b["id"] = fmt.Sprint("n", next)
			rw[p] = append(rw[p], b)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": b})
		case r.Method == "DELETE":
			l := rw[p][:0]
			for _, x := range rw[p] {
				if x["id"] != parts[3] {
					l = append(l, x)
				}
			}
			rw[p] = l
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	analytics.SetNextDNSAPI(srv.URL)
	ip, oldIP := "192.168.1.10", homeIP
	homeIP = func() string { return ip }
	defer func() { homeIP = oldIP }()

	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Sec.Set("nextdns-api-key", "key-one")
	_ = a.Sec.Set("nextdns-key-2", "key-two")
	_ = a.Cfg.Update(func(c *config.Config) {
		c.Analytics.NextDNSAccounts = []config.NextDNSAccount{{Slot: "nextdns-key-2", Label: "Second"}}
		c.LAN.Enabled = true
		c.Names = config.Names{Checkin: "hi", Guest: "wifi", Domain: "home.arpa"}
	})
	names := func(p string) string {
		mu.Lock()
		defer mu.Unlock()
		var l []string
		for _, x := range rw[p] {
			l = append(l, x["name"]+"="+x["content"])
		}
		return strings.Join(l, ",")
	}
	st := a.syncHomeDNS("test")
	if st.Profiles != 3 || len(st.Errors) > 0 {
		t.Fatalf("all three profiles: %+v", st)
	}
	if got := names("p1"); got != "printer.home.arpa=192.168.1.5,hi.home.arpa=192.168.1.10,wifi.home.arpa=192.168.1.10" {
		t.Fatalf("p1: %s", got)
	}
	if got := names("p3"); got != "hi.home.arpa=192.168.1.10,wifi.home.arpa=192.168.1.10" {
		t.Fatalf("p3 (second account): %s", got)
	}
	if a.homeDNSDue() {
		t.Fatal("nothing changed, no sync due")
	}
	ip = "192.168.1.11" // the computer got a new address
	if !a.homeDNSDue() {
		t.Fatal("a new address must be synced")
	}
	a.syncHomeDNS("test")
	if got := names("p2"); got != "hi.home.arpa=192.168.1.11,wifi.home.arpa=192.168.1.11" {
		t.Fatalf("p2 after the address changed: %s", got)
	}
	// the name host is served like the .local one
	r := httptest.NewRequest("GET", "http://wifi.home.arpa/", nil)
	if a.nameHost(r) != "guest" || !a.hostAllowed(r) {
		t.Fatal("wifi.home.arpa must serve the guest card")
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Names.Domain = "" })
	a.syncHomeDNS("test")
	if got := names("p1"); got != "printer.home.arpa=192.168.1.5" {
		t.Fatalf("turned off: only the user's own rewrite stays, got %s", got)
	}
	if got := names("p3"); got != "" {
		t.Fatalf("p3 turned off: %s", got)
	}
}
