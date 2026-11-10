package analytics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (e *Engine) post(path string, body any) ([]map[string]any, error) {
	st, d := e.uni.Request("POST", e.uni.SitePath(path, false), body)
	if st != 200 {
		return nil, fmt.Errorf("%s: %s", path, errText(d, st))
	}
	return dataList(d), nil
}

func (e *Engine) get(path string) ([]map[string]any, error) {
	st, d := e.uni.Request("GET", e.uni.SitePath(path, false), nil)
	if st != 200 {
		return nil, fmt.Errorf("%s: %s", path, errText(d, st))
	}
	return dataList(d), nil
}

func errText(d any, st int) string {
	if m, ok := d.(map[string]any); ok {
		if s, ok := m["error"].(string); ok {
			return s
		}
	}
	return fmt.Sprint(st)
}

func dataList(d any) []map[string]any {
	if m, ok := d.(map[string]any); ok {
		if v, ok := m["data"]; ok {
			return anyList(v)
		}
	}
	return anyList(d)
}

func fromMs(v any) (time.Time, bool) {
	f, ok := v.(float64)
	if !ok {
		if s, ok2 := v.(string); ok2 {
			if _, err := fmt.Sscan(s, &f); err != nil {
				return time.Time{}, false
			}
		} else {
			return time.Time{}, false
		}
	}
	if f <= 0 {
		return time.Time{}, false
	}
	if f < 1e12 {
		f *= 1000
	}
	return time.UnixMilli(int64(f)).UTC(), true
}

func tOrNil(v any) any {
	if t, ok := fromMs(v); ok {
		return t
	}
	return nil
}

func asInt(v any) any {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		var n int64
		if _, err := fmt.Sscan(t, &n); err == nil {
			return n
		}
	}
	return nil
}

func asInt0(v any) int64 {
	if n, ok := asInt(v).(int64); ok {
		return n
	}
	return 0
}

func jsonStr(v any, n int) string {
	b, _ := json.Marshal(v)
	return truncate(string(b), n)
}

// SyncUniFi imports clients, hourly traffic, sessions, events and threats.
func (e *Engine) SyncUniFi() {
	now := utcnow()
	for _, s := range []struct {
		name string
		fn   func(time.Time) (int, string, error)
	}{{"clients", e.syncClients}, {"traffic", e.syncTraffic}, {"sessions", e.syncSessions}, {"events", e.syncEvents}, {"threats", e.syncThreats}} {
		n, note, err := s.fn(now)
		if err != nil {
			e.state("unifi:"+s.name, false, 0, truncate(err.Error(), 300), time.Time{}, nil)
			continue
		}
		var np *string
		if note != "" {
			np = &note
		}
		e.state("unifi:"+s.name, true, n, "", now, np)
	}
}

func (e *Engine) syncClients(now time.Time) (int, string, error) {
	users, err := e.get("rest/user")
	if err != nil {
		return 0, "", err
	}
	active, err := e.get("stat/sta")
	if err != nil {
		return 0, "", err
	}
	nets, _ := e.get("rest/networkconf")
	netName := map[string]string{}
	for _, n := range nets {
		netName[str(n["_id"])] = str(n["name"])
	}
	known, act := map[string]map[string]any{}, map[string]map[string]any{}
	macs := map[string]bool{}
	for _, u := range users {
		if m := strings.ToLower(str(u["mac"])); m != "" {
			known[m] = u
			macs[m] = true
		}
	}
	for _, a := range active {
		if m := strings.ToLower(str(a["mac"])); m != "" {
			act[m] = a
			macs[m] = true
		}
	}
	var rows [][]any
	for mac := range macs {
		u, a := known[mac], act[mac]
		if u == nil {
			u = map[string]any{}
		}
		if a == nil {
			a = map[string]any{}
		}
		network := orS(str(a["network"]), netName[str(u["network_id"])], netName[str(a["network_id"])])
		var fixed any
		if b, _ := u["use_fixedip"].(bool); b {
			fixed = nullS(str(u["fixed_ip"]))
		}
		wired, ok := a["is_wired"].(bool)
		if !ok {
			wired, _ = u["is_wired"].(bool)
		}
		first := tOrNil(orAny(u["first_seen"], a["first_seen"]))
		last := tOrNil(orAny(a["last_seen"], u["last_seen"]))
		if last == nil && len(a) > 0 {
			last = now
		}
		rows = append(rows, []any{mac, nullS(orS(str(u["name"]), str(a["name"]))), nullS(orS(str(a["hostname"]), str(u["hostname"]))),
			nullS(orS(str(a["oui"]), str(u["oui"]))), nullS(network), nullS(orS(str(a["ip"]), str(u["last_ip"]))), fixed, wired, first, last, now})
	}
	err = e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("CREATE TEMP TABLE IF NOT EXISTS _cl AS SELECT * FROM clients LIMIT 0"); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM _cl"); err != nil {
			return err
		}
		if err := insert(tx, "_cl", []string{"mac", "name", "hostname", "vendor", "network", "ip", "fixed_ip", "wired", "first_seen", "last_seen", "updated"}, rows); err != nil {
			return err
		}
		// keep first_seen / last ip of devices that are offline now
		_, err := tx.Exec(`INSERT OR REPLACE INTO clients SELECT n.mac, coalesce(n.name, o.name), coalesce(n.hostname, o.hostname),
			coalesce(n.vendor, o.vendor), coalesce(n.network, o.network), coalesce(n.ip, o.ip), n.fixed_ip, n.wired,
			least(coalesce(n.first_seen, o.first_seen), coalesce(o.first_seen, n.first_seen)),
			greatest(coalesce(n.last_seen, o.last_seen), coalesce(o.last_seen, n.last_seen)), n.updated
			FROM _cl n LEFT JOIN clients o USING (mac)`)
		return err
	})
	if err != nil {
		return 0, "", err
	}
	e.RefreshDevPerson()
	return len(rows), "", nil
}

func orAny(v ...any) any {
	for _, x := range v {
		if x != nil {
			if f, ok := x.(float64); ok && f == 0 {
				continue
			}
			return x
		}
	}
	return nil
}

func (e *Engine) syncTraffic(now time.Time) (int, string, error) {
	start := now.Add(-7 * 24 * time.Hour)
	if last, ok := e.lastTS("unifi:traffic"); ok {
		start = last.Add(-3 * time.Hour)
	}
	var macs []string
	if rs, err := e.db.Query("SELECT mac FROM clients WHERE last_seen >= ?", start.Add(-24*time.Hour)); err == nil {
		for rs.Next() {
			var m string
			if rs.Scan(&m) == nil {
				macs = append(macs, m)
			}
		}
		rs.Close()
	}
	var rows, site [][]any
	for i := 0; i < len(macs); i += 50 {
		data, err := e.post("stat/report/hourly.user", map[string]any{"attrs": []string{"rx_bytes", "tx_bytes", "time"},
			"start": start.UnixMilli(), "end": now.UnixMilli(), "macs": macs[i:min(i+50, len(macs))]})
		if err != nil {
			return 0, "", err
		}
		for _, r := range data {
			mac := strings.ToLower(orS(str(r["user"]), str(r["oid"]), str(r["mac"])))
			if t, ok := fromMs(r["time"]); ok && mac != "" {
				rows = append(rows, []any{t, mac, asInt0(r["rx_bytes"]), asInt0(r["tx_bytes"])})
			}
		}
	}
	data, err := e.post("stat/report/hourly.site", map[string]any{"attrs": []string{"wan-rx_bytes", "wan-tx_bytes", "num_sta", "time"},
		"start": start.UnixMilli(), "end": now.UnixMilli()})
	if err != nil {
		return 0, "", err
	}
	for _, r := range data {
		if t, ok := fromMs(r["time"]); ok {
			site = append(site, []any{t, asInt0(r["wan-rx_bytes"]), asInt0(r["wan-tx_bytes"]), asInt(r["num_sta"])})
		}
	}
	err = e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM client_hourly WHERE hour>=?", start); err != nil {
			return err
		}
		if err := insert(tx, "client_hourly", []string{"hour", "mac", "rx", "tx"}, rows); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM site_hourly WHERE hour>=?", start); err != nil {
			return err
		}
		return insert(tx, "site_hourly", []string{"hour", "wan_rx", "wan_tx", "clients"}, site)
	})
	if err != nil {
		return 0, "", err
	}
	e.ApplyLocalTime(false)
	return len(rows) + len(site), "", nil
}

func (e *Engine) syncSessions(now time.Time) (int, string, error) {
	start := now.Add(-30 * 24 * time.Hour)
	if last, ok := e.lastTS("unifi:sessions"); ok {
		start = last.Add(-24 * time.Hour)
	}
	data, err := e.post("stat/session", map[string]any{"type": "all", "start": start.Unix(), "end": now.Unix()})
	if err != nil {
		return 0, "", err
	}
	var rows [][]any
	var ids []any
	for _, s := range data {
		mac := strings.ToLower(str(s["mac"]))
		st, ok := fromMs(s["assoc_time"])
		if mac == "" || !ok {
			continue
		}
		dur := asInt0(s["duration"])
		stop, ok := fromMs(s["disassoc_time"])
		if !ok {
			stop = st.Add(time.Duration(dur) * time.Second)
		}
		id := orS(str(s["_id"]), hid(mac, s["assoc_time"]))
		ids = append(ids, id)
		wired, _ := s["is_wired"].(bool)
		rows = append(rows, []any{id, mac, st, stop, dur, asInt0(s["rx_bytes"]), asInt0(s["tx_bytes"]),
			nullS(strings.ToLower(str(s["ap_mac"]))), nullS(str(s["ip"])), nullS(str(s["hostname"])), wired})
	}
	err = e.tx(func(tx *sql.Tx) error {
		for i := 0; i < len(ids); i += 500 {
			part := ids[i:min(i+500, len(ids))]
			if _, err := tx.Exec("DELETE FROM sessions WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+")", part...); err != nil {
				return err
			}
		}
		return insert(tx, "sessions", []string{"id", "mac", "start_ts", "stop_ts", "duration", "rx", "tx", "ap_mac", "ip", "hostname", "wired"}, rows)
	})
	return len(rows), "", err
}

// insertNew adds rows whose id (first column) isn't stored yet.
func (e *Engine) insertNew(table string, cols []string, rows [][]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	n := 0
	err := e.tx(func(tx *sql.Tx) error {
		have := map[string]bool{}
		var ids []any
		for _, r := range rows {
			ids = append(ids, r[0])
		}
		for i := 0; i < len(ids); i += 500 {
			part := ids[i:min(i+500, len(ids))]
			rs, err := tx.Query("SELECT id FROM "+table+" WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+")", part...)
			if err != nil {
				return err
			}
			for rs.Next() {
				var id string
				if rs.Scan(&id) == nil {
					have[id] = true
				}
			}
			rs.Close()
		}
		var fresh [][]any
		for _, r := range rows {
			id := fmt.Sprint(r[0])
			if !have[id] {
				have[id] = true
				fresh = append(fresh, r)
			}
		}
		n = len(fresh)
		return insert(tx, table, cols, fresh)
	})
	return n, err
}

func (e *Engine) syncEvents(now time.Time) (int, string, error) {
	var rows [][]any
	if evs, err := e.get("stat/event?within=48&_limit=3000"); err == nil {
		for _, ev := range evs {
			if t, ok := fromMs(ev["time"]); ok {
				rows = append(rows, []any{orS(str(ev["_id"]), hid("ev", ev["time"], ev["key"], ev["msg"])), t, "event", nullS(str(ev["key"])),
					nullS(str(ev["subsystem"])), truncate(str(ev["msg"]), 1000), jsonStr(ev, 4000)})
			}
		}
	}
	ms := now.UnixMilli()
	st, d := e.uni.Request("POST", e.uni.SitePath("system-log/all", true),
		map[string]any{"timestampFrom": ms - 2*86400000, "timestampTo": ms, "pageSize": 1000, "pageNumber": 0})
	if st == 200 {
		for _, ev := range dataList(d) {
			t, ok := fromMs(orAny(ev["timestamp"], ev["time"]))
			if !ok {
				continue
			}
			msg := orS(str(ev["message"]), str(ev["title"]))
			rows = append(rows, []any{orS(str(ev["id"]), hid("sl", ev["timestamp"], ev["key"], msg)), t, "system-log",
				nullS(orS(str(ev["key"]), str(ev["category"]))), nullS(orS(str(ev["category"]), str(ev["severity"]))), truncate(msg, 1000), jsonStr(ev, 4000)})
		}
	}
	n, err := e.insertNew("events", []string{"id", "ts", "source", "key", "subsystem", "msg", "raw"}, rows)
	return n, "", err
}

// syncThreats imports IPS/IDS detections. Newer UniFi Network versions dropped stat/ips/event, so detections are
// also read from the gateway's syslog (CEF security events) and the UniFi system log.
func (e *Engine) syncThreats(now time.Time) (int, string, error) {
	start := now.Add(-30 * 24 * time.Hour)
	if last, ok := e.lastTS("unifi:threats"); ok {
		start = last.Add(-6 * time.Hour)
	}
	apiOK := true
	var rows [][]any
	data, err := e.post("stat/ips/event", map[string]any{"start": start.UnixMilli(), "end": now.UnixMilli(), "_limit": 5000})
	if err != nil {
		if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "NotFound") {
			return 0, "", err
		}
		apiOK = false
	}
	for _, ev := range data {
		t, ok := fromMs(orAny(ev["timestamp"], ev["time"]))
		if !ok {
			continue
		}
		rows = append(rows, []any{orS(str(ev["_id"]), hid("ips", ev["timestamp"], ev["src_ip"], ev["inner_alert_signature"])), t,
			nullS(orS(str(ev["inner_alert_signature"]), str(ev["msg"]))), nullS(orS(str(ev["inner_alert_category"]), str(ev["catname"]))),
			nullS(orS(str(ev["inner_alert_action"]), str(ev["action"]))), nullS(str(ev["src_ip"])), nullS(orS(str(ev["dest_ip"]), str(ev["dst_ip"]))),
			asInt(ev["src_port"]), asInt(ev["dest_port"]), nullS(str(ev["proto"])), nullS(orS(str(ev["src_ip_country"]), str(ev["srcipCountry"]))), jsonStr(ev, 4000)})
	}
	n, err := e.insertNew("threats", []string{"id", "ts", "signature", "category", "action", "src_ip", "dst_ip", "src_port", "dst_port", "proto", "country", "raw"}, rows)
	if err != nil {
		return 0, "", err
	}
	m, _ := e.ThreatsFromLogs()
	note := ""
	if !apiOK {
		note = "No threat API on this UniFi version — read from syslog security events and the system log"
	}
	return n + m, note, nil
}

const threatMatch = "(lower(coalesce(%s,'')) SIMILAR TO '.*(intrusion|threat|ips alert|ids alert|honeypot|malicious|cybersecure).*')"

func tm(col string) string { return fmt.Sprintf(threatMatch, col) }

// ThreatsFromLogs copies threat detections found in syslog (CEF) and UniFi system-log events into the threats table.
func (e *Engine) ThreatsFromLogs() (int, error) {
	if e.db == nil {
		return 0, errors.New("not open")
	}
	var n int64
	err := e.tx(func(tx *sql.Tx) error {
		var before, after int64
		_ = tx.QueryRow("SELECT count(*) FROM threats").Scan(&before)
		if _, err := tx.Exec(`INSERT INTO threats
			SELECT id, ts, signature, category, action, src, dst, sport, dport, proto, NULL, msg FROM (
			  SELECT substr(md5(concat_ws('|', 'sys', ts, src, dst, name, msg)), 1, 20) AS id, ts, coalesce(msg, name) AS signature,
			         category, action, src, dst, sport, dport, proto, msg
			  FROM syslog WHERE kind = 'cef' AND (` + tm("category") + ` OR ` + tm("name") + ` OR ` + tm("msg") + `)
			) s WHERE s.id NOT IN (SELECT id FROM threats)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO threats
			SELECT 'ev' || substr(id, 1, 18), ts, msg, coalesce(key, subsystem), NULL, NULL, NULL, NULL, NULL, NULL, NULL, raw
			FROM events WHERE (` + tm("key") + ` OR ` + tm("subsystem") + ` OR ` + tm("msg") + `)
			  AND 'ev' || substr(id, 1, 18) NOT IN (SELECT id FROM threats)`); err != nil {
			return err
		}
		_ = tx.QueryRow("SELECT count(*) FROM threats").Scan(&after)
		n = after - before
		return nil
	})
	return int(n), err
}

// SyncDPI stores a snapshot of per-client application usage.
func (e *Engine) SyncDPI() error {
	now := utcnow()
	data, err := e.post("stat/stadpi", map[string]any{"type": "by_app"})
	if err != nil {
		return err
	}
	var rows [][]any
	for _, x := range data {
		mac := strings.ToLower(str(x["mac"]))
		for _, a := range anyList(x["by_app"]) {
			rows = append(rows, []any{now, mac, asInt(a["cat"]), asInt(a["app"]), asInt0(a["rx_bytes"]), asInt0(a["tx_bytes"])})
		}
	}
	if err := e.tx(func(tx *sql.Tx) error {
		return insert(tx, "dpi_snapshots", []string{"ts", "mac", "cat", "app", "rx", "tx"}, rows)
	}); err != nil {
		return err
	}
	e.state("unifi:dpi", true, len(rows), "", now, nil)
	return nil
}

// Maintenance deletes raw logs older than the retention period and compacts the database.
func (e *Engine) Maintenance() {
	days := e.conf().RawDays
	if days <= 0 {
		days = 90
	}
	cut := utcnow().Add(-time.Duration(days) * 24 * time.Hour)
	e.RollupSyslog(26)
	e.wmu.Lock()
	_, _ = e.db.Exec("DELETE FROM dns_queries WHERE ts < ?", cut)
	_, _ = e.db.Exec("DELETE FROM syslog WHERE ts < ?", cut)
	_, _ = e.db.Exec("CHECKPOINT")
	e.wmu.Unlock()
	e.state("maintenance", true, 0, "", utcnow(), strp(fmt.Sprintf("Removed raw logs older than %d days", days)))
}
