package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mdns"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Home DNS names (Settings › Access): hi.home.arpa and wifi.home.arpa (any domain the admin picks), added as a
// rewrite to every profile of every NextDNS account HomeOwl has a key for, pointing at this computer. The
// names are public DNS entries, but they answer with a home address, so they only lead anywhere at home.
// Unlike .local they work on every device, an Echo Show too. HomeOwl keeps them up to date when this
// computer's address changes, and removes the ones it made when they're turned off.

// homeIP is this computer's home address (tests replace it).
var homeIP = mdns.PrimaryIPv4

type ndOwned struct {
	Slot    string `json:"slot"`
	Profile string `json:"profile"`
	ID      string `json:"id"`
	Name    string `json:"name"`
}

type homeDNSState struct {
	IP       string    `json:"ip,omitempty"`
	Names    []string  `json:"names,omitempty"`
	Owned    []ndOwned `json:"owned,omitempty"`
	Profiles int       `json:"profiles"`
	Accounts int       `json:"accounts"`
	Errors   []string  `json:"errors,omitempty"`
	Time     int64     `json:"time,omitempty"`
}

func homeDNSNames(c config.Config) []string {
	d := strings.Trim(strings.ToLower(c.Names.Domain), ".")
	if d == "" || !c.LAN.Enabled {
		return nil
	}
	var out []string
	for _, l := range []string{c.Names.Checkin, c.Names.Guest} {
		if l != "" {
			out = append(out, l+"."+d)
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) homeDNSPath() string { return filepath.Join(a.DataDir, "home-dns.json") }

func (a *App) homeDNSLoad() homeDNSState {
	var st homeDNSState
	if b, err := os.ReadFile(a.homeDNSPath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// syncHomeDNS brings the NextDNS rewrites in line with the names and this computer's address.
func (a *App) syncHomeDNS(by string) homeDNSState {
	a.hdMu.Lock()
	defer a.hdMu.Unlock()
	old := a.homeDNSLoad()
	want, ip := homeDNSNames(a.Cfg.Get()), homeIP()
	if a.An == nil || (len(want) > 0 && ip == "") {
		return old
	}
	accounts, errs := a.An.NDAccountProfiles()
	st := homeDNSState{IP: ip, Names: want, Accounts: len(accounts), Time: time.Now().UnixMilli()}
	for slot, e := range errs {
		st.Errors = append(st.Errors, slot+": "+e)
	}
	var added, removed []string
	isWanted := map[string]bool{}
	for _, n := range want {
		isWanted[n] = true
	}
	reached := map[string]bool{}
	for slot, profs := range accounts {
		for _, p := range profs {
			key := slot + "/" + p.ID
			rws, err := a.An.NDRewrites(slot, p.ID)
			if err != nil {
				st.Errors = append(st.Errors, p.Name+": "+err.Error())
				continue
			}
			reached[key] = true
			mine := map[string]bool{} // rewrite IDs HomeOwl made in this profile
			for _, o := range old.Owned {
				if o.Slot == slot && o.Profile == p.ID {
					mine[o.ID] = true
				}
			}
			ok := false
			for _, n := range want {
				have := ""
				for _, r := range rws {
					if !strings.EqualFold(r.Name, n) {
						continue
					}
					if r.Content == ip && have == "" {
						have = r.ID
						continue
					}
					if err := a.An.NDDeleteRewrite(slot, p.ID, r.ID); err != nil { // an old address or a duplicate
						st.Errors = append(st.Errors, p.Name+": "+err.Error())
					}
				}
				if have == "" {
					id, err := a.An.NDAddRewrite(slot, p.ID, n, ip)
					if err != nil {
						st.Errors = append(st.Errors, p.Name+": "+err.Error())
						continue
					}
					have = id
					added = append(added, n+" in "+p.Name)
				}
				st.Owned = append(st.Owned, ndOwned{slot, p.ID, have, n})
				ok = true
			}
			for _, r := range rws { // names HomeOwl made that are no longer wanted
				if mine[r.ID] && !isWanted[strings.ToLower(r.Name)] {
					if err := a.An.NDDeleteRewrite(slot, p.ID, r.ID); err != nil {
						st.Errors = append(st.Errors, p.Name+": "+err.Error())
						st.Owned = append(st.Owned, ndOwned{slot, p.ID, r.ID, r.Name})
					} else {
						removed = append(removed, r.Name+" from "+p.Name)
					}
				}
			}
			if ok {
				st.Profiles++
			}
		}
	}
	for _, o := range old.Owned { // profiles that couldn't be read this time: keep track, try again later
		if !reached[o.Slot+"/"+o.Profile] {
			st.Owned = append(st.Owned, o)
		}
	}
	if b, err := json.Marshal(st); err == nil {
		_ = os.WriteFile(a.homeDNSPath(), b, 0o600)
	}
	if len(added)+len(removed) > 0 {
		desc := "Home DNS names in NextDNS"
		if len(added) > 0 {
			desc += fmt.Sprintf(": pointed %d to %s", len(added), ip)
		}
		if len(removed) > 0 {
			desc += fmt.Sprintf(", removed %d", len(removed))
		}
		pub := unifi.Preview{Method: "PUT", Path: "/profiles/*/rewrites", Target: "NextDNS rewrites", Description: desc,
			Before: map[string]any{"removed": removed}, After: map[string]any{"added": added}}
		_, _ = a.Changes.External("api.nextdns.io", by, pub, false, func() (int, any) { return 200, nil })
	}
	return st
}

// homeDNSDue: the names or the address changed, or it's time for a check (an account may have been added).
func (a *App) homeDNSDue() bool {
	st := a.homeDNSLoad()
	want := homeDNSNames(a.Cfg.Get())
	if len(want) == 0 && len(st.Owned) == 0 {
		return false
	}
	if strings.Join(want, ",") != strings.Join(st.Names, ",") || (len(want) > 0 && homeIP() != st.IP) {
		return true
	}
	age := time.Since(time.UnixMilli(st.Time))
	return age > 6*time.Hour || (len(st.Errors) > 0 && age > 30*time.Minute)
}

func (a *App) homeDNSLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		if a.homeDNSDue() {
			a.syncHomeDNS("HomeOwl")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// homeDNSView is the Settings › Access part.
func (a *App) homeDNSView() map[string]any {
	c := a.Cfg.Get()
	st := a.homeDNSLoad()
	slots := 0
	if a.An != nil {
		slots = len(a.An.NDSlots())
	}
	a.mu.Lock()
	p80 := a.port80 != nil
	a.mu.Unlock()
	var links []string
	for _, n := range homeDNSNames(c) {
		u := "http://" + n
		if !p80 {
			u += fmt.Sprintf(":%d", c.LAN.Port)
		}
		links = append(links, u)
	}
	return map[string]any{"domain": c.Names.Domain, "names": homeDNSNames(c), "links": links, "nextdns_accounts": slots,
		"state": st, "pending": a.homeDNSDue()}
}
