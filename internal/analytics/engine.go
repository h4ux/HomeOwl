// Package analytics imports NextDNS query logs, UniFi traffic/sessions/events/threats and the gateway's
// syslog into an embedded DuckDB database, keeps hourly/daily summaries forever and raw logs for a
// configurable number of days, and answers the dashboards (named queries, Explore drill-down, SQL console).
package analytics

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/duckdb/duckdb-go/v2" // embedded DuckDB (statically linked)

	"github.com/h4ux/homeowl/internal/config"
)

// UniFi is the subset of the UniFi client the importer needs.
type UniFi interface {
	Request(method, path string, body any) (int, any)
	SitePath(path string, v2 bool) string
	Connected() bool
	Host() string
}

type Secrets interface {
	Get(slot string) string
	Set(slot, value string) error
	Delete(slot string)
}

var intervals = map[string]time.Duration{
	"nextdns": 5 * time.Minute, "unifi": 30 * time.Minute, "dpi": 6 * time.Hour, "rollup": 10 * time.Minute, "maintenance": 24 * time.Hour,
}

type Engine struct {
	cfg    *config.Store
	dir    string
	DBPath string
	db     *sql.DB
	uni    UniFi
	sec    Secrets

	wmu sync.Mutex // one writer at a time

	mu       sync.Mutex
	due      map[string]bool
	running  map[string]bool
	ndErrors map[string]string
	started  bool
	openErr  string

	sys     syslogState
	tzCache *time.Location
}

func New(cfg *config.Store, dataDir string, uni UniFi, sec Secrets) *Engine {
	return &Engine{cfg: cfg, dir: dataDir, DBPath: filepath.Join(dataDir, "analytics.duckdb"), uni: uni, sec: sec,
		due: map[string]bool{}, running: map[string]bool{}, ndErrors: map[string]string{}}
}

func (e *Engine) conf() config.Analytics { return e.cfg.Get().Analytics }

func (e *Engine) Open() bool { return e.db != nil }

const schema = `
CREATE TABLE IF NOT EXISTS dns_queries(ts TIMESTAMP, profile_id VARCHAR, domain VARCHAR, root VARCHAR, tracker VARCHAR,
  status VARCHAR, reasons VARCHAR, protocol VARCHAR, encrypted BOOLEAN, client_ip VARCHAR, client VARCHAR,
  device_id VARCHAR, device_name VARCHAR, device_model VARCHAR, local_ip VARCHAR);
CREATE TABLE IF NOT EXISTS dns_hourly_device(hour TIMESTAMP, profile_id VARCHAR, device VARCHAR, status VARCHAR, n BIGINT);
CREATE TABLE IF NOT EXISTS dns_daily_domain(day DATE, profile_id VARCHAR, root VARCHAR, status VARCHAR, tracker VARCHAR, n BIGINT);
CREATE TABLE IF NOT EXISTS client_hourly(hour TIMESTAMP, mac VARCHAR, rx BIGINT, tx BIGINT);
CREATE TABLE IF NOT EXISTS site_hourly(hour TIMESTAMP, wan_rx BIGINT, wan_tx BIGINT, clients INTEGER);
CREATE TABLE IF NOT EXISTS sessions(id VARCHAR, mac VARCHAR, start_ts TIMESTAMP, stop_ts TIMESTAMP, duration INTEGER,
  rx BIGINT, tx BIGINT, ap_mac VARCHAR, ip VARCHAR, hostname VARCHAR, wired BOOLEAN);
CREATE TABLE IF NOT EXISTS events(id VARCHAR, ts TIMESTAMP, source VARCHAR, key VARCHAR, subsystem VARCHAR, msg VARCHAR, raw VARCHAR);
CREATE TABLE IF NOT EXISTS threats(id VARCHAR, ts TIMESTAMP, signature VARCHAR, category VARCHAR, action VARCHAR,
  src_ip VARCHAR, dst_ip VARCHAR, src_port INTEGER, dst_port INTEGER, proto VARCHAR, country VARCHAR, raw VARCHAR);
CREATE TABLE IF NOT EXISTS syslog(ts TIMESTAMP, host VARCHAR, kind VARCHAR, severity INTEGER, name VARCHAR, rule VARCHAR,
  action VARCHAR, src VARCHAR, dst VARCHAR, sport INTEGER, dport INTEGER, proto VARCHAR, iface_in VARCHAR,
  iface_out VARCHAR, category VARCHAR, msg VARCHAR);
CREATE TABLE IF NOT EXISTS syslog_hourly(hour TIMESTAMP, kind VARCHAR, action VARCHAR, rule VARCHAR, dport INTEGER, proto VARCHAR, n BIGINT);
CREATE TABLE IF NOT EXISTS clients(mac VARCHAR PRIMARY KEY, name VARCHAR, hostname VARCHAR, vendor VARCHAR, network VARCHAR,
  ip VARCHAR, fixed_ip VARCHAR, wired BOOLEAN, first_seen TIMESTAMP, last_seen TIMESTAMP, updated TIMESTAMP);
CREATE TABLE IF NOT EXISTS dpi_snapshots(ts TIMESTAMP, mac VARCHAR, cat INTEGER, app INTEGER, rx BIGINT, tx BIGINT);
CREATE TABLE IF NOT EXISTS nextdns_profiles(id VARCHAR PRIMARY KEY, name VARCHAR);
CREATE TABLE IF NOT EXISTS sync_state(source VARCHAR PRIMARY KEY, last_ts TIMESTAMP, last_run TIMESTAMP, ok BOOLEAN,
  rows BIGINT, error VARCHAR, note VARCHAR);
ALTER TABLE nextdns_profiles ADD COLUMN IF NOT EXISTS account VARCHAR;
ALTER TABLE nextdns_profiles ADD COLUMN IF NOT EXISTS logs VARCHAR;
ALTER TABLE nextdns_profiles ADD COLUMN IF NOT EXISTS checked TIMESTAMP;
CREATE TABLE IF NOT EXISTS dns_daily_dr(day DATE, profile_id VARCHAR, device VARCHAR, root VARCHAR, status VARCHAR, n BIGINT, slots BIGINT);
CREATE TABLE IF NOT EXISTS people(id VARCHAR PRIMARY KEY, name VARCHAR, color VARCHAR, note VARCHAR);
ALTER TABLE people ADD COLUMN IF NOT EXISTS kind VARCHAR;
CREATE TABLE IF NOT EXISTS person_devices(person_id VARCHAR, kind VARCHAR, key VARCHAR);
CREATE TABLE IF NOT EXISTS category_rules(pattern VARCHAR PRIMARY KEY, category VARCHAR, created TIMESTAMP);
CREATE TABLE IF NOT EXISTS root_category(root VARCHAR PRIMARY KEY, category VARCHAR, source VARCHAR);
CREATE TABLE IF NOT EXISTS dev_person(label VARCHAR PRIMARY KEY, person_id VARCHAR);
CREATE TABLE IF NOT EXISTS tz_offsets(hour TIMESTAMP PRIMARY KEY, off INTEGER);
ALTER TABLE dns_queries ADD COLUMN IF NOT EXISTS lts TIMESTAMP;
ALTER TABLE client_hourly ADD COLUMN IF NOT EXISTS lhour TIMESTAMP;
CREATE OR REPLACE VIEW dnsq AS SELECT q.*, coalesce(nullif(q.device_name,''), nullif(q.local_ip,''), 'Unidentified') AS device,
  coalesce(rc.category, 'Uncategorized') AS category, coalesce(p0.person_id, p1.person_id, p2.person_id, p3.person_id) AS person_id
  FROM dns_queries q LEFT JOIN root_category rc ON rc.root = q.root
  LEFT JOIN dev_person p0 ON p0.label = 'dns:' || q.profile_id || '/' || coalesce(nullif(q.device_name,''), nullif(q.local_ip,''), 'Unidentified')
  LEFT JOIN dev_person p1 ON p1.label = coalesce(nullif(q.device_name,''), nullif(q.local_ip,''), 'Unidentified')
  LEFT JOIN dev_person p2 ON p2.label = 'ip:' || q.local_ip
  LEFT JOIN dev_person p3 ON p3.label = 'profile:' || q.profile_id;
CREATE OR REPLACE VIEW dnsd AS SELECT d.*, coalesce(rc.category, 'Uncategorized') AS category,
  coalesce(p0.person_id, p1.person_id, p3.person_id) AS person_id
  FROM dns_daily_dr d LEFT JOIN root_category rc ON rc.root = d.root
  LEFT JOIN dev_person p0 ON p0.label = 'dns:' || d.profile_id || '/' || d.device
  LEFT JOIN dev_person p1 ON p1.label = d.device
  LEFT JOIN dev_person p3 ON p3.label = 'profile:' || d.profile_id;
CREATE OR REPLACE VIEW dnsh AS SELECT h.*, coalesce(p0.person_id, p1.person_id, p3.person_id) AS person_id
  FROM dns_hourly_device h
  LEFT JOIN dev_person p0 ON p0.label = 'dns:' || h.profile_id || '/' || h.device
  LEFT JOIN dev_person p1 ON p1.label = h.device
  LEFT JOIN dev_person p3 ON p3.label = 'profile:' || h.profile_id;
CREATE OR REPLACE VIEW trafq AS SELECT h.*, coalesce(c.name, c.hostname, h.mac) AS client, c.network, c.vendor, p.person_id
  FROM client_hourly h LEFT JOIN clients c ON c.mac = h.mac LEFT JOIN dev_person p ON p.label = 'mac:' || h.mac;
`

const deviceSQL = "coalesce(nullif(device_name,''), nullif(local_ip,''), 'Unidentified')"
const drSelect = "SELECT CAST(ts AS DATE), profile_id, " + deviceSQL + ", root, status, count(*), count(DISTINCT epoch(ts)::BIGINT // 300) FROM dns_queries WHERE true"

// OpenDB opens (or creates) the database. SQL gets no file or network access, which keeps the SQL console safe.
func (e *Engine) OpenDB() error {
	if e.db != nil {
		return nil
	}
	db, err := sql.Open("duckdb", e.DBPath+"?enable_external_access=false&autoinstall_known_extensions=false&autoload_known_extensions=false")
	if err == nil {
		err = db.Ping()
	}
	if err != nil && strings.Contains(err.Error(), "version") {
		// a database written by a newer DuckDB (e.g. the earlier Python version of this app): keep it aside, start fresh
		old := e.DBPath + ".from-older-version"
		_ = os.Rename(e.DBPath, old)
		_ = os.Remove(e.DBPath + ".wal")
		log.Printf("analytics: %v — moved the old database to %s and starting a new one (history is re-imported)", err, old)
		db, err = sql.Open("duckdb", e.DBPath+"?enable_external_access=false&autoinstall_known_extensions=false&autoload_known_extensions=false")
		if err == nil {
			err = db.Ping()
		}
	}
	if err != nil {
		e.openErr = err.Error()
		return err
	}
	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			e.openErr = err.Error()
			_ = db.Close()
			return fmt.Errorf("schema: %w", err)
		}
	}
	e.db = db
	e.openErr = ""
	e.migrate()
	return nil
}

func (e *Engine) migrate() {
	var a, b int64
	_ = e.db.QueryRow("SELECT count(*) FROM dns_daily_dr").Scan(&a)
	_ = e.db.QueryRow("SELECT count(*) FROM dns_queries").Scan(&b)
	if a == 0 && b > 0 {
		e.wmu.Lock()
		_, _ = e.db.Exec("INSERT INTO dns_daily_dr " + drSelect + " GROUP BY ALL")
		e.wmu.Unlock()
	}
	e.importLegacy()
	e.ApplyLocalTime(false)
	_, _ = e.RefreshCategories(false)
	e.RefreshDevPerson()
}

// importLegacy restores people, their devices and category rules exported by the installer from the earlier
// (Python) version's database, which the embedded DuckDB may be too old/new to open directly.
func (e *Engine) importLegacy() {
	path := filepath.Join(e.dir, "import-legacy.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var in struct {
		People        [][]any `json:"people"`
		PersonDevices [][]any `json:"person_devices"`
		CategoryRules [][]any `json:"category_rules"`
	}
	if json.Unmarshal(b, &in) != nil {
		return
	}
	err = e.tx(func(tx *sql.Tx) error {
		for _, r := range in.People {
			if len(r) >= 4 {
				if _, err := tx.Exec("INSERT OR REPLACE INTO people VALUES (?,?,?,?)", r[0], r[1], r[2], r[3]); err != nil {
					return err
				}
			}
		}
		for _, r := range in.PersonDevices {
			if len(r) >= 3 {
				if _, err := tx.Exec(`INSERT INTO person_devices SELECT ?, ?, ? WHERE NOT EXISTS
					(SELECT 1 FROM person_devices WHERE person_id=? AND kind=? AND key=?)`, r[0], r[1], r[2], r[0], r[1], r[2]); err != nil {
					return err
				}
			}
		}
		for _, r := range in.CategoryRules {
			if len(r) >= 2 {
				if _, err := tx.Exec("INSERT OR REPLACE INTO category_rules VALUES (?,?,?)", r[0], r[1], utcnow()); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		_ = os.Rename(path, path+".imported")
		log.Printf("analytics: restored %d people and %d category rules from the previous version", len(in.People), len(in.CategoryRules))
	}
}

// Start opens the database and runs the scheduler and syslog receiver (when analytics are enabled).
func (e *Engine) Start() {
	if !e.conf().Enabled {
		e.ApplySyslog()
		return
	}
	if err := e.OpenDB(); err != nil {
		log.Printf("analytics: %v", err)
		return
	}
	e.mu.Lock()
	first := !e.started
	e.started = true
	e.mu.Unlock()
	if first {
		go e.scheduler()
		go e.syslogFlusher()
	}
	e.ApplySyslog()
}

func (e *Engine) scheduler() {
	last := map[string]time.Time{}
	for {
		for task, every := range intervals {
			c := e.conf()
			if !c.Enabled || e.db == nil {
				continue
			}
			e.mu.Lock()
			due := e.due[task]
			e.mu.Unlock()
			if time.Since(last[task]) < every && !due {
				continue
			}
			if (task == "unifi" || task == "dpi") && !e.uni.Connected() {
				continue
			}
			if task == "nextdns" && len(e.NDSlots()) == 0 {
				continue
			}
			e.mu.Lock()
			e.due[task] = false
			e.mu.Unlock()
			last[task] = time.Now()
			go e.run(task)
		}
		time.Sleep(10 * time.Second)
	}
}

func (e *Engine) Trigger(task string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if task == "all" {
		for k := range intervals {
			e.due[k] = true
		}
	} else if _, ok := intervals[task]; ok {
		e.due[task] = true
	}
}

func (e *Engine) run(task string) {
	e.mu.Lock()
	if e.running[task] {
		e.due[task] = true // asked again while running (e.g. a new NextDNS account): run once more afterwards
		e.mu.Unlock()
		return
	}
	e.running[task] = true
	e.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			e.state(task, false, 0, fmt.Sprint("crashed: ", r), time.Time{}, nil)
		}
		e.mu.Lock()
		e.running[task] = false
		e.mu.Unlock()
	}()
	var err error
	switch task {
	case "nextdns":
		_, err = e.SyncNextDNS()
	case "unifi":
		e.SyncUniFi()
	case "dpi":
		err = e.SyncDPI()
	case "rollup":
		e.RollupSyslog(3)
	case "maintenance":
		e.Maintenance()
	}
	if err != nil {
		e.state(task, false, 0, err.Error(), time.Time{}, nil)
		log.Printf("analytics %s failed: %v", task, err)
	}
}

// state records the outcome of an import. note=nil keeps the old note on errors and clears it on success.
func (e *Engine) state(source string, ok bool, rows int, errText string, lastTS time.Time, note *string) {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	var prevTS sql.NullTime
	var prevRows sql.NullInt64
	var prevNote sql.NullString
	_ = e.db.QueryRow("SELECT last_ts, rows, note FROM sync_state WHERE source=?", source).Scan(&prevTS, &prevRows, &prevNote)
	ts := any(nil)
	if !lastTS.IsZero() {
		ts = lastTS
	} else if prevTS.Valid {
		ts = prevTS.Time
	}
	n := any(nil)
	if note != nil {
		n = *note
	} else if !ok && prevNote.Valid {
		n = prevNote.String
	}
	er := any(nil)
	if errText != "" {
		er = errText
	}
	_, _ = e.db.Exec("INSERT OR REPLACE INTO sync_state VALUES (?,?,?,?,?,?,?)", source, ts, utcnow(), ok, prevRows.Int64+int64(rows), er, n)
}

func (e *Engine) lastTS(source string) (time.Time, bool) {
	var t sql.NullTime
	if err := e.db.QueryRow("SELECT last_ts FROM sync_state WHERE source=?", source).Scan(&t); err != nil || !t.Valid {
		return time.Time{}, false
	}
	return t.Time.UTC(), true
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// insert adds rows with multi-row VALUES statements.
func insert(x execer, table string, cols []string, rows [][]any) error {
	const chunk = 300
	if len(rows) == 0 {
		return nil
	}
	ph := "(" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ")"
	for i := 0; i < len(rows); i += chunk {
		part := rows[i:min(i+chunk, len(rows))]
		args := make([]any, 0, len(part)*len(cols))
		for _, r := range part {
			args = append(args, r...)
		}
		q := "INSERT INTO " + table + " (" + strings.Join(cols, ",") + ") VALUES " + strings.TrimSuffix(strings.Repeat(ph+",", len(part)), ",")
		if _, err := x.Exec(q, args...); err != nil {
			return err
		}
	}
	return nil
}

// tx runs fn in a transaction while holding the writer lock.
func (e *Engine) tx(fn func(tx *sql.Tx) error) error {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---- reading

// Rows runs a query and returns objects with JSON-friendly values (times as epoch ms, dates as YYYY-MM-DD).
func (e *Engine) Rows(q string, args ...any) ([]map[string]any, error) {
	cols, data, err := e.table(q, 0, args...)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(data))
	for i, r := range data {
		m := make(map[string]any, len(cols))
		for j, c := range cols {
			m[c] = r[j]
		}
		out[i] = m
	}
	return out, nil
}

func (e *Engine) table(q string, limit int, args ...any) ([]string, [][]any, error) {
	if e.db == nil {
		return nil, nil, errors.New("analytics isn't running")
	}
	rows, err := e.db.Query(q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return scanAll(rows, limit)
}

func scanAll(rows *sql.Rows, limit int) ([]string, [][]any, error) {
	cols, _ := rows.Columns()
	types, _ := rows.ColumnTypes()
	var out [][]any
	for rows.Next() {
		if limit > 0 && len(out) > limit {
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		for i, v := range vals {
			vals[i] = jsonable(v, types[i].DatabaseTypeName())
		}
		out = append(out, vals)
	}
	if out == nil {
		out = [][]any{}
	}
	return cols, out, rows.Err()
}

func jsonable(v any, dbType string) any {
	switch t := v.(type) {
	case nil:
		return nil
	case time.Time:
		if dbType == "DATE" {
			return t.UTC().Format("2006-01-02")
		}
		return t.UnixMilli()
	case *big.Int:
		if t.IsInt64() {
			return t.Int64()
		}
		f, _ := new(big.Float).SetInt(t).Float64()
		return f
	case float64:
		return float64(int64(t*100+sign(t)*0.5)) / 100
	case float32:
		return float64(t)
	case []byte:
		return hex.EncodeToString(t)
	case interface{ Float64() float64 }:
		return t.Float64()
	case string, bool, int64, int32, int16, int8, int, uint64, uint32, uint16, uint8:
		return t
	}
	return fmt.Sprint(v)
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

func (e *Engine) scalar(q string, args ...any) any {
	var v any
	if e.db == nil || e.db.QueryRow(q, args...).Scan(&v) != nil {
		return nil
	}
	return v
}

func (e *Engine) count(q string, args ...any) int64 {
	var n int64
	if e.db != nil {
		_ = e.db.QueryRow(q, args...).Scan(&n)
	}
	return n
}

// ---- SQL console

var commentRE = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/`)
var readOnlyFirst = map[string]bool{"select": true, "with": true, "from": true, "describe": true, "summarize": true, "show": true,
	"explain": true, "pivot": true, "unpivot": true, "values": true, "table": true}

// RunSQL runs one read-only statement inside a transaction that is always rolled back.
func (e *Engine) RunSQL(q string, limit int) (map[string]any, error) {
	if e.db == nil {
		return nil, errors.New("analytics isn't running")
	}
	s := strings.TrimSpace(commentRE.ReplaceAllString(q, " "))
	s = strings.TrimSpace(strings.TrimRight(s, "; \n\t"))
	if s == "" {
		return nil, errors.New("empty query")
	}
	if strings.Contains(s, ";") {
		return nil, errors.New("one statement at a time, please")
	}
	first := strings.ToLower(strings.Fields(s)[0])
	if !readOnlyFirst[first] {
		return nil, errors.New("only read-only queries are allowed (SELECT, WITH, DESCRIBE, SUMMARIZE, SHOW …)")
	}
	if limit <= 0 {
		limit = 5000
	}
	tx, err := e.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	t0 := time.Now()
	rows, err := tx.Query(s)
	if err != nil {
		return nil, cleanErr(err)
	}
	defer rows.Close()
	cols, data, err := scanAll(rows, limit)
	if err != nil {
		return nil, cleanErr(err)
	}
	trunc := len(data) > limit
	if trunc {
		data = data[:limit]
	}
	return map[string]any{"columns": cols, "rows": data, "truncated": trunc, "ms": time.Since(t0).Milliseconds()}, nil
}

func cleanErr(err error) error {
	m := err.Error()
	if i := strings.Index(m, "\n"); i > 0 {
		m = m[:i]
	}
	if len(m) > 500 {
		m = m[:500]
	}
	return errors.New(m)
}

func utcnow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func strp(s string) *string { return &s }
