package unifi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A fake console with a guest Wi-Fi, a home Wi-Fi and a Wi-Fi on a guest network.
func fakeConsole(t *testing.T) (*Client, *[]string, *sync.Mutex) {
	var mu sync.Mutex
	var puts []string
	wlans := map[string]map[string]any{
		"wguest": {"_id": "wguest", "name": "Guests", "is_guest": true, "security": "wpapsk", "x_passphrase": "old-guest-pass"},
		"whome":  {"_id": "whome", "name": "Home", "security": "wpapsk", "x_passphrase": "home-secret-pass", "networkconf_id": "nhome"},
		"wcafe":  {"_id": "wcafe", "name": "Cafe", "security": "wpapsk", "x_passphrase": "cafe-pass-1", "networkconf_id": "nguest"},
		"wopen":  {"_id": "wopen", "name": "Open", "is_guest": true, "security": "open"},
	}
	nets := map[string]map[string]any{"nhome": {"_id": "nhome", "purpose": "corporate"}, "nguest": {"_id": "nguest", "purpose": "guest"}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		send := func(v any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"rc": "ok"}, "data": []any{v}})
		}
		switch {
		case strings.HasSuffix(p, "/integration/v1/info"):
			_ = json.NewEncoder(w).Encode(map[string]any{"applicationVersion": "9.0"})
		case strings.Contains(p, "/rest/wlanconf/"):
			x := wlans[filepath.Base(p)]
			if x == nil {
				http.NotFound(w, r)
				return
			}
			if r.Method == "PUT" {
				var b map[string]any
				_ = json.NewDecoder(r.Body).Decode(&b)
				for k, v := range b {
					x[k] = v
				}
				puts = append(puts, x["x_passphrase"].(string))
			}
			send(x)
		case strings.Contains(p, "/rest/networkconf/"):
			send(nets[filepath.Base(p)])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New()
	if _, err := c.Connect(srv.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	return c, &puts, &mu
}

func TestGuestPasswordRotation(t *testing.T) {
	c, puts, mu := fakeConsole(t)
	dir := t.TempDir()
	ch := NewChanges(dir)
	ch.Write = true

	for _, id := range []string{"whome"} {
		if _, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": id, "password": "new-pass-1234"}, true, "alex", true); err == nil {
			t.Fatalf("%s is not a guest network and must be refused", id)
		}
	}
	if _, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": "wopen"}, true, "alex", true); err == nil {
		t.Fatal("an open network has no password to change")
	}
	if _, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": "wguest", "password": "short"}, true, "alex", true); err == nil {
		t.Fatal("a 5-character password must be refused")
	}
	// a Wi-Fi on a guest network counts as guest; so does one an admin picked
	if _, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": "wcafe"}, true, "alex", true); err != nil {
		t.Fatalf("Wi-Fi on a guest network: %v", err)
	}
	c.GuestSSIDs = func() []string { return []string{"whome"} }
	if _, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": "whome"}, true, "alex", true); err != nil {
		t.Fatalf("picked Wi-Fi: %v", err)
	}
	c.GuestSSIDs = nil

	prev, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": "wguest", "password": "new-pass-1234"}, true, "alex", true)
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := json.Marshal(prev)
	if strings.Contains(string(pj), "old-guest-pass") || strings.Contains(string(pj), "new-pass-1234") {
		t.Fatalf("the preview must not contain passwords: %s", pj)
	}
	res, err := ch.Run(c, Action{"kind": "guestpass", "wlan_id": "wguest", "password": "new-pass-1234"}, false, "alex", true)
	if err != nil {
		t.Fatal(err)
	}
	chg := res["result"].(Change)
	if !chg.OK || !chg.Undoable {
		t.Fatalf("change: %+v", chg)
	}
	if err := ch.Undo(c, chg.ID, "alex", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string{}, *puts...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "new-pass-1234" || got[1] != "old-guest-pass" {
		t.Fatalf("console saw %v, want the new password then the old one back", got)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "changes-log.jsonl"))
	if strings.Contains(string(log), "old-guest-pass") || strings.Contains(string(log), "new-pass-1234") || len(log) == 0 {
		t.Fatalf("the change log must not contain passwords:\n%s", log)
	}
}

func TestNewGuestPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p := NewGuestPassword()
		if len(p) != 14 || strings.Count(p, "-") != 2 || checkPassphrase(p) != nil || strings.ContainsAny(p, "il1o0") {
			t.Fatalf("bad password %q", p)
		}
		seen[p] = true
	}
	if len(seen) < 50 {
		t.Fatal("passwords repeat")
	}
}
