package server

import (
	"strings"
	"testing"
)

func TestPrivateRelay(t *testing.T) {
	a := checkinApp(t)
	// def456 already had mask.icloud.com on its denylist (switched off) before HomeOwl
	f := &fakeDeny{lists: map[string]map[string]bool{"abc123": {"tiktok.com": true}, "def456": {"mask.icloud.com": false}}}
	old := appDenyFor
	appDenyFor = func(*App) ndDeny { return f }
	defer func() { appDenyFor = old }()

	if _, err := a.setPrivateRelay(map[string]any{"profile": "../x", "block": true}, "admin"); err == nil {
		t.Fatal("bad profile accepted")
	}
	if _, err := a.setPrivateRelay(map[string]any{"profile": "abc123", "block": true}, "admin"); err != nil {
		t.Fatal(err)
	}
	if !f.lists["abc123"]["mask.icloud.com"] || !f.lists["abc123"]["mask-h2.icloud.com"] || !f.lists["abc123"]["tiktok.com"] {
		t.Fatalf("both names blocked, the rest kept: %v", f.lists)
	}
	if st, _ := relayState(f, "abc123"); st != "on" {
		t.Fatalf("state: %s", st)
	}
	f.calls = nil
	if _, err := a.setPrivateRelay(map[string]any{"profile": "abc123", "block": true}, "admin"); err != nil || len(f.calls) != 0 {
		t.Fatalf("turning it off twice changes nothing: %v %v", f.calls, err)
	}
	if _, err := a.setPrivateRelay(map[string]any{"profile": "abc123", "block": false}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, there := f.lists["abc123"]["mask.icloud.com"]; there || !f.lists["abc123"]["tiktok.com"] {
		t.Fatalf("HomeOwl's entries removed, the rest kept: %v", f.lists)
	}

	// someone's own entry: switched on and off, never removed
	if _, err := a.setPrivateRelay(map[string]any{"profile": "def456", "block": true}, "admin"); err != nil {
		t.Fatal(err)
	}
	if !f.lists["def456"]["mask.icloud.com"] || !f.lists["def456"]["mask-h2.icloud.com"] {
		t.Fatalf("both on: %v", f.lists)
	}
	if _, err := a.setPrivateRelay(map[string]any{"profile": "def456", "block": false}, "admin"); err != nil {
		t.Fatal(err)
	}
	on, there := f.lists["def456"]["mask.icloud.com"]
	if !there || on {
		t.Fatalf("the owner's own entry stays, switched off: %v", f.lists)
	}
	if _, there := f.lists["def456"]["mask-h2.icloud.com"]; there {
		t.Fatalf("HomeOwl's entry removed: %v", f.lists)
	}
	n := 0
	for _, c := range a.Changes.List() {
		if strings.Contains(c.Description, "Private Relay") {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("each switch is logged once: %d", n)
	}
}
