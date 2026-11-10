package analytics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// NextDNS API base; HOMEOWL_NEXTDNS_API overrides it for tests.
var nextdnsAPI = func() string {
	if v := os.Getenv("HOMEOWL_NEXTDNS_API"); v != "" {
		return v
	}
	return "https://api.nextdns.io"
}()

var ndHTTP = &http.Client{Timeout: 60 * time.Second}

// NDSlots returns the secret slots that hold NextDNS API keys (one per NextDNS account).
func (e *Engine) NDSlots() []string {
	var slots []string
	seen := map[string]bool{}
	for _, a := range e.conf().NextDNSAccounts {
		if a.Slot != "" && !seen[a.Slot] {
			slots = append(slots, a.Slot)
			seen[a.Slot] = true
		}
	}
	if !seen["nextdns-api-key"] {
		slots = append([]string{"nextdns-api-key"}, slots...) // key saved by older versions
	}
	out := slots[:0]
	for _, s := range slots {
		if e.sec.Get(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func (e *Engine) ndGet(path string, params url.Values, key, slot string) (map[string]any, error) {
	if key == "" {
		if slot == "" {
			slot = "nextdns-api-key"
		}
		key = e.sec.Get(slot)
	}
	if key == "" {
		return nil, errors.New("no NextDNS API key saved")
	}
	u := nextdnsAPI + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", config.AppSlug+"/"+config.Version)
		resp, err := ndHTTP.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("can't reach the NextDNS API from this computer (%v) — check its internet connection and DNS", err)
			time.Sleep(2 * time.Second)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			lastErr = errors.New("NextDNS API kept refusing (rate limit)")
			time.Sleep(time.Duration(math.Min(60, 3*math.Pow(float64(attempt+1), 2))) * time.Second)
			continue
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return nil, fmt.Errorf("NextDNS refused the API key (%d) — it may have been regenerated. Paste the current key from my.nextdns.io › Account › API", resp.StatusCode)
		case resp.StatusCode == 404:
			return nil, errors.New("NextDNS says this profile doesn't exist for this API key — it belongs to another NextDNS account (add that account's key) or was deleted")
		case resp.StatusCode >= 400:
			return nil, fmt.Errorf("NextDNS API %d: %s", resp.StatusCode, truncate(string(body), 300))
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("NextDNS API: bad response: %v", err)
		}
		return out, nil
	}
	return nil, lastErr
}

func logsDesc(p map[string]any) string {
	st, _ := p["settings"].(map[string]any)
	logs, ok := st["logs"].(map[string]any)
	if !ok {
		return ""
	}
	if en, ok := logs["enabled"].(bool); ok && !en {
		return "off"
	}
	if ret, ok := logs["retention"].(float64); ok && ret > 0 {
		if ret < 86400 {
			return fmt.Sprintf("on · kept %d h", int(ret/3600))
		}
		return fmt.Sprintf("on · kept %d days", int(math.Round(ret/86400)))
	}
	return "on"
}

type NDProfile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account string `json:"account,omitempty"`
	Logs    string `json:"logs,omitempty"`
}

// ValidateNextDNSKey lists the profiles a new key can see (and so proves it works).
func (e *Engine) ValidateNextDNSKey(key string) ([]NDProfile, error) {
	j, err := e.ndGet("/profiles", nil, key, "")
	if err != nil {
		return nil, err
	}
	var out []NDProfile
	for _, x := range anyList(j["data"]) {
		if id := str(x["id"]); id != "" {
			out = append(out, NDProfile{ID: id, Name: orS(str(x["name"]), id)})
		}
	}
	return out, nil
}

// RefreshNextDNSProfiles reloads the profiles of every saved account.
func (e *Engine) RefreshNextDNSProfiles() []NDProfile {
	var out []NDProfile
	errs := map[string]string{}
	for _, slot := range e.NDSlots() {
		j, err := e.ndGet("/profiles", nil, "", slot)
		if err != nil {
			errs[slot] = err.Error()
			continue
		}
		for _, x := range anyList(j["data"]) {
			id := str(x["id"])
			if id == "" {
				continue
			}
			logs := logsDesc(x)
			if logs == "" {
				if d, err := e.ndGet("/profiles/"+id, nil, "", slot); err == nil {
					m, _ := d["data"].(map[string]any)
					logs = logsDesc(m)
				}
			}
			out = append(out, NDProfile{ID: id, Name: orS(str(x["name"]), id), Account: slot, Logs: logs})
		}
	}
	e.mu.Lock()
	e.ndErrors = errs
	e.mu.Unlock()
	if e.db != nil {
		_ = e.tx(func(tx *sql.Tx) error {
			for _, p := range out {
				if _, err := tx.Exec("INSERT OR REPLACE INTO nextdns_profiles (id, name, account, logs, checked) VALUES (?,?,?,?,?)",
					p.ID, p.Name, p.Account, nullS(p.Logs), utcnow()); err != nil {
					return err
				}
			}
			for _, slot := range e.NDSlots() { // an account that answered: unlink profiles it no longer has
				if _, failed := errs[slot]; failed {
					continue
				}
				args := []any{slot}
				q := "UPDATE nextdns_profiles SET account = NULL WHERE account = ?"
				var ids []string
				for _, p := range out {
					if p.Account == slot {
						ids = append(ids, "?")
						args = append(args, p.ID)
					}
				}
				if len(ids) > 0 {
					q += " AND id NOT IN (" + strings.Join(ids, ",") + ")"
				}
				if _, err := tx.Exec(q, args...); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return out
}

func (e *Engine) UnlinkAccount(slot string) {
	if e.db != nil {
		e.wmu.Lock()
		_, _ = e.db.Exec("UPDATE nextdns_profiles SET account = NULL WHERE account = ?", slot)
		e.wmu.Unlock()
	}
}

type target struct{ id, slot string }

func (e *Engine) ndTargets() []target {
	known := map[string]string{}
	var order []string
	if rs, err := e.db.Query("SELECT id, coalesce(account,'') FROM nextdns_profiles ORDER BY name"); err == nil {
		for rs.Next() {
			var id, acc string
			if rs.Scan(&id, &acc) == nil {
				known[id] = acc
				order = append(order, id)
			}
		}
		rs.Close()
	}
	slots := e.NDSlots()
	has := map[string]bool{}
	for _, s := range slots {
		has[s] = true
	}
	var ids []string
	c := e.conf()
	if c.NextDNSAll {
		for _, id := range order {
			if has[known[id]] {
				ids = append(ids, id)
			}
		}
	} else {
		ids = c.NextDNSProfiles
	}
	var out []target
	for _, id := range ids {
		s := known[id]
		if !has[s] {
			s = ""
			if len(slots) > 0 {
				s = slots[0]
			}
		}
		out = append(out, target{id, s})
	}
	return out
}

func (e *Engine) TargetIDs() []string {
	out := []string{}
	if e.db == nil {
		return out
	}
	for _, t := range e.ndTargets() {
		out = append(out, t.id)
	}
	return out
}

func (e *Engine) storeDNS(pid string, from, to time.Time, rows [][]any) error {
	return e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM dns_queries WHERE profile_id=? AND ts>=? AND ts<?", pid, from, to); err != nil {
			return err
		}
		if err := insert(tx, "dns_queries", []string{"ts", "profile_id", "domain", "root", "tracker", "status", "reasons", "protocol",
			"encrypted", "client_ip", "client", "device_id", "device_name", "device_model", "local_ip"}, rows); err != nil {
			return err
		}
		return rollupDNS(tx, pid, from, to)
	})
}

func rollupDNS(tx *sql.Tx, pid string, from, to time.Time) error {
	h0 := from.Truncate(time.Hour)
	h1 := to.Add(time.Hour).Truncate(time.Hour)
	d0 := time.Date(h0.Year(), h0.Month(), h0.Day(), 0, 0, 0, 0, time.UTC)
	e1 := to.Add(24 * time.Hour)
	d1 := time.Date(e1.Year(), e1.Month(), e1.Day(), 0, 0, 0, 0, time.UTC)
	for _, s := range []struct {
		q    string
		args []any
	}{
		{"DELETE FROM dns_hourly_device WHERE profile_id=? AND hour>=? AND hour<?", []any{pid, h0, h1}},
		{`INSERT INTO dns_hourly_device SELECT date_trunc('hour', ts), profile_id, ` + deviceSQL + `, status, count(*)
		  FROM dns_queries WHERE profile_id=? AND ts>=? AND ts<? GROUP BY ALL`, []any{pid, h0, h1}},
		{"DELETE FROM dns_daily_domain WHERE profile_id=? AND day>=CAST(? AS DATE) AND day<CAST(? AS DATE)", []any{pid, d0, d1}},
		{`INSERT INTO dns_daily_domain SELECT CAST(ts AS DATE), profile_id, root, status, max(tracker), count(*)
		  FROM dns_queries WHERE profile_id=? AND ts>=? AND ts<? GROUP BY CAST(ts AS DATE), profile_id, root, status`, []any{pid, d0, d1}},
		{"DELETE FROM dns_daily_dr WHERE profile_id=? AND day>=CAST(? AS DATE) AND day<CAST(? AS DATE)", []any{pid, d0, d1}},
		{"INSERT INTO dns_daily_dr " + drSelect + " AND profile_id=? AND ts>=? AND ts<? GROUP BY ALL", []any{pid, d0, d1}},
	} {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) fetchNextDNS(pid string, from, to time.Time, slot string) ([][]any, error) {
	var rows [][]any
	cursor := ""
	for {
		p := url.Values{}
		p.Set("from", from.UTC().Format("2006-01-02T15:04:05.000Z"))
		p.Set("to", to.UTC().Format("2006-01-02T15:04:05.000Z"))
		p.Set("sort", "asc")
		p.Set("limit", "1000")
		if e.conf().NextDNSRaw {
			p.Set("raw", "1")
		}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		j, err := e.ndGet("/profiles/"+pid+"/logs", p, "", slot)
		if err != nil {
			return nil, err
		}
		for _, x := range anyList(j["data"]) {
			ts, err := time.Parse(time.RFC3339Nano, str(x["timestamp"]))
			if err != nil {
				continue
			}
			ts = ts.UTC()
			if ts.Before(from) || !ts.Before(to) {
				continue
			}
			d, _ := x["device"].(map[string]any)
			var reasons []string
			for _, r := range anyList(x["reasons"]) {
				reasons = append(reasons, orS(str(r["name"]), str(r["id"])))
			}
			status := orS(str(x["status"]), "default")
			enc, _ := x["encrypted"].(bool)
			rows = append(rows, []any{ts, pid, nullS(str(x["domain"])), nullS(orS(str(x["root"]), str(x["domain"]))), nullS(str(x["tracker"])),
				status, strings.Join(reasons, ", "), nullS(str(x["protocol"])), enc, nullS(str(x["clientIp"])), nullS(str(x["client"])),
				nullS(str(d["id"])), nullS(str(d["name"])), nullS(str(d["model"])), nullS(str(d["localIp"]))})
		}
		meta, _ := j["meta"].(map[string]any)
		pg, _ := meta["pagination"].(map[string]any)
		cursor = str(pg["cursor"])
		if cursor == "" {
			return rows, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type ndJob struct {
	slot   string
	top    time.Time
	gapCur time.Time
	gapLo  time.Time
	hasGap bool
	n      int
	failed bool
}

// SyncNextDNS brings every profile up to now, then fills older history backwards one day per profile per round,
// so recent days show up within minutes even during a long first import.
func (e *Engine) SyncNextDNS() (int, error) {
	e.RefreshNextDNSProfiles()
	now := utcnow()
	back := e.conf().BackfillDays
	if back <= 0 {
		back = 90
	}
	logs := map[string]string{}
	if rs, err := e.db.Query("SELECT id, coalesce(logs,'') FROM nextdns_profiles"); err == nil {
		for rs.Next() {
			var id, l string
			if rs.Scan(&id, &l) == nil {
				logs[id] = l
			}
		}
		rs.Close()
	}
	jobs := map[string]*ndJob{}
	var order []string
	for _, t := range e.ndTargets() {
		src, gsrc := "nextdns:"+t.id, "nextdns:"+t.id+":history"
		top, hasTop := e.lastTS(src)
		j := &ndJob{slot: t.slot}
		var gc sql.NullTime
		var gn sql.NullString
		gapErr := e.db.QueryRow("SELECT last_ts, note FROM sync_state WHERE source=?", gsrc).Scan(&gc, &gn)
		hasGap := gapErr == nil && gc.Valid
		if !hasTop || (now.Sub(top) > 24*time.Hour && !hasGap) {
			lo := now.Add(-time.Duration(back) * 24 * time.Hour)
			if hasTop {
				lo = top
			}
			e.state(gsrc, true, 0, "", now, strp(lo.Format(time.RFC3339Nano)))
			gc, gn, hasGap = sql.NullTime{Time: now, Valid: true}, sql.NullString{String: lo.Format(time.RFC3339Nano), Valid: true}, true
			top = now
			e.state(src, true, 0, "", now, strp("Importing history…"))
		}
		j.top = top
		if hasGap {
			lo, err := time.Parse(time.RFC3339Nano, gn.String)
			if err == nil {
				j.hasGap, j.gapCur, j.gapLo = true, gc.Time.UTC(), lo.UTC()
			}
		}
		jobs[t.id] = j
		order = append(order, t.id)
	}
	total := 0
	// 1) recent: from each profile's newest imported time up to now
	for _, pid := range order {
		j := jobs[pid]
		for win := j.top; win.Before(now); {
			end := minT(now, win.Add(24*time.Hour))
			rows, err := e.fetchNextDNS(pid, win, end, j.slot)
			if err == nil {
				err = e.storeDNS(pid, win, end, rows)
			}
			if err != nil {
				j.failed = true
				e.state("nextdns:"+pid, false, 0, err.Error(), time.Time{}, nil)
				break
			}
			j.n += len(rows)
			total += len(rows)
			var note *string
			if j.hasGap {
				note = strp("Importing history…")
			}
			e.state("nextdns:"+pid, true, len(rows), "", end, note)
			win = end
		}
	}
	if total > 0 {
		e.afterDNS()
	}
	// 2) history: one day per profile per round, going back in time
	rounds := 0
	for {
		active := 0
		rounds++
		for _, pid := range order {
			j := jobs[pid]
			if !j.hasGap || j.failed {
				continue
			}
			active++
			start := maxT(j.gapLo, j.gapCur.Add(-24*time.Hour))
			rows, err := e.fetchNextDNS(pid, start, j.gapCur, j.slot)
			if err == nil {
				err = e.storeDNS(pid, start, j.gapCur, rows)
			}
			if err != nil {
				j.failed = true
				e.state("nextdns:"+pid, false, 0, err.Error(), time.Time{}, nil)
				continue
			}
			j.n += len(rows)
			total += len(rows)
			j.gapCur = start
			gsrc := "nextdns:" + pid + ":history"
			if !start.After(j.gapLo) {
				e.wmu.Lock()
				_, _ = e.db.Exec("DELETE FROM sync_state WHERE source=?", gsrc)
				e.wmu.Unlock()
				note := ""
				if logs[pid] == "off" {
					note = "Logging is OFF in this NextDNS profile (my.nextdns.io › this profile › Settings › Logs) — nothing to import"
				} else if j.n == 0 && e.count("SELECT count(*) FROM (SELECT 1 FROM dns_queries WHERE profile_id=? LIMIT 1)", pid) == 0 {
					note = "NextDNS returned no queries for this profile — check that Logs are enabled and that devices use this profile"
				}
				e.state("nextdns:"+pid, true, len(rows), "", time.Time{}, &note)
				j.hasGap = false
			} else {
				e.state(gsrc, true, 0, "", start, strp(j.gapLo.Format(time.RFC3339Nano)))
				e.state("nextdns:"+pid, true, len(rows), "", time.Time{}, strp(fmt.Sprintf("Importing history… back to %s (newest first)", start.Format("2006-01-02"))))
			}
		}
		if active == 0 {
			break
		}
		if total > 0 && rounds%5 == 0 {
			e.afterDNS()
		}
	}
	if total > 0 {
		e.afterDNS()
	}
	log.Printf("analytics: NextDNS import added %d queries", total)
	return total, nil
}

func (e *Engine) afterDNS() {
	e.ApplyLocalTime(false)
	_, _ = e.RefreshCategories(false)
}

// ---- small helpers for decoded JSON

func anyList(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case float64:
		if t == math.Trunc(t) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprint(t)
	}
	return fmt.Sprint(v)
}

func orS(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func nullS(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func minT(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxT(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// HasLegacyKey reports whether a NextDNS key is stored in the original slot (from older versions).
func (e *Engine) HasLegacyKey() bool { return e.sec.Get("nextdns-api-key") != "" }
