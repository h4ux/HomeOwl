package presence

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPresence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presence.json")
	tr := Open(path)
	alex := Person{ID: "p1", Name: "Alex", Devices: []string{"aa:bb:cc:00:00:01", "aa:bb:cc:00:00:02"}}
	o := Options{Grace: 10 * time.Minute, History: true, Days: 30}
	t0 := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	phone := map[string]string{"aa:bb:cc:00:00:01": "Alex's iPhone"}

	if ev := tr.Observe(t0, phone, []Person{alex}, o); len(ev) != 0 || !tr.Now()["p1"].Home {
		t.Fatalf("first look: %v %+v", ev, tr.Now())
	}
	// the phone sleeps for 5 minutes: still home
	if ev := tr.Observe(t0.Add(5*time.Minute), nil, []Person{alex}, o); len(ev) != 0 || !tr.Now()["p1"].Home {
		t.Fatalf("short sleep counted as leaving: %v", ev)
	}
	// gone for longer than the grace period: left when last seen
	ev := tr.Observe(t0.Add(12*time.Minute), nil, []Person{alex}, o)
	if len(ev) != 1 || ev[0].Kind != "leave" || ev[0].T != t0.UnixMilli() || ev[0].DeviceName != "Alex's iPhone" {
		t.Fatalf("leave: %+v", ev)
	}
	// back with the other device
	ev = tr.Observe(t0.Add(3*time.Hour), map[string]string{"aa:bb:cc:00:00:02": "Alex's watch"}, []Person{alex}, o)
	if len(ev) != 1 || ev[0].Kind != "arrive" || ev[0].DeviceName != "Alex's watch" {
		t.Fatalf("arrive: %+v", ev)
	}
	if err := tr.Save(); err != nil {
		t.Fatal(err)
	}
	again := Open(path)
	if h := again.Since(0); len(h) != 2 || !again.Now()["p1"].Home {
		t.Fatalf("reload: %+v %+v", h, again.Now())
	}
	// history off: arrivals still change the state but aren't kept, and the old history goes
	o.History = false
	again.Observe(t0.Add(4*time.Hour), nil, []Person{alex}, o)
	if len(again.Since(0)) != 0 {
		t.Fatal("history kept while off")
	}
	// someone without tagged devices any more disappears
	again.Observe(t0.Add(5*time.Hour), nil, nil, o)
	if _, ok := again.Now()["p1"]; ok {
		t.Fatal("untagged person still tracked")
	}
	// old history is trimmed
	o.History, o.Days = true, 7
	old := Open(filepath.Join(t.TempDir(), "p.json"))
	old.Events = []Event{{T: t0.AddDate(0, 0, -10).UnixMilli(), Kind: "arrive"}, {T: t0.UnixMilli(), Kind: "leave"}}
	old.Observe(t0, nil, nil, o)
	if h := old.Since(0); len(h) != 1 {
		t.Fatalf("trim: %+v", h)
	}
}

// Rebuild: connections become arrivals and departures; short gaps don't count as leaving; only the window changes.
func TestRebuild(t *testing.T) {
	tr := Open(filepath.Join(t.TempDir(), "p.json"))
	min := int64(60_000)
	tr.Events = []Event{{T: 1 * min, Person: "p1", Kind: "arrive"}, {T: 50 * min, Person: "p1", Kind: "leave"}, {T: 60 * min, Person: "p2", Kind: "arrive"}}
	spans := []Span{
		{Device: "aa", Start: 100 * min, Stop: 200 * min},
		{Device: "bb", Start: 205 * min, Stop: 300 * min}, // a 5-minute gap: still home
		{Device: "aa", Start: 400 * min, Stop: 500 * min},
		{Device: "aa", Start: 590 * min, Stop: 700 * min}, // still connected near the end: no leave yet
	}
	n := tr.Rebuild("p1", "Alex", spans, 10*time.Minute, 40*min, 705*min)
	var got []string
	for _, e := range tr.Since(0) {
		got = append(got, fmt.Sprintf("%s:%s:%d", e.Person, e.Kind, e.T/min))
	}
	want := "p1:arrive:1 p2:arrive:60 p1:arrive:100 p1:leave:300 p1:arrive:400 p1:leave:500 p1:arrive:590"
	if n != 5 || strings.Join(got, " ") != want {
		t.Fatalf("%d events: %s", n, strings.Join(got, " "))
	}
}
