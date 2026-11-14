package server

import (
	"strings"
	"testing"
	"time"
)

func TestArchiveSuggestions(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	day := float64(86400)
	ts := func(daysAgo float64) float64 { return float64(now.Unix()) - daysAgo*day }
	known := []map[string]any{
		{"mac": "02:00:00:00:00:01", "name": "Alex's iPhone", "oui": "", "last_seen": ts(10)},          // old copy, Alex's
		{"mac": "06:00:00:00:00:02", "name": "Alex's iPhone", "oui": "", "last_seen": ts(0)},           // now online
		{"mac": "0a:00:00:00:00:03", "hostname": "iPhone", "last_seen": ts(20)},                        // generic name: no duplicate
		{"mac": "0e:00:00:00:00:04", "hostname": "iPhone", "last_seen": ts(1)},                         //
		{"mac": "00:11:22:00:00:05", "hostname": "nas-box", "oui": "Synology", "last_seen": ts(30)},    // same name, other maker
		{"mac": "00:11:22:00:00:06", "hostname": "nas-box", "oui": "Raspberry", "last_seen": ts(1)},    //
		{"mac": "12:00:00:00:00:07", "hostname": "Sam-laptop", "last_seen": ts(200)},                   // stale private
		{"mac": "16:00:00:00:00:08", "hostname": "kitchen", "last_seen": ts(200), "use_fixedip": true}, // fixed IP: kept
		{"mac": "00:11:22:00:00:09", "hostname": "printer", "last_seen": ts(400)},                      // not private, but half a year: stale
		{"mac": "00:11:22:00:00:0c", "name": "Old phone", "last_seen": ts(120)},                        // not private, 4 months: kept
		{"mac": "1a:00:00:00:00:0a", "name": "TV", "last_seen": ts(5)},                                 // archived already
		{"mac": "1e:00:00:00:00:0b", "name": "TV", "last_seen": ts(0)},
	}
	online := []map[string]any{{"mac": "06:00:00:00:00:02", "name": "Alex's iPhone"}, {"mac": "1e:00:00:00:00:0b", "name": "TV"}}
	owner := map[string][2]string{"02:00:00:00:00:01": {"p1", "Alex"}, "12:00:00:00:00:07": {"p2", "Sam"}}
	got := a.archiveSuggestions(known, online, map[string]archivedDevice{"1a:00:00:00:00:0a": {}}, owner, now)
	by := map[string]archiveCand{}
	for _, c := range got {
		by[c.MAC] = c
	}
	if len(got) != 3 {
		t.Fatalf("three suggestions: %+v", got)
	}
	d := by["02:00:00:00:00:01"]
	if d.Kind != "duplicate" || d.KeptMAC != "06:00:00:00:00:02" || !d.KeptOnline || d.PersonID != "p1" {
		t.Fatalf("old copy: %+v", d)
	}
	if c := by["12:00:00:00:00:07"]; c.Kind != "stale" || !strings.Contains(c.Reason, "linked to Sam") {
		t.Fatalf("stale private address (Sam's): %+v", got)
	}
	if by["00:11:22:00:00:09"].Kind != "stale" {
		t.Fatalf("printer unseen for 400 days: %+v", got)
	}

	// archive, then restore
	if _, err := a.archiveAPI("add", map[string]any{"macs": []any{"02:00:00:00:00:01", "bad"}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if m := a.archive.MACs(); len(m) != 1 || m[0] != "02:00:00:00:00:01" {
		t.Fatalf("archived: %v", m)
	}
	if _, err := a.archiveAPI("restore", map[string]any{"macs": []any{"02:00:00:00:00:01"}}, "admin"); err != nil || len(a.archive.MACs()) != 0 {
		t.Fatalf("restored: %v %v", err, a.archive.MACs())
	}
	if _, err := a.archiveAPI("add", map[string]any{"macs": []any{}}, "admin"); err == nil {
		t.Fatal("nothing to archive")
	}
}
