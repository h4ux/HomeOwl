package analytics

import (
	"errors"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/categories"
)

var Ranges = map[string]int{"24h": 1, "7d": 7, "30d": 30, "90d": 90, "1y": 365, "all": 36500}

// Status is what Settings › Analytics shows.
func (e *Engine) Status() map[string]any {
	c := e.conf()
	e.mu.Lock()
	running := map[string]bool{}
	for k, v := range e.running {
		if v {
			running[k] = true
		}
	}
	nde := map[string]string{}
	for k, v := range e.ndErrors {
		nde[k] = v
	}
	e.mu.Unlock()
	st := map[string]any{
		"engine": true, "engine_version": e.engineVersion(), "enabled": c.Enabled, "open": e.db != nil, "open_error": nullS(e.openErr),
		"config": c, "nextdns_key": len(e.NDSlots()) > 0, "nextdns_errors": nde, "syslog": e.SyslogStatus(), "running": running,
		"lan_ip": LocalIPTowards(e.uni.Host()),
	}
	if e.db == nil {
		return st
	}
	src, _ := e.Rows("SELECT source, last_ts, last_run, ok, rows, error, note FROM sync_state WHERE source NOT LIKE '%:history' ORDER BY source")
	st["sources"] = src
	counts := map[string]int64{}
	for _, t := range []string{"dns_queries", "client_hourly", "sessions", "events", "threats", "syslog", "clients"} {
		counts[t] = e.count("SELECT count(*) FROM " + t)
	}
	st["counts"] = counts
	var size int64
	for _, p := range []string{e.DBPath, e.DBPath + ".wal"} {
		if fi, err := os.Stat(p); err == nil {
			size += fi.Size()
		}
	}
	st["db_bytes"] = size
	profiles := map[string]string{}
	info, _ := e.Rows("SELECT id, name, account, logs FROM nextdns_profiles ORDER BY name")
	for _, p := range info {
		profiles[str(p["id"])] = str(p["name"])
	}
	st["profiles"] = profiles
	st["profile_info"] = info
	st["targets"] = e.TargetIDs()
	return st
}

func (e *Engine) engineVersion() string {
	if v, ok := e.scalar("SELECT version()").(string); ok {
		return v
	}
	return ""
}

// LocalIPTowards returns this computer's LAN address that the console would reach (for the syslog instructions).
func LocalIPTowards(host string) string {
	t := regexp.MustCompile(`^https?://`).ReplaceAllString(host, "")
	t = strings.Split(strings.Split(t, "/")[0], ":")[0]
	if t == "" {
		t = "192.168.1.1"
	}
	c, err := net.Dial("udp", net.JoinHostPort(t, "9"))
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

// ---- named queries for the Analytics tabs

type qctx struct {
	start                           time.Time
	bucket                          string
	profile, device, mac, q, status string
}

type queryFn func(c qctx) (string, []any)

func pf(c qctx) (string, []any) {
	if c.profile != "" {
		return " AND profile_id = ?", []any{c.profile}
	}
	return "", nil
}

func bucket(c qctx, col string) string {
	return "epoch_ms(date_trunc('" + c.bucket + "', " + col + "))"
}

func cat(a ...[]any) []any {
	var out []any
	for _, x := range a {
		out = append(out, x...)
	}
	return out
}

func like(s string) string { return "%" + s + "%" }

var queries = map[string]queryFn{
	"dns_summary": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked, count(DISTINCT device) AS devices,
			(SELECT count(DISTINCT root) FROM dns_daily_domain WHERE day >= CAST(? AS DATE)` + w + `) AS domains
			FROM dns_hourly_device WHERE hour >= ?` + w, cat([]any{c.start}, a, []any{c.start}, a)
	},
	"dns_timeline": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT ` + bucket(c, "hour") + ` AS t, sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked
			FROM dns_hourly_device WHERE hour >= ?` + w + ` GROUP BY 1 ORDER BY 1`, cat([]any{c.start}, a)
	},
	"dns_top_domains": func(c qctx) (string, []any) { return topDomains(c, false) },
	"dns_top_blocked": func(c qctx) (string, []any) { return topDomains(c, true) },
	"dns_devices": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT device, sum(n) AS queries, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked,
			round(100.0 * coalesce(sum(n) FILTER (WHERE status='blocked'),0) / nullif(sum(n),0), 1) AS block_pct,
			string_agg(DISTINCT profile_id, ', ') AS profiles
			FROM dns_hourly_device WHERE hour >= ?` + w + ` GROUP BY device ORDER BY queries DESC LIMIT 100`, cat([]any{c.start}, a)
	},
	"dns_profiles": func(c qctx) (string, []any) {
		return `SELECT h.profile_id, coalesce(p.name, h.profile_id) AS name, sum(n) AS queries,
			coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked, count(DISTINCT device) AS devices
			FROM dns_hourly_device h LEFT JOIN nextdns_profiles p ON p.id = h.profile_id
			WHERE hour >= ? GROUP BY ALL ORDER BY queries DESC`, []any{c.start}
	},
	"dns_trackers": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT tracker, sum(n) AS queries, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked
			FROM dns_daily_domain WHERE day >= CAST(? AS DATE) AND tracker IS NOT NULL AND tracker <> ''` + w + `
			GROUP BY tracker ORDER BY queries DESC LIMIT 20`, cat([]any{c.start}, a)
	},
	"dns_reasons": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT trim(r) AS reason, count(*) AS blocked FROM (SELECT unnest(string_split(reasons, ',')) AS r FROM dns_queries
			WHERE ts >= ? AND status='blocked' AND reasons <> ''` + w + `) GROUP BY 1 ORDER BY 2 DESC LIMIT 15`, cat([]any{c.start}, a)
	},
	"dns_protocols": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT coalesce(protocol,'?') AS protocol, count(*) AS queries, round(100.0*avg(CASE WHEN encrypted THEN 1 ELSE 0 END),1) AS encrypted_pct
			FROM dns_queries WHERE ts >= ?` + w + ` GROUP BY 1 ORDER BY 2 DESC`, cat([]any{c.start}, a)
	},
	"dns_device_domains": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT root AS domain, count(*) AS queries, count(*) FILTER (WHERE status='blocked') AS blocked, max(tracker) AS tracker
			FROM dns_queries WHERE ts >= ? AND ` + deviceSQL + ` = ?` + w + ` GROUP BY root ORDER BY queries DESC LIMIT 30`, cat([]any{c.start, c.device}, a)
	},
	"dns_device_timeline": func(c qctx) (string, []any) {
		w, a := pf(c)
		return `SELECT ` + bucket(c, "hour") + ` AS t, sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked
			FROM dns_hourly_device WHERE hour >= ? AND device = ?` + w + ` GROUP BY 1 ORDER BY 1`, cat([]any{c.start, c.device}, a)
	},
	"dns_log": func(c qctx) (string, []any) {
		w, a := pf(c)
		cond, args := "", []any{}
		if c.q != "" {
			cond += " AND (domain ILIKE ? OR device_name ILIKE ? OR local_ip ILIKE ?)"
			args = append(args, like(c.q), like(c.q), like(c.q))
		}
		if c.status != "" {
			cond += " AND status = ?"
			args = append(args, c.status)
		}
		if c.device != "" {
			cond += " AND " + deviceSQL + " = ?"
			args = append(args, c.device)
		}
		return `SELECT epoch_ms(ts) AS t, profile_id, ` + deviceSQL + ` AS device, domain, status, reasons, protocol
			FROM dns_queries WHERE ts >= ?` + w + cond + ` ORDER BY ts DESC LIMIT 300`, cat([]any{c.start}, a, args)
	},
	"traffic_summary": func(c qctx) (string, []any) {
		return `SELECT coalesce(sum(wan_rx),0) AS wan_rx, coalesce(sum(wan_tx),0) AS wan_tx,
			(SELECT count(DISTINCT mac) FROM client_hourly WHERE hour >= ? AND rx+tx > 0) AS active_clients,
			(SELECT count(*) FROM clients WHERE first_seen >= ?) AS new_clients
			FROM site_hourly WHERE hour >= ?`, []any{c.start, c.start, c.start}
	},
	"traffic_timeline": func(c qctx) (string, []any) {
		return `SELECT ` + bucket(c, "hour") + ` AS t, sum(wan_rx) AS download, sum(wan_tx) AS upload, max(clients) AS clients
			FROM site_hourly WHERE hour >= ? GROUP BY 1 ORDER BY 1`, []any{c.start}
	},
	"traffic_clients": func(c qctx) (string, []any) {
		return `SELECT h.mac, coalesce(c.name, c.hostname, h.mac) AS client, c.network, c.vendor, sum(h.rx) AS download, sum(h.tx) AS upload,
			sum(h.rx + h.tx) AS total FROM client_hourly h LEFT JOIN clients c USING (mac)
			WHERE h.hour >= ? GROUP BY ALL ORDER BY total DESC LIMIT 50`, []any{c.start}
	},
	"traffic_networks": func(c qctx) (string, []any) {
		return `SELECT coalesce(c.network, 'Unknown') AS network, sum(h.rx + h.tx) AS total, count(DISTINCT h.mac) AS clients
			FROM client_hourly h LEFT JOIN clients c USING (mac) WHERE h.hour >= ? GROUP BY 1 ORDER BY 2 DESC`, []any{c.start}
	},
	"traffic_client_timeline": func(c qctx) (string, []any) {
		return `SELECT ` + bucket(c, "hour") + ` AS t, sum(rx) AS download, sum(tx) AS upload FROM client_hourly
			WHERE hour >= ? AND mac = ? GROUP BY 1 ORDER BY 1`, []any{c.start, c.mac}
	},
	"online_time": func(c qctx) (string, []any) {
		return `SELECT s.mac, coalesce(c.name, c.hostname, any_value(s.hostname), s.mac) AS client, count(*) AS sessions,
			round(sum(least(s.duration, epoch(s.stop_ts) - epoch(greatest(s.start_ts, ?)))) / 3600.0, 1) AS hours_online
			FROM sessions s LEFT JOIN clients c USING (mac) WHERE s.stop_ts >= ? GROUP BY s.mac, c.name, c.hostname
			ORDER BY hours_online DESC LIMIT 40`, []any{c.start, c.start}
	},
	"new_clients": func(c qctx) (string, []any) {
		return `SELECT mac, coalesce(name, hostname, mac) AS client, vendor, network, ip, epoch_ms(first_seen) AS first_seen,
			epoch_ms(last_seen) AS last_seen FROM clients WHERE first_seen >= ? ORDER BY first_seen DESC LIMIT 100`, []any{c.start}
	},
	"apps": func(c qctx) (string, []any) {
		return `WITH latest AS (SELECT * FROM dpi_snapshots WHERE ts = (SELECT max(ts) FROM dpi_snapshots))
			SELECT cat, sum(rx + tx) AS total, count(DISTINCT mac) AS clients FROM latest GROUP BY cat ORDER BY total DESC LIMIT 15`, nil
	},
	"sec_summary": func(c qctx) (string, []any) {
		return `SELECT (SELECT count(*) FROM threats WHERE ts >= ?) AS threats,
			(SELECT coalesce(sum(n),0) FROM syslog_hourly WHERE hour >= ? AND kind='firewall' AND action='blocked') AS fw_blocked,
			(SELECT coalesce(sum(n),0) FROM syslog_hourly WHERE hour >= ?) AS syslog_lines,
			(SELECT count(*) FROM events WHERE ts >= ?) AS events`, []any{c.start, c.start, c.start, c.start}
	},
	"threat_timeline": func(c qctx) (string, []any) {
		return `SELECT ` + bucket(c, "ts") + ` AS t, count(*) AS threats FROM threats WHERE ts >= ? GROUP BY 1 ORDER BY 1`, []any{c.start}
	},
	"threat_top": func(c qctx) (string, []any) {
		return `SELECT coalesce(signature,'?') AS signature, count(*) AS hits, count(DISTINCT src_ip) AS sources, max(action) AS action
			FROM threats WHERE ts >= ? GROUP BY 1 ORDER BY 2 DESC LIMIT 15`, []any{c.start}
	},
	"threat_countries": func(c qctx) (string, []any) {
		return `SELECT coalesce(country,'?') AS country, count(*) AS hits FROM threats WHERE ts >= ? GROUP BY 1 ORDER BY 2 DESC LIMIT 12`, []any{c.start}
	},
	"threat_list": func(c qctx) (string, []any) {
		return `SELECT epoch_ms(ts) AS t, signature, category, action, src_ip, country, dst_ip, dst_port, proto
			FROM threats WHERE ts >= ? ORDER BY ts DESC LIMIT 200`, []any{c.start}
	},
	"fw_timeline": func(c qctx) (string, []any) {
		return `SELECT ` + bucket(c, "hour") + ` AS t, coalesce(sum(n) FILTER (WHERE action='blocked'),0) AS blocked,
			coalesce(sum(n) FILTER (WHERE action IS DISTINCT FROM 'blocked'),0) AS other
			FROM syslog_hourly WHERE hour >= ? AND kind='firewall' GROUP BY 1 ORDER BY 1`, []any{c.start}
	},
	"fw_sources": func(c qctx) (string, []any) {
		return `SELECT src, count(*) AS hits, count(*) FILTER (WHERE action='blocked') AS blocked, count(DISTINCT dport) AS ports
			FROM syslog WHERE ts >= ? AND kind='firewall' GROUP BY src ORDER BY hits DESC LIMIT 20`, []any{c.start}
	},
	"fw_ports": func(c qctx) (string, []any) {
		return `SELECT dport, any_value(proto) AS proto, sum(n) AS hits FROM syslog_hourly WHERE hour >= ? AND kind='firewall' AND dport IS NOT NULL
			GROUP BY dport ORDER BY hits DESC LIMIT 20`, []any{c.start}
	},
	"fw_rules": func(c qctx) (string, []any) {
		return `SELECT coalesce(rule,'?') AS rule, any_value(action) AS action, sum(n) AS hits FROM syslog_hourly
			WHERE hour >= ? AND kind='firewall' GROUP BY 1 ORDER BY 3 DESC LIMIT 20`, []any{c.start}
	},
	"syslog_latest": func(c qctx) (string, []any) {
		cond, args := "", []any{}
		if c.q != "" {
			cond = " AND (msg ILIKE ? OR src ILIKE ? OR dst ILIKE ? OR name ILIKE ?)"
			args = []any{like(c.q), like(c.q), like(c.q), like(c.q)}
		}
		return `SELECT epoch_ms(ts) AS t, kind, coalesce(name, rule) AS what, action, src, dst, dport, proto, msg
			FROM syslog WHERE ts >= ?` + cond + ` ORDER BY ts DESC LIMIT 300`, cat([]any{c.start}, args)
	},
	"events": func(c qctx) (string, []any) {
		cond, args := "", []any{}
		if c.q != "" {
			cond, args = " AND msg ILIKE ?", []any{like(c.q)}
		}
		return `SELECT epoch_ms(ts) AS t, source, key, subsystem, msg FROM events WHERE ts >= ?` + cond + ` ORDER BY ts DESC LIMIT 300`, cat([]any{c.start}, args)
	},
}

func topDomains(c qctx, blocked bool) (string, []any) {
	w, a := pf(c)
	cond := "status <> 'blocked'"
	if blocked {
		cond = "status = 'blocked'"
	}
	return `SELECT root AS domain, sum(n) AS queries, max(tracker) AS tracker FROM dns_daily_domain
		WHERE day >= CAST(? AS DATE) AND ` + cond + w + ` GROUP BY root ORDER BY queries DESC LIMIT 25`, cat([]any{c.start}, a)
}

// Query runs one of the named queries with the UI's parameters.
func (e *Engine) Query(name string, p map[string]any) (map[string]any, error) {
	fn, ok := queries[name]
	if !ok {
		return nil, errors.New("unknown query")
	}
	days, ok := Ranges[str(p["range"])]
	if !ok {
		days = 7
	}
	c := qctx{start: utcnow().Add(-time.Duration(days) * 24 * time.Hour), bucket: "day",
		profile: str(p["profile"]), device: str(p["device"]), mac: strings.ToLower(str(p["mac"])), q: strings.TrimSpace(str(p["q"])), status: str(p["status"])}
	if days <= 7 {
		c.bucket = "hour"
	}
	q, args := fn(c)
	cols, rows, err := e.table(q, 0, args...)
	if err != nil {
		return nil, cleanErr(err)
	}
	return map[string]any{"columns": cols, "rows": rows}, nil
}

// Meta is everything the People / Categories editors need.
func (e *Engine) Meta() (map[string]any, error) {
	if e.db == nil {
		return nil, errors.New("analytics isn't running")
	}
	since := utcnow().Add(-30 * 24 * time.Hour)
	dns, _ := e.Rows(`SELECT device, any_value(local_ip) AS local_ip, any_value(device_model) AS model, count(*) AS queries, max(ts) AS last
		FROM dnsq WHERE ts >= ? GROUP BY device ORDER BY 4 DESC LIMIT 400`, since)
	if len(dns) == 0 {
		dns, _ = e.Rows("SELECT device, sum(n) AS queries FROM dns_hourly_device WHERE hour >= ? GROUP BY 1 ORDER BY 2 DESC LIMIT 400", since)
	}
	clients, _ := e.Rows("SELECT mac, name, hostname, ip, vendor, network, last_seen FROM clients ORDER BY last_seen DESC NULLS LAST LIMIT 1000")
	rules, _ := e.Rows("SELECT pattern, category, created FROM category_rules ORDER BY category, pattern")
	profiles := map[string]string{}
	if rs, err := e.Rows("SELECT id, name FROM nextdns_profiles"); err == nil {
		for _, p := range rs {
			profiles[str(p["id"])] = str(p["name"])
		}
	}
	return map[string]any{"people": e.People(), "categories": categories.Categories, "rules": nz(rules), "dns_devices": nz(dns),
		"clients": nz(clients), "timezone": e.conf().Timezone, "profiles": profiles}, nil
}

func nz(r []map[string]any) []map[string]any {
	if r == nil {
		return []map[string]any{}
	}
	return r
}
