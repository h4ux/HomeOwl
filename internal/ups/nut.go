// Package ups watches UPSes through a NUT (Network UPS Tools) server, keeps their history, and runs the
// scripts you chose when the power fails — like NUT's own upsmon, including a graceful shutdown of this computer.
package ups

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultPort is upsd's TCP port.
const DefaultPort = 3493

// Conn is one connection to upsd, speaking its line-based text protocol (docs/net-protocol in NUT).
type Conn struct {
	c       net.Conn
	r       *bufio.Reader
	Timeout time.Duration
}

// ErrNUT is an "ERR …" answer from upsd, e.g. ACCESS-DENIED, UNKNOWN-UPS, DATA-STALE, DRIVER-NOT-CONNECTED.
type ErrNUT struct{ Code string }

func (e *ErrNUT) Error() string { return "NUT server: " + nutErrText(e.Code) }

func nutErrText(code string) string {
	switch strings.Fields(code + " ")[0] {
	case "ACCESS-DENIED":
		return "access denied — check the user name and password (upsd.users)"
	case "UNKNOWN-UPS":
		return "no UPS by that name on this server"
	case "DATA-STALE":
		return "the UPS driver has no fresh data (is the UPS connected?)"
	case "DRIVER-NOT-CONNECTED":
		return "the UPS driver isn't running on the NUT server"
	case "USERNAME-REQUIRED", "PASSWORD-REQUIRED":
		return "this needs a user name and password"
	case "INVALID-USERNAME", "INVALID-PASSWORD":
		return "the user name or password is not valid"
	case "ALREADY-LOGGED-IN":
		return "already registered with this UPS"
	}
	return strings.ToLower(code)
}

// IsNUT reports whether err is upsd answering ERR code.
func IsNUT(err error, code string) bool {
	var e *ErrNUT
	return errors.As(err, &e) && strings.HasPrefix(e.Code, code)
}

// Dial connects to upsd at host:port.
func Dial(ctx context.Context, host string, port int, timeout time.Duration) (*Conn, error) {
	if port <= 0 {
		port = DefaultPort
	}
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("can't reach the NUT server at %s:%d: %w", host, port, err)
	}
	return &Conn{c: c, r: bufio.NewReaderSize(c, 8192), Timeout: timeout}, nil
}

func (c *Conn) Close() error {
	if c == nil || c.c == nil {
		return nil
	}
	_ = c.c.SetDeadline(time.Now().Add(time.Second))
	_, _ = c.c.Write([]byte("LOGOUT\n"))
	return c.c.Close()
}

// quote makes a protocol argument: quoted, with \ and " escaped. Line breaks are refused (they'd start a new command).
func quote(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("line breaks are not allowed")
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, nil
}

func (c *Conn) send(line string) error {
	_ = c.c.SetDeadline(time.Now().Add(c.Timeout))
	_, err := c.c.Write([]byte(line + "\n"))
	return err
}

func (c *Conn) readLine() (string, error) {
	_ = c.c.SetDeadline(time.Now().Add(c.Timeout))
	l, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(l) > 4096 {
		return "", errors.New("NUT server sent a line that is too long")
	}
	return strings.TrimRight(l, "\r\n"), nil
}

// simple sends a command whose answer is one line ("OK …" or "ERR …").
func (c *Conn) simple(line string) (string, error) {
	if err := c.send(line); err != nil {
		return "", err
	}
	l, err := c.readLine()
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(l, "ERR ") {
		return "", &ErrNUT{Code: strings.TrimPrefix(l, "ERR ")}
	}
	return l, nil
}

// Auth sends USERNAME and PASSWORD (skipped when both are empty: read-only access without a login).
func (c *Conn) Auth(user, pass string) error {
	if user == "" && pass == "" {
		return nil
	}
	u, err := quote(user)
	if err != nil {
		return err
	}
	p, err := quote(pass)
	if err != nil {
		return err
	}
	if _, err := c.simple("USERNAME " + u); err != nil {
		return err
	}
	_, err = c.simple("PASSWORD " + p)
	return err
}

// Login registers this connection as a client (upsmon secondary) of the UPS, so the NUT primary waits for us
// before it turns the power off. Needs a upsd.users entry with "upsmon secondary" (or "upsmon slave").
func (c *Conn) Login(ups string) error {
	u, err := quote(ups)
	if err != nil {
		return err
	}
	_, err = c.simple("LOGIN " + u)
	return err
}

// list runs LIST <what> and returns the lines between BEGIN and END, split into words (quotes understood).
func (c *Conn) list(what string) ([][]string, error) {
	if err := c.send("LIST " + what); err != nil {
		return nil, err
	}
	first, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(first, "ERR ") {
		return nil, &ErrNUT{Code: strings.TrimPrefix(first, "ERR ")}
	}
	if !strings.HasPrefix(first, "BEGIN LIST ") {
		return nil, fmt.Errorf("unexpected answer from the NUT server: %q", trunc(first, 80))
	}
	var out [][]string
	for i := 0; ; i++ {
		if i > 2000 {
			return nil, errors.New("NUT server sent too many lines")
		}
		l, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(l, "END LIST ") {
			return out, nil
		}
		if w, err := words(l); err == nil {
			out = append(out, w)
		}
	}
}

// UPSInfo is one UPS that upsd serves.
type UPSInfo struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// List returns the UPSes on the server.
func (c *Conn) List() ([]UPSInfo, error) {
	ls, err := c.list("UPS")
	if err != nil {
		return nil, err
	}
	var out []UPSInfo
	for _, w := range ls {
		if len(w) >= 2 && w[0] == "UPS" {
			u := UPSInfo{Name: w[1]}
			if len(w) >= 3 && w[2] != "Description unavailable" && w[2] != "Unavailable" {
				u.Desc = w[2]
			}
			out = append(out, u)
		}
	}
	return out, nil
}

// Vars returns all variables of one UPS (battery.charge, ups.status, input.voltage, …).
func (c *Conn) Vars(ups string) (map[string]string, error) {
	q, err := quote(ups)
	if err != nil {
		return nil, err
	}
	ls, err := c.list("VAR " + q)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, w := range ls {
		if len(w) >= 4 && w[0] == "VAR" && w[1] == ups {
			out[w[2]] = w[3]
		}
	}
	return out, nil
}

// words splits a protocol line into words; "quoted strings" may contain spaces and \-escapes.
func words(l string) ([]string, error) {
	var out []string
	var b strings.Builder
	in, esc, have := false, false, false
	for _, r := range l {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\' && in:
			esc = true
		case r == '"':
			in = !in
			have = true
		case r == ' ' && !in:
			if have || b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
				have = false
			}
		default:
			b.WriteRune(r)
		}
	}
	if in {
		return nil, errors.New("unterminated quote")
	}
	if have || b.Len() > 0 {
		out = append(out, b.String())
	}
	return out, nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
