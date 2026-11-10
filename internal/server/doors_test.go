package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/config"
)

func TestDoors(t *testing.T) {
	unlocked := ""
	acc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/developer/doors":
			_, _ = io.WriteString(w, `{"code":"SUCCESS","data":[{"id":"d1","name":"Front door","door_lock_relay_status":"lock","door_position_status":"close","is_bind_hub":true}]}`)
		case r.URL.Path == "/api/v1/developer/system/logs":
			_, _ = io.WriteString(w, `{"code":"SUCCESS","data":{"hits":[]}}`)
		case r.Method == "PUT" && r.URL.Path == "/api/v1/developer/doors/d1/unlock":
			unlocked = "d1"
			_, _ = io.WriteString(w, `{"code":"SUCCESS","data":null}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer acc.Close()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := a.doorsConnect(ctx, map[string]any{"token": "wrong", "host": acc.URL}); err == nil {
		t.Fatal("bad token accepted")
	}
	v, err := a.doorsConnect(ctx, map[string]any{"token": "secret-token", "host": acc.URL})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "secret-token") || !strings.Contains(string(raw), "Front door") {
		t.Fatalf("view: %s", raw)
	}
	if _, err := a.doorUnlock(ctx, map[string]any{"id": "d1", "dry_run": false}, "alex"); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("unlock while not allowed: %v", err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Access.Unlock = true })
	if p, err := a.doorUnlock(ctx, map[string]any{"id": "d1"}, "alex"); err != nil || p.(map[string]any)["preview"] == nil || unlocked != "" {
		t.Fatalf("preview: %v %v", p, err)
	}
	if _, err := a.doorUnlock(ctx, map[string]any{"id": "d1", "dry_run": false}, "alex"); err == nil || unlocked != "" {
		t.Fatalf("unlock while changes are locked: %v", err)
	}
	if _, err := a.doorUnlock(ctx, map[string]any{"id": "nope", "dry_run": false}, "alex"); err == nil {
		t.Fatal("unknown door")
	}
	a.Changes.Write = true
	if _, err := a.doorUnlock(ctx, map[string]any{"id": "d1", "dry_run": false}, "alex"); err != nil || unlocked != "d1" {
		t.Fatalf("unlock: %v", err)
	}
	l := a.Changes.List()
	if len(l) != 1 || l[0].By != "alex" || !l[0].OK || l[0].Undoable || !strings.Contains(l[0].Description, "Front door") {
		t.Fatalf("change log: %+v", l)
	}
	// a fresh start picks the saved token up again; disconnect forgets it
	b, _ := New(a.DataDir, false)
	b.configureAccess()
	if !b.Access.Configured() {
		t.Fatal("saved token not used after a restart")
	}
	b.doorsDisconnect()
	if b.Access.Configured() || b.Sec.Get(accessTokenSlot) != "" || b.Cfg.Get().Access.Unlock {
		t.Fatal("disconnect")
	}
}
