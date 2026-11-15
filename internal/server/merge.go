package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Merging check-ins: the same phone checked in twice — it got a new Wi-Fi address (a private address changed, the
// profile was reinstalled, the network was forgotten) and was checked in again under the same name. Both check-ins
// then look like separate devices, and the old one "stops using its setup". Merging keeps one (the newest, by
// default) and marks the others "merged": their old address leaves the person and is archived, and the kept one is
// approved for device approval. Nothing is deleted.

// nameWords: a device name's words, without "'s", punctuation or case ("Alex's iPhone 15" → alex iphone 15).
func nameWords(n string) []string {
	n = strings.ToLower(n)
	for _, x := range []string{"’s", "'s"} {
		n = strings.ReplaceAll(n, x, "")
	}
	return strings.FieldsFunc(n, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127) })
}

// similarNames: the same words (in any order), or most of them (one name's words all in the other, or ≥ 60% shared).
func similarNames(a, b string) (same, similar bool) {
	wa, wb := nameWords(a), nameWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false, false
	}
	sa, sb := append([]string(nil), wa...), append([]string(nil), wb...)
	sort.Strings(sa)
	sort.Strings(sb)
	if strings.Join(sa, " ") == strings.Join(sb, " ") {
		return true, true
	}
	set := map[string]bool{}
	for _, w := range wa {
		set[w] = true
	}
	common := 0
	for _, w := range wb {
		if set[w] {
			common++
		}
	}
	short, union := len(wa), len(wa)+len(wb)-common
	if len(wb) < short {
		short = len(wb)
	}
	return false, (common == short && short >= 2) || float64(common)/float64(union) >= 0.6
}

// mergeDismissed: pairs an admin said aren't the same device ("id|id", smaller first).
func (a *App) mergeDismissed() map[string]bool {
	out := map[string]bool{}
	var l []string
	if b, err := os.ReadFile(filepath.Join(a.DataDir, "merge-dismissed.json")); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	for _, k := range l {
		out[k] = true
	}
	return out
}

func pairKey(x, y string) string {
	if x > y {
		x, y = y, x
	}
	return x + "|" + y
}

// dismissMerge: these devices aren't the same — stop suggesting any pair of them.
func (a *App) dismissMerge(ids []string) error {
	m := a.mergeDismissed()
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			m[pairKey(ids[i], ids[j])] = true
		}
	}
	l := []string{}
	for k := range m {
		l = append(l, k)
	}
	sort.Strings(l)
	b, _ := json.Marshal(l)
	return os.WriteFile(filepath.Join(a.DataDir, "merge-dismissed.json"), b, 0o600)
}

type dupGroup struct {
	List  []CheckIn
	Exact bool // every name the same
}

// checkinDuplicates: approved check-ins of the same person (and kind) with the same or similar names, newest
// first, leaving out pairs an admin said aren't the same.
func (a *App) checkinDuplicates() []dupGroup {
	var l []CheckIn
	for _, x := range a.checkins.List() {
		if x.Status == "approved" {
			l = append(l, x)
		}
	}
	gone := a.mergeDismissed()
	parent := map[int]int{}
	var find func(int) int
	find = func(i int) int {
		if p, ok := parent[i]; ok && p != i {
			parent[i] = find(p)
			return parent[i]
		}
		return i
	}
	exactPair := map[[2]int]bool{}
	for i := range l {
		for j := i + 1; j < len(l); j++ {
			x, y := l[i], l[j]
			if x.PersonID != y.PersonID || (x.Kind != "" && y.Kind != "" && x.Kind != y.Kind) || gone[pairKey(x.ID, y.ID)] {
				continue
			}
			same, sim := similarNames(x.Name, y.Name)
			if !sim {
				continue
			}
			if same {
				exactPair[[2]int{i, j}] = true
			}
			parent[find(j)] = find(i)
		}
	}
	byRoot := map[int][]int{}
	for i := range l {
		byRoot[find(i)] = append(byRoot[find(i)], i)
	}
	var out []dupGroup
	for _, idx := range byRoot {
		if len(idx) < 2 {
			continue
		}
		g := dupGroup{Exact: true}
		for _, i := range idx {
			g.List = append(g.List, l[i])
		}
		for _, x := range g.List[1:] {
			if same, _ := similarNames(g.List[0].Name, x.Name); !same {
				g.Exact = false
			}
		}
		sort.Slice(g.List, func(i, j int) bool { return g.List[i].Time > g.List[j].Time })
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].List[0].Name < out[j].List[0].Name })
	return out
}

// duplicateAttention: Home's "needs you" — one line per device that looks checked in more than once.
func (a *App) duplicateAttention() []map[string]any {
	out := []map[string]any{}
	for _, g := range a.checkinDuplicates() {
		ids, names := []string{}, []string{}
		for _, x := range g.List {
			ids = append(ids, x.ID)
			if !contains(names, x.Name) {
				names = append(names, x.Name)
			}
		}
		out = append(out, map[string]any{"name": g.List[0].Name, "names": names, "person": g.List[0].PersonName, "count": len(g.List), "ids": ids, "exact": g.Exact})
	}
	return out
}

// mergeCheckins keeps one check-in and folds the others into it.
func (a *App) mergeCheckins(keepID string, dropIDs []string, by string) error {
	keep, ok := a.checkins.Get(keepID)
	if !ok || keep.Status != "approved" {
		return errors.New("pick the device to keep")
	}
	var drops []CheckIn
	for _, id := range dropIDs {
		if id == keepID {
			continue
		}
		x, ok := a.checkins.Get(id)
		if !ok || x.Status != "approved" {
			return errors.New("only approved devices can be merged")
		}
		drops = append(drops, x)
	}
	if len(drops) == 0 {
		return errors.New("pick another device to merge")
	}
	seen := keep.SetupSeen
	var oldMACs []string
	for _, x := range drops {
		if x.SetupSeen > seen {
			seen = x.SetupSeen
		}
		a.checkins.Edit(x.ID, func(y *CheckIn) { y.Status, y.By, y.MergedInto = "merged", by, keep.ID })
		if x.MAC != "" && x.MAC != keep.MAC {
			oldMACs = append(oldMACs, x.MAC)
		}
	}
	a.checkins.Edit(keep.ID, func(y *CheckIn) {
		if seen > y.SetupSeen {
			y.SetupSeen = seen
		}
	})
	if a.An.Open() {
		if keep.PersonID != "" && keep.MAC != "" {
			_ = a.An.AssignDevice(keep.PersonID, "mac", keep.MAC)
		}
		for _, mac := range oldMACs {
			if _, still := a.checkins.Registered(mac); !still {
				_ = a.An.UnassignDevice("mac", mac)
			}
		}
	}
	main := keep.Main
	for _, mac := range oldMACs { // Who's home followed an old address: it follows the one in use now
		if a.Cfg.Get().Presence.Devices[mac] != "" {
			if _, still := a.checkins.Registered(mac); !still {
				a.movePresenceTag(mac, keep.MAC)
				main = true
			}
		}
	}
	for _, x := range drops {
		main = main || x.Main
	}
	if main != keep.Main {
		a.checkins.Edit(keep.ID, func(y *CheckIn) { y.Main = main })
	}
	if main && keep.PersonID != "" && keep.MAC != "" && a.Cfg.Get().Presence.Devices[keep.MAC] == "" {
		_ = a.presenceTag(map[string]any{"mac": keep.MAC, "person": keep.PersonID})
	}
	if len(oldMACs) > 0 {
		a.archive.mu.Lock()
		m := a.archive.load()
		for _, mac := range oldMACs {
			if _, still := a.checkins.Registered(mac); !still {
				m[mac] = archivedDevice{MAC: mac, Name: keep.Name, At: time.Now().Unix(), By: by, Reason: "Merged into " + keep.Name + " (" + keep.MAC + ")"}
			}
		}
		err := a.archive.save(m)
		a.archive.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if keep.MAC != "" {
		s := a.approval
		s.mu.Lock()
		st := s.load()
		if st.Approved != nil && st.Approved[keep.MAC] == 0 {
			st.Approved[keep.MAC] = time.Now().Unix()
			_ = s.save(st)
		}
		s.mu.Unlock()
	}
	for _, al := range a.Alerts.List() { // the dropped one's "stopped using its setup" is no longer news
		if al.Kind != "setup_lost" && al.Kind != "setup_back" {
			continue
		}
		for _, x := range drops {
			if al.Data["id"] == x.ID {
				a.Alerts.MarkRead(al.ID)
			}
		}
	}
	return nil
}
