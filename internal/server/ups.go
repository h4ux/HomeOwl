package server

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
	"github.com/h4ux/homeowl/internal/ups"
)

// UPS: a NUT server's UPSes — status, history, and the scripts HomeOwl runs on power events (like upsmon).

const nutPassSlot = "nut-password"

func newUPSMonitor(a *App) *ups.Monitor {
	return &ups.Monitor{
		Hist:    ups.NewHistory(filepath.Join(a.DataDir, "ups-history.json")),
		Scripts: &ups.Scripts{Dir: filepath.Join(a.DataDir, "ups-scripts"), Timeout: 2 * time.Minute},
		Settings: func() (ups.Settings, bool) {
			c := a.Cfg.Get().UPS
			if !c.Enabled || c.Host == "" {
				return ups.Settings{}, false
			}
			return upsSettings(c, a.Sec.Get(nutPassSlot)), true
		},
	}
}

func upsSettings(c config.UPS, pass string) ups.Settings {
	set := ups.Settings{Host: c.Host, Port: c.Port, User: c.User, Pass: pass, Power: c.Name, Poll: time.Duration(c.Poll) * time.Second,
		Secondary: c.Secondary, Rule: ups.Rule{LowBattery: c.LowBattery, AfterMinutes: c.AfterMinutes, BelowCharge: c.BelowCharge, BelowRuntime: c.BelowRuntime}}
	for _, x := range c.Actions {
		set.Actions = append(set.Actions, ups.Action{Event: x.Event, Script: x.Script, Delay: x.Delay})
	}
	return set
}

// upsView is the UPS page: status and history for everyone, settings and scripts for admins.
func (a *App) upsView(admin bool, days int) map[string]any {
	if days != 7 && days != 30 {
		days = 1
	}
	c := a.Cfg.Get().UPS
	st := a.UPS.Status()
	now := time.Now()
	samples, _ := a.UPS.Hist.Since(now.AddDate(0, 0, -days).UnixMilli(), "")
	_, events := a.UPS.Hist.Since(now.AddDate(-1, 0, 0).UnixMilli(), "")
	outages := ups.Outages(samples, events)
	if days > 1 { // keep the charts light: one reading per 10 minutes per UPS over a week
		samples = thin(samples, 10*60000)
	}
	// newest first, at most 500
	ev := make([]ups.Event, 0, 500)
	for i := len(events) - 1; i >= 0 && len(ev) < 500; i-- {
		e := events[i]
		if !admin {
			e.Output = ""
		}
		ev = append(ev, e)
	}
	out := map[string]any{"configured": c.Enabled && c.Host != "", "status": st, "samples": samples, "events": ev, "outages": outages, "days": days}
	if !admin {
		return out
	}
	who, root := "", ups.IsRoot()
	if u, err := user.Current(); err == nil {
		who = u.Username
		if i := strings.LastIndexByte(who, '\\'); i >= 0 { // DOMAIN\name on Windows
			who = who[i+1:]
		}
	}
	actions := c.Actions
	if actions == nil {
		actions = []config.UPSAction{}
	}
	out["settings"] = map[string]any{"host": c.Host, "port": portOr(c.Port), "user": c.User, "has_password": a.Sec.Get(nutPassSlot) != "",
		"name": c.Name, "poll": pollOr(c.Poll), "secondary": c.Secondary, "low_battery": c.LowBattery, "after_minutes": c.AfterMinutes,
		"below_charge": c.BelowCharge, "below_runtime": c.BelowRuntime, "actions": actions}
	out["scripts"] = a.UPS.Scripts.List()
	out["folder"] = a.UPS.Scripts.Dir
	out["suggestions"] = a.UPS.Scripts.Suggestions(runtime.GOOS, who, root)
	out["os"], out["run_as"], out["root"] = runtime.GOOS, who, root
	out["event_names"] = ups.EventNames
	out["write"] = a.Changes.Write && !a.ReadOnly
	return out
}

func portOr(p int) int {
	if p <= 0 {
		return ups.DefaultPort
	}
	return p
}

func pollOr(p int) int {
	if p < 2 {
		return 5
	}
	return p
}

// thin keeps one sample per UPS per step (the one with the lowest charge, so dips stay visible).
func thin(ss []ups.Sample, step int64) []ups.Sample {
	type k struct {
		u string
		b int64
	}
	idx := map[k]int{}
	out := []ups.Sample{}
	for _, s := range ss {
		key := k{s.UPS, s.T / step}
		if i, ok := idx[key]; ok {
			if s.Charge != nil && (out[i].Charge == nil || *s.Charge < *out[i].Charge) {
				out[i] = s
			}
			continue
		}
		idx[key] = len(out)
		out = append(out, s)
	}
	return out
}

var nutHostRE = regexp.MustCompile(`^[A-Za-z0-9.:\-\[\]]{1,253}$`)

// upsConnect tests a NUT server with the given address, user and password, then saves it and starts watching.
func (a *App) upsConnect(ctx context.Context, body map[string]any) (any, error) {
	c := a.Cfg.Get().UPS
	host := strings.Trim(s(body, "host"), "[]")
	if host == "" || !nutHostRE.MatchString(host) || strings.Contains(host, "://") {
		return nil, errors.New("enter the NUT server's IP address or host name")
	}
	port, ok := n(body, "port")
	if !ok || port <= 0 {
		port = ups.DefaultPort
	}
	if port > 65535 {
		return nil, errors.New("the port must be 1–65535")
	}
	pass := ""
	if v, ok := body["password"].(string); ok && v != "" {
		pass = v
	} else if host == c.Host && portOr(c.Port) == port {
		pass = a.Sec.Get(nutPassSlot) // same server: keep the saved password
	}
	set := ups.Settings{Host: host, Port: port, User: s(body, "user"), Pass: pass, Power: s(body, "name")}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := ups.Test(tctx, set)
	if err != nil {
		return nil, err
	}
	if pass != "" {
		if err := a.Sec.Set(nutPassSlot, pass); err != nil {
			return nil, err
		}
	} else {
		a.Sec.Delete(nutPassSlot)
	}
	first := !c.Enabled && c.Host == ""
	if err := a.Cfg.Update(func(x *config.Config) {
		u := &x.UPS
		u.Enabled, u.Host, u.Port, u.User, u.Name, u.Secondary = true, host, port, set.User, set.Power, b(body, "secondary")
		if first {
			u.LowBattery = true // upsmon's rule until you say otherwise
		}
	}); err != nil {
		return nil, err
	}
	a.UPS.Wake()
	return map[string]any{"ok": true, "ups": list}, nil
}

func (a *App) upsDisconnect() {
	a.Sec.Delete(nutPassSlot)
	_ = a.Cfg.Update(func(x *config.Config) { x.UPS.Enabled, x.UPS.Host, x.UPS.User, x.UPS.Secondary = false, "", "", false })
	a.UPS.Wake()
}

func clampInt(body map[string]any, k string, lo, hi int) (int, error) {
	v, ok := n(body, k)
	if !ok {
		return 0, nil
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("%s must be %d–%d", strings.ReplaceAll(k, "_", " "), lo, hi)
	}
	return v, nil
}

// upsSave saves the shutdown rule, the actions and how often to read the UPS.
func (a *App) upsSave(body map[string]any) error {
	poll, err := clampInt(body, "poll", 0, 300)
	if err != nil {
		return err
	}
	after, err := clampInt(body, "after_minutes", 0, 1440)
	if err != nil {
		return err
	}
	charge, err := clampInt(body, "below_charge", 0, 95)
	if err != nil {
		return err
	}
	rt, err := clampInt(body, "below_runtime", 0, 600)
	if err != nil {
		return err
	}
	var acts []config.UPSAction
	raw, _ := body["actions"].([]any)
	if len(raw) > 30 {
		return errors.New("at most 30 actions")
	}
	for _, r := range raw {
		m, _ := r.(map[string]any)
		ev, script := s(m, "event"), s(m, "script")
		if _, ok := ups.EventNames[ev]; !ok {
			return fmt.Errorf("unknown event %q", ev)
		}
		if script == "" || strings.ContainsAny(script, `/\`) || strings.HasPrefix(script, ".") {
			return errors.New("pick a script from the scripts folder")
		}
		d, err := clampInt(m, "delay", 0, 3600)
		if err != nil {
			return err
		}
		acts = append(acts, config.UPSAction{Event: ev, Script: script, Delay: d})
	}
	name := s(body, "name")
	if strings.ContainsAny(name, "\r\n\"") {
		return errors.New("not a valid UPS name")
	}
	err = a.Cfg.Update(func(x *config.Config) {
		u := &x.UPS
		if poll > 0 {
			u.Poll = max(poll, 2)
		}
		u.Name, u.Secondary, u.LowBattery = name, b(body, "secondary"), b(body, "low_battery")
		u.AfterMinutes, u.BelowCharge, u.BelowRuntime, u.Actions = after, charge, rt, acts
	})
	a.UPS.Wake()
	return err
}

// upsTestScript runs a script once with HOMEOWL_UPS_TEST=1 — it executes on this computer, so it needs the change
// lock open and is written to the Changes log.
func (a *App) upsTestScript(ctx context.Context, body map[string]any, by string) (any, error) {
	script, event := s(body, "script"), s(body, "event")
	if _, ok := ups.EventNames[event]; !ok {
		event = "shutdown"
	}
	found := false
	for _, f := range a.UPS.Scripts.List() {
		if f.Name == script {
			if f.Problem != "" {
				return nil, errors.New(f.Problem)
			}
			found = true
		}
	}
	if !found {
		return nil, errors.New("no such script in the scripts folder")
	}
	pub := unifi.Preview{Method: "RUN", Path: script, Target: "this computer (UPS script)",
		After: map[string]any{"event": event, "HOMEOWL_UPS_TEST": "1"}, Description: fmt.Sprintf("Test UPS script %s", script)}
	var res ups.Result
	_, err := a.Changes.External("this computer", by, pub, true, func() (int, any) {
		tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		res = a.UPS.RunNow(tctx, script, event, by)
		if res.Error != "" || res.Exit != 0 {
			return 500, map[string]any{"exit": res.Exit, "error": res.Error}
		}
		return 200, map[string]any{"exit": res.Exit}
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
