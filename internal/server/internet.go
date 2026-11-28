package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/netq"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Internet quality (Network › Internet): HomeOwl's own reading every minute (internal/netq), UniFi's speed tests
// copied from the gateway (and run on a schedule if the admin wants), outages with their likely cause, and the
// "ISP or us?" verdict. Internet down / back alerts come from here too.

type internetState struct {
	w         *netq.Watcher
	mu        sync.Mutex
	imported  time.Time // UniFi's speed-test archive was last read
	importErr string
	running   bool // a speed test HomeOwl started is in progress
}

func newInternet(a *App) *internetState {
	st := &internetState{}
	st.w = &netq.Watcher{
		Store:   netq.Open(filepath.Join(a.DataDir, "internet-history.json")),
		Gateway: a.homeGateway,
		Enabled: func() bool { return !a.Cfg.Get().Internet.Off },
		OnEvent: a.internetEvent,
	}
	return st
}

// homeGateway: the gateway address on the network this computer is on, from UniFi's networks (their subnets give
// the gateway's own address), else the console's address when it's a private one. The subnets tell whether this
// computer is at home at all.
func (a *App) homeGateway() (string, []netip.Prefix) {
	var subnets []netip.Prefix
	var gws []netip.Addr
	if b := a.Cache.Peek(); b != nil {
		leg, _ := b["legacy"].(map[string]any)
		for _, n := range unifi.List(leg["networks"]) {
			if n["enabled"] == false {
				continue
			}
			p, err := netip.ParsePrefix(str(n, "ip_subnet"))
			if err != nil || !p.Addr().Is4() {
				continue
			}
			subnets, gws = append(subnets, p.Masked()), append(gws, p.Addr())
		}
	}
	if local := netq.LocalIP(); local.IsValid() {
		for i, p := range subnets {
			if p.Contains(local) {
				return gws[i].String(), subnets
			}
		}
	}
	if h := consoleHost(a.Cfg.Get().Host); h != "" {
		if ip, err := netip.ParseAddr(h); err == nil && ip.IsPrivate() {
			return h, subnets
		}
	}
	return "", subnets
}

// consoleHost: "192.168.1.1", "https://192.168.1.1:443/" → "192.168.1.1"
func consoleHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (a *App) internetEvent(e netq.Event) {
	if a.Cfg.Get().Alerts.Internet == "off" {
		return
	}
	o := e.Outage
	cause := map[string]string{"isp": "your provider", "home": "inside the home"}[o.Cause]
	switch e.Kind {
	case "down":
		body := "Your gateway answers, but nothing past it does: it looks like your provider."
		if o.Cause == "home" {
			body = "HomeOwl can't reach your gateway either: the gateway's power or cable, or this computer's connection."
		}
		a.Alerts.Add(alerts.Alert{Kind: "internet_down", Level: "warn", Title: "The internet is down", Body: body,
			Data: map[string]any{"start": o.Start, "cause": o.Cause}})
	case "up":
		mins := o.Minutes(time.Now())
		a.Alerts.Add(alerts.Alert{Kind: "internet_up", Level: "info", Title: "The internet is back",
			Body: fmt.Sprintf("It was down for %s (%s).", fmtMinutes(mins), cause), Data: map[string]any{"start": o.Start, "end": o.End, "cause": o.Cause}})
	}
}

func fmtMinutes(m float64) string {
	switch {
	case m < 1.5:
		return "about a minute"
	case m < 90:
		return fmt.Sprintf("%.0f minutes", m)
	}
	return fmt.Sprintf("%.1f hours", m/60)
}

// ---- speed tests

var speedAttrs = []string{"xput_download", "xput_upload", "latency", "rundate", "server", "interface_name", "time"}

// importSpeedTests copies new results from UniFi's speed-test archive (a year back the first time, then a month).
func (a *App) importSpeedTests(ctx context.Context) error {
	if a.UniFi == nil || !a.UniFi.Connected() {
		return errors.New("not connected to UniFi")
	}
	st := a.internet
	now := time.Now()
	back := 30 * 24 * time.Hour
	st.mu.Lock()
	if st.imported.IsZero() {
		back = 365 * 24 * time.Hour
	}
	st.mu.Unlock()
	code, d := a.UniFi.RequestCtx(ctx, "POST", a.UniFi.SitePath("stat/report/archive.speedtest", false),
		map[string]any{"attrs": speedAttrs, "start": now.Add(-back).UnixMilli(), "end": now.UnixMilli()})
	var err error
	if code != 200 {
		err = fmt.Errorf("UniFi's speed-test history: %s", unifi.ErrText(d))
	} else {
		st.w.Store.MergeTests(netq.ParseUniFi(unifi.List(unifi.Data(d))))
	}
	st.mu.Lock()
	st.imported = now
	st.importErr = ""
	if err != nil {
		st.importErr = err.Error()
	}
	st.mu.Unlock()
	return err
}

// runSpeedTest asks the gateway for a speed test (an allow-listed device command, logged as a change) and reads the
// result when it's done. source: schedule or homeowl.
func (a *App) runSpeedTest(source, by string, requireUnlock bool) error {
	st := a.internet
	st.mu.Lock()
	if st.running {
		st.mu.Unlock()
		return errors.New("a speed test is already running")
	}
	st.running = true
	st.mu.Unlock()
	desc := "Run an internet speed test on the gateway"
	if source == "schedule" {
		desc = "Scheduled internet speed test (Network › Internet)"
	}
	_, err := a.Changes.Run(a.UniFi, unifi.Action{"kind": "devcmd", "cmd": "speedtest", "label": "gateway", "description": desc}, false, by, requireUnlock)
	if err != nil {
		st.mu.Lock()
		st.running = false
		st.mu.Unlock()
		return err
	}
	st.w.Store.AddTrigger(netq.Trigger{T: time.Now().UnixMilli(), Source: source, By: by})
	go func() {
		defer func() { st.mu.Lock(); st.running = false; st.mu.Unlock() }()
		for _, wait := range []time.Duration{75 * time.Second, 90 * time.Second, 2 * time.Minute} {
			time.Sleep(wait)
			n := len(a.recentTests(10 * time.Minute))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := a.importSpeedTests(ctx)
			cancel()
			if err == nil && len(a.recentTests(10*time.Minute)) > n {
				break
			}
		}
		if err := st.w.Store.Save(); err != nil {
			log.Printf("internet: %v", err)
		}
	}()
	return nil
}

func (a *App) recentTests(d time.Duration) []netq.SpeedTest {
	_, ts, _ := a.internet.w.Store.Since(time.Now().Add(-d).UnixMilli())
	return ts
}

// speedTestDue: is a scheduled speed test due at now, given the last one HomeOwl started?
func speedTestDue(c config.Internet, last, now time.Time) bool {
	switch c.SpeedTest {
	case "6h":
		return now.Sub(last) >= 6*time.Hour-time.Minute
	case "daily":
		if now.Hour() != c.Hour {
			return false
		}
		y, m, d := last.Date()
		ny, nm, nd := now.Date()
		return y != ny || m != nm || d != nd
	}
	return false
}

// internetLoop: UniFi's speed-test archive every 30 minutes, and the speed-test schedule.
func (a *App) internetLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if !a.UniFi.Connected() {
				continue
			}
			c := a.Cfg.Get().Internet
			last := a.internet.w.Store.LastTrigger("schedule")
			if speedTestDue(c, time.UnixMilli(last), now) && last < now.Add(-time.Hour).UnixMilli() {
				if err := a.runSpeedTest("schedule", "HomeOwl (schedule)", false); err != nil {
					log.Printf("internet: scheduled speed test: %v", err)
				}
			}
			a.internet.mu.Lock()
			due := now.Sub(a.internet.imported) >= 30*time.Minute
			a.internet.mu.Unlock()
			if due {
				ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
				if err := a.importSpeedTests(ictx); err != nil {
					log.Printf("internet: %v", err)
				}
				cancel()
			}
		}
	}
}

// nextSpeedTest: when the schedule runs next (ms, 0 = no schedule).
func nextSpeedTest(c config.Internet, last, now time.Time) int64 {
	switch c.SpeedTest {
	case "6h":
		n := last.Add(6 * time.Hour)
		if n.Before(now) {
			n = now.Add(time.Minute)
		}
		return n.UnixMilli()
	case "daily":
		n := time.Date(now.Year(), now.Month(), now.Day(), c.Hour, 0, 0, 0, now.Location())
		if !n.After(now) && !speedTestDue(c, last, now) {
			n = n.AddDate(0, 0, 1)
		}
		return n.UnixMilli()
	}
	return 0
}

// ---- the page

// internetView: status, verdict, charts, speed tests and outages for everyone; settings for admins.
func (a *App) internetView(admin bool, days int) map[string]any {
	if days != 7 {
		days = 1
	}
	now := time.Now()
	store := a.internet.w.Store
	week := now.AddDate(0, 0, -7).UnixMilli()
	samples, _, outages := store.Since(week)
	_, tests, allOut := store.Since(now.AddDate(-1, 0, 0).UnixMilli())
	from := now.AddDate(0, 0, -days).UnixMilli()
	var shown []netq.Sample
	for _, s := range samples {
		if s.T >= from {
			shown = append(shown, s)
		}
	}
	step := int64(5 * 60000)
	if days == 7 {
		step = 30 * 60000
	}
	status := a.internet.w.Status()
	wk := netq.Summarize(samples, outages, week, now)
	dy := netq.Summarize(samples, outages, now.Add(-24*time.Hour).UnixMilli(), now)
	c := a.Cfg.Get().Internet
	plan := netq.Plan{Down: c.PlanDown, Up: c.PlanUp}
	verdict := netq.Judge(wk, dy, tests, plan, status.Down, status.Away, now)
	// newest first
	rt := make([]netq.SpeedTest, 0, 200)
	for i := len(tests) - 1; i >= 0 && len(rt) < 200; i-- {
		t := tests[i]
		if plan.Down > 0 && t.Down < plan.Down/2 {
			t.Note = "under half your plan"
		}
		rt = append(rt, t)
	}
	ro := make([]netq.Outage, 0, 200)
	for i := len(allOut) - 1; i >= 0 && len(ro) < 200; i-- {
		ro = append(ro, allOut[i])
	}
	a.internet.mu.Lock()
	running, importErr := a.internet.running, a.internet.importErr
	a.internet.mu.Unlock()
	out := map[string]any{"days": days, "status": status, "verdict": verdict, "week": wk, "day": dy, "buckets": netq.Buckets(shown, step),
		"tests": rt, "outages": ro, "plan": plan, "running": running, "unifi": a.unifiWAN(), "connected": a.UniFi.Connected(),
		"targets": netq.Targets, "schedule": c.SpeedTest, "next_test": nextSpeedTest(c, time.UnixMilli(store.LastTrigger("schedule")), now)}
	if admin {
		out["settings"] = map[string]any{"readings": !c.Off, "speed_test": c.SpeedTest, "hour": c.Hour, "plan_down": c.PlanDown, "plan_up": c.PlanUp,
			"alerts": a.Cfg.Get().Alerts.Internet != "off"}
		out["import_error"] = importErr
	}
	return out
}

// unifiWAN: what UniFi itself says about the connection (from the last collection).
func (a *App) unifiWAN() map[string]any {
	out := map[string]any{}
	b := a.Cache.Peek()
	if b == nil {
		return out
	}
	leg, _ := b["legacy"].(map[string]any)
	for _, h := range unifi.List(leg["health"]) {
		switch h["subsystem"] {
		case "wan":
			out["status"], out["isp"] = h["status"], h["isp_name"] // not the public address: it stays off the page
		case "www":
			for _, k := range []string{"latency", "xput_down", "xput_up", "speedtest_lastrun", "drops", "uptime"} {
				if v, ok := h[k]; ok {
					out[k] = v
				}
			}
		}
	}
	return out
}

// internetSave: the readings, the speed-test schedule, the plan and the alerts.
func (a *App) internetSave(body map[string]any) error {
	sched := s(body, "speed_test")
	if sched != "" && sched != "daily" && sched != "6h" {
		return errors.New("speed test schedule must be off, daily or every 6 hours")
	}
	hour, err := clampInt(body, "hour", 0, 23)
	if err != nil {
		return err
	}
	pd, pu := planNum(body, "plan_down"), planNum(body, "plan_up")
	if pd < 0 || pu < 0 || pd > 100000 || pu > 100000 {
		return errors.New("the plan's speed must be 0–100000 Mbps")
	}
	return a.Cfg.Update(func(c *config.Config) {
		c.Internet = config.Internet{Off: has(body, "readings") && !b(body, "readings"), SpeedTest: sched, Hour: hour, PlanDown: pd, PlanUp: pu}
		if has(body, "alerts") {
			c.Alerts.Internet = map[bool]string{true: "", false: "off"}[b(body, "alerts")]
		}
	})
}

func planNum(body map[string]any, k string) float64 {
	switch v := body[k].(type) {
	case float64:
		return v
	case string:
		var f float64
		if _, err := fmt.Sscan(strings.TrimSpace(v), &f); err == nil {
			return f
		}
	}
	return 0
}
