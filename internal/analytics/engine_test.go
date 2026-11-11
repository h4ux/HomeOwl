package analytics

import (
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// openTestEngine runs the real embedded DuckDB in a temporary folder, so this also checks
// the schema and queries against whatever DuckDB version the driver links in.
func openTestEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := New(cfg, dir, nil, nil)
	if err := e.OpenDB(); err != nil {
		t.Fatalf("open DuckDB: %v", err)
	}
	t.Cleanup(func() { _ = e.db.Close() })
	return e
}

func TestDuckDBVersion(t *testing.T) {
	e := openTestEngine(t)
	var v string
	if err := e.db.QueryRow("SELECT version()").Scan(&v); err != nil {
		t.Fatal(err)
	}
	t.Logf("DuckDB %s", v)
	// SQL must not be able to read files (the SQL console relies on this)
	if _, err := e.RunSQL("SELECT * FROM read_csv('/etc/hosts')", 10); err == nil {
		t.Fatal("file access should be disabled")
	}
}

// Two NextDNS profiles both have a device called "iPhone 16": one profile belongs to Liroy as a whole,
// and in the other profile "iPhone 16" is Noa's. Each query must land on the right person.
func TestPeopleByProfileAndDevice(t *testing.T) {
	e := openTestEngine(t)
	now := utcnow().Truncate(time.Hour).Add(-2 * time.Hour)
	mustExec := func(q string, a ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec("INSERT INTO nextdns_profiles(id, name) VALUES ('aaa111','Liroy'), ('bbb222','Kids')")
	q := `INSERT INTO dns_queries(ts, profile_id, domain, root, status, device_name, local_ip) VALUES (?,?,?,?,?,?,?)`
	for i := 0; i < 3; i++ {
		mustExec(q, now, "aaa111", "www.youtube.com", "youtube.com", "default", "iPhone 16", "192.168.1.20")
	}
	mustExec(q, now, "aaa111", "api.apple.com", "apple.com", "default", "iPhone 16pro", "192.168.1.21")
	for i := 0; i < 2; i++ {
		mustExec(q, now, "bbb222", "roblox.com", "roblox.com", "blocked", "iPhone 16", "192.168.1.30")
	}
	mustExec(q, now, "bbb222", "example.org", "example.org", "default", "Kitchen iPad", "192.168.1.31")
	mustExec(`INSERT INTO dns_hourly_device SELECT date_trunc('hour', ts), profile_id, ` + deviceSQL + `, status, count(*) FROM dns_queries GROUP BY ALL`)
	mustExec(`INSERT INTO clients(mac, name, ip) VALUES ('aa:bb:cc:00:00:01', 'iPhone 16pro', '192.168.1.21')`)
	e.ApplyLocalTime(true)

	liroy, err := e.SavePerson(Person{Name: "Liroy", Devices: []PersonDevice{{Kind: "profile", Key: "aaa111"}}})
	if err != nil {
		t.Fatal(err)
	}
	noa, err := e.SavePerson(Person{Name: "Noa", Devices: []PersonDevice{{Kind: "ndev", Key: "bbb222/iPhone 16"}}})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := e.Rows("SELECT profile_id, device, coalesce(person_id,'') AS person, count(*) AS n FROM dnsq GROUP BY ALL ORDER BY ALL")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"aaa111|iPhone 16": liroy, "aaa111|iPhone 16pro": liroy, "bbb222|iPhone 16": noa, "bbb222|Kitchen iPad": ""}
	for _, r := range rows {
		k := str(r["profile_id"]) + "|" + str(r["device"])
		if str(r["person"]) != want[k] {
			t.Errorf("%s belongs to %q, want %q", k, str(r["person"]), want[k])
		}
	}
	// a whole profile also brings in the UniFi traffic of its devices
	var owner string
	_ = e.db.QueryRow("SELECT person_id FROM dev_person WHERE label = 'mac:aa:bb:cc:00:00:01'").Scan(&owner)
	if owner != liroy {
		t.Errorf("iPhone 16pro's traffic belongs to %q, want Liroy", owner)
	}

	// Explore, filtered to each person (raw and hourly paths)
	for _, rg := range []string{"7d", "90d"} {
		r, err := e.Explore(ExploreRequest{Range: rg, Filters: map[string]any{"person": noa}, Views: []string{"summary", "heatmap", "by_device", "by_category"}})
		if err != nil {
			t.Fatalf("explore %s: %v", rg, err)
		}
		if s, _ := r["summary"].(map[string]any); toInt(s["total"]) != 2 {
			t.Errorf("explore %s: Noa has %v queries, want 2", rg, s["total"])
		}
	}

	m, err := e.Meta()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(m["dns_devices"].([]map[string]any)); n != 4 {
		t.Errorf("editor lists %d devices, want 4 (one per profile and name)", n)
	}
	if n := len(m["nd_profiles"].([]map[string]any)); n != 2 {
		t.Errorf("editor lists %d profiles, want 2", n)
	}
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return -1
}

// A device check-in gives a UniFi client (by MAC) to a person; giving it to someone else moves it.
func TestAssignDevice(t *testing.T) {
	e := openTestEngine(t)
	alex, err := e.SavePerson(Person{Name: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	sam, err := e.SavePerson(Person{Name: "Sam", Devices: []PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:09"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO clients(mac, name, ip) VALUES ('aa:bb:cc:00:00:09', 'Tablet', '192.168.1.40')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO client_hourly(hour, mac, rx, tx) VALUES (now(), 'aa:bb:cc:00:00:09', 100, 10)`); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignDevice(alex, "mac", "AA:BB:CC:00:00:09"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignDevice("nobody", "mac", "aa:bb:cc:00:00:09"); err == nil {
		t.Fatal("assigning to a missing person must fail")
	}
	if err := e.AssignDevice(alex, "bogus", "x"); err == nil {
		t.Fatal("unknown device kind must fail")
	}
	owners := map[string][]PersonDevice{}
	for _, p := range e.People() {
		owners[p.ID] = p.Devices
	}
	if len(owners[alex]) != 1 || owners[alex][0].Key != "aa:bb:cc:00:00:09" || len(owners[sam]) != 0 {
		t.Fatalf("the tablet should now be Alex's only: %+v", owners)
	}
	var who string
	if err := e.db.QueryRow(`SELECT person_id FROM trafq WHERE mac='aa:bb:cc:00:00:09'`).Scan(&who); err != nil || who != alex {
		t.Fatalf("traffic belongs to %q (%v), want Alex", who, err)
	}
}

// Groups (shared things like the TV) own devices like people do; people stay "person" by default.
func TestGroups(t *testing.T) {
	e := openTestEngine(t)
	alex, err := e.SavePerson(Person{Name: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := e.SavePerson(Person{Name: "Living room", Kind: "group", Devices: []PersonDevice{{Kind: "mac", Key: "aa:bb:cc:00:00:20"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.SavePerson(Person{Name: "Odd", Kind: "robot"}); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, p := range e.People() {
		kinds[p.Name] = p.Kind
		if p.ID == tv && len(p.Devices) != 1 {
			t.Fatalf("the group lost its device: %+v", p)
		}
	}
	if kinds["Alex"] != "person" || kinds["Living room"] != "group" || kinds["Odd"] != "person" {
		t.Fatalf("kinds: %v (alex %s)", kinds, alex)
	}
	if err := e.AssignDevice(tv, "mac", "aa:bb:cc:00:00:21"); err != nil {
		t.Fatal(err)
	}
}
