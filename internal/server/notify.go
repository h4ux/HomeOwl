package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Alerts (Records › Alerts): a device HomeOwl has never seen joins, the guest password changed on its schedule…
// The list lives in alerts.json; ntfy and webhook addresses live in the secrets store (they work like passwords).

var isMac = runtime.GOOS == "darwin"

const (
	slotNtfy    = "alerts-ntfy"
	slotWebhook = "alerts-webhook"
)

func (a *App) alertChannels() alerts.Channels {
	c := a.Cfg.Get()
	return alerts.Channels{MacOS: c.Alerts.MacOS, Ntfy: a.Sec.Get(slotNtfy), Webhook: a.Sec.Get(slotWebhook)}
}

// deliverAlert is the store's Send: problems are kept for Settings, not retried.
func (a *App) deliverAlert(al alerts.Alert) {
	if al.Data["delivered"] == true {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	errs := alerts.Deliver(ctx, a.alertChannels(), al)
	msg := ""
	for _, e := range errs {
		msg += e.Error() + "; "
	}
	a.mu.Lock()
	a.alertErr = strings.TrimSuffix(msg, "; ")
	a.mu.Unlock()
	if msg != "" {
		log.Printf("alerts: %s", msg)
	}
}

func (a *App) alertsView(admin bool) map[string]any {
	out := map[string]any{"items": a.Alerts.List(), "unread": a.Alerts.Unread()}
	if admin {
		out["attention"] = a.setupAttention() // device setups that stopped: Home lists them
		c := a.Cfg.Get()
		a.mu.Lock()
		e := a.alertErr
		a.mu.Unlock()
		out["settings"] = map[string]any{"new_device": orDefault(c.Alerts.NewDevice, "home"), "setup": c.Alerts.Setup != "off", "macos": c.Alerts.MacOS, "macos_possible": isMac,
			"ntfy_set": a.Sec.Get(slotNtfy) != "", "webhook_set": a.Sec.Get(slotWebhook) != "", "error": nilIfEmpty(e)}
	}
	return out
}

// alertsSettings saves what to alert about and where to send it ("" keeps a saved address, "-" removes it).
func (a *App) alertsSettings(body map[string]any) error {
	for slot, k := range map[string]string{slotNtfy: "ntfy", slotWebhook: "webhook"} {
		v := s(body, k)
		switch {
		case v == "":
		case v == "-":
			a.Sec.Delete(slot)
		default:
			if err := alerts.CheckURL(v); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			if err := a.Sec.Set(slot, v); err != nil {
				return err
			}
		}
	}
	return a.Cfg.Update(func(c *config.Config) {
		if has(body, "new_device") {
			switch v := s(body, "new_device"); v {
			case "home":
				c.Alerts.NewDevice = ""
			case "all", "off":
				c.Alerts.NewDevice = v
			}
		}
		if has(body, "setup") {
			c.Alerts.Setup = map[bool]string{true: "", false: "off"}[b(body, "setup")]
		}
		if has(body, "macos") {
			c.Alerts.MacOS = b(body, "macos")
		}
	})
}

// testAlert sends a test right away and says what went wrong.
func (a *App) testAlert(ctx context.Context) error {
	al := alerts.Alert{Kind: "test", Level: "info", Title: "Test alert", Body: "If you can read this, HomeOwl's alerts reach you here.", Time: time.Now().UnixMilli()}
	errs := alerts.Deliver(ctx, a.alertChannels(), al)
	a.Alerts.Add(withoutSend(al))
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// withoutSend marks an alert as already delivered (the store's Send skips it).
func withoutSend(al alerts.Alert) alerts.Alert {
	if al.Data == nil {
		al.Data = map[string]any{}
	}
	al.Data["delivered"] = true
	return al
}

// ---- new devices

// seenClients remembers every device address HomeOwl has seen, so a new one stands out.
type seenClients struct {
	mu   sync.Mutex
	path string
	m    map[string]int64 // mac → first seen (ms)
}

func newSeen(dir string) *seenClients {
	s := &seenClients{path: filepath.Join(dir, "seen-clients.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.m)
	}
	return s
}

func (s *seenClients) seeded() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.m != nil }

// add returns the addresses it hadn't seen before.
func (s *seenClients) add(macs []string, now int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]int64{}
	}
	var fresh []string
	for _, m := range macs {
		if _, ok := s.m[m]; !ok && m != "" {
			s.m[m] = now
			fresh = append(fresh, m)
		}
	}
	if len(fresh) > 0 || len(s.m) == 0 {
		b, _ := json.Marshal(s.m)
		_ = os.WriteFile(s.path, b, 0o600)
	}
	return fresh
}

// watchNewDevices gets the clients online now (stat/sta). The first time, everything UniFi already knows is
// remembered quietly; after that, a device that was never seen before raises an alert.
func (a *App) watchNewDevices(online []map[string]any) {
	mode := a.Cfg.Get().Alerts.NewDevice
	if mode == "off" {
		return
	}
	now := time.Now().UnixMilli()
	if !a.seen.seeded() {
		st, d := a.UniFi.Get(a.UniFi.SitePath("rest/user", false))
		if st != 200 {
			return
		}
		var all []string
		for _, c := range append(unifi.List(d), online...) {
			all = append(all, strings.ToLower(str(c, "mac")))
		}
		a.seen.add(all, now)
		return
	}
	byMac := map[string]map[string]any{}
	var macs []string
	for _, c := range online {
		if mac := strings.ToLower(str(c, "mac")); mac != "" {
			byMac[mac] = c
			macs = append(macs, mac)
		}
	}
	var report []map[string]any
	for _, mac := range a.seen.add(macs, now) {
		c := byMac[mac]
		if g, _ := c["is_guest"].(bool); g && mode != "all" {
			continue
		}
		report = append(report, c)
	}
	if len(report) == 0 {
		return
	}
	if len(report) > 5 { // e.g. after a long time offline: one alert, not twenty
		names := []string{}
		for _, c := range report[:5] {
			names = append(names, clientName(c))
		}
		a.Alerts.Add(alerts.Alert{Kind: "new_device", Level: "warn", Title: strconv.Itoa(len(report)) + " new devices joined",
			Body: strings.Join(names, ", ") + "…", Data: map[string]any{"count": len(report)}})
		return
	}
	for _, c := range report {
		mac := strings.ToLower(str(c, "mac"))
		parts := []string{}
		for _, p := range []string{str(c, "oui"), str(c, "ip"), firstNonEmpty(str(c, "essid"), str(c, "network"))} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(mac) > 1 && strings.ContainsRune("26ae", rune(mac[1])) {
			parts = append(parts, "private address")
		}
		a.Alerts.Add(alerts.Alert{Kind: "new_device", Level: "warn", Title: "New device: " + clientName(c), Body: strings.Join(parts, " · "),
			Data: map[string]any{"mac": mac, "ip": str(c, "ip"), "name": clientName(c), "network": str(c, "network"), "guest": c["is_guest"] == true}})
	}
}

func clientName(c map[string]any) string {
	return firstNonEmpty(str(c, "name"), str(c, "hostname"), str(c, "oui"), "Unknown device")
}

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

// ---- guest Wi-Fi password on a schedule

// lastRotation is the most recent scheduled time at or before now (0 when there's no schedule).
func lastRotation(g config.Guest, now time.Time) time.Time {
	h := min(max(g.RotateHour, 0), 23)
	switch g.Rotate {
	case "weekly":
		t := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
		for t.After(now) || int(t.Weekday()) != g.RotateDay%7 {
			t = t.AddDate(0, 0, -1)
		}
		return t
	case "monthly":
		d := min(max(g.RotateDay, 1), 28)
		t := time.Date(now.Year(), now.Month(), d, h, 0, 0, 0, now.Location())
		if t.After(now) {
			t = t.AddDate(0, -1, 0)
		}
		return t
	}
	return time.Time{}
}

func nextRotation(g config.Guest, now time.Time) time.Time {
	switch g.Rotate {
	case "weekly":
		return lastRotation(g, now).AddDate(0, 0, 7)
	case "monthly":
		return lastRotation(g, now).AddDate(0, 1, 0)
	}
	return time.Time{}
}

func (a *App) guestRotateLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		g := a.Cfg.Get().Guest
		due := lastRotation(g, time.Now())
		if g.Rotate == "" || due.IsZero() || due.UnixMilli() <= g.RotateSince || !a.UniFi.Connected() {
			continue
		}
		if _, err := a.rotateGuestPasswords("schedule"); err != nil {
			log.Printf("guest Wi-Fi schedule: %v", err)
		}
	}
}

// rotateGuestPasswords gives every guest Wi-Fi with a shared password a new one (allow-listed, logged, undoable).
func (a *App) rotateGuestPasswords(by string) (int, error) {
	if a.ReadOnly {
		return 0, errors.New("HomeOwl was started in read-only mode")
	}
	res, err := a.guestWiFi(true)
	if err != nil {
		return 0, err
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Guest.RotateSince = time.Now().UnixMilli() }) // once per period, even if it fails
	done, names := 0, []string{}
	var errs []error
	for _, n := range res["networks"].([]map[string]any) {
		if can, _ := n["can_rotate"].(bool); !can {
			continue
		}
		id, _ := n["id"].(string)
		ssid, _ := n["ssid"].(string)
		act := unifi.Action{"kind": "guestpass", "wlan_id": id, "password": unifi.NewGuestPassword(), "description": "Scheduled new guest Wi-Fi password for " + ssid}
		if by != "schedule" {
			act["description"] = "New guest Wi-Fi password for " + ssid
		}
		if _, err := a.Changes.Run(a.UniFi, act, false, by, false); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ssid, err))
			continue
		}
		done++
		names = append(names, ssid)
	}
	a.Cache.Invalidate()
	if done > 0 {
		a.Alerts.Add(alerts.Alert{Kind: "guest_rotated", Title: "New guest Wi-Fi password",
			Body: strings.Join(names, ", ") + " has a new password. The guest card shows it; print new cards from Guest Wi-Fi."})
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		a.Alerts.Add(alerts.Alert{Kind: "guest_rotated", Level: "warn", Title: "Couldn't change the guest Wi-Fi password", Body: err.Error()})
		return done, err
	}
	if done == 0 {
		return 0, errors.New("no guest Wi-Fi with a shared password to change")
	}
	return done, nil
}

// guestSchedule saves the rotation schedule; the next rotation is the next scheduled time from now.
func (a *App) guestSchedule(body map[string]any) (any, error) {
	rot := s(body, "rotate")
	if rot != "" && rot != "weekly" && rot != "monthly" {
		return nil, errors.New("pick weekly, monthly or off")
	}
	day, _ := n(body, "day")
	hour, _ := n(body, "hour")
	err := a.Cfg.Update(func(c *config.Config) {
		c.Guest.Rotate, c.Guest.RotateDay, c.Guest.RotateHour = rot, day, min(max(hour, 0), 23)
		c.Guest.RotateSince = time.Now().UnixMilli()
	})
	return a.guestScheduleView(), err
}

func (a *App) guestScheduleView() map[string]any {
	g := a.Cfg.Get().Guest
	out := map[string]any{"rotate": g.Rotate, "day": g.RotateDay, "hour": g.RotateHour}
	if g.Rotate != "" {
		out["next"] = nextRotation(g, time.Now()).UnixMilli()
	}
	return out
}
