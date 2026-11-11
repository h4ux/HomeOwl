package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/cloudflare"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mdns"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Cloudflare links (Settings › Access): wifi.example.com and hi.example.com on the user's own domain. Each is a
// proxied DNS name with no server behind it, plus a redirect rule that sends the visitor on to HomeOwl's
// address at home (http://<IP>:<port>/wifi). A real domain works in every browser and on every device, with
// nothing to set up on the network. By default the rule only redirects visits from this home's public
// address, so nobody elsewhere learns where HomeOwl lives. HomeOwl keeps the rules up to date when either
// address changes, and only ever touches the names it made.

const cfSlot = "cloudflare-token"

// cfHome finds this home's public addresses, cfCheck tests a link (tests replace them).
var cfHome func(context.Context) []string // nil = (*App).homeAddresses
var cfCheck = cloudflare.Check

type cfName struct {
	Kind   string `json:"kind"` // "wifi" or "hi"
	Host   string `json:"host"`
	Record string `json:"record"`
	Target string `json:"target"`
	Zone   string `json:"zone,omitempty"`  // the zone ID it lives in
	Stale  bool   `json:"stale,omitempty"` // to be removed (an earlier try failed)
	Check  string `json:"check,omitempty"` // what HomeOwl saw opening the link itself ("ok", or the problem)
}

type cfState struct {
	Zone   string   `json:"zone,omitempty"`
	Names  []cfName `json:"names,omitempty"`
	From   []string `json:"from,omitempty"`
	Errors []string `json:"errors,omitempty"`
	Time   int64    `json:"time,omitempty"`
}

func (a *App) cfPath() string { return filepath.Join(a.DataDir, "cloudflare.json") }

func (a *App) cfLoad() cfState {
	var st cfState
	if b, err := os.ReadFile(a.cfPath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (a *App) cfSave(st cfState) {
	if b, err := json.Marshal(st); err == nil {
		_ = os.WriteFile(a.cfPath(), b, 0o600)
	}
}

// cfWant: kind → {host, target} for the names that should exist now.
func (a *App) cfWant(c config.Config) map[string]cfName {
	cf := c.Cloudflare
	out := map[string]cfName{}
	if cf.ZoneID == "" || cf.Zone == "" || !c.LAN.Enabled {
		return out
	}
	base := a.cfBase(c)
	if base == "" {
		return out
	}
	if cf.Wifi != "" && c.Names.Guest != "" {
		out["wifi"] = cfName{Kind: "wifi", Host: cf.Wifi + "." + cf.Zone, Target: base + "/wifi"}
	}
	if cf.Hi != "" {
		out["hi"] = cfName{Kind: "hi", Host: cf.Hi + "." + cf.Zone, Target: base + "/hi"}
	}
	return out
}

// cfBase is where the links lead: http://<HomeOwl's home-network address>[:port]. Only a private address is
// used: the links must never send visitors to an address on the internet.
func (a *App) cfBase(c config.Config) string {
	ip := c.Cloudflare.Address
	if ip == "" {
		ip = homeIP()
	}
	if p := net.ParseIP(ip); p == nil || p.To4() == nil || !p.IsPrivate() {
		return ""
	}
	a.mu.Lock()
	p80 := a.port80 != nil
	a.mu.Unlock()
	if p80 {
		return "http://" + ip
	}
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(c.LAN.Port))
}

func cfRef(kind string) string { return "homeowl_" + kind }

func ruleTarget(r cloudflare.Rule) string {
	fv, _ := r.ActionParameters["from_value"].(map[string]any)
	tu, _ := fv["target_url"].(map[string]any)
	v, _ := tu["value"].(string)
	return v
}

// syncCloudflare makes the names and rules on Cloudflare match the settings and the current addresses.
func (a *App) syncCloudflare(by string) cfState {
	a.cfMu.Lock()
	defer a.cfMu.Unlock()
	old, c := a.cfLoad(), a.Cfg.Get()
	want, zone := a.cfWant(c), c.Cloudflare.ZoneID
	st := cfState{Zone: zone, Time: time.Now().UnixMilli()}
	tok := a.Sec.Get(cfSlot)
	if tok == "" {
		if len(old.Names) > 0 {
			st.Names, st.Zone, st.Errors = old.Names, old.Zone, []string{"No Cloudflare token saved, so the old links can't be removed — connect again"}
		}
		a.cfSave(st)
		return st
	}
	cl := cloudflare.New(tok)
	if len(want) > 0 && !c.Cloudflare.AnyIP {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		st.From = a.cfFrom(ctx)
		cancel()
		if len(st.From) == 0 {
			old.Errors = []string{"Couldn't find this home's public address, so the links weren't changed. HomeOwl tries again in a few minutes."}
			old.Time = st.Time
			a.cfSave(old)
			return old
		}
	}
	var did []string
	fail := func(host string, err error) { st.Errors = append(st.Errors, host+": "+err.Error()) }

	// 1. remove what HomeOwl made that isn't wanted any more (another zone, another name, turned off)
	keep := map[string]cfName{}
	gone := map[string][]cfName{}
	for _, n := range old.Names {
		if w, ok := want[n.Kind]; ok && !n.Stale && old.Zone == zone && w.Host == n.Host {
			keep[n.Kind] = n
			continue
		}
		if n.Zone == "" {
			n.Zone = old.Zone
		}
		gone[n.Zone] = append(gone[n.Zone], n)
	}
	for z, names := range gone {
		rsID, rules, err := cl.Redirects(z)
		for _, n := range names {
			ok := err == nil
			if ok {
				for _, r := range rules {
					if r.Ref == cfRef(n.Kind) && strings.Contains(r.Expression, fmt.Sprintf("%q", n.Host)) {
						if e := cl.DeleteRule(z, rsID, r.ID); e != nil {
							fail(n.Host, e)
							ok = false
						}
					}
				}
			} else {
				fail(n.Host, err)
			}
			if ok && n.Record != "" {
				if e := cl.DeleteRecord(z, n.Record); e != nil {
					fail(n.Host, e)
					ok = false
				}
			}
			if ok {
				did = append(did, "removed "+n.Host)
			} else {
				n.Stale = true // try again later
				st.Names = append(st.Names, n)
			}
		}
	}

	// 2. add or update the wanted ones
	if len(want) > 0 {
		rsID, rules, err := cl.Redirects(zone)
		if err != nil {
			fail(c.Cloudflare.Zone, err)
			for _, n := range keep {
				st.Names = append(st.Names, n)
			}
		} else {
			kinds := make([]string, 0, len(want))
			for k := range want {
				kinds = append(kinds, k)
			}
			sort.Strings(kinds)
			for _, k := range kinds {
				w := want[k]
				rec := keep[k].Record
				recs, err := cl.Records(zone, w.Host)
				if err != nil {
					fail(w.Host, err)
					if rec != "" {
						st.Names = append(st.Names, keep[k])
					}
					continue
				}
				found := false
				for _, r := range recs {
					if r.ID == rec || rec == "" && strings.HasPrefix(r.Comment, "HomeOwl") {
						rec, found = r.ID, true
					}
				}
				if !found {
					if len(recs) > 0 {
						fail(w.Host, errors.New("this name is already used in your Cloudflare DNS — pick another subdomain"))
						continue
					}
					if rec, err = cl.CreateRedirectRecord(zone, w.Host); err != nil {
						fail(w.Host, err)
						continue
					}
					did = append(did, "added "+w.Host)
				}
				w.Record, w.Zone = rec, zone
				st.Names = append(st.Names, w) // from here on it's HomeOwl's to clean up
				rule := cloudflare.RedirectRule(cfRef(k), w.Host, w.Target, st.From)
				var cur *cloudflare.Rule
				for i := range rules {
					if rules[i].Ref == rule.Ref {
						cur = &rules[i]
					}
				}
				switch {
				case cur == nil:
					rsID, err = cl.AddRule(zone, rsID, rule)
					if err == nil {
						did = append(did, w.Host+" → "+w.Target)
					}
				case cur.Expression != rule.Expression || ruleTarget(*cur) != w.Target || !cur.Enabled:
					err = cl.UpdateRule(zone, rsID, cur.ID, rule)
					if err == nil {
						did = append(did, w.Host+" → "+w.Target)
					}
				}
				if err != nil {
					fail(w.Host, err)
				}
			}
		}
	}
	// open each link the way a browser here would (a new rule can take a few seconds to apply)
	for i, n := range st.Names {
		if n.Stale {
			continue
		}
		res := ""
		for try := 0; try < 3; try++ {
			if try > 0 {
				time.Sleep(4 * time.Second)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			res = cfCheck(ctx, n.Host, n.Target)
			cancel()
			if res == "" {
				break
			}
		}
		st.Names[i].Check = "ok"
		if res != "" {
			st.Names[i].Check = res
		}
	}
	a.cfSave(st)
	if len(did) > 0 {
		pub := unifi.Preview{Method: "PUT", Path: "/zones/*/rulesets", Target: "Cloudflare links", Description: "Cloudflare: " + strings.Join(did, ", "),
			Before: map[string]any{}, After: map[string]any{"changes": did}}
		_, _ = a.Changes.External("api.cloudflare.com", by, pub, false, func() (int, any) { return 200, nil })
	}
	return st
}

// cfDue: the settings, HomeOwl's address or the home's public address changed, or a retry is due.
func (a *App) cfDue() bool {
	st, c := a.cfLoad(), a.Cfg.Get()
	want := a.cfWant(c)
	if len(want) == 0 && len(st.Names) == 0 {
		return false
	}
	have := map[string]cfName{}
	for _, n := range st.Names {
		if !n.Stale {
			have[n.Kind] = n
		}
	}
	if len(have) != len(want) || st.Zone != c.Cloudflare.ZoneID {
		return true
	}
	for k, w := range want {
		if have[k].Host != w.Host || have[k].Target != w.Target {
			return true
		}
	}
	age := time.Since(time.UnixMilli(st.Time))
	failing := len(st.Errors) > 0
	for _, n := range st.Names {
		failing = failing || n.Check != "" && n.Check != "ok"
	}
	if failing && age > 10*time.Minute || age > 12*time.Hour {
		return true
	}
	if !c.Cloudflare.AnyIP && len(want) > 0 { // the home's public address may have changed
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		from := a.cfFrom(ctx)
		return len(from) > 0 && strings.Join(from, " ") != strings.Join(st.From, " ")
	}
	return c.Cloudflare.AnyIP != (len(st.From) == 0)
}

func (a *App) cloudflareLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if a.cfDue() {
			a.syncCloudflare("HomeOwl")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) cloudflareView() map[string]any {
	c := a.Cfg.Get()
	cf, st := c.Cloudflare, a.cfLoad()
	var links []map[string]string
	for _, n := range st.Names {
		if !n.Stale {
			links = append(links, map[string]string{"kind": n.Kind, "url": "https://" + n.Host, "target": n.Target, "check": n.Check})
		}
	}
	return map[string]any{"connected": a.Sec.Get(cfSlot) != "", "zone": cf.Zone, "zone_id": cf.ZoneID, "wifi": cf.Wifi, "hi": cf.Hi,
		"any_ip": cf.AnyIP, "links": links, "state": st, "guest_off": c.Names.Guest == "",
		"address": cf.Address, "auto_address": homeIP(), "base": a.cfBase(c)}
}

// cloudflareAPI: connect / zones / settings / sync / disconnect (admins only).
func (a *App) cloudflareAPI(p, by string, body map[string]any) (any, error) {
	switch p {
	case "cloudflare/connect":
		tok := strings.TrimSpace(s(body, "token"))
		if tok == "" {
			return nil, errors.New("paste the Cloudflare API token")
		}
		cl := cloudflare.New(tok)
		if err := cl.Verify(); err != nil {
			return nil, err
		}
		zones, err := cl.Zones()
		if err != nil {
			return nil, err
		}
		if len(zones) == 0 {
			return nil, errors.New("this token can't see any domain — give it access to your domain (Zone Resources › your domain)")
		}
		if err := a.Sec.Set(cfSlot, tok); err != nil {
			return nil, err
		}
		return map[string]any{"zones": zones}, nil
	case "cloudflare/zones":
		tok := a.Sec.Get(cfSlot)
		if tok == "" {
			return nil, errors.New("connect Cloudflare first")
		}
		zones, err := cloudflare.New(tok).Zones()
		return map[string]any{"zones": zones}, err
	case "cloudflare/settings":
		tok := a.Sec.Get(cfSlot)
		if tok == "" {
			return nil, errors.New("connect Cloudflare first")
		}
		cf := config.Cloudflare{ZoneID: s(body, "zone_id"), Wifi: strings.ToLower(strings.TrimSpace(s(body, "wifi"))),
			Hi: strings.ToLower(strings.TrimSpace(s(body, "hi"))), AnyIP: b(body, "any_ip")}
		cf.Address = strings.TrimSpace(s(body, "address"))
		if ip := net.ParseIP(cf.Address); cf.Address != "" && (ip == nil || ip.To4() == nil || !ip.IsPrivate()) {
			return nil, errors.New("the address must be HomeOwl's home-network IPv4 address, like 192.168.1.10")
		}
		for _, l := range []string{cf.Wifi, cf.Hi} {
			if l != "" && !mdns.ValidLabel(l) {
				return nil, errors.New("a subdomain is one word: letters, numbers and dashes (like “wifi”)")
			}
		}
		if cf.Wifi != "" && cf.Wifi == cf.Hi {
			return nil, errors.New("use two different subdomains")
		}
		if cf.ZoneID != "" {
			zones, err := cloudflare.New(tok).Zones()
			if err != nil {
				return nil, err
			}
			for _, z := range zones {
				if z.ID == cf.ZoneID {
					cf.Zone = z.Name
				}
			}
			if cf.Zone == "" {
				return nil, errors.New("pick one of your domains")
			}
		}
		if err := a.Cfg.Update(func(c *config.Config) { c.Cloudflare = cf }); err != nil {
			return nil, err
		}
		a.syncCloudflare(by)
		return a.cloudflareView(), nil
	case "cloudflare/sync":
		a.syncCloudflare(by)
		return a.cloudflareView(), nil
	case "cloudflare/disconnect":
		if err := a.Cfg.Update(func(c *config.Config) { c.Cloudflare = config.Cloudflare{} }); err != nil {
			return nil, err
		}
		st := a.syncCloudflare(by)
		if len(st.Names) > 0 {
			return nil, errors.New("couldn't remove everything from Cloudflare yet: " + strings.Join(st.Errors, "; "))
		}
		a.Sec.Delete(cfSlot)
		_ = os.Remove(a.cfPath())
		return a.cloudflareView(), nil
	}
	return nil, errors.New("unknown Cloudflare request")
}

func (a *App) cfFrom(ctx context.Context) []string {
	if cfHome != nil {
		return cfHome(ctx)
	}
	return a.homeAddresses(ctx)
}

// homeAddresses: every public address this home may reach Cloudflare from — what Cloudflare sees from this
// computer over IPv4 and over IPv6, every WAN of the UniFi gateway (a second internet line or failover), and the
// IPv6 networks of this computer's own addresses. IPv6 networks are /56s, so every device at home is in them.
func (a *App) homeAddresses(ctx context.Context) []string {
	set := map[string]bool{}
	for _, x := range cloudflare.HomeAddresses(ctx) {
		set[x] = true
	}
	add := func(v any) {
		if s, _ := v.(string); s != "" {
			if p := cloudflare.PublicPrefix(net.ParseIP(strings.TrimSpace(strings.Split(s, "/")[0]))); p != "" {
				set[p] = true
			}
		}
	}
	if a.UniFi != nil && a.UniFi.Connected() {
		if st, d := a.UniFi.Get(a.UniFi.SitePath("stat/device", false)); st == 200 {
			for _, dev := range unifi.List(d) {
				for i := 1; i <= 4; i++ {
					w, _ := dev[fmt.Sprintf("wan%d", i)].(map[string]any)
					if w == nil || w["up"] == false {
						continue
					}
					add(w["ip"])
					if l, ok := w["ipv6"].([]any); ok {
						for _, x := range l {
							add(x)
						}
					}
				}
			}
		}
		if st, d := a.UniFi.Get(a.UniFi.SitePath("stat/health", false)); st == 200 {
			for _, h := range unifi.List(d) {
				if h["subsystem"] == "wan" {
					add(h["wan_ip"])
				}
			}
		}
	}
	if ifs, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range ifs {
			if n, ok := ad.(*net.IPNet); ok && n.IP.To4() == nil {
				add(n.IP.String())
			}
		}
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
