package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/auth"
	"github.com/h4ux/homeowl/internal/config"
)

func TestPresenceAPI(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.An.OpenDB(); err != nil {
		t.Fatal(err)
	}
	pid, err := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []auth.UserInput{{Username: "alex", Role: auth.RoleAdmin, Password: "a-long-password"}, {Username: "sam", Role: auth.RoleViewer, Password: "a-long-password"}} {
		if err := a.Users.Save(u, true); err != nil {
			t.Fatal(err)
		}
	}
	call := func(user, method, path, body string) *httptest.ResponseRecorder {
		tok, csrf, _, err := a.Users.Login(user, "a-long-password", "browser")
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:8555"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:5000"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		r.Header.Set("X-Audit-Token", csrf)
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		return w
	}
	if w := call("sam", "POST", "/api/presence/tag", `{"mac":"aa:bb:cc:00:00:01","person":"`+pid+`"}`); w.Code != 403 {
		t.Fatalf("viewer tagged a device: %d %s", w.Code, w.Body)
	}
	if w := call("alex", "POST", "/api/presence/tag", `{"mac":"not-a-mac","person":"`+pid+`"}`); w.Code != 400 {
		t.Fatalf("bad mac: %d", w.Code)
	}
	if w := call("alex", "POST", "/api/presence/tag", `{"mac":"AA:BB:CC:00:00:01","person":"`+pid+`"}`); w.Code != 200 {
		t.Fatalf("tag: %d %s", w.Code, w.Body)
	}
	if got := a.Cfg.Get().Presence.Devices["aa:bb:cc:00:00:01"]; got != pid {
		t.Fatalf("tag saved as %q", got)
	}
	if w := call("sam", "GET", "/api/presence", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "aa:bb:cc:00:00:01") || strings.Contains(w.Body.String(), `"admin"`) {
		t.Fatalf("viewer view: %d %s", w.Code, w.Body)
	}
	if w := call("alex", "POST", "/api/presence/settings", `{"history":false,"away_minutes":1}`); w.Code != 200 || !a.Cfg.Get().Presence.NoHistory || a.Cfg.Get().Presence.AwayMinutes != 2 {
		t.Fatalf("settings: %d %+v", w.Code, a.Cfg.Get().Presence)
	}
	if w := call("alex", "POST", "/api/presence/tag", `{"mac":"aa:bb:cc:00:00:01","person":""}`); w.Code != 200 || len(a.Cfg.Get().Presence.Devices) != 0 {
		t.Fatalf("untag: %d", w.Code)
	}
}

// Admin routes that change something don't run from a GET (a link or an <img> on another page can't make
// HomeOwl act); the read-only ones still do.
func TestAdminGetIsReadOnly(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Users.Save(auth.UserInput{Username: "alex", Role: auth.RoleAdmin, Password: "a-long-password"}, true); err != nil {
		t.Fatal(err)
	}
	tok, _, _, err := a.Users.Login("alex", "a-long-password", "browser")
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8555"+path, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		return w
	}
	for _, p := range []string{"write-mode", "disconnect", "presence/clear", "users/delete", "mcp/token", "update/apply", "undo",
		"guest-wifi/rotate-now", "doors/unlock", "alerts/clear", "analytics/sync", "names/sync", "appblocks/delete"} {
		if w := get("/api/" + p); w.Code == 200 || !strings.Contains(w.Body.String(), "use POST") {
			t.Errorf("GET %s ran: %d %s", p, w.Code, w.Body)
		}
	}
	for _, p := range []string{"checkins", "devtypes", "guest-wifi/schedule"} {
		if w := get("/api/" + p); strings.Contains(w.Body.String(), "use POST") {
			t.Errorf("GET %s refused: %s", p, w.Body)
		}
	}
}

func TestCheckinLinks(t *testing.T) {
	a := checkinApp(t)
	_ = a.Cfg.Update(func(c *config.Config) {
		c.LAN.Enabled, c.LAN.Port = true, 8555
		c.Names.Checkin, c.Names.Domain = "hi", "home.example.com"
	})
	l := a.checkinLinks()
	if len(l) == 0 || l[0] != "http://hi.home.example.com:8555" {
		t.Fatalf("the home-domain name comes first: %v", l)
	}
	a.cfSave(cfState{Zone: "example.com", Names: []cfName{{Kind: "hi", Host: "hi.example.com"}, {Kind: "wifi", Host: "wifi.example.com"}}})
	if l = a.checkinLinks(); l[0] != "https://hi.example.com" || l[1] != "http://hi.home.example.com:8555" || contains(l, "https://wifi.example.com") {
		t.Fatalf("the Cloudflare link comes first: %v", l)
	}
	a.cfSave(cfState{Zone: "example.com", Names: []cfName{{Kind: "hi", Host: "hi.example.com", Check: "it didn't open"}}})
	if l = a.checkinLinks(); l[0] == "https://hi.example.com" {
		t.Fatalf("a link that doesn't work isn't offered: %v", l)
	}
}
