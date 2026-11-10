package analytics

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type syslogState struct {
	mu        sync.Mutex
	conn      *net.UDPConn
	port      int
	Listening bool
	Received  int64
	Accepted  int64
	Dropped   int64
	Error     string
	Last      int64
	buf       [][]any
}

var (
	syslogRE = regexp.MustCompile(`(?s)^<(\d{1,3})>(?:1 )?(.*)$`)
	cefRE    = regexp.MustCompile(`(?s)CEF:(\d+)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|(.*)$`)
	cefKeyRE = regexp.MustCompile(`(?:^|\s)(\w[\w.\-]*)=`)
	iptKV    = regexp.MustCompile(`\b([A-Z]{2,6})=(\S*)`)
	ruleRE   = regexp.MustCompile(`\[([^\]]+)\]`)
	blockRE  = regexp.MustCompile(`(?i)(-D-|-R-|\bBlock|\bDROP|\bREJECT)`)
	allowRE  = regexp.MustCompile(`(?i)(-A-|\bAllow|\bACCEPT)`)
)

var syslogCols = []string{"ts", "host", "kind", "severity", "name", "rule", "action", "src", "dst", "sport", "dport", "proto", "iface_in", "iface_out", "category", "msg"}

// cefExt splits a CEF extension "k1=v one k2=v2" into a map (values may contain spaces; "\=" is an escaped '=').
func cefExt(s string) map[string]string {
	out := map[string]string{}
	locs := cefKeyRE.FindAllStringSubmatchIndex(s, -1)
	for i, l := range locs {
		key := s[l[2]:l[3]]
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[key] = strings.TrimSpace(strings.ReplaceAll(s[l[1]:end], `\=`, "="))
	}
	return out
}

func atoi(s string) any {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return n
}

// ParseSyslog turns one received datagram into a row for the syslog table (without the timestamp).
func ParseSyslog(line, host string) map[string]any {
	body := strings.TrimSpace(line)
	var sev any
	if m := syslogRE.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		sev, body = n%8, m[2]
	}
	row := map[string]any{"host": host, "kind": "other", "severity": sev, "msg": truncate(body, 2000)}
	if c := cefRE.FindStringSubmatch(body); c != nil {
		ext := cefExt(c[8])
		name := truncate(c[6], 200)
		row["kind"] = "cef"
		row["name"] = name
		if n := atoi(c[7]); n != nil {
			row["severity"] = n
		}
		row["src"], row["dst"] = nullS(ext["src"]), nullS(ext["dst"])
		row["sport"], row["dport"] = atoi(ext["spt"]), atoi(ext["dpt"])
		row["proto"] = nullS(ext["proto"])
		var parts []string
		for _, k := range []string{"UNIFIcategory", "UNIFIsubCategory"} {
			if ext[k] != "" {
				parts = append(parts, ext[k])
			}
		}
		row["category"] = orS(strings.Join(parts, "/"), c[3])
		row["msg"] = truncate(orS(ext["msg"], c[6]), 2000)
		act := strings.ToLower(orS(ext["act"], ext["UNIFIaction"]))
		switch {
		case strings.Contains(act, "block") || strings.Contains(strings.ToLower(name), "block") || strings.Contains(act, "drop"):
			row["action"] = "blocked"
		case act != "":
			row["action"] = act
		}
		return row
	}
	if strings.Contains(body, "SRC=") && strings.Contains(body, "DST=") {
		kv := map[string]string{}
		for _, m := range iptKV.FindAllStringSubmatch(body, -1) {
			if _, ok := kv[m[1]]; !ok {
				kv[m[1]] = m[2]
			}
		}
		rule := ""
		if m := ruleRE.FindStringSubmatch(body); m != nil {
			rule = m[1]
		}
		desc := rule + " " + truncate(body, 160)
		row["kind"] = "firewall"
		row["rule"] = nullS(rule)
		if blockRE.MatchString(desc) {
			row["action"] = "blocked"
		} else if allowRE.MatchString(desc) {
			row["action"] = "allowed"
		}
		row["src"], row["dst"] = nullS(kv["SRC"]), nullS(kv["DST"])
		row["sport"], row["dport"] = atoi(kv["SPT"]), atoi(kv["DPT"])
		row["proto"], row["iface_in"], row["iface_out"] = nullS(kv["PROTO"]), nullS(kv["IN"]), nullS(kv["OUT"])
	}
	return row
}

// ApplySyslog starts/stops/moves the UDP receiver to match the settings.
func (e *Engine) ApplySyslog() {
	c := e.conf()
	want := c.Enabled && c.SyslogEnabled
	port := c.SyslogPort
	if port == 0 {
		port = 5514
	}
	s := &e.sys
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil && (!want || s.port != port) {
		_ = s.conn.Close()
		s.conn, s.Listening = nil, false
	}
	if want && s.conn == nil {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
		if err != nil {
			s.Listening, s.port, s.Error = false, port, fmt.Sprintf("can't listen on UDP %d: %v", port, err)
			return
		}
		s.conn, s.port, s.Listening, s.Error = conn, port, true, ""
		go e.syslogLoop(conn)
	}
}

func (e *Engine) syslogLoop(conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		ip := addr.IP.String()
		allowed := e.conf().SyslogSources
		s := &e.sys
		s.mu.Lock()
		s.Received++
		ok := len(allowed) == 0
		for _, a := range allowed {
			if strings.TrimSpace(a) == ip {
				ok = true
			}
		}
		if !ok {
			s.Dropped++
			s.mu.Unlock()
			continue
		}
		row := ParseSyslog(string(buf[:n]), ip)
		now := utcnow()
		vals := make([]any, len(syslogCols))
		vals[0] = now
		for i, c := range syslogCols[1:] {
			vals[i+1] = row[c]
		}
		s.buf = append(s.buf, vals)
		s.Accepted++
		s.Last = now.UnixMilli()
		s.mu.Unlock()
	}
}

func (e *Engine) syslogFlusher() {
	for {
		time.Sleep(5 * time.Second)
		s := &e.sys
		s.mu.Lock()
		rows := s.buf
		s.buf = nil
		s.mu.Unlock()
		if len(rows) == 0 || e.db == nil {
			continue
		}
		if err := e.tx(func(tx *sql.Tx) error { return insert(tx, "syslog", syslogCols, rows) }); err != nil {
			s.mu.Lock()
			s.Error = "store failed: " + err.Error()
			s.mu.Unlock()
		}
	}
}

// RollupSyslog refreshes the hourly syslog summary for the last few hours.
func (e *Engine) RollupSyslog(hours int) {
	h0 := utcnow().Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour)
	_ = e.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM syslog_hourly WHERE hour>=?", h0); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO syslog_hourly SELECT date_trunc('hour', ts), kind, action, rule, dport, proto, count(*)
			FROM syslog WHERE ts>=? GROUP BY ALL`, h0)
		return err
	})
	if _, err := e.ThreatsFromLogs(); err != nil {
		log.Printf("threats from syslog failed: %v", err)
	}
	e.state("syslog", true, 0, "", utcnow(), nil)
}

func (e *Engine) SyslogStatus() map[string]any {
	s := &e.sys
	s.mu.Lock()
	defer s.mu.Unlock()
	var last any
	if s.Last > 0 {
		last = s.Last
	}
	return map[string]any{"listening": s.Listening, "port": s.port, "received": s.Received, "accepted": s.Accepted,
		"dropped": s.Dropped, "error": nullS(s.Error), "last": last}
}
