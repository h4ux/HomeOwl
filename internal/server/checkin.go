package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Device check-in: a family member opens http://<HomeOwl>/hi on a phone, tablet or laptop and says what it is
// and whose it is. HomeOwl matches the request's IP to the UniFi client (MAC) and queues it; an admin approves,
// which sets the UniFi alias (allow-listed action, previewed and undoable) and gives the device to the person.

type CheckIn struct {
	ID         string `json:"id"`
	Time       int64  `json:"time"`
	IP         string `json:"ip"`
	MAC        string `json:"mac"`
	UserID     string `json:"user_id"` // UniFi rest/user _id (for the alias)
	Seen       string `json:"seen"`    // what UniFi calls it now
	Vendor     string `json:"vendor"`
	Network    string `json:"network"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	PersonID   string `json:"person_id"`
	PersonName string `json:"person_name"`
	Status     string `json:"status"` // pending | approved | rejected
	By         string `json:"by,omitempty"`
	NDProfile  string `json:"nd_profile,omitempty"` // NextDNS profile the device's setup uses (devprofile.go)
	ProfileAt  int64  `json:"profile_at,omitempty"` // when the device last downloaded its setup profile
}

var checkinKinds = map[string]bool{"phone": true, "tablet": true, "laptop": true, "computer": true, "tv": true, "console": true,
	"speaker": true, "watch": true, "camera": true, "printer": true, "other": true}

type checkins struct {
	mu   sync.Mutex
	path string
	hits map[string][]time.Time // per IP, for rate limiting
}

func (c *checkins) load() []CheckIn {
	var l []CheckIn
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (c *checkins) save(l []CheckIn) error {
	// keep every pending one, and the last 100 decided ones
	var keep, done []CheckIn
	for _, x := range l {
		if x.Status == "pending" {
			keep = append(keep, x)
		} else {
			done = append(done, x)
		}
	}
	if len(done) > 100 {
		done = done[len(done)-100:]
	}
	b, _ := json.MarshalIndent(append(done, keep...), "", "  ")
	return config.WriteFileAtomic(c.path, b, 0o600)
}

func (c *checkins) List() []CheckIn {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.load()
	if l == nil {
		l = []CheckIn{}
	}
	return l
}

func (c *checkins) allow(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hits == nil {
		c.hits = map[string][]time.Time{}
	}
	var recent []time.Time
	for _, t := range c.hits[ip] {
		if time.Since(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		c.hits[ip] = recent
		return false
	}
	c.hits[ip] = append(recent, time.Now())
	return true
}

// Add queues a check-in; a newer one from the same device replaces its pending one.
func (c *checkins) Add(x CheckIn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.load()
	out := l[:0]
	pending := 0
	for _, y := range l {
		if y.Status == "pending" && y.MAC == x.MAC {
			continue
		}
		if y.Status == "pending" {
			pending++
		}
		out = append(out, y)
	}
	if pending >= 100 {
		return errors.New("too many devices are waiting for approval — ask an admin to look at them first")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	x.ID, x.Time, x.Status = hex.EncodeToString(b), time.Now().Unix(), "pending"
	return c.save(append(out, x))
}

func (c *checkins) Get(id string) (CheckIn, bool) {
	for _, x := range c.List() {
		if x.ID == id {
			return x, true
		}
	}
	return CheckIn{}, false
}

func (c *checkins) Decide(id, status, by string) error {
	return c.decide(id, status, by, nil)
}

// decide closes a pending check-in, letting the admin's choices (name, NextDNS profile) be saved with it.
func (c *checkins) decide(id, status, by string, edit func(*CheckIn)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.load()
	for i := range l {
		if l[i].ID == id && l[i].Status == "pending" {
			l[i].Status, l[i].By = status, by
			if edit != nil {
				edit(&l[i])
			}
			return c.save(l)
		}
	}
	return errors.New("that check-in isn't waiting any more")
}

// Registered is the device's latest approved check-in.
func (c *checkins) Registered(mac string) (CheckIn, bool) {
	var best CheckIn
	for _, x := range c.List() {
		if x.MAC == mac && x.Status == "approved" && x.Time >= best.Time {
			best = x
		}
	}
	return best, best.ID != ""
}

// Touch records that the device fetched its setup profile.
func (c *checkins) Touch(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.load()
	for i := range l {
		if l[i].ID == id {
			l[i].ProfileAt = time.Now().Unix()
			_ = c.save(l)
			return
		}
	}
}

func (c *checkins) Pending() int {
	n := 0
	for _, x := range c.List() {
		if x.Status == "pending" {
			n++
		}
	}
	return n
}

// ------------------------------------------------------------------ the device's side (no sign-in)

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	return strings.TrimPrefix(h, "::ffff:")
}

// clientByIP finds the UniFi client that has this IP right now.
func (a *App) clientByIP(ip string) (map[string]any, error) {
	if !a.UniFi.Connected() {
		return nil, errors.New("HomeOwl isn't connected to the network yet — ask an admin")
	}
	st, d := a.UniFi.Get(a.UniFi.SitePath("stat/sta", false))
	if st != 200 {
		return nil, errors.New("couldn't ask the network who you are — try again in a minute")
	}
	for _, c := range unifi.List(d) {
		if c["ip"] == ip {
			return c, nil
		}
		if v6, ok := c["ipv6"].([]any); ok {
			for _, x := range v6 {
				if x == ip {
					return c, nil
				}
			}
		}
	}
	return nil, nil
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

// hiDevice finds the UniFi client making this request (answering the error itself when there is none).
func (a *App) hiDevice(w http.ResponseWriter, r *http.Request) (map[string]any, string, bool) {
	ip := remoteIP(r)
	if pip := net.ParseIP(ip); pip == nil || pip.IsLoopback() {
		fail(w, 400, "Open this page on the device you want to name (not on the computer running HomeOwl).")
		return nil, "", false
	}
	if !net.ParseIP(ip).IsPrivate() && !net.ParseIP(ip).IsLinkLocalUnicast() {
		fail(w, 403, "Device check-in works only on the home network.")
		return nil, "", false
	}
	c, err := a.clientByIP(ip)
	if err != nil {
		fail(w, 503, err.Error())
		return nil, "", false
	}
	if c == nil {
		fail(w, 404, "HomeOwl can't find this device on your network. Are you on the home Wi-Fi (not mobile data or a VPN)?")
		return nil, "", false
	}
	if g, _ := c["is_guest"].(bool); g {
		fail(w, 403, "Devices on the guest network can't check in.")
		return nil, "", false
	}
	return c, strings.ToLower(str(c, "mac")), true
}

func (a *App) hiAPI(w http.ResponseWriter, r *http.Request) {
	c, mac, ok := a.hiDevice(w, r)
	if !ok {
		return
	}
	ip := remoteIP(r)
	if r.Method == http.MethodGet {
		people := []map[string]string{}
		for _, p := range a.An.People() {
			people = append(people, map[string]string{"id": p.ID, "name": p.Name, "kind": p.Kind})
		}
		waiting := false
		for _, x := range a.checkins.List() {
			if x.MAC == mac && x.Status == "pending" {
				waiting = true
			}
		}
		res := map[string]any{"device": map[string]any{"mac": mac, "name": str(c, "name"), "hostname": str(c, "hostname"),
			"vendor": str(c, "oui"), "network": str(c, "network")}, "people": people, "waiting": waiting}
		if x, ok := a.checkins.Registered(mac); ok {
			res["registered"] = a.deviceSetup(x)
		}
		send(w, 200, res)
		return
	}
	if r.Method != http.MethodPost || !sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "not allowed")
		return
	}
	if !a.checkins.allow(ip) {
		fail(w, 429, "That's a lot of check-ins — try again later.")
		return
	}
	body := readJSON(r)
	name, kind, pid, pname := clean(s(body, "name"), 40), s(body, "kind"), s(body, "person_id"), clean(s(body, "person_name"), 40)
	if name == "" {
		fail(w, 400, "Give the device a name, e.g. “Alex's iPad”.")
		return
	}
	if !checkinKinds[kind] {
		kind = "other"
	}
	found := false
	for _, p := range a.An.People() {
		if pid != "" && p.ID == pid {
			found = true
		} else if pid == "" && pname != "" && strings.EqualFold(p.Name, pname) {
			pid, found = p.ID, true // "someone else" turned out to be someone we know
		}
	}
	if !found {
		pid = ""
	} else {
		pname = ""
	}
	if pid == "" && pname == "" {
		fail(w, 400, "Choose whose device this is.")
		return
	}
	uid := str(c, "user_id")
	if uid == "" {
		uid = str(c, "_id")
	}
	seen := str(c, "name")
	if seen == "" {
		seen = str(c, "hostname")
	}
	x := CheckIn{IP: ip, MAC: mac, UserID: uid, Seen: seen, Vendor: str(c, "oui"), Network: str(c, "network"), Name: name, Kind: kind,
		PersonID: pid, PersonName: pname}
	if err := a.checkins.Add(x); err != nil {
		fail(w, 400, err.Error())
		return
	}
	send(w, 200, map[string]any{"ok": true})
}

// clean trims, drops control characters and caps the length (in characters).
func clean(v string, max int) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
	if utf8.RuneCountInString(v) > max {
		v = string([]rune(v)[:max])
	}
	return v
}

// ------------------------------------------------------------------ the admin's side

// approveCheckIn gives the device to the person (creating them if the device named someone new) and marks it done.
// The UniFi alias is set by the UI through the normal allow-listed action, so it is previewed and can be undone.
func (a *App) approveCheckIn(body map[string]any, by string) (any, error) {
	x, ok := a.checkins.Get(s(body, "id"))
	if !ok || x.Status != "pending" {
		return nil, errors.New("that check-in isn't waiting any more")
	}
	pid := s(body, "person_id")
	if pid == "" {
		pid = x.PersonID
	}
	if pid == "-" || !a.An.Open() { // "-": the admin chose nobody; without analytics there are no people
		pid, x.PersonName = "", ""
	} else if pid == "" && x.PersonName != "" {
		for _, p := range a.An.People() {
			if strings.EqualFold(p.Name, x.PersonName) {
				pid = p.ID
			}
		}
		if pid == "" {
			id, err := a.An.SavePerson(analytics.Person{Name: x.PersonName})
			if err != nil {
				return nil, err
			}
			pid = id
		}
	}
	if name := clean(s(body, "name"), 40); name != "" {
		x.Name = name
	}
	nd := s(body, "nd_profile")
	if nd != "" && !ndProfileRE.MatchString(nd) {
		return nil, errors.New("that isn't a NextDNS profile ID")
	}
	if pid != "" && x.MAC != "" {
		if err := a.An.AssignDevice(pid, "mac", x.MAC); err != nil {
			return nil, err
		}
	}
	if pid != "" && nd != "" { // the setup profile names the device in NextDNS's logs: those are this person's too
		if err := a.An.AssignDevice(pid, "ndev", nd+"/"+x.Name); err != nil {
			return nil, err
		}
	}
	if err := a.checkins.decide(x.ID, "approved", by, func(c *CheckIn) { c.Name, c.NDProfile = x.Name, nd }); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "person_id": pid, "checkins": a.checkinView()}, nil
}

func (a *App) checkinView() map[string]any {
	c := a.Cfg.Get()
	links := []string{}
	_, ips := localNames()
	for _, ip := range ips {
		if ip.To4() != nil && ip.IsPrivate() {
			links = append(links, "http://"+net.JoinHostPort(ip.String(), strconv.Itoa(c.LAN.Port))+"/hi")
		}
	}
	if c.LAN.ExtraHost != "" {
		links = append([]string{"http://" + net.JoinHostPort(c.LAN.ExtraHost, strconv.Itoa(c.LAN.Port)) + "/hi"}, links...)
	}
	if u, ok := a.namesView()["checkin_url"].(string); ok && c.LAN.Enabled {
		links = append([]string{u}, links...)
	}
	return map[string]any{"items": a.checkins.List(), "links": links, "lan": c.LAN.Enabled}
}

func (a *App) pendingFor(who *Who) int {
	if who == nil || !who.Admin() {
		return 0
	}
	return a.checkins.Pending()
}
