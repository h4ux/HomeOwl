package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/apps"
	"github.com/h4ux/homeowl/internal/unifi"
)

// App blocking (Family › Apps): an app is blocked for the people on one or more NextDNS profiles by adding the app's
// own domains (internal/apps) to each profile's denylist. "Always", or on a schedule: HomeOwl switches the entries on
// and off itself, since NextDNS's API has no schedule for the denylist. HomeOwl only switches and removes entries it
// added; a domain that was already on a denylist is left alone.

type AppBlock struct {
	ID       string              `json:"id"`
	App      string              `json:"app"`
	Profiles []string            `json:"profiles"`
	Mode     string              `json:"mode"`           // always | schedule
	Days     []int               `json:"days,omitempty"` // 0 = Sunday; the day the blocked time starts
	From     string              `json:"from,omitempty"` // "21:00"
	To       string              `json:"to,omitempty"`   // "07:00" (the next morning when before From)
	Owned    map[string][]string `json:"owned,omitempty"`
	Applied  *bool               `json:"applied,omitempty"` // the state last written to NextDNS
	Checked  int64               `json:"checked,omitempty"`
	Error    string              `json:"error,omitempty"`
	By       string              `json:"by,omitempty"`
	At       int64               `json:"at,omitempty"`
}

type appBlocks struct {
	mu   sync.Mutex // one writer at a time: the store and NextDNS
	path string
}

func newAppBlocks(dir string) *appBlocks {
	return &appBlocks{path: filepath.Join(dir, "app-blocks.json")}
}

func (s *appBlocks) load() []AppBlock {
	l := []AppBlock{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (s *appBlocks) save(l []AppBlock) error {
	b, _ := json.MarshalIndent(l, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

var hhmmRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func hhmm(s string) int {
	return int(s[0]-'0')*600 + int(s[1]-'0')*60 + int(s[3]-'0')*10 + int(s[4]-'0')
}

// blockedNow: always, or inside the schedule. A window that ends before it starts runs past midnight and belongs
// to the day it starts on.
func (b AppBlock) blockedNow(now time.Time) bool {
	if b.Mode != "schedule" {
		return true
	}
	if !hhmmRE.MatchString(b.From) || !hhmmRE.MatchString(b.To) {
		return false
	}
	on := map[int]bool{}
	for _, d := range b.Days {
		on[d] = true
	}
	t, from, to := now.Hour()*60+now.Minute(), hhmm(b.From), hhmm(b.To)
	day, prev := int(now.Weekday()), (int(now.Weekday())+6)%7
	if from < to {
		return on[day] && t >= from && t < to
	}
	return (on[day] && t >= from) || (on[prev] && t < to)
}

// ndDeny is the part of the NextDNS API app blocking uses (the analytics engine; a fake in tests).
type ndDeny interface {
	NDDenylist(profile string) ([]analytics.NDDeny, error)
	NDDenyAdd(profile, domain string, active bool) error
	NDDenySet(profile, domain string, active bool) error
	NDDenyRemove(profile, domain string) error
}

var appDenyFor = func(a *App) ndDeny { return a.An }

var ndProfileIDRE = regexp.MustCompile(`^[a-f0-9]{6}$`)

func inList(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// applyBlock brings the profiles' denylists in line with the block: its domains added (or switched) to want.
// Returns what changed, for the log.
func (a *App) applyBlock(b *AppBlock, want bool, nd ndDeny) (changed []string, err error) {
	app, ok := apps.Get(b.App)
	if !ok {
		return nil, errors.New("unknown app")
	}
	if b.Owned == nil {
		b.Owned = map[string][]string{}
	}
	var errs []string
	for _, p := range b.Profiles {
		list, err := nd.NDDenylist(p)
		if err != nil {
			errs = append(errs, p+": "+err.Error())
			continue
		}
		cur := map[string]bool{}
		for _, x := range list {
			cur[x.ID] = x.Active
		}
		for _, d := range app.Roots {
			active, there := cur[d]
			switch {
			case !there:
				if err := nd.NDDenyAdd(p, d, want); err != nil {
					errs = append(errs, p+": "+err.Error())
					continue
				}
				if !inList(b.Owned[p], d) {
					b.Owned[p] = append(b.Owned[p], d)
				}
				changed = append(changed, d)
			case inList(b.Owned[p], d) && active != want:
				if err := nd.NDDenySet(p, d, want); err != nil {
					errs = append(errs, p+": "+err.Error())
					continue
				}
				changed = append(changed, d)
			}
		}
	}
	if len(errs) > 0 {
		return changed, errors.New(strings.Join(errs, "; "))
	}
	return changed, nil
}

// unblock takes out the entries HomeOwl added (only those) from the profiles it no longer covers (all, when keep is nil).
func (a *App) unblock(b *AppBlock, keep []string, nd ndDeny) (removed []string, err error) {
	var errs []string
	for p, doms := range b.Owned {
		if inList(keep, p) {
			continue
		}
		var left []string
		for _, d := range doms {
			if err := nd.NDDenyRemove(p, d); err != nil && !strings.Contains(err.Error(), "404") {
				errs = append(errs, p+": "+err.Error())
				left = append(left, d)
				continue
			}
			removed = append(removed, d)
		}
		if len(left) > 0 {
			b.Owned[p] = left
		} else {
			delete(b.Owned, p)
		}
	}
	if len(errs) > 0 {
		return removed, errors.New(strings.Join(errs, "; "))
	}
	return removed, nil
}

func (a *App) logBlock(by string, b AppBlock, desc string, before, after map[string]any) {
	pub := unifi.Preview{Method: "PUT", Path: "/profiles/" + strings.Join(b.Profiles, ",") + "/denylist", Target: "NextDNS denylist", Description: desc, Before: before, After: after}
	_, _ = a.Changes.External("api.nextdns.io", by, pub, false, func() (int, any) { return 200, nil })
}

func appName(id string) string {
	if x, ok := apps.Get(id); ok {
		return x.Name
	}
	return id
}

func blockWhen(b AppBlock) string {
	if b.Mode != "schedule" {
		return "always"
	}
	names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	var d []string
	for _, x := range b.Days {
		if x >= 0 && x < 7 {
			d = append(d, names[x])
		}
	}
	return fmt.Sprintf("%s–%s on %s", b.From, b.To, strings.Join(d, ", "))
}

// saveAppBlock creates or changes a block and applies it straight away.
func (a *App) saveAppBlock(body map[string]any, by string) (map[string]any, error) {
	if !a.An.Open() {
		return nil, errors.New("analytics isn't running")
	}
	nb := AppBlock{ID: s(body, "id"), App: s(body, "app"), Mode: s(body, "mode"), From: s(body, "from"), To: s(body, "to"), By: by, At: time.Now().UnixMilli()}
	if _, ok := apps.Get(nb.App); !ok {
		return nil, errors.New("unknown app")
	}
	for _, x := range anyArr(body["profiles"]) {
		if p, _ := x.(string); ndProfileIDRE.MatchString(p) && !inList(nb.Profiles, p) {
			nb.Profiles = append(nb.Profiles, p)
		}
	}
	if len(nb.Profiles) == 0 {
		return nil, errors.New("choose at least one NextDNS profile")
	}
	sort.Strings(nb.Profiles)
	if nb.Mode != "schedule" {
		nb.Mode, nb.From, nb.To = "always", "", ""
	} else {
		for _, x := range anyArr(body["days"]) {
			if f, ok := x.(float64); ok && f >= 0 && f < 7 && !hasInt(nb.Days, int(f)) {
				nb.Days = append(nb.Days, int(f))
			}
		}
		sort.Ints(nb.Days)
		if len(nb.Days) == 0 || !hhmmRE.MatchString(nb.From) || !hhmmRE.MatchString(nb.To) || nb.From == nb.To {
			return nil, errors.New("pick the days and a start and end time (HH:MM)")
		}
	}

	if err := a.storeBlock(nb, by); err != nil {
		return nil, err
	}
	return a.appBlocksView(), nil
}

// storeBlock writes the block to NextDNS and to the store (one at a time).
func (a *App) storeBlock(nb AppBlock, by string) error {
	ab := a.appBlocks
	ab.mu.Lock()
	defer ab.mu.Unlock()
	l := ab.load()
	idx := -1
	for i, x := range l {
		if x.ID == nb.ID && nb.ID != "" {
			idx = i
			nb.Owned = x.Owned
			continue
		}
		if x.App == nb.App {
			for _, p := range nb.Profiles {
				if inList(x.Profiles, p) {
					return fmt.Errorf("%s already has a block on that profile — change that one instead", appName(nb.App))
				}
			}
		}
	}
	if nb.ID == "" {
		r := make([]byte, 6)
		_, _ = rand.Read(r)
		nb.ID = hex.EncodeToString(r)
	} else if idx < 0 {
		return errors.New("that block is gone")
	}

	nd := appDenyFor(a)
	var before map[string]any
	if idx >= 0 {
		before = map[string]any{"profiles": l[idx].Profiles, "when": blockWhen(l[idx])}
	}
	removed, rerr := a.unblock(&nb, nb.Profiles, nd) // profiles taken off the block
	want := nb.blockedNow(time.Now())
	changed, err := a.applyBlock(&nb, want, nd)
	nb.Checked = time.Now().UnixMilli()
	if err == nil && rerr == nil {
		nb.Applied, nb.Error = &want, ""
	} else {
		nb.Applied, nb.Error = nil, errors.Join(err, rerr).Error()
	}
	if idx >= 0 {
		l[idx] = nb
	} else {
		l = append(l, nb)
	}
	if e := ab.save(l); e != nil {
		return e
	}
	state := "blocked now"
	if !want {
		state = "not blocked right now"
	}
	a.logBlock(by, nb, fmt.Sprintf("Block %s (%s) — %s", appName(nb.App), blockWhen(nb), state), before,
		map[string]any{"profiles": nb.Profiles, "when": blockWhen(nb), "domains": changed, "removed": removed})
	if nb.Error != "" {
		return fmt.Errorf("saved, but NextDNS didn't take all of it: %s (HomeOwl tries again)", nb.Error)
	}
	return nil
}

func hasInt(l []int, v int) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func anyArr(v any) []any { l, _ := v.([]any); return l }

// deleteAppBlock removes the block and the entries HomeOwl added for it.
func (a *App) deleteAppBlock(id, by string) error {
	ab := a.appBlocks
	ab.mu.Lock()
	defer ab.mu.Unlock()
	l := ab.load()
	for i, b := range l {
		if b.ID != id {
			continue
		}
		removed, err := a.unblock(&b, nil, appDenyFor(a))
		if err != nil {
			l[i] = b
			_ = ab.save(l)
			return fmt.Errorf("NextDNS didn't remove everything: %v", err)
		}
		a.logBlock(by, b, "Unblock "+appName(b.App), map[string]any{"profiles": b.Profiles, "when": blockWhen(b)}, map[string]any{"removed": removed})
		return ab.save(append(l[:i], l[i+1:]...))
	}
	return errors.New("that block is gone")
}

// appBlocksTick switches scheduled blocks on and off, retries failures, and every few hours makes sure the
// entries are still there (someone may have removed one in NextDNS).
func (a *App) appBlocksTick(now time.Time) {
	if !a.An.Open() {
		return
	}
	ab := a.appBlocks
	ab.mu.Lock()
	defer ab.mu.Unlock()
	l := ab.load()
	dirty := false
	nd := appDenyFor(a)
	for i := range l {
		b := &l[i]
		want := b.blockedNow(now)
		stale := now.Sub(time.UnixMilli(b.Checked)) > 6*time.Hour
		retry := b.Applied == nil && now.Sub(time.UnixMilli(b.Checked)) > 5*time.Minute
		if !(b.Applied != nil && *b.Applied != want) && !stale && !retry {
			continue
		}
		changed, err := a.applyBlock(b, want, nd)
		b.Checked, dirty = now.UnixMilli(), true
		if err != nil {
			b.Applied, b.Error = nil, err.Error()
		} else {
			b.Applied, b.Error = &want, ""
		}
		if len(changed) > 0 {
			verb := "Schedule: blocked " + appName(b.App)
			if !want {
				verb = "Schedule: allowed " + appName(b.App) + " again"
			}
			if b.Mode != "schedule" || stale && !retry {
				verb = "Put back " + appName(b.App) + " block entries"
			}
			a.logBlock("schedule", *b, verb, nil, map[string]any{"domains": changed, "blocked": want})
		}
	}
	if dirty {
		_ = ab.save(l)
	}
}

func (a *App) appBlocksLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.appBlocksTick(now)
		}
	}
}

// profilePeople: who uses each NextDNS profile (a whole profile, or a device in it, attached to them).
func (a *App) profilePeople() map[string][]string {
	out := map[string][]string{}
	if !a.An.Open() {
		return out
	}
	for _, p := range a.An.People() {
		seen := map[string]bool{}
		for _, d := range p.Devices {
			id := ""
			switch d.Kind {
			case "profile":
				id = d.Key
			case "ndev":
				id, _, _ = strings.Cut(d.Key, "/")
			}
			if id != "" && !seen[id] {
				seen[id] = true
				out[id] = append(out[id], p.Name)
			}
		}
	}
	return out
}

func (a *App) appBlocksView() map[string]any {
	a.appBlocks.mu.Lock()
	l := a.appBlocks.load()
	a.appBlocks.mu.Unlock()
	now := time.Now()
	type row struct {
		AppBlock
		Name    string `json:"name"`
		When    string `json:"when"`
		Blocked bool   `json:"blocked_now"`
	}
	rows := []row{}
	for _, b := range l {
		rows = append(rows, row{b, appName(b.App), blockWhen(b), b.blockedNow(now)})
	}
	profiles := []map[string]any{}
	people := a.profilePeople()
	for _, p := range a.ndProfilesCached() {
		ppl := people[p.ID]
		if ppl == nil {
			ppl = []string{}
		}
		profiles = append(profiles, map[string]any{"id": p.ID, "name": p.Name, "people": ppl})
	}
	return map[string]any{"blocks": rows, "apps": apps.Catalog, "profiles": profiles}
}
