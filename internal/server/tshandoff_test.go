package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/auth"
)

func TestTailscaleHandoff(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Users.Save(auth.UserInput{Username: "alex", Role: auth.RoleAdmin, Password: "example-pass-123", Tailscale: "alex@example.com"}, true); err != nil {
		t.Fatal(err)
	}

	// only HomeOwl's own addresses get a code
	for _, back := range []string{"http://192.168.1.77:8555", "https://evil.lan", "http://example.com", "javascript:alert(1)", "http://user@localhost:8555", ""} {
		if _, err := a.tsHandoffURL("alex", back); err == nil {
			t.Fatalf("%q must not get a code", back)
		}
	}
	to, err := a.tsHandoffURL("alex", "http://127.0.0.1:8555")
	if err != nil || !strings.HasPrefix(to, "http://127.0.0.1:8555/api/auth/ts-back?code=") {
		t.Fatalf("handoff: %q %v", to, err)
	}
	code := to[strings.Index(to, "code=")+5:]

	redeem := func(host, code string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://"+host+"/api/auth/ts-back?code="+code, nil)
		w := httptest.NewRecorder()
		a.authAPI(w, r, "ts-back")
		return w
	}
	if w := redeem("localhost:8555", code); w.Code != 403 {
		t.Fatalf("a code works only on the address it was made for: %d", w.Code)
	}
	// the wrong-host try used it up
	to, _ = a.tsHandoffURL("alex", "http://127.0.0.1:8555")
	code = to[strings.Index(to, "code=")+5:]
	w := redeem("127.0.0.1:8555", code)
	if w.Code != 302 || w.Header().Get("Location") != "/" {
		t.Fatalf("redeem: %d %v", w.Code, w.Header())
	}
	var token string
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			token = c.Value
		}
	}
	if u, _, ok := a.Users.Check(token); !ok || u.Username != "alex" {
		t.Fatalf("session: %v %v", u, ok)
	}
	if w := redeem("127.0.0.1:8555", code); w.Code != 403 {
		t.Fatalf("a code works once: %d", w.Code)
	}

	// expired
	to, _ = a.tsHandoffURL("alex", "http://127.0.0.1:8555")
	code = to[strings.Index(to, "code=")+5:]
	a.tsCodes.mu.Lock()
	c := a.tsCodes.codes[code]
	c.exp = time.Now().Add(-time.Second)
	a.tsCodes.codes[code] = c
	a.tsCodes.mu.Unlock()
	if w := redeem("127.0.0.1:8555", code); w.Code != 403 {
		t.Fatalf("an expired code must fail: %d", w.Code)
	}

	// the tailnet side refuses anything that didn't come over the tailnet
	r := httptest.NewRequest("GET", "http://127.0.0.1:8555/api/auth/ts-go?back=http://127.0.0.1:8555", nil)
	rw := httptest.NewRecorder()
	a.authAPI(rw, r, "ts-go")
	if rw.Code != http.StatusForbidden {
		t.Fatalf("ts-go off the tailnet: %d", rw.Code)
	}
}
