package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/auth"
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
