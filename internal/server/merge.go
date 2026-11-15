package server

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// Merging check-ins: the same phone checked in twice — it got a new Wi-Fi address (a private address changed, the
// profile was reinstalled, the network was forgotten) and was checked in again under the same name. Both check-ins
// then look like separate devices, and the old one "stops using its setup". Merging keeps one (the newest, by
// default) and marks the others "merged": their old address leaves the person and is archived, and the kept one is
// approved for device approval. Nothing is deleted.

// checkinDuplicates: groups of approved check-ins with the same name for the same person, newest first.
func (a *App) checkinDuplicates() [][]CheckIn {
	groups := map[string][]CheckIn{}
	for _, x := range a.checkins.List() {
		if x.Status != "approved" {
			continue
		}
		k := x.PersonID + "|" + strings.ToLower(strings.TrimSpace(x.Name))
		groups[k] = append(groups[k], x)
	}
	var out [][]CheckIn
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(i, j int) bool { return g[i].Time > g[j].Time })
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].Name < out[j][0].Name })
	return out
}

// duplicateAttention: Home's "needs you" — one line per device checked in more than once.
func (a *App) duplicateAttention() []map[string]any {
	out := []map[string]any{}
	for _, g := range a.checkinDuplicates() {
		ids := []string{}
		for _, x := range g {
			ids = append(ids, x.ID)
		}
		out = append(out, map[string]any{"name": g[0].Name, "person": g[0].PersonName, "count": len(g), "ids": ids})
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
