package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMenubarAPI(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	var icon string
	var level, count, height int
	visible := true
	a.Menubar = &Menubar{Token: "test-token-123", Styles: []string{"outline", "dot", "filled", "logo", "count"},
		SetVisible: func(v bool) { visible = v }, SetIcon: func(s string, l, c int) { icon, level, count = s, l, c },
		Close: func() {}, SetHeight: func(h int) { height = h }}
	call := func(method, path, token, remote, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8555"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		if token != "" {
			r.Header.Set("X-HomeOwl-Menubar", token)
		}
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		return w
	}
	const local = "127.0.0.1:5000"
	for _, c := range []struct{ token, remote string }{{"", local}, {"wrong", local}, {"test-token-123", "192.168.1.30:5000"}} {
		if w := call("GET", "/api/menubar", c.token, c.remote, ""); w.Code != 403 {
			t.Fatalf("%+v: %d %s", c, w.Code, w.Body)
		}
	}
	if w := call("GET", "/api/menubar", "test-token-123", local, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"version"`) {
		t.Fatalf("summary: %d %s", w.Code, w.Body)
	}
	// the token opens nothing else
	if w := call("GET", "/api/settings", "test-token-123", local, ""); w.Code != 401 {
		t.Fatalf("token reached the rest of the API: %d", w.Code)
	}
	if w := call("POST", "/api/menubar/action", "test-token-123", local, `{"do":"status","level":7,"count":3,"height":300}`); w.Code != 200 || level != 2 || count != 3 || height != 300 || icon != "outline" {
		t.Fatalf("status: %d %s level=%d count=%d icon=%q", w.Code, w.Body, level, count, icon)
	}
	if w := call("POST", "/api/menubar/action", "test-token-123", local, `{"do":"open","view":"../etc"}`); w.Code != 400 {
		t.Fatalf("open with a bad page: %d", w.Code)
	}
	if w := call("POST", "/api/menubar/action", "test-token-123", local, `{"do":"icon","icon":"nope"}`); w.Code != 400 {
		t.Fatalf("bad icon: %d", w.Code)
	}
	if w := call("POST", "/api/menubar/action", "test-token-123", local, `{"do":"icon","icon":"count"}`); w.Code != 200 || icon != "count" || a.Cfg.Get().Menubar.Icon != "count" {
		t.Fatalf("icon: %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/api/menubar/action", "test-token-123", local, `{"do":"hide"}`); w.Code != 200 || visible || !a.Cfg.Get().Menubar.Hide {
		t.Fatalf("hide: %d visible=%v", w.Code, visible)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8555/api/menubar/action", strings.NewReader(`{"do":"status"}`))
	r.RemoteAddr, r.Header = local, http.Header{"X-Homeowl-Menubar": {"test-token-123"}, "Origin": {"http://evil.example"}}
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin: %d", w.Code)
	}
}
