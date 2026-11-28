package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Guests (Network › Wi-Fi › Guest Wi-Fi › Guests): devices on the guest Wi-Fi never wait for approval; the admins can
// be told when one joins for the first time; and guest devices that haven't been seen for a while can be removed
// from UniFi's client list ("forget"), so it doesn't fill up with visitors' phones. A removed device can join again
// with the guest password: it then counts as new.

// ---- which networks are for guests

var guestNetsCache struct {
	sync.Mutex
	at    time.Time
	ssids map[string]bool // Wi-Fi names
	nets  map[string]bool // network ids
}

// guestNets: the guest Wi-Fi names and the networks behind them (UniFi's guest flag, a guest network, or picked on
// the Guest Wi-Fi page). Read at most every 10 minutes.
func (a *App) guestNets() (ssids, nets map[string]bool) {
	c := &guestNetsCache
	c.Lock()
	defer c.Unlock()
	if time.Since(c.at) < 10*time.Minute && c.ssids != nil {
		return c.ssids, c.nets
	}
	if a.UniFi == nil || !a.UniFi.Connected() {
		return map[string]bool{}, map[string]bool{}
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("rest/wlanconf", false))
	if st != 200 {
		return map[string]bool{}, map[string]bool{}
	}
	c.ssids, c.nets, c.at = map[string]bool{}, map[string]bool{}, time.Now()
	home := map[string]bool{} // networks a home Wi-Fi uses too: a device on them isn't a guest by its network alone
	for _, w := range unifi.List(d) {
		id := str(w, "networkconf_id")
		if !a.UniFi.IsGuestWLAN(w) {
			if id != "" {
				home[id] = true
			}
			continue
		}
		if n := str(w, "name"); n != "" {
			c.ssids[n] = true
		}
		if id != "" {
			c.nets[id] = true
		}
	}
	for id := range home {
		delete(c.nets, id)
	}
	return c.ssids, c.nets
}

// forgetGuestNets drops the cache (the admin picked other guest Wi-Fi networks).
func forgetGuestNets() {
	guestNetsCache.Lock()
	guestNetsCache.at = time.Time{}
	guestNetsCache.Unlock()
}

// isGuestClient: a client online on the guest Wi-Fi (UniFi says so, it's on a guest Wi-Fi name, or on the guest
// network, which is also where HomeOwl puts guests it found on the home Wi-Fi).
func (a *App) isGuestClient(c map[string]any) bool {
	if c["is_guest"] == true {
		return true
	}
	if c["is_wired"] == true || a.onSetupWiFi(c) {
		return false
	}
	ssids, nets := a.guestNets()
	return ssids[str(c, "essid")] || nets[str(c, "network_id")]
}

// ---- the guest devices HomeOwl has seen

type guestSeen struct {
	MAC      string `json:"mac"`
	Name     string `json:"name,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	SSID     string `json:"ssid,omitempty"`
	First    int64  `json:"first"` // ms
	Last     int64  `json:"last"`  // ms
}

type guestLog struct {
	mu   sync.Mutex
	path string
	m    map[string]guestSeen
}

func newGuestLog(dir string) *guestLog {
	g := &guestLog{path: filepath.Join(dir, "guests-seen.json"), m: map[string]guestSeen{}}
	if b, err := os.ReadFile(g.path); err == nil {
		_ = json.Unmarshal(b, &g.m)
	}
	if g.m == nil {
		g.m = map[string]guestSeen{}
	}
	return g
}

func (g *guestLog) saveLocked() {
	b, _ := json.Marshal(g.m)
	if err := config.WriteFileAtomic(g.path, b, 0o600); err != nil {
		log.Printf("guests: %v", err)
	}
}

// observe: guests online now are remembered (or their last time moved on); a device seen on a home network isn't
// a guest, so it's dropped from the list.
func (g *guestLog) observe(guests, others []map[string]any, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	changed := false
	ms := now.UnixMilli()
	for _, c := range others {
		if mac := strings.ToLower(str(c, "mac")); mac != "" {
			if _, ok := g.m[mac]; ok {
				delete(g.m, mac)
				changed = true
			}
		}
	}
	for _, c := range guests {
		mac := strings.ToLower(str(c, "mac"))
		if mac == "" {
			continue
		}
		x, ok := g.m[mac]
		if !ok {
			x = guestSeen{MAC: mac, First: ms}
		}
		x.Name, x.Hostname, x.Vendor = orStr(str(c, "name"), x.Name), orStr(str(c, "hostname"), x.Hostname), orStr(str(c, "oui"), x.Vendor)
		x.SSID = orStr(str(c, "essid"), x.SSID)
		// write at most every 10 minutes per device: a minute's precision doesn't matter for days
		if !ok || ms-x.Last > 10*60000 {
			changed = true
		}
		x.Last = ms
		g.m[mac] = x
	}
	if changed {
		g.saveLocked()
	}
}

func (g *guestLog) list() []guestSeen {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]guestSeen, 0, len(g.m))
	for _, x := range g.m {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out
}

func (g *guestLog) drop(mac string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.m[mac]; ok {
		delete(g.m, mac)
		g.saveLocked()
	}
}

// guestTick runs with the presence check, once a minute.
func (a *App) guestTick(online []map[string]any) {
	var guests, others []map[string]any
	for _, c := range online {
		if a.isGuestClient(c) {
			guests = append(guests, c)
		} else {
			others = append(others, c)
		}
	}
	a.guests.observe(guests, others, time.Now())
}

// guestAlertsOn: tell the admins when a device joins the guest Wi-Fi.
func guestAlertsOn(c config.Config) bool { return c.Guest.Alert || c.Alerts.NewDevice == "all" }

// ---- removing guests that haven't been seen for a while

const maxForgetPerRun = 25 // more than this at once waits for the next run (and says so)

type forgetCand struct {
	MAC    string `json:"mac"`
	Name   string `json:"name"`
	SSID   string `json:"ssid,omitempty"`
	Last   int64  `json:"last"`   // ms
	Due    int64  `json:"due"`    // ms: removed from then on (0 = never)
	Reason string `json:"reason"` // why it's kept, if it is
}

// guestCandidates: the guest devices HomeOwl knows, when each was last seen (by HomeOwl or UniFi), and when it's due
// for removal; kept ones say why.
func (a *App) guestCandidates(now time.Time) []forgetCand {
	c := a.Cfg.Get()
	days := c.Guest.ForgetDays
	known := map[string]map[string]any{}
	if b := a.Cache.Peek(); b != nil {
		leg, _ := b["legacy"].(map[string]any)
		for _, k := range unifi.List(leg["known_clients"]) {
			known[strings.ToLower(str(k, "mac"))] = k
		}
	}
	seen := map[string]guestSeen{}
	for _, x := range a.guests.list() {
		seen[x.MAC] = x
	}
	// UniFi's own guest flag on a known client counts too (devices that left before HomeOwl watched)
	for mac, k := range known {
		if _, ok := seen[mac]; !ok && k["is_guest"] == true {
			seen[mac] = guestSeen{MAC: mac, Name: orStr(str(k, "name"), str(k, "hostname"), str(k, "oui")), Last: int64(num(k["last_seen"])) * 1000}
		}
	}
	out := []forgetCand{}
	for mac, x := range seen {
		last := x.Last
		if t := a.online.at(mac); t.UnixMilli() > last {
			last = t.UnixMilli()
		}
		k := known[mac]
		if ls := int64(num(k["last_seen"])) * 1000; ls > last {
			last = ls
		}
		f := forgetCand{MAC: mac, Name: orStr(str(k, "name"), x.Name, x.Hostname, x.Vendor, mac), SSID: x.SSID, Last: last}
		switch {
		case a.isCheckedIn(mac):
			f.Reason = "checked in as a family device"
		case c.Presence.Devices[mac] != "":
			f.Reason = "tagged in Who's home"
		case k != nil && (k["use_fixedip"] == true || str(k, "fixed_ip") != ""):
			f.Reason = "has a fixed IP address in UniFi"
		case k != nil && str(k, "note") != "":
			f.Reason = "has a note in UniFi"
		case days > 0:
			f.Due = last + int64(days)*86400000
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out
}

func (a *App) isCheckedIn(mac string) bool { _, ok := a.checkins.Registered(mac); return ok }

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return 0
}

// forgetStaleGuests removes guest devices whose time is up. Online devices never are. Returns the names removed.
func (a *App) forgetStaleGuests(now time.Time, by string) ([]string, error) {
	days := a.Cfg.Get().Guest.ForgetDays
	if days <= 0 || !a.UniFi.Connected() {
		return nil, nil
	}
	var due []forgetCand
	for _, f := range a.guestCandidates(now) {
		if f.Due > 0 && f.Due <= now.UnixMilli() && now.Sub(a.online.at(f.MAC)) > 10*time.Minute {
			due = append(due, f)
		}
	}
	more := 0
	if len(due) > maxForgetPerRun {
		more = len(due) - maxForgetPerRun
		due = due[:maxForgetPerRun]
	}
	var names []string
	var errs []string
	for _, f := range due {
		act := unifi.Action{"kind": "stacmd", "cmd": "forget-sta", "mac": f.MAC, "label": f.Name,
			"description": fmt.Sprintf("Guest not seen for %d days: remove %s from UniFi's client list", days, f.Name)}
		if err := a.runOK(act, by); err != nil {
			errs = append(errs, f.Name+": "+err.Error())
			continue
		}
		a.guests.drop(f.MAC)
		a.forgetGuestApproval(f.MAC)
		names = append(names, f.Name)
	}
	if len(names) > 0 {
		a.Cache.Invalidate()
		body := strings.Join(names, ", ") + fmt.Sprintf(". Not seen on the guest Wi-Fi for %d days; they can join again with the guest password.", days)
		if more > 0 {
			body += fmt.Sprintf(" %d more go at the next run.", more)
		}
		a.Alerts.Add(alerts.Alert{Kind: "guest_forgot", Level: "info", Title: fmt.Sprintf("Removed %s", plural(len(names), "guest device")), Body: body,
			Data: map[string]any{"count": len(names)}})
	}
	if len(errs) > 0 {
		return names, fmt.Errorf("couldn't remove %s", strings.Join(errs, "; "))
	}
	return names, nil
}

// forgetGuestApproval: a removed guest that comes back is new again, for the approval and for the alert.
func (a *App) forgetGuestApproval(mac string) {
	s := a.approval
	s.mu.Lock()
	st := s.load()
	delete(st.Guests, mac)
	delete(st.Approved, mac)
	_ = s.save(st)
	s.mu.Unlock()
	a.seen.forget(mac)
}

// guestForgetLoop checks every hour (and a minute after starting).
func (a *App) guestForgetLoop(ctx context.Context) {
	first := time.NewTimer(time.Minute)
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		}
		if _, err := a.forgetStaleGuests(time.Now(), "HomeOwl (guest cleanup)"); err != nil {
			log.Printf("guests: %v", err)
		}
	}
}

// ---- settings

// guestPolicyView: the guest settings and the guest devices with when each goes (admins).
func (a *App) guestPolicyView() map[string]any {
	c := a.Cfg.Get()
	return map[string]any{"alert": guestAlertsOn(c), "forget_days": c.Guest.ForgetDays, "devices": a.guestCandidates(time.Now())}
}

// guestPolicySave: {alert, forget_days}.
func (a *App) guestPolicySave(body map[string]any) error {
	days := c0(body, "forget_days")
	if days < 0 || days > 3650 {
		return fmt.Errorf("days must be 0–3650")
	}
	return a.Cfg.Update(func(c *config.Config) {
		if has(body, "alert") {
			c.Guest.Alert = b(body, "alert")
			if !c.Guest.Alert && c.Alerts.NewDevice == "all" { // "all" meant guests too: now it's this switch
				c.Alerts.NewDevice = ""
			}
		}
		if has(body, "forget_days") {
			c.Guest.ForgetDays = days
		}
	})
}

func c0(body map[string]any, k string) int {
	v, _ := n(body, k)
	return v
}
