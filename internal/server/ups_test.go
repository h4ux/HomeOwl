package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// a minimal upsd: one UPS "rack", user alex / pw
func fakeNUT(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					l = strings.TrimSpace(l)
					out := "ERR UNKNOWN-COMMAND\n"
					switch {
					case l == `USERNAME "alex"`:
						out = "OK\n"
					case strings.HasPrefix(l, "PASSWORD"):
						out = "ERR ACCESS-DENIED\n"
						if l == `PASSWORD "pw"` {
							out = "OK\n"
						}
					case l == "LIST UPS":
						out = "BEGIN LIST UPS\nUPS rack \"Rack UPS\"\nEND LIST UPS\n"
					case l == `LIST VAR "rack"`:
						out = "BEGIN LIST VAR rack\nVAR rack ups.status \"OL\"\nVAR rack battery.charge \"100\"\nEND LIST VAR rack\n"
					case l == "LOGOUT":
						return
					}
					_, _ = c.Write([]byte(out))
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestUPS(t *testing.T) {
	port := fakeNUT(t)
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if v := a.upsView(false, 1); v["configured"] != false || v["settings"] != nil {
		t.Fatalf("viewer, not configured: %v", v)
	}
	if _, err := a.upsConnect(ctx, map[string]any{"host": "127.0.0.1", "port": float64(port), "user": "alex", "password": "bad"}); err == nil {
		t.Fatal("bad password accepted")
	}
	if _, err := a.upsConnect(ctx, map[string]any{"host": "http://x", "port": float64(port)}); err == nil {
		t.Fatal("URL accepted as host")
	}
	if _, err := a.upsConnect(ctx, map[string]any{"host": "127.0.0.1", "port": float64(port), "user": "alex", "password": "pw"}); err != nil {
		t.Fatal(err)
	}
	c := a.Cfg.Get().UPS
	if !c.Enabled || !c.LowBattery || a.Sec.Get(nutPassSlot) != "pw" {
		t.Fatalf("saved: %+v", c)
	}
	// reconnecting to the same server without a password keeps the saved one
	if _, err := a.upsConnect(ctx, map[string]any{"host": "127.0.0.1", "port": float64(port), "user": "alex"}); err != nil {
		t.Fatalf("keep password: %v", err)
	}
	a.UPS.Tick(ctx, upsSettings(a.Cfg.Get().UPS, a.Sec.Get(nutPassSlot)))
	v := a.upsView(true, 1)
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), `"pw"`) || !strings.Contains(string(raw), `"rack"`) || !strings.Contains(string(raw), `"has_password":true`) {
		t.Fatalf("admin view: %s", raw)
	}
	if vv, _ := json.Marshal(a.upsView(false, 1)); strings.Contains(string(vv), "settings") || strings.Contains(string(vv), "folder") {
		t.Fatalf("viewer sees settings: %s", vv)
	}

	for _, bad := range []map[string]any{
		{"actions": []any{map[string]any{"event": "nope", "script": "a.sh"}}},
		{"actions": []any{map[string]any{"event": "onbatt", "script": "../a.sh"}}},
		{"actions": []any{map[string]any{"event": "onbatt", "script": "a.sh", "delay": float64(99999)}}},
		{"below_charge": float64(100)},
	} {
		if err := a.upsSave(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if err := a.upsSave(map[string]any{"low_battery": true, "below_charge": float64(30), "poll": float64(10),
		"actions": []any{map[string]any{"event": "shutdown", "script": "off.sh"}, map[string]any{"event": "onbatt", "script": "note.sh", "delay": float64(60)}}}); err != nil {
		t.Fatal(err)
	}
	if c := a.Cfg.Get().UPS; c.BelowCharge != 30 || c.Poll != 10 || len(c.Actions) != 2 || c.Actions[1].Delay != 60 {
		t.Fatalf("settings: %+v", c)
	}

	if err := a.UPS.Scripts.SaveSuggestion(runtime.GOOS, "../../evil", false); err == nil {
		t.Fatal("wrote a file that isn't a suggestion")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// a script test needs the change lock open and is logged
	_ = a.UPS.Scripts.EnsureDir()
	if err := os.WriteFile(filepath.Join(a.UPS.Scripts.Dir, "note.sh"), []byte("#!/bin/sh\necho \"$NOTIFYTYPE test=$HOMEOWL_UPS_TEST\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.upsTestScript(ctx, map[string]any{"script": "note.sh", "event": "onbatt"}, "alex"); err == nil {
		t.Fatal("ran while changes are locked")
	}
	a.Changes.Write = true
	if _, err := a.upsTestScript(ctx, map[string]any{"script": "missing.sh"}, "alex"); err == nil {
		t.Fatal("ran a missing script")
	}
	r, err := a.upsTestScript(ctx, map[string]any{"script": "note.sh", "event": "onbatt"}, "alex")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(r); !strings.Contains(string(b), "ONBATT test=1") {
		t.Fatalf("test run: %s", b)
	}
	if l := a.Changes.List(); len(l) != 1 || l[0].By != "alex" || !l[0].OK || l[0].Undoable {
		t.Fatalf("change log: %+v", l)
	}
	a.upsDisconnect()
	if c := a.Cfg.Get().UPS; c.Enabled || a.Sec.Get(nutPassSlot) != "" || len(c.Actions) != 2 {
		t.Fatalf("disconnect: %+v", c)
	}
}
