package netq

import (
	"context"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

var t0 = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

func at(min int) int64 { return t0.Add(time.Duration(min) * time.Minute).UnixMilli() }

func newW(t *testing.T) *Watcher {
	return &Watcher{Store: Open(filepath.Join(t.TempDir(), "internet.json"))}
}

func ok(min int) Sample   { return Sample{T: at(min), Gw: f(1.5), Net: f(10)} }
func isp(min int) Sample  { return Sample{T: at(min), Gw: f(1.5)} }
func home(min int) Sample { return Sample{T: at(min)} }

func TestOutageOpensAfterTwoMissesAndCloses(t *testing.T) {
	w := newW(t)
	var ev []Event
	for _, s := range []Sample{ok(0), isp(1), ok(2)} { // one miss isn't an outage
		ev = append(ev, w.observe(s)...)
	}
	if len(ev) != 0 || len(w.Store.Outages) != 0 {
		t.Fatalf("a single miss opened an outage: %+v", ev)
	}
	ev = append(ev, w.observe(isp(3))...)
	ev = append(ev, w.observe(isp(4))...)
	if len(ev) != 1 || ev[0].Kind != "down" || ev[0].Outage.Start != at(3) || ev[0].Outage.Cause != "isp" {
		t.Fatalf("want one down event from minute 3, got %+v", ev)
	}
	w.observe(home(5))
	w.observe(isp(6))
	ev = w.observe(ok(7))
	if len(ev) != 1 || ev[0].Kind != "up" {
		t.Fatalf("want up, got %+v", ev)
	}
	o := w.Store.Outages[0]
	if o.End != at(7) || o.IspMin != 3 || o.HomeMin != 1 || o.Cause != "isp" {
		t.Fatalf("outage = %+v", o)
	}
	if st := w.Status(); st.Down != nil {
		t.Fatalf("still down after it came back: %+v", st.Down)
	}
}

func TestOutageCauseHomeWhenGatewaySilent(t *testing.T) {
	w := newW(t)
	w.observe(home(0))
	ev := w.observe(home(1))
	if len(ev) != 1 || ev[0].Outage.Cause != "home" {
		t.Fatalf("want a home outage, got %+v", ev)
	}
	// no gateway known: a miss is the provider's, not the home's
	w2 := newW(t)
	w2.observe(Sample{T: at(0), NoGw: true})
	ev = w2.observe(Sample{T: at(1), NoGw: true})
	if len(ev) != 1 || ev[0].Outage.Cause != "isp" {
		t.Fatalf("no gateway known: want isp, got %+v", ev)
	}
}

func TestGapClosesOutageAtLastReading(t *testing.T) {
	w := newW(t)
	w.observe(isp(0))
	w.observe(isp(1))
	ev := w.observe(isp(60)) // the computer slept for an hour
	if len(ev) != 1 || ev[0].Kind != "up" || ev[0].Outage.End != at(2) {
		t.Fatalf("want the outage closed a minute after the last reading, got %+v", ev)
	}
	if w.Status().Down != nil {
		t.Fatal("a reading after the gap started a new outage straight away")
	}
}

func TestTickSkipsAwayFromHome(t *testing.T) {
	w := newW(t)
	home := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	w.Gateway = func() (string, []netip.Prefix) { return "192.168.1.1", home }
	w.LocalIP = func() netip.Addr { return netip.MustParseAddr("10.20.30.40") }
	probed := false
	w.Probe = func(context.Context, string) (*float64, *float64) { probed = true; return nil, nil }
	w.Tick(context.Background(), t0)
	if probed || len(w.Store.Samples) != 0 || !w.Status().Away {
		t.Fatalf("away from home: probed=%v samples=%d", probed, len(w.Store.Samples))
	}
	w.LocalIP = func() netip.Addr { return netip.MustParseAddr("192.168.1.50") }
	w.Probe = func(_ context.Context, gw string) (*float64, *float64) {
		if gw != "192.168.1.1" {
			t.Errorf("probed gateway %q", gw)
		}
		return f(1), f(9)
	}
	w.Tick(context.Background(), t0)
	if len(w.Store.Samples) != 1 || w.Status().Away {
		t.Fatalf("at home: want one reading, got %d", len(w.Store.Samples))
	}
}

func TestMergeTestsDedupesAndLabels(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "x.json"))
	s.AddTrigger(Trigger{T: at(0), Source: "schedule", By: "HomeOwl (schedule)"})
	in := []SpeedTest{{T: at(1), Down: 900, Up: 800}, {T: at(-600), Down: 500, Up: 400}, {T: at(5), Down: 0, Up: 0}}
	if got := s.MergeTests(in); len(got) != 2 {
		t.Fatalf("added %d, want 2 (the empty one skipped)", len(got))
	}
	if got := s.MergeTests([]SpeedTest{{T: at(1) + 20000, Down: 900, Up: 800}}); len(got) != 0 {
		t.Fatal("the same test was added twice")
	}
	if s.Tests[0].Source != "unifi" || s.Tests[1].Source != "schedule" {
		t.Fatalf("sources = %q, %q", s.Tests[0].Source, s.Tests[1].Source)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if r := Open(s.path); len(r.Tests) != 2 || len(r.Triggered) != 1 {
		t.Fatalf("reloaded %d tests, %d triggers", len(r.Tests), len(r.Triggered))
	}
}

func TestParseUniFi(t *testing.T) {
	got := ParseUniFi([]map[string]any{
		{"time": float64(at(0)), "xput_download": 940.5, "xput_upload": 912.0, "latency": float64(6), "server": map[string]any{"city": "Example City", "provider_name": "Example Net"}, "interface_name": "eth8"},
		{"rundate": float64(at(10) / 1000), "xput_download": "300", "xput_upload": "20", "server": "Sample Town"},
		{"time": float64(at(20))}, // no speeds: skipped
	})
	if len(got) != 2 {
		t.Fatalf("got %d tests", len(got))
	}
	if got[0].Down != 940.5 || got[0].Server != "Example Net, Example City" || got[0].WAN != "eth8" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].T != at(10) || got[1].Down != 300 || got[1].Server != "Sample Town" {
		t.Fatalf("second = %+v", got[1])
	}
}

func day(n int, gw, net float64) []Sample {
	var ss []Sample
	for i := 0; i < n; i++ {
		ss = append(ss, Sample{T: at(i), Gw: f(gw), Net: f(net + float64(i%3))})
	}
	return ss
}

func TestJudge(t *testing.T) {
	now := t0.Add(24 * time.Hour)
	from := t0.UnixMilli()
	healthy := day(1440, 1.2, 9)
	st := Summarize(healthy, nil, from, now)
	if st.Uptime == nil || *st.Uptime != 100 || *st.GwMedian != 1.2 || *st.NetMedian != 10 {
		t.Fatalf("stats = %+v", st)
	}
	v := Judge(st, st, []SpeedTest{{T: at(100), Down: 950, Up: 900}}, Plan{Down: 1000}, nil, false, now)
	if v.Level != "good" || !strings.Contains(strings.Join(v.Lines, " "), "950 Mbps down") {
		t.Fatalf("healthy: %+v", v)
	}

	outs := []Outage{{Start: at(100), End: at(110), Cause: "isp"}, {Start: at(300), End: at(305), Cause: "isp"}, {Start: at(500), End: at(520), Cause: "isp"}}
	wk := Summarize(healthy, outs, from, now)
	if wk.IspCount != 3 || wk.IspMin != 35 || *wk.Uptime > 98 {
		t.Fatalf("week with outages = %+v", wk)
	}
	v = Judge(wk, st, nil, Plan{}, nil, false, now)
	if v.Who != "isp" || v.Level != "warn" {
		t.Fatalf("provider drops: %+v", v)
	}

	slowWifi := Summarize(day(1440, 45, 50), nil, from, now)
	v = Judge(slowWifi, slowWifi, nil, Plan{}, nil, false, now)
	if v.Who != "home" {
		t.Fatalf("slow to the gateway should be home: %+v", v)
	}

	slowTests := []SpeedTest{{T: at(10), Down: 300}, {T: at(20), Down: 350}}
	v = Judge(st, st, slowTests, Plan{Down: 1000}, nil, false, now)
	if v.Who != "isp" || !strings.Contains(v.Lines[0], "under half") {
		t.Fatalf("slow speed tests: %+v", v)
	}

	cur := &Outage{Start: now.Add(-5 * time.Minute).UnixMilli(), Cause: "home"}
	if v = Judge(st, st, nil, Plan{}, cur, false, now); v.Level != "bad" || v.Who != "home" {
		t.Fatalf("down now: %+v", v)
	}
	if v = Judge(Stats{}, Stats{}, nil, Plan{}, nil, true, now); v.Level != "info" || !strings.Contains(v.Title, "away") {
		t.Fatalf("away: %+v", v)
	}
}

func TestBuckets(t *testing.T) {
	ss := []Sample{{T: at(0), Net: f(10), Gw: f(1)}, {T: at(1), Net: f(20), Gw: f(3)}, {T: at(2)}, {T: at(5), Net: f(5)}}
	b := Buckets(ss, 5*60000)
	if len(b) != 2 || *b[0].Net != 15 || *b[0].Gw != 2 || b[0].Missed < 33 || b[0].Missed > 34 || b[1].Gw != nil {
		t.Fatalf("buckets = %+v", b)
	}
}
