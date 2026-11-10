package access

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fakeAccess(t *testing.T, unlocked *string) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"code":"CODE_UNAUTHORIZED","msg":"unauthorized"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/developer/doors":
			_, _ = io.WriteString(w, `{"code":"SUCCESS","data":[
			  {"id":"d2","name":"Garage","door_lock_relay_status":"unlock","door_position_status":null,"is_bind_hub":true},
			  {"id":"d1","name":"Front door","door_lock_relay_status":"lock","door_position_status":"close","is_bind_hub":true}]}`)
		case r.Method == "POST" && r.URL.Path == "/api/v1/developer/system/logs":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["topic"] != "door_openings" {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `{"code":"SUCCESS","data":{"hits":[
			  {"@timestamp":"2026-01-01T08:00:00Z","_source":{"actor":{"display_name":"Alex","type":"user"},"authentication":{"credential_provider":"NFC"},
			    "event":{"result":"ACCESS","display_message":"Access Granted (NFC)"},"target":[{"type":"device","display_name":"Reader"},{"type":"door","display_name":"Front door","id":"d1"}]}},
			  {"_source":{"actor":{"display_name":"N/A"},"event":{"result":"BLOCKED","published":1767268800000},"target":[{"type":"door","display_name":"Garage","id":"d2"}]}}]}}`)
		case r.Method == "PUT" && r.URL.Path == "/api/v1/developer/doors/d1/unlock":
			*unlocked = "d1"
			_, _ = io.WriteString(w, `{"code":"SUCCESS","data":null}`)
		default:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"code":"CODE_NOT_FOUND","msg":"not found"}`)
		}
	}))
}

func TestAccess(t *testing.T) {
	var unlocked string
	srv := fakeAccess(t, &unlocked)
	defer srv.Close()
	ctx := context.Background()
	c := &Client{}
	if _, err := c.Doors(ctx); err == nil {
		t.Fatal("not configured: want an error")
	}
	c.Configure(srv.URL, "wrong", false)
	if _, err := c.Doors(ctx); err == nil {
		t.Fatal("bad token: want an error")
	}
	c.Configure(srv.URL, "tok", false)
	doors, err := c.Doors(ctx)
	if err != nil || len(doors) != 2 || doors[0].Name != "Front door" || doors[0].Lock != "locked" || doors[0].Position != "closed" ||
		doors[1].Lock != "unlocked" || doors[1].Position != "unknown" {
		t.Fatalf("%+v %v", doors, err)
	}
	log, err := c.Log(ctx, time.Now().Add(-time.Hour))
	if err != nil || len(log) != 2 || log[0].Door != "Garage" || log[0].Who != "" || log[0].Result != "BLOCKED" ||
		log[1].Who != "Alex" || log[1].Method != "NFC" || log[1].Door != "Front door" || log[1].Time != 1767254400000 {
		t.Fatalf("%+v %v", log, err)
	}
	if st, _ := c.Unlock(ctx, "../x", "alex"); st != 400 {
		t.Fatal("bad id accepted")
	}
	if st, d := c.Unlock(ctx, "d1", "alex"); st != 200 || unlocked != "d1" {
		t.Fatalf("unlock %d %v", st, d)
	}
	if b, err := BaseFor("https://192.168.1.1", 0); err != nil || b != "https://192.168.1.1:12445" {
		t.Fatal(b, err)
	}
}
