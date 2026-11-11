package presence

import (
	"path/filepath"
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
