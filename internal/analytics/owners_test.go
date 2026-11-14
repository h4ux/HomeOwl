package analytics

import (
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
		cl("aa:bb:cc:00:00:10", "Alexs-MacBook", "", "", 10*time.Hour, 0),                    // same as the NextDNS device
		cl("12:bb:cc:00:00:11", "", "Sams-iPhone", "", 100*time.Hour, 0),                     // Sam's phone, new private address
		cl("aa:bb:cc:00:00:12", "", "alex-mbp", "", 10*time.Hour, 0),                         // name
		cl("aa:bb:cc:00:00:13", "Living room", "", "Roku", 10*time.Hour, 0),                 // shared
		cl("aa:bb:cc:00:00:14", "alex-and-sam-ipad", "", "", 10*time.Hour, 0),                // two names: no guess
		cl("aa:bb:cc:00:00:15", "Kitchen speaker", "", "", 10*time.Hour, 0),                  // admin set it as a speaker
		cl("aa:bb:cc:00:00:16", "Galaxy-Tab", "", "", 10*time.Hour, 0),                       // nothing to go on
		cl("aa:bb:cc:00:00:17", "Alexander's iPad", "", "", 10*time.Hour, 0),                 // not "Alex"
		cl("aa:bb:cc:00:00:18", "Hall Echo", "", "", 10*time.Hour, 0),                        // smart home, but no such group
	}
	got := map[string]OwnerHint{}
	for _, h := range ownerHints(people, cls, map[string]string{"aa:bb:cc:00:00:15": "speaker"}, func(mac, pid string) bool { return mac == "aa:bb:cc:00:00:18" }) {
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
		if !ok || h.PersonID != w[0] || h.Signal != w[1] || h.Strength != w[2] {
			t.Errorf("%s: %+v, want %v", mac, h, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected guesses: %+v", got)
	}
	// a device that was online at the same time as Sam's isn't "his phone with a new address"
	cls[1].Last = now
	if h := ownerHints(people, cls, nil, nil); func() bool {
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
	for _, h := range ownerHints(people, cls, nil, func(mac, pid string) bool { return pid == "p1" }) {
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
	h, err := e.OwnerSuggestions(nil, nil)
	if err != nil || len(h) != 1 || h[0].PersonID != pid || h[0].MAC != "aa:bb:cc:00:00:20" {
		t.Fatalf("%v %+v", err, h)
	}
}
