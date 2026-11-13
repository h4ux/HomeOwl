package analytics

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/apps"
)

// Explore: cross-filtered drill-down. One request carries the filters and the views the page needs; every view
// applies all filters, so clicking a bar narrows everything else on the page.
//
//	dnsq  raw NextDNS queries (kept raw_days) with device / category / person attached
//	dnsd  daily per-device per-domain summary (kept forever) — used for long ranges
//	trafq hourly UniFi traffic per client, with person attached

var filterKeys = []string{"profile", "person", "device", "category", "root", "domain", "status", "hour", "dow", "q", "lfrom", "lto", "app"}
var rawOnly = []string{"domain", "hour", "dow", "lfrom", "lto"}
var sorts = map[string]string{"t": "ts", "device": "device", "domain": "domain", "status": "status", "category": "category", "person": "person_id"}

const slot = "epoch(ts)::BIGINT // 300" // 5-minute activity slots → "active time" estimate

type where struct {
	c []string
	a []any
}

func newWhere() *where                        { return &where{c: []string{"true"}} }
func (w *where) add(cond string, args ...any) { w.c = append(w.c, cond); w.a = append(w.a, args...) }
func (w *where) sql() string                  { return strings.Join(w.c, " AND ") }
func (w *where) clone() *where {
	return &where{c: append([]string{}, w.c...), a: append([]any{}, w.a...)}
}

func parseLocal(s string) (time.Time, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "T", " ")
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type ExploreRequest struct {
	Range   string         `json:"range"`
	Filters map[string]any `json:"filters"`
	Views   []string       `json:"views"`
	Raw     map[string]any `json:"raw"`
	Page    map[string]any `json:"page"`
}

type xctx struct {
	e            *Engine
	f            map[string]any
	useRaw       bool
	bucket       string
	start        time.Time
	lfrom, lto   time.Time
	hasLF, hasLT bool
	req          ExploreRequest
}

func cleanFilters(in map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range filterKeys {
		v, ok := in[k]
		if !ok || v == nil || v == "" {
			continue
		}
		if k == "hour" || k == "dow" {
			n, err := strconv.Atoi(str(v))
			if err != nil || (k == "hour" && (n < 0 || n > 23)) || (k == "dow" && (n < 1 || n > 7)) {
				continue
			}
			out[k] = n
			continue
		}
		out[k] = truncate(str(v), 300)
	}
	return out
}

func (e *Engine) Explore(r ExploreRequest) (map[string]any, error) {
	if e.db == nil {
		return nil, errors.New("analytics isn't set up yet")
	}
	f := cleanFilters(r.Filters)
	days, ok := Ranges[r.Range]
	if !ok {
		r.Range, days = "7d", 7
	}
	rawDays := e.conf().RawDays
	now := utcnow()
	x := &xctx{e: e, f: f, req: r, start: now.Add(-time.Duration(days) * 24 * time.Hour)}
	x.lfrom, x.hasLF = parseLocal(str(f["lfrom"]))
	x.lto, x.hasLT = parseLocal(str(f["lto"]))
	if x.hasLF {
		x.start = maxT(x.start, x.lfrom.Add(-24*time.Hour)) // coarse UTC bound; the exact bound uses local time
	}
	x.useRaw = days <= rawDays
	for _, k := range rawOnly {
		if _, ok := f[k]; ok {
			x.useRaw = true
		}
	}
	var rawFrom sql.NullTime
	_ = e.db.QueryRow("SELECT min(ts) FROM dns_queries").Scan(&rawFrom)
	span := float64(days)
	switch {
	case x.hasLF && x.hasLT:
		span = maxF(1.0/24, x.lto.Sub(x.lfrom).Hours()/24)
	case rawFrom.Valid && x.useRaw:
		span = minF(float64(days), maxF(1, now.Sub(maxT(rawFrom.Time.UTC(), x.start)).Hours()/24))
	case !x.useRaw:
		var d0 sql.NullTime
		if e.db.QueryRow("SELECT min(day) FROM dns_daily_dr").Scan(&d0) == nil && d0.Valid {
			span = minF(float64(days), maxF(1, now.Sub(maxT(d0.Time.UTC(), x.start)).Hours()/24+1))
		}
	}
	x.bucket = "week"
	if span <= 2 {
		x.bucket = "hour"
	} else if span <= 95 {
		x.bucket = "day"
	}
	src := "summary"
	if x.useRaw {
		src = "raw"
	}
	var rf any
	if rawFrom.Valid {
		rf = rawFrom.Time.UnixMilli()
	}
	out := map[string]any{"source": src, "bucket": x.bucket, "filters": f, "range": r.Range, "raw_days": rawDays, "raw_from": rf,
		"clipped": x.useRaw && rawFrom.Valid && rawFrom.Time.After(x.start.Add(6*time.Hour)) && !x.hasLF}
	for i, v := range r.Views {
		if i >= 20 {
			break
		}
		fn, ok := viewFns[v]
		if !ok {
			continue
		}
		res, err := fn(x)
		if err != nil {
			out[v] = map[string]any{"error": cleanErr(err).Error()}
		} else {
			out[v] = res
		}
	}
	return out, nil
}

func (x *xctx) rawWhere(skip ...string) *where {
	sk := map[string]bool{}
	for _, s := range skip {
		sk[s] = true
	}
	w := newWhere()
	w.add("ts >= ?", x.start)
	if x.hasLF {
		w.add("lts >= ?", x.lfrom)
	}
	if x.hasLT {
		w.add("lts < ?", x.lto)
	}
	for _, kc := range [][2]string{{"profile", "profile_id"}, {"person", "person_id"}, {"device", "device"}, {"category", "category"},
		{"root", "root"}, {"domain", "domain"}, {"status", "status"}} {
		v, ok := x.f[kc[0]]
		if !ok || sk[kc[0]] {
			continue
		}
		if kc[0] == "person" && v == "__none__" {
			w.add("person_id IS NULL")
		} else {
			w.add(kc[1]+" = ?", v)
		}
	}
	if v, ok := x.f["hour"]; ok && !sk["hour"] {
		w.add("hour(lts) = ?", v)
	}
	if v, ok := x.f["dow"]; ok && !sk["dow"] {
		w.add("isodow(lts) = ?", v)
	}
	if q, ok := x.f["q"]; ok {
		l := like(str(q))
		w.add("(domain ILIKE ? OR device ILIKE ? OR local_ip ILIKE ?)", l, l, l)
	}
	appWhere(w, x.f, sk)
	return w
}

// appWhere narrows to one app's domains (internal/apps).
func appWhere(w *where, f map[string]any, sk map[string]bool) {
	id, ok := f["app"]
	if !ok || sk["app"] {
		return
	}
	a, ok := apps.Get(str(id))
	if !ok {
		w.add("false")
		return
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(a.Roots)), ",")
	args := make([]any, len(a.Roots))
	for i, r := range a.Roots {
		args[i] = r
	}
	w.add("root IN ("+ph+")", args...)
}

// appsJoin maps root domains to apps, for SQL (the catalog is constants; quotes are escaped anyway).
func appsJoin() string {
	var b strings.Builder
	b.WriteString("(VALUES ")
	for i, p := range apps.Pairs() {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("('" + strings.ReplaceAll(p[0], "'", "''") + "','" + strings.ReplaceAll(p[1], "'", "''") + "')")
	}
	b.WriteString(") AS apps(app_root, app)")
	return b.String()
}

// vByApp: which apps were used (by their domains), how much, on how many devices, first and last seen.
func vByApp(x *xctx) (any, error) {
	rows, err := byAppRows(x)
	for _, r := range rows {
		if a, ok := apps.Get(str(r["key"])); ok {
			r["name"], r["category"] = a.Name, a.Category
		}
	}
	return rows, err
}

func byAppRows(x *xctx) ([]map[string]any, error) {
	if x.useRaw {
		w := x.rawWhere("app")
		return x.e.Rows(fmt.Sprintf(`SELECT app AS key, count(*) AS total, count(*) FILTER (WHERE status='blocked') AS blocked,
			count(DISTINCT %s) * 5 AS active_min, count(DISTINCT device) AS devices, count(DISTINCT person_id) AS people,
			min(ts) AS first, max(ts) AS last, count(DISTINCT CAST(lts AS DATE)) AS days
			FROM dnsq JOIN %s ON dnsq.root = apps.app_root WHERE %s GROUP BY 1 ORDER BY total DESC LIMIT 100`, slot, appsJoin(), w.sql()), w.a...)
	}
	w := x.sumWhere("app")
	return x.e.Rows(fmt.Sprintf(`SELECT app AS key, sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked,
		NULL AS active_min, count(DISTINCT device) AS devices, count(DISTINCT person_id) AS people, min(day) AS first, max(day) AS last,
		count(DISTINCT day) AS days FROM dnsd JOIN %s ON dnsd.root = apps.app_root WHERE %s GROUP BY 1 ORDER BY total DESC LIMIT 100`, appsJoin(), w.sql()), w.a...)
}

func (x *xctx) sumWhere(skip ...string) *where {
	sk := map[string]bool{}
	for _, s := range skip {
		sk[s] = true
	}
	w := newWhere()
	w.add("day >= CAST(? AS DATE)", x.start)
	for _, kc := range [][2]string{{"profile", "profile_id"}, {"person", "person_id"}, {"device", "device"}, {"category", "category"},
		{"root", "root"}, {"status", "status"}} {
		v, ok := x.f[kc[0]]
		if !ok || sk[kc[0]] {
			continue
		}
		if kc[0] == "person" && v == "__none__" {
			w.add("person_id IS NULL")
		} else {
			w.add(kc[1]+" = ?", v)
		}
	}
	if q, ok := x.f["q"]; ok {
		l := like(str(q))
		w.add("(root ILIKE ? OR device ILIKE ?)", l, l)
	}
	appWhere(w, x.f, sk)
	return w
}

var viewFns = map[string]func(*xctx) (any, error){
	"summary": vSummary, "timeline": vTimeline, "heatmap": vHeatmap, "by_device": vByDevice, "by_domain": vByDomain,
	"by_category": vByCategory, "by_person": vByPerson, "by_profile": vByProfile, "by_status": vByStatus, "raw": vRaw, "traffic": vTraffic, "by_app": vByApp,
}

func first(r []map[string]any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if len(r) == 0 {
		return map[string]any{}, nil
	}
	return r[0], nil
}

func vSummary(x *xctx) (any, error) {
	if x.useRaw {
		w := x.rawWhere()
		return first(x.e.Rows(`SELECT count(*) AS total, count(*) FILTER (WHERE status='blocked') AS blocked, count(DISTINCT device) AS devices,
			count(DISTINCT root) AS domains, count(DISTINCT `+slot+`) * 5 AS active_min, count(DISTINCT CAST(lts AS DATE)) AS active_days,
			min(ts) AS first, max(ts) AS last FROM dnsq WHERE `+w.sql(), w.a...))
	}
	w := x.sumWhere()
	return first(x.e.Rows(`SELECT sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked, count(DISTINCT device) AS devices,
		count(DISTINCT root) AS domains, NULL AS active_min, count(DISTINCT day) AS active_days,
		min(day) AS first, max(day) AS last FROM dnsd WHERE `+w.sql(), w.a...))
}

func vTimeline(x *xctx) (any, error) {
	b := x.bucket
	if x.useRaw {
		w := x.rawWhere()
		return x.e.Rows(fmt.Sprintf(`SELECT strftime(date_trunc('%[1]s', lts), '%%Y-%%m-%%d %%H:%%M') AS k,
			strftime(date_trunc('%[1]s', lts) + INTERVAL 1 %[1]s, '%%Y-%%m-%%d %%H:%%M') AS k2,
			count(*) AS total, count(*) FILTER (WHERE status='blocked') AS blocked, count(DISTINCT %[2]s) * 5 AS active_min
			FROM dnsq WHERE %[3]s AND lts IS NOT NULL GROUP BY 1, 2 ORDER BY 1`, b, slot, w.sql()), w.a...)
	}
	if b == "hour" {
		b = "day"
	}
	w := x.sumWhere()
	return x.e.Rows(fmt.Sprintf(`SELECT strftime(date_trunc('%[1]s', day), '%%Y-%%m-%%d %%H:%%M') AS k,
		strftime(date_trunc('%[1]s', day) + INTERVAL 1 %[1]s, '%%Y-%%m-%%d %%H:%%M') AS k2,
		sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked, NULL AS active_min
		FROM dnsd WHERE %[2]s GROUP BY 1, 2 ORDER BY 1`, b, w.sql()), w.a...)
}

// weekdayCounts: how many Mondays, Tuesdays… are in the period, to turn totals into per-day averages.
func weekdayCounts(lo, hi time.Time) map[string]int {
	out := map[string]int{}
	for d := 1; d <= 7; d++ {
		out[strconv.Itoa(d)] = 0
	}
	if lo.IsZero() {
		return out
	}
	for d := time.Date(lo.Year(), lo.Month(), lo.Day(), 0, 0, 0, 0, time.UTC); !d.After(hi); d = d.AddDate(0, 0, 1) {
		wd := int(d.Weekday())
		if wd == 0 {
			wd = 7
		}
		out[strconv.Itoa(wd)]++
	}
	return out
}

func (x *xctx) span(q string, args []any) (time.Time, time.Time) {
	var lo, hi sql.NullTime
	_ = x.e.db.QueryRow(q, args...).Scan(&lo, &hi)
	if !lo.Valid {
		return time.Time{}, time.Time{}
	}
	return lo.Time.UTC(), hi.Time.UTC()
}

func vHeatmap(x *xctx) (any, error) {
	_, hasCat := x.f["category"]
	_, hasRoot := x.f["root"]
	if x.useRaw || hasCat || hasRoot {
		w := x.rawWhere("hour", "dow")
		cells, err := x.e.Rows(`SELECT isodow(lts) AS dow, hour(lts) AS hour, count(*) AS n, count(DISTINCT `+slot+`) * 5 AS active_min
			FROM dnsq WHERE `+w.sql()+` AND lts IS NOT NULL GROUP BY 1, 2`, w.a...)
		if err != nil {
			return nil, err
		}
		lo, hi := x.span("SELECT min(lts), max(lts) FROM dnsq WHERE "+w.sql(), w.a)
		return map[string]any{"cells": nz(cells), "weekdays": weekdayCounts(lo, hi), "source": "raw"}, nil
	}
	w := newWhere()
	w.add("h.hour >= ?", x.start)
	for _, kc := range [][2]string{{"profile", "profile_id"}, {"device", "device"}, {"status", "status"}} {
		if v, ok := x.f[kc[0]]; ok {
			w.add("h."+kc[1]+" = ?", v)
		}
	}
	if v, ok := x.f["person"]; ok {
		if v == "__none__" {
			w.add("h.person_id IS NULL")
		} else {
			w.add("h.person_id = ?", v)
		}
	}
	base := "FROM dnsh h JOIN tz_offsets o ON o.hour = h.hour WHERE " + w.sql()
	cells, err := x.e.Rows(`SELECT isodow(h.hour + to_minutes(o.off)) AS dow, hour(h.hour + to_minutes(o.off)) AS hour, sum(h.n) AS n,
		count(DISTINCT h.hour) * 60 AS active_min `+base+` GROUP BY 1, 2`, w.a...)
	if err != nil {
		return nil, err
	}
	lo, hi := x.span("SELECT min(h.hour + to_minutes(o.off)), max(h.hour + to_minutes(o.off)) "+base, w.a)
	return map[string]any{"cells": nz(cells), "weekdays": weekdayCounts(lo, hi), "source": "hourly"}, nil
}

func (x *xctx) group(keySQL, keyName, extra string, limit int) ([]map[string]any, error) {
	skip := map[string]string{"device": "device", "root": "root", "category": "category", "person_id": "person", "profile_id": "profile", "status": "status"}[keyName]
	if n, err := strconv.Atoi(str(x.req.Page[keyName+"_limit"])); err == nil && n > 0 {
		limit = n
	}
	limit = max(1, min(500, limit))
	if x.useRaw {
		w := x.rawWhere(skip)
		r, err := x.e.Rows(fmt.Sprintf(`SELECT %s AS key, count(*) AS total, count(*) FILTER (WHERE status='blocked') AS blocked,
			count(DISTINCT %s) * 5 AS active_min, max(ts) AS last%s
			FROM dnsq WHERE %s GROUP BY 1 ORDER BY total DESC LIMIT %d`, keySQL, slot, extra, w.sql(), limit), w.a...)
		return nz(r), err
	}
	w := x.sumWhere(skip)
	active := "NULL"
	if keyName == "root" {
		active = "sum(slots) * 5"
	}
	r, err := x.e.Rows(fmt.Sprintf(`SELECT %s AS key, sum(n) AS total, coalesce(sum(n) FILTER (WHERE status='blocked'),0) AS blocked,
		%s AS active_min, max(day) AS last%s
		FROM dnsd WHERE %s GROUP BY 1 ORDER BY total DESC LIMIT %d`, keySQL, active, extra, w.sql(), limit), w.a...)
	return nz(r), err
}

func vByDevice(x *xctx) (any, error) {
	return x.group("device", "device", ", any_value(person_id) AS person_id, count(DISTINCT root) AS domains", 60)
}

func vByDomain(x *xctx) (any, error) {
	extra := ", any_value(category) AS category, count(DISTINCT device) AS devices"
	if x.useRaw {
		extra += ", max(tracker) AS tracker"
	}
	return x.group("root", "root", extra, 60)
}

func vByCategory(x *xctx) (any, error) {
	return x.group("category", "category", ", count(DISTINCT root) AS domains, count(DISTINCT device) AS devices", 40)
}

func (e *Engine) names(q string) map[string]string {
	out := map[string]string{}
	if r, err := e.Rows(q); err == nil {
		for _, m := range r {
			out[str(m["id"])] = str(m["name"])
		}
	}
	return out
}

func vByPerson(x *xctx) (any, error) {
	r, err := x.group("coalesce(person_id, '__none__')", "person_id", ", count(DISTINCT device) AS devices", 50)
	if err != nil {
		return nil, err
	}
	names := x.e.names("SELECT id, name FROM people")
	for _, i := range r {
		k := str(i["key"])
		switch {
		case names[k] != "":
			i["name"] = names[k]
		case k == "__none__":
			i["name"] = "Not assigned"
		default:
			i["name"] = k
		}
	}
	return r, nil
}

func vByProfile(x *xctx) (any, error) {
	r, err := x.group("profile_id", "profile_id", "", 20)
	if err != nil {
		return nil, err
	}
	names := x.e.names("SELECT id, name FROM nextdns_profiles")
	for _, i := range r {
		i["name"] = orS(names[str(i["key"])], str(i["key"]))
	}
	return r, nil
}

func vByStatus(x *xctx) (any, error) { return x.group("status", "status", "", 10) }

func vRaw(x *xctx) (any, error) {
	p := x.req.Raw
	lim, _ := strconv.Atoi(str(p["limit"]))
	if lim <= 0 {
		lim = 100
	}
	lim = min(10000, lim)
	off, _ := strconv.Atoi(str(p["offset"]))
	off = max(0, off)
	sort, ok := sorts[str(p["sort"])]
	if !ok {
		sort = "ts"
	}
	dir := "DESC"
	if strings.EqualFold(str(p["dir"]), "asc") {
		dir = "ASC"
	}
	w := x.rawWhere()
	total := x.e.count("SELECT count(*) FROM dnsq WHERE "+w.sql(), w.a...)
	items, err := x.e.Rows(fmt.Sprintf(`SELECT epoch_ms(ts) AS t, strftime(lts, '%%Y-%%m-%%d %%H:%%M:%%S') AS local, device, person_id, domain, root, category,
		status, reasons, tracker, protocol, profile_id, local_ip, device_model
		FROM dnsq WHERE %s ORDER BY %s %s, ts DESC LIMIT %d OFFSET %d`, w.sql(), sort, dir, lim, off), w.a...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"total": total, "offset": off, "limit": lim, "items": nz(items)}, nil
}

// ---- traffic (UniFi hourly bytes per client) — follows person / device / time filters

func (x *xctx) trafficWhere() *where {
	w := newWhere()
	w.add("hour >= ?", x.start)
	if x.hasLF {
		w.add("lhour >= ?", x.lfrom)
	}
	if x.hasLT {
		w.add("lhour < ?", x.lto)
	}
	if v, ok := x.f["person"]; ok {
		if v == "__none__" {
			w.add("person_id IS NULL")
		} else {
			w.add("person_id = ?", v)
		}
	}
	if v, ok := x.f["device"]; ok {
		w.add("mac IN (SELECT mac FROM clients WHERE ? IN (name, hostname, ip, fixed_ip))", v)
	}
	return w
}

func vTraffic(x *xctx) (any, error) {
	w := x.trafficWhere()
	hw := w.clone()
	if v, ok := x.f["hour"]; ok {
		hw.add("hour(lhour) = ?", v)
	}
	if v, ok := x.f["dow"]; ok {
		hw.add("isodow(lhour) = ?", v)
	}
	b := x.bucket
	tot, err := x.e.Rows("SELECT coalesce(sum(rx),0) AS rx, coalesce(sum(tx),0) AS tx, count(DISTINCT mac) AS clients, count(*) FILTER (WHERE rx+tx > 1e6) AS active_hours FROM trafq WHERE "+hw.sql(), hw.a...)
	if err != nil {
		return nil, err
	}
	tl, _ := x.e.Rows(fmt.Sprintf(`SELECT strftime(date_trunc('%[1]s', lhour), '%%Y-%%m-%%d %%H:%%M') AS k, strftime(date_trunc('%[1]s', lhour) + INTERVAL 1 %[1]s, '%%Y-%%m-%%d %%H:%%M') AS k2,
		sum(rx) AS rx, sum(tx) AS tx FROM trafq WHERE %[2]s AND lhour IS NOT NULL GROUP BY 1, 2 ORDER BY 1`, b, hw.sql()), hw.a...)
	hm, _ := x.e.Rows(`SELECT isodow(lhour) AS dow, hour(lhour) AS hour, sum(rx + tx) AS bytes, count(*) FILTER (WHERE rx+tx > 1e6) AS active_hours
		FROM trafq WHERE `+w.sql()+` AND lhour IS NOT NULL GROUP BY 1, 2`, w.a...)
	cl, _ := x.e.Rows(`SELECT mac, any_value(client) AS client, any_value(network) AS network, any_value(person_id) AS person_id,
		sum(rx) AS rx, sum(tx) AS tx, sum(rx + tx) AS total FROM trafq WHERE `+hw.sql()+` GROUP BY mac ORDER BY total DESC LIMIT 40`, hw.a...)
	lo, hi := x.span("SELECT min(lhour), max(lhour) FROM trafq WHERE "+w.sql(), w.a)
	var ignored []string
	for _, k := range []string{"category", "root", "domain", "status", "profile", "q", "app"} {
		if _, ok := x.f[k]; ok {
			ignored = append(ignored, k)
		}
	}
	var note any
	if len(ignored) > 0 {
		note = "Traffic can't be split by " + strings.Join(ignored, ", ") + " — showing all of this device/person's traffic."
	}
	sum := map[string]any{}
	if len(tot) > 0 {
		sum = tot[0]
	}
	return map[string]any{"summary": sum, "timeline": nz(tl), "heatmap": nz(hm), "clients": nz(cl), "note": note, "weekdays": weekdayCounts(lo, hi)}, nil
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
