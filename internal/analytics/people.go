package analytics

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // time zones work on every OS, including Windows

	"github.com/h4ux/homeowl/internal/categories"
)

// ---- local time: hour-of-day / weekday views use the household's clock (DST included)

func (e *Engine) location() *time.Location {
	if name := strings.TrimSpace(e.conf().Timezone); name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			return l
		}
	}
	return time.Local
}

func ValidTimezone(name string) bool {
	if name == "" {
		return true
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func hours(a, b time.Time) []time.Time {
	var out []time.Time
	for ; a.Before(b); a = a.Add(time.Hour) {
		out = append(out, a)
	}
	return out
}

func (e *Engine) tzFill(x execer, q interface {
	QueryRow(string, ...any) *sql.Row
}, lo, hi time.Time) error {
	lo, hi = lo.Truncate(time.Hour), hi.Truncate(time.Hour)
	var h0, h1 sql.NullTime
	_ = q.QueryRow("SELECT min(hour), max(hour) FROM tz_offsets").Scan(&h0, &h1)
	var hs []time.Time
	if !h0.Valid {
		hs = hours(lo, hi.Add(time.Hour))
	} else {
		if lo.Before(h0.Time) {
			hs = append(hs, hours(lo, h0.Time.UTC())...)
		}
		if hi.After(h1.Time) {
			hs = append(hs, hours(h1.Time.UTC().Add(time.Hour), hi.Add(time.Hour))...)
		}
	}
	if len(hs) == 0 {
		return nil
	}
	loc := e.location()
	rows := make([][]any, len(hs))
	for i, h := range hs {
		_, off := h.In(loc).Zone()
		rows[i] = []any{h, off / 60}
	}
	return insert(x, "tz_offsets", []string{"hour", "off"}, rows)
}

// ApplyLocalTime fills the local-time columns of rows imported since the last run (or all rows after a time-zone change).
func (e *Engine) ApplyLocalTime(reset bool) {
	if e.db == nil {
		return
	}
	_ = e.tx(func(tx *sql.Tx) error {
		if reset {
			for _, s := range []string{"DELETE FROM tz_offsets", "UPDATE dns_queries SET lts = NULL", "UPDATE client_hourly SET lhour = NULL"} {
				if _, err := tx.Exec(s); err != nil {
					return err
				}
			}
		}
		var lo1, hi1, lo2, hi2, lo3, hi3 sql.NullTime
		_ = tx.QueryRow("SELECT min(ts), max(ts) FROM dns_queries WHERE lts IS NULL").Scan(&lo1, &hi1)
		_ = tx.QueryRow("SELECT min(hour), max(hour) FROM client_hourly WHERE lhour IS NULL").Scan(&lo2, &hi2)
		_ = tx.QueryRow("SELECT min(hour), max(hour) FROM dns_hourly_device").Scan(&lo3, &hi3)
		var lo, hi time.Time
		hi = utcnow().Add(24 * time.Hour)
		for _, p := range [][2]sql.NullTime{{lo1, hi1}, {lo2, hi2}, {lo3, hi3}} {
			if p[0].Valid && (lo.IsZero() || p[0].Time.Before(lo)) {
				lo = p[0].Time.UTC()
			}
			if p[1].Valid && p[1].Time.After(hi) {
				hi = p[1].Time.UTC()
			}
		}
		if lo.IsZero() {
			return nil
		}
		if err := e.tzFill(tx, tx, lo, hi); err != nil {
			return err
		}
		if lo1.Valid {
			if _, err := tx.Exec(`UPDATE dns_queries SET lts = dns_queries.ts + to_minutes(o.off) FROM tz_offsets o
				WHERE dns_queries.lts IS NULL AND o.hour = date_trunc('hour', dns_queries.ts)`); err != nil {
				return err
			}
		}
		if lo2.Valid {
			if _, err := tx.Exec(`UPDATE client_hourly SET lhour = client_hourly.hour + to_minutes(o.off) FROM tz_offsets o
				WHERE client_hourly.lhour IS NULL AND o.hour = date_trunc('hour', client_hourly.hour)`); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- categories

func (e *Engine) rules() []categories.Rule {
	rows, err := e.db.Query("SELECT pattern, category FROM category_rules ORDER BY length(pattern) DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []categories.Rule
	for rows.Next() {
		var r categories.Rule
		if rows.Scan(&r.Pattern, &r.Category) == nil {
			out = append(out, r)
		}
	}
	return out
}

// RefreshCategories categorises new root domains (all of them when full=true, e.g. after a rule change).
func (e *Engine) RefreshCategories(full bool) (int, error) {
	if e.db == nil {
		return 0, nil
	}
	rules := e.rules()
	q := `SELECT root, max(tracker) FROM (SELECT root, tracker FROM dns_daily_domain UNION ALL SELECT DISTINCT root, NULL FROM dns_daily_dr)
	      WHERE root IS NOT NULL`
	if !full {
		q += " AND root NOT IN (SELECT root FROM root_category)"
	}
	rs, err := e.db.Query(q + " GROUP BY root")
	if err != nil {
		return 0, err
	}
	var rows [][]any
	for rs.Next() {
		var root string
		var tracker sql.NullString
		if rs.Scan(&root, &tracker) == nil {
			cat, src := categories.Categorize(root, "", tracker.String, rules)
			rows = append(rows, []any{root, cat, src})
		}
	}
	rs.Close()
	err = e.tx(func(tx *sql.Tx) error {
		if full {
			if _, err := tx.Exec("DELETE FROM root_category"); err != nil {
				return err
			}
		}
		for i := 0; i < len(rows); i += 300 {
			part := rows[i:min(i+300, len(rows))]
			args := []any{}
			for _, r := range part {
				args = append(args, r...)
			}
			if _, err := tx.Exec("INSERT OR REPLACE INTO root_category VALUES "+strings.TrimSuffix(strings.Repeat("(?,?,?),", len(part)), ","), args...); err != nil {
				return err
			}
		}
		return nil
	})
	return len(rows), err
}

var rulePatternRE = regexp.MustCompile(`^(~[\w.\-]+|(\*\.)?[\w.\-]+)$`)

func (e *Engine) SaveRule(pattern, category string) error {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || len(pattern) > 200 || !rulePatternRE.MatchString(pattern) {
		return errors.New("use a domain (example.com), *.example.com, or ~word")
	}
	category = strings.TrimSpace(category)
	if len(category) > 60 {
		category = category[:60]
	}
	if category == "" {
		return errors.New("pick a category")
	}
	e.wmu.Lock()
	_, err := e.db.Exec("INSERT OR REPLACE INTO category_rules VALUES (?,?,?)", pattern, category, utcnow())
	e.wmu.Unlock()
	if err != nil {
		return err
	}
	_, err = e.RefreshCategories(true)
	return err
}

func (e *Engine) DeleteRule(pattern string) error {
	e.wmu.Lock()
	_, err := e.db.Exec("DELETE FROM category_rules WHERE pattern=?", pattern)
	e.wmu.Unlock()
	if err != nil {
		return err
	}
	_, err = e.RefreshCategories(true)
	return err
}

// ---- people

type PersonDevice struct {
	Kind string `json:"kind"` // dns | mac
	Key  string `json:"key"`
}

type Person struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Color   string         `json:"color"`
	Note    string         `json:"note"`
	Kind    string         `json:"kind"` // "person" (default) or "group": shared things like the TV, servers or smart-home gear
	Devices []PersonDevice `json:"devices"`
}

func (e *Engine) People() []Person {
	out := []Person{}
	if e.db == nil {
		return out
	}
	rs, err := e.db.Query("SELECT id, name, coalesce(color,''), coalesce(note,''), coalesce(nullif(kind,''),'person') FROM people ORDER BY name")
	if err != nil {
		return out
	}
	for rs.Next() {
		var p Person
		if rs.Scan(&p.ID, &p.Name, &p.Color, &p.Note, &p.Kind) == nil {
			p.Devices = []PersonDevice{}
			out = append(out, p)
		}
	}
	rs.Close()
	ds, err := e.db.Query("SELECT person_id, kind, key FROM person_devices")
	if err != nil {
		return out
	}
	defer ds.Close()
	for ds.Next() {
		var pid string
		var d PersonDevice
		if ds.Scan(&pid, &d.Kind, &d.Key) == nil {
			for i := range out {
				if out[i].ID == pid {
					out[i].Devices = append(out[i].Devices, d)
				}
			}
		}
	}
	return out
}

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func hid(parts ...any) string {
	h := sha1.Sum([]byte(fmt.Sprint(parts...)))
	return hex.EncodeToString(h[:])[:20]
}

// Kinds of things a person can own:
//
//	ndev    a device inside one NextDNS profile, key "<profile id>/<device name>"
//	profile a whole NextDNS profile (every device in it, unless one is given to someone else), key = profile id
//	mac     a UniFi client
//	dns     a NextDNS device name in any profile (older versions; still honoured)
var validKind = map[string]bool{"ndev": true, "profile": true, "mac": true, "dns": true}

func (e *Engine) SavePerson(p Person) (string, error) {
	if e.db == nil {
		return "", errors.New("analytics isn't running")
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return "", errors.New("give the person a name")
	}
	if len(p.Name) > 60 {
		p.Name = p.Name[:60]
	}
	if p.ID == "" {
		p.ID = hid("person", time.Now().UnixNano(), p.Name)[:10]
	}
	if !colorRE.MatchString(p.Color) {
		p.Color = ""
	}
	if p.Kind != "group" {
		p.Kind = "person"
	}
	if len(p.Note) > 300 {
		p.Note = p.Note[:300]
	}
	var devs [][]any
	for _, d := range p.Devices {
		k := strings.TrimSpace(d.Key)
		if len(k) > 200 {
			k = k[:200]
		}
		if validKind[d.Kind] && k != "" {
			if d.Kind == "mac" {
				k = strings.ToLower(k)
			}
			devs = append(devs, []any{p.ID, d.Kind, k})
		}
	}
	err := e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT OR REPLACE INTO people(id, name, color, note, kind) VALUES (?,?,?,?,?)", p.ID, p.Name, p.Color, p.Note, p.Kind); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM person_devices WHERE person_id=?", p.ID); err != nil {
			return err
		}
		for _, d := range devs { // a device belongs to one person
			if _, err := tx.Exec("DELETE FROM person_devices WHERE kind=? AND key=?", d[1], d[2]); err != nil {
				return err
			}
		}
		return insert(tx, "person_devices", []string{"person_id", "kind", "key"}, devs)
	})
	if err != nil {
		return "", err
	}
	e.RefreshDevPerson()
	return p.ID, nil
}

// AssignDevice gives one device (e.g. kind "mac") to a person, taking it away from whoever had it.
func (e *Engine) AssignDevice(personID, kind, key string) error {
	if e.db == nil {
		return errors.New("analytics isn't running")
	}
	key = strings.TrimSpace(key)
	if kind == "mac" {
		key = strings.ToLower(key)
	}
	if !validKind[kind] || key == "" || len(key) > 200 {
		return errors.New("bad device")
	}
	var n int
	if err := e.db.QueryRow("SELECT count(*) FROM people WHERE id=?", personID).Scan(&n); err != nil || n == 0 {
		return errors.New("no such person")
	}
	err := e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM person_devices WHERE kind=? AND key=?", kind, key); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO person_devices VALUES (?,?,?)", personID, kind, key)
		return err
	})
	if err == nil {
		e.RefreshDevPerson()
	}
	return err
}

func (e *Engine) DeletePerson(id string) error {
	err := e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM person_devices WHERE person_id=?", id); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM people WHERE id=?", id)
		return err
	})
	e.RefreshDevPerson()
	return err
}

// RefreshDevPerson expands each person's devices into the labels DNS logs and UniFi stats use (name, IP, MAC).
func (e *Engine) RefreshDevPerson() {
	if e.db == nil {
		return
	}
	type cl struct{ mac, name, host, ip, fip string }
	var clients []cl
	if rs, err := e.db.Query("SELECT mac, coalesce(name,''), coalesce(hostname,''), coalesce(ip,''), coalesce(fixed_ip,'') FROM clients"); err == nil {
		for rs.Next() {
			var c cl
			if rs.Scan(&c.mac, &c.name, &c.host, &c.ip, &c.fip) == nil {
				clients = append(clients, c)
			}
		}
		rs.Close()
	}
	labels := map[string]string{}
	var order []string
	add := func(l, pid string) {
		if l == "" {
			return
		}
		if _, ok := labels[l]; !ok {
			labels[l] = pid
			order = append(order, l)
		}
	}
	toClients := func(name, ip, pid string) { // the same device as UniFi sees it → its traffic
		for _, c := range clients {
			if (name != "" && (name == c.name || name == c.host || name == c.ip || name == c.fip)) || (ip != "" && (ip == c.ip || ip == c.fip)) {
				add("mac:"+c.mac, pid)
			}
		}
	}
	// most specific first: a labelled device beats a whole profile, and the first owner wins
	rs, err := e.db.Query(`SELECT person_id, kind, key FROM person_devices
		ORDER BY CASE kind WHEN 'ndev' THEN 0 WHEN 'dns' THEN 1 WHEN 'mac' THEN 2 ELSE 3 END`)
	if err != nil {
		return
	}
	type pd struct{ pid, kind, key string }
	var all []pd
	for rs.Next() {
		var d pd
		if rs.Scan(&d.pid, &d.kind, &d.key) == nil {
			all = append(all, d)
		}
	}
	rs.Close()
	for _, d := range all {
		pid, key := d.pid, d.key
		switch d.kind {
		case "ndev":
			if prof, dev, ok := strings.Cut(key, "/"); ok {
				add("dns:"+prof+"/"+dev, pid)
				toClients(dev, "", pid)
			}
		case "dns":
			add(key, pid)
			toClients(key, "", pid)
		case "profile":
			add("profile:"+key, pid)
			// devices seen in this profile that nobody owns yet → their UniFi traffic too
			if ds, err := e.db.Query(`SELECT coalesce(nullif(device_name,''), nullif(local_ip,''), '') AS d, coalesce(any_value(local_ip),'')
				FROM dns_queries WHERE profile_id = ? AND ts >= ? GROUP BY 1`, key, utcnow().Add(-30*24*time.Hour)); err == nil {
				for ds.Next() {
					var dev, ip string
					if ds.Scan(&dev, &ip) == nil && dev != "" {
						if _, taken := labels["dns:"+key+"/"+dev]; taken {
							continue
						}
						if _, taken := labels[dev]; taken {
							continue
						}
						toClients(dev, ip, pid)
					}
				}
				ds.Close()
			}
		case "mac":
			add("mac:"+key, pid)
			for _, c := range clients {
				if c.mac == key {
					add(c.name, pid)
					add(c.host, pid)
					for _, ip := range []string{c.ip, c.fip} {
						if ip != "" {
							add("ip:"+ip, pid)
							add(ip, pid)
						}
					}
				}
			}
		}
	}
	rs.Close()
	rows := make([][]any, 0, len(order))
	for _, l := range order {
		rows = append(rows, []any{l, labels[l]})
	}
	_ = e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM dev_person"); err != nil {
			return err
		}
		return insert(tx, "dev_person", []string{"label", "person_id"}, rows)
	})
}

// UnassignDevice takes one device link away from whoever had it.
func (e *Engine) UnassignDevice(kind, key string) error {
	if e.db == nil {
		return errors.New("analytics isn't running")
	}
	if _, err := e.db.Exec("DELETE FROM person_devices WHERE kind=? AND key=?", kind, key); err != nil {
		return err
	}
	e.RefreshDevPerson()
	return nil
}

// NDSeen is what the NextDNS logs say about one named device in one profile.
type NDSeen struct {
	Last     time.Time // zero = not in the logs (last 30 days)
	ClientIP string    // the public address of its last query
	Day      int       // queries in the last 24 hours
}

// NDProfileLatest is the newest query imported for a NextDNS profile (are its logs arriving?).
func (e *Engine) NDProfileLatest(profile string) (time.Time, error) {
	if e.db == nil {
		return time.Time{}, errors.New("analytics isn't running")
	}
	var last sql.NullTime
	if err := e.db.QueryRow(`SELECT max(ts) FROM dns_queries WHERE profile_id = ? AND ts >= ?`, profile, utcnow().Add(-7*24*time.Hour)).Scan(&last); err != nil {
		return time.Time{}, err
	}
	return last.Time, nil
}

// NDDeviceSeen looks a device up in the imported NextDNS logs by profile and device name (the setup profiles
// name the device, so this shows whether one is installed and in use).
func (e *Engine) NDDeviceSeen(profile, name string) (NDSeen, error) {
	var s NDSeen
	if e.db == nil {
		return s, errors.New("analytics isn't running")
	}
	var last sql.NullTime
	var ip sql.NullString
	var day sql.NullInt64
	err := e.db.QueryRow(`SELECT max(ts), arg_max(client_ip, ts), count(*) FILTER (WHERE ts >= ?)
		FROM dns_queries WHERE profile_id = ? AND device_name = ? AND ts >= ?`,
		utcnow().Add(-24*time.Hour), profile, name, utcnow().Add(-30*24*time.Hour)).Scan(&last, &ip, &day)
	if err != nil {
		return s, err
	}
	if last.Valid {
		s.Last = last.Time
	}
	s.ClientIP, s.Day = ip.String, int(day.Int64)
	return s, nil
}
