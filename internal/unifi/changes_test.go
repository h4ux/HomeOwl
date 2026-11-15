package unifi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	ch := NewChanges(dir)
	ch.recordAt("https://console", "admin", Preview{Description: "Allow the printer"}, 200, true, nil, &req{"DELETE", "/v2/api/site/default/firewall-policies/abc", nil})
	ch.recordAt("https://console", "admin", Preview{Description: "Guest password"}, 200, true, nil, &req{"PUT", "/api/s/default/rest/wlanconf/w1", map[string]any{"x_passphrase": "old-secret"}})
	ch.recordAt("https://console", "admin", Preview{Description: "Refused"}, 400, false, map[string]any{"error": "no"}, nil)

	re := NewChanges(dir) // a restart (an update)
	l := re.List()
	if len(l) != 3 || l[0].ID != 1 || l[2].ID != 3 {
		t.Fatalf("the log is back: %+v", l)
	}
	if !l[0].Undoable || l[1].Undoable || l[2].Undoable {
		t.Fatalf("undo kept, except a password's: %+v", l)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "changes-undo.json"))
	if strings.Contains(string(b), "old-secret") {
		t.Fatal("a password went to disk")
	}
	// numbering goes on
	c := re.recordAt("https://console", "admin", Preview{Description: "Next"}, 200, true, nil, nil)
	if c.ID != 4 {
		t.Fatalf("next id: %d", c.ID)
	}
	// undone stays undone
	re.mu.Lock()
	re.list[0].Undone = true
	re.saveUndoLocked()
	re.mu.Unlock()
	if l := NewChanges(dir).List(); !l[0].Undone {
		t.Fatal("undone is remembered")
	}
}
