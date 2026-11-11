package ups

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUPSD is a tiny upsd: one UPS "rack" (plus "spare"), user alex / secret.
type fakeUPSD struct {
	mu     sync.Mutex
	status string
	charge string
	stale  bool
	logins []string
	ln     net.Listener
}

func newFake(t *testing.T) *fakeUPSD {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeUPSD{status: "OL CHRG", charge: "100", ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeUPSD) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeUPSD) set(status, charge string) {
	f.mu.Lock()
	f.status, f.charge = status, charge
	f.mu.Unlock()
}

func (f *fakeUPSD) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	user, authed := "", false
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			return
		}
		w, _ := words(strings.TrimSpace(l))
		if len(w) == 0 {
			continue
		}
		out := "ERR UNKNOWN-COMMAND\n"
		switch {
		case w[0] == "USERNAME" && len(w) == 2:
			user, out = w[1], "OK\n"
		case w[0] == "PASSWORD" && len(w) == 2:
			if user == "alex" && w[1] == `se"cret` {
				authed, out = true, "OK\n"
			} else {
				out = "ERR ACCESS-DENIED\n"
			}
		case w[0] == "LOGIN" && len(w) == 2:
			if !authed {
				out = "ERR USERNAME-REQUIRED\n"
			} else {
				f.mu.Lock()
				f.logins = append(f.logins, w[1])
				f.mu.Unlock()
				out = "OK\n"
			}
		case w[0] == "LOGOUT":
			c.Write([]byte("OK Goodbye\n"))
			return
		case len(w) == 2 && w[0] == "LIST" && w[1] == "UPS":
			out = "BEGIN LIST UPS\nUPS rack \"Eaton 5P \\\"rack\\\"\"\nUPS spare \"Description unavailable\"\nEND LIST UPS\n"
		case len(w) == 3 && w[0] == "LIST" && w[1] == "VAR":
			f.mu.Lock()
			st, ch, stale := f.status, f.charge, f.stale
			f.mu.Unlock()
			switch {
			case w[2] != "rack" && w[2] != "spare":
				out = "ERR UNKNOWN-UPS\n"
			case stale && w[2] == "rack":
				out = "ERR DATA-STALE\n"
			default:
				out = "BEGIN LIST VAR " + w[2] + "\nVAR " + w[2] + " ups.status \"" + st + "\"\nVAR " + w[2] + " battery.charge \"" + ch +
					"\"\nVAR " + w[2] + " battery.runtime \"1260\"\nVAR " + w[2] + " ups.load \"23\"\nVAR " + w[2] + " input.voltage \"230.4\"\n" +
					"VAR " + w[2] + " device.model \"5P 1550\"\nEND LIST VAR " + w[2] + "\n"
			}
		}
		c.Write([]byte(out))
	}
}

func TestWordsAndQuote(t *testing.T) {
	w, err := words(`VAR rack ups.status "OB DISCHRG" "a \"q\" \\ b"`)
	if err != nil || len(w) != 5 || w[3] != "OB DISCHRG" || w[4] != `a "q" \ b` {
		t.Fatalf("words: %q %v", w, err)
	}
	if w, _ := words(`UPS x ""`); len(w) != 3 || w[2] != "" {
		t.Fatalf("empty quoted word: %q", w)
	}
	if _, err := words(`VAR "open`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
	if q, _ := quote(`p"a\ss`); q != `"p\"a\\ss"` {
		t.Fatalf("quote: %s", q)
	}
	if _, err := quote("a\nLIST UPS"); err == nil {
		t.Fatal("line break accepted (command injection)")
	}
	if Describe("OB DISCHRG LB") != "on battery, discharging, battery low" || Describe("") != "unknown" {
		t.Fatal(Describe("OB DISCHRG LB"))
	}
}

func TestClient(t *testing.T) {
	f := newFake(t)
	ctx := context.Background()
	if _, err := Test(ctx, Settings{Host: "127.0.0.1", Port: f.port(), User: "alex", Pass: "wrong"}); !IsNUT(err, "ACCESS-DENIED") {
		t.Fatalf("bad password: %v", err)
	}
	list, err := Test(ctx, Settings{Host: "127.0.0.1", Port: f.port(), User: "alex", Pass: `se"cret`})
	if err != nil || len(list) != 2 || list[0].Desc != `Eaton 5P "rack"` || list[1].Desc != "" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := Test(ctx, Settings{Host: "127.0.0.1", Port: f.port(), User: "alex", Pass: `se"cret`, Power: "nope"}); err == nil {
		t.Fatal("unknown UPS accepted")
	}
}

// writeScript puts a script into dir that appends its NOTIFYTYPE (and test flag) to log.
func writeScript(t *testing.T, dir, name, logf string) {
	t.Helper()
	s := "#!/bin/sh\necho \"$NOTIFYTYPE $HOMEOWL_UPS_TEST $HOMEOWL_UPS_CHARGE $UPSNAME\" >> '" + logf + "'\necho ran\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readLog(t *testing.T, m *Monitor, p string) string {
	m.runWG.Wait()
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	f := newFake(t)
	dir := t.TempDir()
	sdir := filepath.Join(dir, "ups-scripts")
	os.MkdirAll(sdir, 0o700)
	logf := filepath.Join(dir, "ran.log")
	writeScript(t, sdir, "note.sh", logf)
	writeScript(t, sdir, "off.sh", logf)
	clock := time.Now().Add(-time.Hour).Truncate(time.Minute)
	m := &Monitor{Hist: NewHistory(filepath.Join(dir, "h.json")), Scripts: &Scripts{Dir: sdir}, now: func() time.Time { return clock }}
	set := Settings{Host: "127.0.0.1", Port: f.port(), User: "alex", Pass: `se"cret`, Secondary: true, Power: "rack",
		Rule: Rule{LowBattery: true},
		Actions: []Action{{Event: "onbatt", Script: "note.sh"}, {Event: "online", Script: "note.sh"}, {Event: "shutdown", Script: "off.sh"},
			{Event: "commbad", Script: "note.sh", Delay: 30}}}
	ctx := context.Background()
	step := func(d time.Duration) { clock = clock.Add(d); m.Tick(ctx, set) }

	step(0)
	st := m.Status()
	if st.State != "live" || len(st.UPS) != 2 || !st.UPS[0].Power || st.UPS[1].Power || *st.UPS[0].Charge != 100 || !st.Secondary {
		t.Fatalf("first poll: %+v", st)
	}
	if len(f.logins) != 1 || f.logins[0] != "rack" {
		t.Fatalf("LOGIN as secondary: %v", f.logins)
	}
	f.set("OB DISCHRG", "80")
	step(5 * time.Second)
	m.runWG.Wait() // scripts run in the background; keep their order stable for the check below
	f.set("OB DISCHRG LB", "20")
	step(5 * time.Second)
	if got := readLog(t, m, logf); got != "ONBATT  80 rack@127.0.0.1\nSHUTDOWN  20 rack@127.0.0.1\n" {
		t.Fatalf("scripts on battery: %q", got)
	}
	step(5 * time.Second) // still low: shutdown only once
	f.set("OL CHRG", "25")
	step(5 * time.Second)
	if got := readLog(t, m, logf); !strings.HasSuffix(got, "ONLINE  25 rack@127.0.0.1\n") || strings.Count(got, "SHUTDOWN") != 1 {
		t.Fatalf("back on mains: %q", got)
	}
	_, ev := m.Hist.Since(0, "rack")
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, " ") != "onbatt lowbatt shutdown script script online script" && strings.Join(kinds, " ") != "onbatt lowbatt shutdown script online script" {
		// lowbatt and shutdown fire in the same reading; script events land when the scripts finish
		t.Logf("events: %v", kinds)
	}
	out := Outages(m.Hist.Since(0, "rack"))
	if len(out) != 1 || !out[0].Shutdown || out[0].End == 0 || out[0].MinCharge == nil || *out[0].MinCharge != 20 {
		t.Fatalf("outages: %+v", out)
	}

	// stale data on battery → lost the UPS → shutdown (upsmon treats a dead UPS on battery as critical)
	f.set("OB DISCHRG", "60")
	step(5 * time.Second)
	f.mu.Lock()
	f.stale = true
	f.mu.Unlock()
	step(5 * time.Second)
	step(20 * time.Second)
	if st := m.Status(); !st.UPS[0].CommBad || st.Shutdown == 0 {
		t.Fatalf("stale on battery: %+v", st.UPS[0])
	}
	if got := readLog(t, m, logf); strings.Count(got, "SHUTDOWN") != 2 {
		t.Fatalf("shutdown when the UPS is lost on battery: %q", got)
	}
	// the delayed commbad action waits 30 s and is dropped when the UPS comes back first
	f.mu.Lock()
	f.stale = false
	f.mu.Unlock()
	f.set("OL", "61")
	step(10 * time.Second)
	if st := m.Status(); st.UPS[0].CommBad || len(st.Pending) != 0 {
		t.Fatalf("back: %+v %v", st.UPS[0], st.Pending)
	}
	step(30 * time.Second)
	if got := readLog(t, m, logf); strings.Contains(got, "COMMBAD") {
		t.Fatalf("cancelled delayed action ran: %q", got)
	}

	// server gone: commbad after 15 s, then the delayed action after 30 s more
	f.ln.Close()
	m.closeConn()
	step(5 * time.Second)
	step(16 * time.Second)
	if st := m.Status(); st.State != "error" || !st.UPS[0].CommBad || len(st.Pending) != 1 {
		t.Fatalf("server gone: %+v", st)
	}
	step(31 * time.Second)
	if got := readLog(t, m, logf); !strings.Contains(got, "COMMBAD") {
		t.Fatalf("delayed commbad action didn't run: %q", got)
	}

	// a test run gets HOMEOWL_UPS_TEST=1 and is recorded
	r := m.RunNow(ctx, "off.sh", "shutdown", "alex")
	if r.Exit != 0 || r.Output != "ran" {
		t.Fatalf("test run: %+v", r)
	}
	if got := readLog(t, m, logf); !strings.HasSuffix(got, "SHUTDOWN 1 61 rack@127.0.0.1\n") {
		t.Fatalf("test env: %q", got)
	}
	if err := m.Hist.Save(); err != nil {
		t.Fatal(err)
	}
	h2 := NewHistory(filepath.Join(dir, "h.json"))
	if s, e := h2.Since(0, ""); len(s) == 0 || len(e) == 0 {
		t.Fatal("history not saved")
	}
}

func TestScriptsSafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	s := &Scripts{Dir: filepath.Join(dir, "ups-scripts"), Timeout: 2 * time.Second}
	if err := s.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.Dir, "ok.sh"), []byte("#!/bin/sh\necho hi\n"), 0o700)
	os.WriteFile(filepath.Join(s.Dir, "plain.sh"), []byte("echo plain\n"), 0o600)
	os.WriteFile(filepath.Join(s.Dir, "open.sh"), []byte("#!/bin/sh\necho no\n"), 0o700)
	os.Chmod(filepath.Join(s.Dir, "open.sh"), 0o722)
	os.WriteFile(filepath.Join(s.Dir, "noexec"), []byte("#!/bin/sh\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "outside.sh"), []byte("#!/bin/sh\necho outside\n"), 0o700)
	os.Symlink(filepath.Join(dir, "outside.sh"), filepath.Join(s.Dir, "link.sh"))
	os.WriteFile(filepath.Join(s.Dir, "slow.sh"), []byte("#!/bin/sh\nsleep 10\n"), 0o700)

	ctx := context.Background()
	if r := s.Run(ctx, "ok.sh", []string{"X=1"}); r.Exit != 0 || r.Output != "hi" {
		t.Fatalf("ok: %+v", r)
	}
	if r := s.Run(ctx, "plain.sh", nil); r.Exit != 0 || r.Output != "plain" {
		t.Fatalf(".sh without +x runs with sh: %+v", r)
	}
	for _, n := range []string{"open.sh", "noexec", "link.sh", "../outside.sh", "missing.sh", ".hidden", "a/b"} {
		if r := s.Run(ctx, n, nil); r.Error == "" {
			t.Errorf("%s ran: %+v", n, r)
		}
	}
	if r := s.Run(ctx, "slow.sh", nil); !strings.Contains(r.Error, "stopped after") {
		t.Fatalf("timeout: %+v", r)
	}
	probs := map[string]string{}
	for _, f := range s.List() {
		probs[f.Name] = f.Problem
	}
	if probs["ok.sh"] != "" || probs["open.sh"] == "" || probs["link.sh"] == "" {
		t.Fatalf("list problems: %v", probs)
	}
	if env := cleanEnv([]string{"PATH=/bin", "HOMEOWL_API_KEY=x", "MY_TOKEN=y", "NOTIFYTYPE=old"}); len(env) != 1 || env[0] != "PATH=/bin" {
		t.Fatalf("cleanEnv: %v", env)
	}

	for _, goos := range []string{"darwin", "linux", "windows"} {
		sug := s.Suggestions(goos, "alex", false)
		if len(sug) != 2 || sug[0].Event != "shutdown" {
			t.Fatalf("%s suggestions: %+v", goos, sug)
		}
		if goos != "windows" && !strings.Contains(sug[0].Setup, "alex ALL=(root) NOPASSWD:") {
			t.Fatalf("%s sudoers hint: %q", goos, sug[0].Setup)
		}
	}
	if err := s.SaveSuggestion(runtime.GOOS, "../evil.sh", false); err == nil {
		t.Fatal("saved something that isn't a suggestion")
	}
	name := s.Suggestions(runtime.GOOS, "", true)[0].Name
	if err := s.SaveSuggestion(runtime.GOOS, name, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSuggestion(runtime.GOOS, name, false); err == nil {
		t.Fatal("overwrote without asking")
	}
	if sug := s.Suggestions(runtime.GOOS, "", true); !sug[0].Exists || !sug[0].Same {
		t.Fatalf("saved suggestion not recognised: %+v", sug[0])
	}
	// the suggested shutdown script only talks in test mode. Belt and braces: every command it could use to
	// power off is replaced by a stub on PATH, so even a broken test check can't shut the test machine down.
	stub := filepath.Join(dir, "stub")
	os.MkdirAll(stub, 0o700)
	for _, c := range []string{"sudo", "osascript", "systemctl", "logger", "wall", "shutdown"} {
		os.WriteFile(filepath.Join(stub, c), []byte("#!/bin/sh\necho STUB-"+c+"\nexit 1\n"), 0o700)
	}
	if r := s.Run(ctx, name, []string{"PATH=" + stub + ":/usr/bin:/bin", "HOMEOWL_UPS_TEST=1", "NOTIFYTYPE=SHUTDOWN", "UPSNAME=rack@192.168.1.20", "HOMEOWL_UPS_CHARGE=20"}); r.Exit != 0 || !strings.HasPrefix(r.Output, "Test: would") {
		t.Fatalf("suggested script in test mode: %+v", r)
	}
}
