package analytics

import (
	"strings"
	"testing"
	"time"
)

func TestOwnerHints(t *testing.T) {
	now := time.Now()
	people := []Person{
		{ID: "p1", Name: "Alex", Devices: []PersonDevice{{Kind: "ndev", Key: "abc123/Alex's MacBook"}, {Kind: "mac", Key: "aa:bb:cc:00:00:01"}}},
		{ID: "p2", Name: "Sam Doe", Devices: []PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:02"}}},
		{ID: "p3", Name: "Al"}, // too short to look for
		{ID: "g1", Name: "TV & streaming", Kind: "group"},
	}
	cl := func(mac, name, host, vendor string, first, last time.Duration) OwnerClient {
		return OwnerClient{MAC: mac, Name: name, Hostname: host, Vendor: vendor, First: now.Add(-first), Last: now.Add(-last)}
	}
	cls := []OwnerClient{
		cl("aa:bb:cc:00:00:01", "Alex phone", "", "Apple", 900*time.Hour, 0),
		cl("aa:bb:cc:00:00:02", "", "Sams-iPhone", "Apple", 900*time.Hour, 200*time.Hour), // Sam's old address
		cl("aa:bb:cc:00:00:10", "Alexs-MacBook", "", "", 10*time.Hour, 0),                 // same as the NextDNS device
		cl("12:bb:cc:00:00:11", "", "Sams-iPhone", "", 100*time.Hour, 0),                  // Sam's phone, new private address
		cl("aa:bb:cc:00:00:12", "", "alex-mbp", "", 10*time.Hour, 0),                      // name
		cl("aa:bb:cc:00:00:13", "Living room", "", "Roku", 10*time.Hour, 0),               // shared
		cl("aa:bb:cc:00:00:14", "alex-and-sam-ipad", "", "", 10*time.Hour, 0),             // two names: no guess
		cl("aa:bb:cc:00:00:15", "Kitchen speaker", "", "", 10*time.Hour, 0),               // admin set it as a speaker
		cl("aa:bb:cc:00:00:16", "Galaxy-Tab", "", "", 10*time.Hour, 0),                    // nothing to go on
		cl("aa:bb:cc:00:00:17", "Alexander's iPad", "", "", 10*time.Hour, 0),              // not "Alex"
		cl("aa:bb:cc:00:00:18", "Hall Echo", "", "", 10*time.Hour, 0),                     // smart home, but no such group
	}
	got := map[string]OwnerHint{}
	for _, h := range ownerHints(people, cls, map[string]string{"aa:bb:cc:00:00:15": "speaker"}, func(mac, pid string) bool { return mac == "aa:bb:cc:00:00:18" }, nil) {
		got[h.MAC] = h
	}
	want := map[string][3]string{
		"aa:bb:cc:00:00:10": {"p1", "same-device", "strong"},
		"12:bb:cc:00:00:11": {"p2", "new-address", "strong"},
		"aa:bb:cc:00:00:12": {"p1", "name", "strong"},
		"aa:bb:cc:00:00:13": {"g1", "shared", "likely"},
		"aa:bb:cc:00:00:15": {"g1", "shared", "likely"},
	}
	for mac, w := range want {
		h, ok := got[mac]
		if !ok || h.PersonID != w[0] || h.Signal != w[1] || h.Strength != w[2] || h.Percent < 50 || h.Percent > 98 {
			t.Errorf("%s: %+v, want %v", mac, h, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected guesses: %+v", got)
	}
	// a device that was online at the same time as Sam's isn't "his phone with a new address"
	cls[1].Last = now
	if h := ownerHints(people, cls, nil, nil, nil); func() bool {
		for _, x := range h {
			if x.MAC == "12:bb:cc:00:00:11" && x.Signal == "new-address" {
				return true
			}
		}
		return false
	}() {
		t.Error("two devices online together are two devices")
	}
	// turned down: no more guess for that person
	for _, h := range ownerHints(people, cls, nil, func(mac, pid string) bool { return pid == "p1" }, nil) {
		if h.PersonID == "p1" {
			t.Errorf("dismissed guess came back: %+v", h)
		}
	}
}

func TestOwnerSuggestionsDB(t *testing.T) {
	e := openTestEngine(t)
	pid, err := e.SavePerson(Person{Name: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO clients(mac, name, hostname, last_seen) VALUES ('aa:bb:cc:00:00:20', 'Alexs-iPad', '', ?), ('aa:bb:cc:00:00:21', 'old', 'alex-pc', ?)`,
		utcnow(), utcnow().Add(-200*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO sessions(id, mac, start_ts, stop_ts, duration, wired) VALUES ('s1', 'aa:bb:cc:00:00:20', ?, NULL, 600, false)`, utcnow().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if tg := e.together(e.People(), []OwnerClient{{MAC: "aa:bb:cc:00:00:20"}}); tg == nil {
		t.Fatal("the sessions query failed")
	}
	h, err := e.OwnerSuggestions(nil, nil)
	if err != nil || len(h) != 1 || h[0].PersonID != pid || h[0].MAC != "aa:bb:cc:00:00:20" {
		t.Fatalf("%v %+v", err, h)
	}
}

func TestTogether(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	from := now.Add(-30 * 24 * time.Hour)
	people := []Person{
		{ID: "p1", Name: "Alex", Devices: []PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:01"}}},
		{ID: "p2", Name: "Sam", Devices: []PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:02"}}},
		{ID: "g1", Name: "TV", Kind: "group", Devices: []PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:03"}}},
	}
	sess := map[string][]span{}
	add := func(mac string, a, b time.Time) { sess[mac] = append(sess[mac], span{a, b}) }
	for d := 1; d <= 12; d++ { // each day: Alex out 08:00–17:00, Sam out 09:00–15:00
		day := from.Add(time.Duration(d) * 24 * time.Hour)
		h := func(x float64) time.Time { return day.Add(time.Duration(x * float64(time.Hour))) }
		add("aa:bb:cc:00:00:01", h(-7), h(8))
		add("aa:bb:cc:00:00:01", h(17), h(17.2)) // a phone asleep drops off and comes back: one stay
		add("aa:bb:cc:00:00:01", h(17.4), h(23))
		add("aa:bb:cc:00:00:02", h(-7), h(9))
		add("aa:bb:cc:00:00:02", h(15), h(23))
		add("aa:bb:cc:00:00:10", h(-7), h(8.05)) // Alex's watch: leaves and comes back with Alex
		add("aa:bb:cc:00:00:10", h(16.95), h(23))
		if d%2 == 0 { // a tablet that goes out with Sam only some days
			add("aa:bb:cc:00:00:11", h(-7), h(9))
			add("aa:bb:cc:00:00:11", h(15), h(23))
		} else {
			add("aa:bb:cc:00:00:11", h(-7), h(23))
		}
		add("aa:bb:cc:00:00:12", h(-7), h(23)) // a laptop that stays home: no comings and goings to match
	}
	// the same days, a device that leaves with both of them (they go out together): can't tell
	for d := 13; d <= 22; d++ {
		day := from.Add(time.Duration(d) * 24 * time.Hour)
		h := func(x float64) time.Time { return day.Add(time.Duration(x * float64(time.Hour))) }
		for _, m := range []string{"aa:bb:cc:00:00:01", "aa:bb:cc:00:00:02", "aa:bb:cc:00:00:13"} {
			add(m, h(-7), h(10))
			add(m, h(18), h(23))
		}
	}
	got := togetherHints(people, []string{"aa:bb:cc:00:00:10", "aa:bb:cc:00:00:11", "aa:bb:cc:00:00:12", "aa:bb:cc:00:00:13"}, sess,
		map[string]string{"aa:bb:cc:00:00:01": "Alex's iPhone"}, from, now)
	w, ok := got["aa:bb:cc:00:00:10"]
	if !ok || w.PersonID != "p1" || w.Percent < 90 || w.Anchor != "Alex's iPhone" || w.Events < 20 {
		t.Fatalf("watch: %+v (all %+v)", w, got)
	}
	if tb, ok := got["aa:bb:cc:00:00:11"]; !ok || tb.PersonID != "p2" {
		t.Fatalf("tablet that only goes out with Sam: %+v", got)
	}
	if _, ok := got["aa:bb:cc:00:00:12"]; ok {
		t.Fatal("a device that never leaves can't be matched")
	}
	if _, ok := got["aa:bb:cc:00:00:13"]; ok {
		t.Fatal("a device that leaves with two people at once can't be told")
	}
	// in the suggestions it carries the measured percent
	h := ownerHints(people, []OwnerClient{{MAC: "aa:bb:cc:00:00:10", Name: "Watch", Last: now}}, nil, nil, got)
	if len(h) != 1 || h[0].Signal != "together" || h[0].Percent != w.Percent || !strings.Contains(h[0].Reason, "Alex's iPhone") {
		t.Fatalf("%+v", h)
	}
}
