package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Device approval (Settings › Device profiles): with it on, a device that joins the home Wi-Fi for the first time
// is blocked in UniFi until an admin approves it — unless it's a checked-in device (it came through the setup Wi-Fi
// and its profile, or it was approved before). Everything the console already knew when it was turned on counts as
// approved, so nothing in the house is cut off. Guest networks and the setup Wi-Fi aren't gated, nor are wired
// devices (UniFi's block is for Wi-Fi). Blocks go through the normal allow-listed action — logged, with undo — and
// HomeOwl only ever blocks on its own: unblocking is the admin's Approve, or unblocking it in UniFi (which works
// while HomeOwl is down, and HomeOwl then counts as an approval). A stopped HomeOwl blocks nothing new.

const maxBlocksPerTick = 10 // a sudden flood (say, the list of known devices was lost) stops at this and alerts

type approvalStore struct {
	mu   sync.Mutex
	path string
}

type pendingDevice struct {
	MAC      string `json:"mac"`
	Name     string `json:"name,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	SSID     string `json:"ssid,omitempty"`
	At       int64  `json:"at"`
	Kept     bool   `json:"kept,omitempty"` // the admin chose to keep it blocked: no more alerts
}

type approvalState struct {
	Approved map[string]int64         `json:"approved"` // MAC → when
	Blocked  map[string]pendingDevice `json:"blocked"`
	Grace    map[string]pendingDevice `json:"grace,omitempty"` // new, still online during the grace period (At: since)
	Used     map[string]int64         `json:"used,omitempty"`  // approval links already used → when they expire
	Seeded   int64                    `json:"seeded,omitempty"`
	// SeedRecent: the seed took only devices seen recently (older seeds approved everything UniFi ever saw, and
	// are trimmed once)
	SeedRecent bool `json:"seed_recent,omitempty"`
}

// recentSeed: turning approval on approves the devices seen this recently; older ones wait for approval when they
// come back
const recentSeed = 24 * time.Hour

func newApprovalStore(dir string) *approvalStore {
	return &approvalStore{path: filepath.Join(dir, "device-approval.json")}
}

func (s *approvalStore) load() approvalState {
	st := approvalState{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Approved == nil {
		st.Approved = map[string]int64{}
	}
	if st.Blocked == nil {
		st.Blocked = map[string]pendingDevice{}
	}
	return st
}

func (s *approvalStore) save(st approvalState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

// gated: a Wi-Fi client on a network the approval covers.
func (a *App) gated(c map[string]any) bool {
	return c["is_wired"] != true && c["is_guest"] != true && !a.onSetupWiFi(c)
}

// movingHere: a checked-in device is expected to show up with a new address and this host name (fixedmac.go),
// so its new address isn't a stranger even before HomeOwl has followed it.
func (a *App) movingHere(hostname string) bool {
	if hostname == "" {
		return false
	}
	for _, x := range a.checkins.List() {
		if x.FixedFrom != "" && x.Status == "approved" && strings.EqualFold(x.FixedHost, hostname) {
			return true
		}
	}
	return false
}

// approvalTick runs with the presence check, once a minute, after HomeOwl followed moved devices.
func (a *App) approvalTick(online []map[string]any) {
	if !a.Cfg.Get().Onboard.Approve {
		return
	}
	s := a.approval
	s.mu.Lock()
	st := s.load()
	if st.Seeded == 0 || len(st.Approved) == 0 { // never block without knowing what was there before
		s.mu.Unlock()
		return
	}
	if !st.SeedRecent { // an older seed approved every device UniFi ever saw: trim it once
		s.mu.Unlock()
		if _, err := a.trimSeed(); err != nil {
			log.Printf("device approval: %v", err)
		}
		s.mu.Lock()
		st = s.load()
	}
	var block []map[string]any
	var unblocked []string
	var settle []CheckIn
	for _, c := range online {
		mac := strings.ToLower(str(c, "mac"))
		if mac == "" || !a.gated(c) || st.Approved[mac] > 0 {
			continue
		}
		if d, done := st.Blocked[mac]; done {
			if x, ok := a.checkins.Registered(mac); ok && x.Time > d.At { // checked in and approved after the block
				settle = append(settle, x)
				continue
			}
			// online well after the block, so someone unblocked it in UniFi (say, while HomeOwl was down): that's an
			// approval (right after a block, the console may still list it for a moment)
			if c["blocked"] != true && time.Now().Unix()-d.At > 300 {
				delete(st.Blocked, mac)
				st.Approved[mac] = time.Now().Unix()
				unblocked = append(unblocked, orStr(d.Name, orStr(d.Hostname, mac)))
			}
			continue
		}
		if _, ok := a.checkins.Registered(mac); ok {
			st.Approved[mac] = time.Now().Unix()
			continue
		}
		if a.checkins.Waiting(mac) { // it checked in: that request is its approval (no second one, no block meanwhile)
			delete(st.Grace, mac)
			continue
		}
		if a.movingHere(str(c, "hostname")) {
			continue
		}
		block = append(block, c)
	}
	_ = s.save(st)
	s.mu.Unlock()
	for _, x := range settle {
		if err := a.approvalSettle(x.MAC, x.Name, "device approval"); err != nil {
			log.Printf("device approval: letting %s on: %v", x.Name, err)
		}
	}
	for _, n := range unblocked {
		log.Printf("device approval: %s was unblocked in UniFi, so it counts as approved", n)
		a.Alerts.Add(alerts.Alert{Kind: "approval", Level: "info", Title: n + " was unblocked in UniFi",
			Body: "It's connected again, so HomeOwl counts it as approved and won't block it again."})
	}

	if g := a.Cfg.Get().Onboard.GraceMinutes; g > 0 {
		block = a.graceFilter(block, time.Duration(g)*time.Minute, time.Now())
	}
	if len(block) > maxBlocksPerTick {
		a.Alerts.Add(alerts.Alert{Kind: "approval_flood", Level: "warn", Title: "Device approval paused",
			Body: "More than 10 new devices joined at once, so HomeOwl blocked none of them. Look at Settings › Device profiles › Approve new devices."})
		return
	}
	for _, c := range block {
		a.blockForApproval(c)
	}
}

// graceFilter: new devices get the grace period (and the admins an alert); those whose grace is over are blocked.
func (a *App) graceFilter(cands []map[string]any, grace time.Duration, now time.Time) []map[string]any {
	s := a.approval
	s.mu.Lock()
	st := s.load()
	if st.Grace == nil {
		st.Grace = map[string]pendingDevice{}
	}
	var due []map[string]any
	var fresh []pendingDevice
	for _, c := range cands {
		mac := strings.ToLower(str(c, "mac"))
		d, ok := st.Grace[mac]
		if !ok {
			d = pendingDevice{MAC: mac, Name: str(c, "name"), Hostname: str(c, "hostname"), Vendor: str(c, "oui"), SSID: str(c, "essid"), At: now.Unix()}
			st.Grace[mac] = d
			fresh = append(fresh, d)
			continue
		}
		if now.Sub(time.Unix(d.At, 0)) >= grace {
			due = append(due, c)
			delete(st.Grace, mac)
		}
	}
	for mac, d := range st.Grace { // approved meanwhile, or long gone
		if st.Approved[mac] > 0 || now.Sub(time.Unix(d.At, 0)) > 30*24*time.Hour {
			delete(st.Grace, mac)
		}
	}
	_ = s.save(st)
	s.mu.Unlock()
	if len(fresh) > maxBlocksPerTick {
		a.Alerts.Add(alerts.Alert{Kind: "approval_flood", Level: "warn", Title: fmt.Sprintf("%d new devices joined at once", len(fresh)),
			Body: "They're online for now. Look at Settings › Device profiles › Approve new devices."})
		return due
	}
	for _, d := range fresh {
		name := orStr(d.Name, orStr(d.Hostname, d.MAC))
		a.Alerts.Add(alerts.Alert{Kind: "approval", Level: "warn", Title: name + " joined the home Wi-Fi",
			Body: "It's online for now. HomeOwl blocks it in " + fmtGrace(grace) + " unless you approve it.",
			Data: map[string]any{"mac": d.MAC, "name": name, "grace": true}})
	}
	return due
}

// fmtGrace: 15 minutes, 4 hours, 3 days.
func fmtGrace(d time.Duration) string {
	m := int(d.Minutes())
	switch {
	case m%(24*60) == 0:
		return plural(m/(24*60), "day")
	case m%60 == 0:
		return plural(m/60, "hour")
	}
	return plural(m, "minute")
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return strconv.Itoa(n) + " " + w + "s"
}

func (a *App) blockForApproval(c map[string]any) {
	mac := strings.ToLower(str(c, "mac"))
	name := clientName(c)
	act := unifi.Action{"kind": "stacmd", "cmd": "block-sta", "mac": mac, "description": "Device approval: " + name + " is new, blocked until approved"}
	if _, err := a.Changes.Run(a.UniFi, act, false, "device approval", false); err != nil {
		log.Printf("device approval: couldn't block %s: %v", mac, err)
		return
	}
	s := a.approval
	s.mu.Lock()
	st := s.load()
	st.Blocked[mac] = pendingDevice{MAC: mac, Name: str(c, "name"), Hostname: str(c, "hostname"), Vendor: str(c, "oui"), SSID: str(c, "essid"), At: time.Now().Unix()}
	_ = s.save(st)
	s.mu.Unlock()
	a.Alerts.Add(alerts.Alert{Kind: "approval", Level: "warn", Title: name + " is waiting for your approval",
		Body: "A new device joined " + orStr(str(c, "essid"), "the Wi-Fi") + ". HomeOwl blocked it until you approve it.",
		Data: map[string]any{"mac": mac, "name": name}})
}

// approvalCheckedIn: the device checked in, so that request replaces its grace period's.
func (a *App) approvalCheckedIn(mac string) {
	s := a.approval
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load()
	if _, ok := st.Grace[strings.ToLower(mac)]; ok {
		delete(st.Grace, strings.ToLower(mac))
		_ = s.save(st)
	}
}

// approvalSettle: an approved device (a check-in, say) is approved for device approval too: out of its grace period,
// and unblocked if HomeOwl had blocked it.
func (a *App) approvalSettle(mac, name, by string) error {
	mac = strings.ToLower(mac)
	if mac == "" {
		return nil
	}
	s := a.approval
	s.mu.Lock()
	st := s.load()
	_, blocked := st.Blocked[mac]
	delete(st.Grace, mac)
	if st.Seeded > 0 { // approval in use (or used): remember it, so it's never blocked later
		st.Approved[mac] = time.Now().Unix()
	}
	err := s.save(st)
	s.mu.Unlock()
	if err != nil || !blocked {
		return err
	}
	act := unifi.Action{"kind": "stacmd", "cmd": "unblock-sta", "mac": mac, "description": "Device approval: approved " + orStr(name, mac)}
	res, err := a.Changes.Run(a.UniFi, act, false, by, false)
	if err != nil {
		return err
	}
	if ch, ok := res["result"].(unifi.Change); ok && !ch.OK {
		return errors.New(ch.Error)
	}
	s.mu.Lock()
	st = s.load()
	delete(st.Blocked, mac)
	err = s.save(st)
	s.mu.Unlock()
	return err
}

// approvalBlocked: is this MAC blocked by device approval (the new-device alert then says nothing more)?
func (a *App) approvalBlocked(mac string) bool {
	s := a.approval
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.load().Blocked[strings.ToLower(mac)]
	return ok
}

func (a *App) approvalView() map[string]any {
	s := a.approval
	s.mu.Lock()
	st := s.load()
	s.mu.Unlock()
	waiting, kept := []pendingDevice{}, []pendingDevice{}
	for _, d := range st.Blocked {
		if d.Kept {
			kept = append(kept, d)
		} else {
			waiting = append(waiting, d)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].At > waiting[j].At })
	sort.Slice(kept, func(i, j int) bool { return kept[i].At > kept[j].At })
	gm := a.Cfg.Get().Onboard.GraceMinutes
	grace := []map[string]any{}
	for _, d := range st.Grace {
		if st.Approved[d.MAC] == 0 {
			grace = append(grace, map[string]any{"mac": d.MAC, "name": d.Name, "hostname": d.Hostname, "vendor": d.Vendor, "ssid": d.SSID, "at": d.At,
				"ends": d.At + int64(gm)*60})
		}
	}
	sort.Slice(grace, func(i, j int) bool { return grace[i]["at"].(int64) > grace[j]["at"].(int64) })
	return map[string]any{"on": a.Cfg.Get().Onboard.Approve, "approved": len(st.Approved), "seeded": st.Seeded, "recent_hours": int(recentSeed.Hours()), "waiting": waiting, "kept": kept,
		"grace_minutes": gm, "grace": grace, "push": a.Sec.Get(slotNtfy) != ""}
}

// approvalAPI: on/off, approve (unblock, optionally give it to someone), keep blocked.
func (a *App) approvalAPI(op string, body map[string]any, by string) (map[string]any, error) {
	switch op {
	case "set":
		if has(body, "grace_minutes") && !has(body, "on") {
			g, _ := n(body, "grace_minutes")
			if g < 0 || g > 30*24*60 {
				return nil, errors.New("the grace period is up to 30 days")
			}
			if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.GraceMinutes = int(g) }); err != nil {
				return nil, err
			}
			break
		}
		on := b(body, "on")
		if on {
			if err := a.seedApproval(); err != nil {
				return nil, err
			}
		}
		if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.Approve = on }); err != nil {
			return nil, err
		}
	case "trim":
		n, err := a.trimSeed()
		if err != nil {
			return nil, err
		}
		v := a.approvalView()
		v["trimmed"] = n
		return v, nil
	case "approve", "keep", "block":
		mac := strings.ToLower(s(body, "mac"))
		if x, ok := a.checkins.pendingFor(mac); ok && op == "approve" {
			req := map[string]any{"id": x.ID}
			if pid := s(body, "person_id"); pid != "" {
				req["person_id"] = pid
			}
			if _, err := a.approveCheckIn(req, by); err != nil {
				return nil, err
			}
			break
		}
		as := a.approval
		as.mu.Lock()
		st := as.load()
		d, ok := st.Blocked[mac]
		g, inGrace := st.Grace[mac]
		as.mu.Unlock()
		if inGrace && !ok { // still online in its grace period: approve without an unblock, or block it now
			as.mu.Lock()
			st = as.load()
			delete(st.Grace, mac)
			if op == "approve" {
				st.Approved[mac] = time.Now().Unix()
			}
			err := as.save(st)
			as.mu.Unlock()
			if err != nil {
				return nil, err
			}
			if op == "approve" {
				if pid := s(body, "person_id"); pid != "" && a.An.Open() {
					if err := a.An.AssignDevice(pid, "mac", mac); err != nil {
						return nil, err
					}
				}
			} else {
				a.blockForApproval(map[string]any{"mac": mac, "name": g.Name, "hostname": g.Hostname, "oui": g.Vendor, "essid": g.SSID})
			}
			break
		}
		if op == "block" {
			return nil, errors.New("that device isn't in its grace period any more")
		}
		if !ok {
			return nil, errors.New("that device isn't waiting any more")
		}
		if op == "keep" {
			d.Kept = true
			as.mu.Lock()
			st = as.load()
			st.Blocked[mac] = d
			err := as.save(st)
			as.mu.Unlock()
			if err != nil {
				return nil, err
			}
			break
		}
		act := unifi.Action{"kind": "stacmd", "cmd": "unblock-sta", "mac": mac, "description": "Device approval: approved " + orStr(d.Name, orStr(d.Hostname, mac))}
		if _, err := a.Changes.Run(a.UniFi, act, false, by, false); err != nil {
			return nil, err
		}
		if pid := s(body, "person_id"); pid != "" && a.An.Open() {
			if err := a.An.AssignDevice(pid, "mac", mac); err != nil {
				return nil, err
			}
		}
		as.mu.Lock()
		st = as.load()
		delete(st.Blocked, mac)
		st.Approved[mac] = time.Now().Unix()
		err := as.save(st)
		as.mu.Unlock()
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("not found")
	}
	return a.approvalView(), nil
}

// seedApproval: everything the console knows now (every client it has seen, and those online) is approved.
func (a *App) seedApproval() error {
	if !a.UniFi.Connected() {
		return errors.New("connect UniFi first: HomeOwl needs the devices you already have, so it doesn't block them")
	}
	st1, d1 := a.UniFi.Get(a.UniFi.SitePath("rest/user", false))
	st2, d2 := a.UniFi.Get(a.UniFi.SitePath("stat/sta", false))
	if st1 != 200 || st2 != 200 {
		return errors.New("couldn't read the devices UniFi knows — try again in a minute")
	}
	s := a.approval
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load()
	now := time.Now()
	if st.Seeded != 0 { // a new start: the old seed's devices go, approvals made by hand stay
		for mac, t := range st.Approved {
			if t == st.Seeded {
				delete(st.Approved, mac)
			}
		}
	}
	for mac := range recentMACs(unifi.List(d1), unifi.List(d2), now) {
		if _, blocked := st.Blocked[mac]; !blocked && st.Approved[mac] == 0 {
			st.Approved[mac] = now.Unix()
		}
	}
	for _, x := range a.checkins.List() {
		if x.Status == "approved" && x.MAC != "" && st.Approved[x.MAC] == 0 {
			st.Approved[x.MAC] = now.Unix()
		}
	}
	if len(st.Approved) == 0 {
		return errors.New("UniFi lists no devices seen in the last day, so there's nothing to start from")
	}
	st.Seeded, st.SeedRecent = now.Unix(), true
	return s.save(st)
}

// recentMACs: devices online now or seen within recentSeed.
func recentMACs(known, online []map[string]any, now time.Time) map[string]bool {
	out := map[string]bool{}
	for _, c := range online {
		if mac := strings.ToLower(str(c, "mac")); mac != "" {
			out[mac] = true
		}
	}
	for _, c := range known {
		mac := strings.ToLower(str(c, "mac"))
		if ls, ok := c["last_seen"].(float64); ok && mac != "" && now.Sub(time.Unix(int64(ls), 0)) < recentSeed {
			out[mac] = true
		}
	}
	return out
}

// trimSeed takes back the approvals a seed gave to devices not seen recently (approvals made by hand and checked-in
// devices stay). Returns how many went.
func (a *App) trimSeed() (int, error) {
	st1, d1 := a.UniFi.Get(a.UniFi.SitePath("rest/user", false))
	st2, d2 := a.UniFi.Get(a.UniFi.SitePath("stat/sta", false))
	if st1 != 200 || st2 != 200 {
		return 0, errors.New("couldn't read the devices UniFi knows — try again in a minute")
	}
	recent := recentMACs(unifi.List(d1), unifi.List(d2), time.Now())
	s := a.approval
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load()
	n := 0
	for mac, t := range st.Approved {
		if t != st.Seeded || recent[mac] {
			continue
		}
		if _, ok := a.checkins.Registered(mac); ok {
			continue
		}
		delete(st.Approved, mac)
		n++
	}
	if len(st.Approved) == 0 {
		return 0, errors.New("nothing was seen in the last day, so HomeOwl left the list alone")
	}
	st.SeedRecent = true
	return n, s.save(st)
}
