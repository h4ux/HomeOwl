package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
	"github.com/h4ux/homeowl/internal/unifi"
)

// "My device": what the check-in page (the HomeOwl icon on a family member's Home Screen) shows once the device is
// approved — whether it's OK, how it's connected right now, and the sites NextDNS blocked on it, which they can ask
// an admin to allow. The page has no sign-in: HomeOwl knows only which device opened it, so it shows that device
// and nothing about anyone else's.

// SiteRequest: a family member asks for a blocked site. Allowing it puts the domain on the allowlist of the
// device's NextDNS profile (so for everyone on that profile), which an admin can take off again.
type SiteRequest struct {
	ID      string `json:"id"`
	Time    int64  `json:"time"`
	MAC     string `json:"mac,omitempty"`
	Device  string `json:"device"`
	Person  string `json:"person,omitempty"`
	Profile string `json:"profile,omitempty"`
	Domain  string `json:"domain"`
	Note    string `json:"note,omitempty"`
	Reasons string `json:"reasons,omitempty"` // what blocked it, from NextDNS's log
	Status  string `json:"status"`            // pending | allowed | denied | removed
	By      string `json:"by,omitempty"`
	Decided int64  `json:"decided,omitempty"`
}

const (
	keepRequests   = 300
	pendingPerDev  = 5
	myBlockedSince = 24 * time.Hour
)

var (
	domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	reqIDRE  = regexp.MustCompile(`^[a-f0-9]{16}$`)
)

type siteRequests struct {
	mu   sync.Mutex
	path string
}

func newSiteRequests(dir string) *siteRequests {
	return &siteRequests{path: filepath.Join(dir, "site-requests.json")}
}

func (s *siteRequests) load() []SiteRequest {
	l := []SiteRequest{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (s *siteRequests) save(l []SiteRequest) error {
	if len(l) > keepRequests {
		l = l[len(l)-keepRequests:]
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

func (s *siteRequests) List() []SiteRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *siteRequests) pending() int {
	n := 0
	for _, r := range s.List() {
		if r.Status == "pending" {
			n++
		}
	}
	return n
}

// normDomain: what someone typed or tapped ("https://www.Example.com/page") as a host name, or "".
func normDomain(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#:"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSuffix(v, ".")
	if len(v) > 253 || !domainRE.MatchString(v) {
		return ""
	}
	return v
}

// ---- the device's own view

type hiNetCache struct {
	mu  sync.Mutex
	at  time.Time
	aps map[string]string
	wan map[string]any
}

// hiNetwork: the access points' names and the internet's state, read at most every 30 seconds.
func (a *App) hiNetwork() (map[string]string, map[string]any) {
	n := &a.hiNet
	n.mu.Lock()
	defer n.mu.Unlock()
	if time.Since(n.at) < 30*time.Second || a.UniFi == nil || !a.UniFi.Connected() {
		return n.aps, n.wan
	}
	aps, wan := map[string]string{}, map[string]any{}
	if st, d := a.UniFi.Get(a.UniFi.SitePath("stat/device", false)); st == 200 {
		for _, dev := range unifi.List(d) {
			aps[strings.ToLower(str(dev, "mac"))] = orStr(str(dev, "name"), str(dev, "model"))
		}
	}
	if st, d := a.UniFi.Get(a.UniFi.SitePath("stat/health", false)); st == 200 {
		for _, h := range unifi.List(d) {
			switch h["subsystem"] {
			case "wan":
				wan["status"], wan["isp"] = h["status"], h["isp_name"]
			case "www":
				for _, k := range []string{"latency", "xput_down", "xput_up", "speedtest_lastrun"} {
					if v, ok := h[k]; ok {
						wan[k] = v
					}
				}
				if wan["status"] == nil {
					wan["status"] = h["status"]
				}
			}
		}
	}
	n.aps, n.wan, n.at = aps, wan, time.Now()
	return aps, wan
}

func signalWord(dbm float64) string {
	switch {
	case dbm == 0:
		return ""
	case dbm >= -60:
		return "great"
	case dbm >= -70:
		return "good"
	case dbm >= -80:
		return "weak"
	}
	return "poor"
}

// myDevice: the approved device's status, connection, recent blocks and its own requests.
func (a *App) myDevice(x CheckIn, c map[string]any) map[string]any {
	res := map[string]any{"setup": a.setupStatus(x)}
	aps, wan := a.hiNetwork()
	conn := map[string]any{"network": str(c, "network"), "internet": wan}
	if w, _ := c["is_wired"].(bool); w {
		conn["wired"] = true
	} else {
		sig, _ := c["signal"].(float64)
		conn["ssid"], conn["signal"], conn["quality"] = str(c, "essid"), sig, signalWord(sig)
		conn["ap"] = aps[strings.ToLower(str(c, "ap_mac"))]
		conn["rate"], _ = c["tx_rate"].(float64) // kbps
		conn["band"] = map[string]string{"ng": "2.4 GHz", "na": "5 GHz", "6e": "6 GHz"}[str(c, "radio")]
	}
	res["connection"] = conn
	reqs := []SiteRequest{}
	pend := map[string]string{}
	for _, r := range a.requests.List() {
		if r.MAC == x.MAC {
			reqs = append([]SiteRequest{r}, reqs...)
			pend[r.Domain] = r.Status // the latest answer for that site
		}
	}
	if len(reqs) > 10 {
		reqs = reqs[:10]
	}
	for i := range reqs {
		reqs[i].MAC, reqs[i].Profile, reqs[i].By = "", "", "" // the page needs the domain and the answer
	}
	res["requests"] = reqs
	if x.NDProfile != "" {
		res["can_request"] = true
		if a.An.Open() {
			bl, err := a.An.NDDeviceBlocked(ndProfilesOf(x), x.Name, time.Now().Add(-myBlockedSince), 15)
			if err == nil {
				out := []map[string]any{}
				for _, b := range bl {
					out = append(out, map[string]any{"domain": b.Domain, "reasons": b.Reasons, "n": b.N, "last": b.Last.Unix(), "asked": pend[b.Domain]})
				}
				res["blocked"] = out
			}
		}
	}
	return res
}

func ndProfilesOf(x CheckIn) []string {
	l := []string{x.NDProfile}
	if x.ProfileND != "" && x.ProfileND != x.NDProfile {
		l = append(l, x.ProfileND)
	}
	return l
}

// hiRequest: POST /api/hi/request {domain, note} from an approved device's page.
func (a *App) hiRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "not allowed")
		return
	}
	_, mac, ok := a.hiDevice(w, r)
	if !ok {
		return
	}
	x, ok := a.checkins.Registered(mac)
	if !ok {
		fail(w, 404, "This device isn't approved yet.")
		return
	}
	if !a.checkins.allow(remoteIP(r)) {
		fail(w, 429, "That's a lot of requests — try again later.")
		return
	}
	body := readJSON(r)
	req, err := a.addRequest(x, s(body, "domain"), clean(s(body, "note"), 200))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	req.MAC, req.Profile = "", ""
	send(w, 200, map[string]any{"request": req})
}

// the NextDNS allowlist (tests swap it)
type ndAllow interface {
	NDAllowAdd(profile, domain string) error
	NDAllowRemove(profile, domain string) error
}

var ndAllowFor = func(a *App) ndAllow { return a.An }

func (a *App) addRequest(x CheckIn, domain, note string) (SiteRequest, error) {
	if x.NDProfile == "" {
		return SiteRequest{}, errors.New("This device doesn't use NextDNS, so there's nothing HomeOwl can allow for it.")
	}
	d := normDomain(domain)
	if d == "" {
		return SiteRequest{}, errors.New("That doesn't look like a site's address, e.g. example.com.")
	}
	reasons := ""
	if a.An.Open() {
		if bl, err := a.An.NDDeviceBlocked(ndProfilesOf(x), x.Name, time.Now().Add(-7*24*time.Hour), 200); err == nil {
			for _, b := range bl {
				if b.Domain == d {
					reasons = b.Reasons
				}
			}
		}
	}
	who := x.PersonName
	if x.PersonID != "" && a.An.Open() {
		for _, p := range a.An.People() {
			if p.ID == x.PersonID {
				who = p.Name
			}
		}
	}
	s := a.requests
	s.mu.Lock()
	l := s.load()
	n := 0
	for _, r := range l {
		if r.MAC == x.MAC && r.Status == "pending" {
			if r.Domain == d {
				s.mu.Unlock()
				return r, nil // already asked
			}
			n++
		}
	}
	if n >= pendingPerDev {
		s.mu.Unlock()
		return SiteRequest{}, fmt.Errorf("This device already has %d requests waiting — an admin will look at them.", n)
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	req := SiteRequest{ID: hex.EncodeToString(b), Time: time.Now().Unix(), MAC: x.MAC, Device: x.Name, Person: who, Profile: x.NDProfile,
		Domain: d, Note: note, Reasons: reasons, Status: "pending"}
	l = append(l, req)
	err := s.save(l)
	s.mu.Unlock()
	if err != nil {
		return SiteRequest{}, err
	}
	body := "On " + x.Name + "."
	if note != "" {
		body += " “" + note + "”"
	}
	if reasons != "" {
		body += " Blocked by: " + reasons + "."
	}
	body += " Allowing it puts it on the allowlist of the NextDNS profile " + a.ndProfileName(x.NDProfile) + ", for everyone using it."
	a.Alerts.Add(alerts.Alert{Kind: "site_request", Level: "info", Title: orStr(who, x.Name) + " asks to allow " + d, Body: body,
		Data: map[string]any{"request": req.ID, "domain": d}})
	return req, nil
}

func (a *App) ndProfileName(id string) string {
	if n := a.An.NDProfileName(id); n != "" {
		return n + " (" + id + ")"
	}
	return id
}

// decideRequest: allow, deny, or remove (take an allowed site off the allowlist again).
func (a *App) decideRequest(id, op, by string) (SiteRequest, error) {
	s := a.requests
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.load()
	i := -1
	for k := range l {
		if l[k].ID == id {
			i = k
		}
	}
	if i < 0 {
		return SiteRequest{}, errors.New("that request is gone")
	}
	r := &l[i]
	want := map[string][]string{"allow": {"pending", "denied", "removed"}, "deny": {"pending"}, "remove": {"allowed"}}[op]
	if want == nil {
		return SiteRequest{}, errors.New("allow, deny or remove")
	}
	if !contains(want, r.Status) {
		return *r, fmt.Errorf("already %s", r.Status)
	}
	switch op {
	case "allow", "remove":
		desc := "Allow " + r.Domain + " (asked for " + r.Device + ")"
		method, call := "POST", func() error { return ndAllowFor(a).NDAllowAdd(r.Profile, r.Domain) }
		if op == "remove" {
			desc, method, call = "Stop allowing "+r.Domain+" (asked for "+r.Device+")", "DELETE", func() error { return ndAllowFor(a).NDAllowRemove(r.Profile, r.Domain) }
		}
		var err error
		pub := unifi.Preview{Method: method, Path: "/profiles/" + r.Profile + "/allowlist", Target: "NextDNS allowlist", Description: desc,
			After: map[string]any{"domain": r.Domain}}
		if _, lerr := a.Changes.External("api.nextdns.io", by, pub, false, func() (int, any) {
			if err = call(); err != nil {
				return 502, map[string]any{"error": err.Error()}
			}
			return 200, nil
		}); lerr != nil {
			return *r, lerr
		}
		if err != nil {
			return *r, errors.New("NextDNS: " + err.Error())
		}
		r.Status = map[string]string{"allow": "allowed", "remove": "removed"}[op]
	case "deny":
		r.Status = "denied"
	}
	r.By, r.Decided = by, time.Now().Unix()
	if err := s.save(l); err != nil {
		return *r, err
	}
	for _, al := range a.Alerts.List() {
		if al.Kind == "site_request" && al.Data["request"] == id && !al.Read {
			a.Alerts.MarkRead(al.ID)
		}
	}
	return *r, nil
}

// requestsView: for admins — waiting first, then what was decided.
func (a *App) requestsView() map[string]any {
	l := a.requests.List()
	out := make([]SiteRequest, 0, len(l))
	for i := len(l) - 1; i >= 0; i-- {
		out = append(out, l[i])
	}
	names := map[string]string{}
	for _, r := range out {
		if _, ok := names[r.Profile]; !ok {
			names[r.Profile] = a.ndProfileName(r.Profile)
		}
	}
	return map[string]any{"items": out, "profiles": names}
}
