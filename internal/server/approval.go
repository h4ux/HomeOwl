package server

import (
	"encoding/json"
	"errors"
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

// Device approval (Settings › Device profiles): with it on, a device that joins the home Wi-Fi for the first time
// is blocked in UniFi until an admin approves it — unless it's a checked-in device (it came through the setup Wi-Fi
// and its profile, or it was approved before). Everything the console already knew when it was turned on counts as
// approved, so nothing in the house is cut off. Guest networks and the setup Wi-Fi aren't gated, nor are wired
// devices (UniFi's block is for Wi-Fi). Blocks go through the normal allow-listed action — logged, with undo — and
// HomeOwl only ever blocks on its own: unblocking is the admin's Approve.

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
	Seeded   int64                    `json:"seeded,omitempty"`
}

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
	var block []map[string]any
	for _, c := range online {
		mac := strings.ToLower(str(c, "mac"))
		if mac == "" || !a.gated(c) || st.Approved[mac] > 0 {
			continue
		}
		if _, done := st.Blocked[mac]; done {
			continue
		}
		if _, ok := a.checkins.Registered(mac); ok {
			st.Approved[mac] = time.Now().Unix()
			continue
		}
		if a.movingHere(str(c, "hostname")) {
			continue
		}
		block = append(block, c)
	}
	_ = s.save(st)
	s.mu.Unlock()

	if len(block) > maxBlocksPerTick {
		a.Alerts.Add(alerts.Alert{Kind: "approval_flood", Level: "warn", Title: "Device approval paused",
			Body: "More than 10 new devices joined at once, so HomeOwl blocked none of them. Look at Settings › Device profiles › Approve new devices."})
		return
	}
	for _, c := range block {
		a.blockForApproval(c)
	}
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
	return map[string]any{"on": a.Cfg.Get().Onboard.Approve, "approved": len(st.Approved), "seeded": st.Seeded, "waiting": waiting, "kept": kept}
}

// approvalAPI: on/off, approve (unblock, optionally give it to someone), keep blocked.
func (a *App) approvalAPI(op string, body map[string]any, by string) (map[string]any, error) {
	switch op {
	case "set":
		on := b(body, "on")
		if on {
			if err := a.seedApproval(); err != nil {
				return nil, err
			}
		}
		if err := a.Cfg.Update(func(c *config.Config) { c.Onboard.Approve = on }); err != nil {
			return nil, err
		}
	case "approve", "keep":
		mac := strings.ToLower(s(body, "mac"))
		as := a.approval
		as.mu.Lock()
		st := as.load()
		d, ok := st.Blocked[mac]
		as.mu.Unlock()
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
	now := time.Now().Unix()
	for _, c := range append(unifi.List(d1), unifi.List(d2)...) {
		if mac := strings.ToLower(str(c, "mac")); mac != "" && st.Approved[mac] == 0 {
			if _, blocked := st.Blocked[mac]; !blocked {
				st.Approved[mac] = now
			}
		}
	}
	for _, x := range a.checkins.List() {
		if x.Status == "approved" && x.MAC != "" {
			st.Approved[x.MAC] = now
		}
	}
	if len(st.Approved) == 0 {
		return errors.New("UniFi lists no devices yet, so there's nothing to start from")
	}
	st.Seeded = now
	return s.save(st)
}
