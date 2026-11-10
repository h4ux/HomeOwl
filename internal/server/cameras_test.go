package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/auth"
	"github.com/h4ux/homeowl/internal/config"
)

func TestCamerasAccess(t *testing.T) {
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case "/proxy/protect/integration/v1/cameras":
			_, _ = w.Write([]byte(`[{"id":"cam1","name":"Front door","state":"CONNECTED","featureFlags":{"isDoorbell":true}}]`))
		case "/proxy/protect/integration/v1/cameras/cam1/snapshot":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
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
	a.Protect.Store.Apply([]byte(`{"type":"add","item":{"id":"e1","modelKey":"event","type":"ring","device":"cam1"}}`))
	for _, u := range []auth.UserInput{{Username: "alex", Role: auth.RoleAdmin, Password: "a-long-password"}, {Username: "sam", Role: auth.RoleViewer, Password: "a-long-password"}} {
		if err := a.Users.Save(u, true); err != nil {
			t.Fatal(err)
		}
	}
	get := func(user, path string) *httptest.ResponseRecorder {
		tok, _ := a.Users.SessionFor(user, "browser")
		r := httptest.NewRequest("GET", "http://127.0.0.1:8555"+path, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		return w
	}
	w := get("alex", "/api/cameras?days=7")
	var v struct {
		Cameras []map[string]any `json:"cameras"`
		Events  []map[string]any `json:"events"`
		Days    int              `json:"days"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || len(v.Cameras) != 1 || v.Cameras[0]["doorbell"] != true || len(v.Events) != 1 || v.Days != 7 {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	if w := get("alex", "/api/cameras/snapshot?id=cam1"); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.Len() != 4 {
		t.Fatalf("snapshot: %d %s", w.Code, w.Body)
	}
	if w := get("alex", "/api/cameras/snapshot?id=..%2Fx"); w.Code == 200 {
		t.Fatalf("bad id accepted")
	}
	if w := get("sam", "/api/cameras"); w.Code != 403 {
		t.Fatalf("viewer without permission: %d", w.Code)
	}
	if w := get("sam", "/api/cameras/snapshot?id=cam1"); w.Code != 403 {
		t.Fatalf("viewer snapshot without permission: %d", w.Code)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Cameras.Viewers = true })
	if w := get("sam", "/api/cameras"); w.Code != 200 || !strings.Contains(w.Body.String(), "Front door") {
		t.Fatalf("viewer with permission: %d %s", w.Code, w.Body)
	}
}

func TestCamerasWithoutProtect(t *testing.T) {
	a := checkinApp(t)
	v := a.camerasView(0)
	if v["no_protect"] != true || v["days"] != 1 {
		t.Fatalf("%v", v)
	}
}
