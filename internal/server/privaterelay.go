package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// iCloud Private Relay (Family › Apps): with it on, Safari's traffic skips NextDNS, so blocks and HomeOwl's view
// don't reach it. A profile can't turn it off on a personal (unsupervised) iPhone, but a network can: Apple's devices
// stop using it when mask.icloud.com and mask-h2.icloud.com don't resolve. The switch adds them to a NextDNS
// profile's denylist; devices using that profile (at home, and anywhere with HomeOwl's encrypted-DNS profile) then
// show "Private Relay is turned off for this network". Off takes out only the entries HomeOwl added; an entry that
// was already on the denylist is switched off, not removed.

var relayDomains = []string{"mask.icloud.com", "mask-h2.icloud.com"}

type privateRelay struct {
	mu   sync.Mutex
	path string
}

type relayStore struct {
	Owned map[string][]string `json:"owned"` // profile → the entries HomeOwl added
}

func newPrivateRelay(dir string) *privateRelay {
	return &privateRelay{path: filepath.Join(dir, "private-relay.json")}
}

func (s *privateRelay) load() relayStore {
	st := relayStore{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Owned == nil {
		st.Owned = map[string][]string{}
	}
	return st
}

func (s *privateRelay) save(st relayStore) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

// relayState: "on" (Private Relay turned off by the profile), "off", or "partial".
func relayState(nd ndDeny, profile string) (string, error) {
	list, err := nd.NDDenylist(profile)
	if err != nil {
		return "", err
	}
	n := 0
	for _, x := range list {
		if x.Active && inList(relayDomains, x.ID) {
			n++
		}
	}
	switch n {
	case 0:
		return "off", nil
	case len(relayDomains):
		return "on", nil
	}
	return "partial", nil
}

func (a *App) privateRelayView() map[string]any {
	nd := appDenyFor(a)
	people := a.profilePeople()
	rows := []map[string]any{}
	for _, p := range a.ndProfilesCached() {
		row := map[string]any{"id": p.ID, "name": p.Name, "people": orEmpty(people[p.ID])}
		if st, err := relayState(nd, p.ID); err != nil {
			row["error"] = err.Error()
		} else {
			row["state"] = st
		}
		rows = append(rows, row)
	}
	return map[string]any{"profiles": rows, "domains": relayDomains}
}

// setPrivateRelay turns Private Relay off (block true) or back on (false) for the devices on one NextDNS profile.
func (a *App) setPrivateRelay(body map[string]any, by string) (map[string]any, error) {
	if !a.An.Open() {
		return nil, errors.New("analytics isn't running")
	}
	p, block := s(body, "profile"), b(body, "block")
	if !ndProfileIDRE.MatchString(p) {
		return nil, errors.New("that isn't a NextDNS profile ID")
	}
	nd := appDenyFor(a)
	pr := a.privateRelay
	pr.mu.Lock()
	defer pr.mu.Unlock()
	st := pr.load()
	list, err := nd.NDDenylist(p)
	if err != nil {
		return nil, err
	}
	cur := map[string]bool{}
	there := map[string]bool{}
	for _, x := range list {
		cur[x.ID], there[x.ID] = x.Active, true
	}
	var changed, errs []string
	for _, d := range relayDomains {
		owned := inList(st.Owned[p], d)
		var err error
		switch {
		case block && !there[d]:
			if err = nd.NDDenyAdd(p, d, true); err == nil {
				st.Owned[p] = append(st.Owned[p], d)
			}
		case block && !cur[d]:
			err = nd.NDDenySet(p, d, true)
		case !block && there[d] && owned:
			err = nd.NDDenyRemove(p, d)
			if err != nil && strings.Contains(err.Error(), "404") {
				err = nil
			}
			if err == nil {
				st.Owned[p] = without(st.Owned[p], d)
			}
		case !block && cur[d]: // the owner's own entry: switched off, left on the list
			err = nd.NDDenySet(p, d, false)
		default:
			continue
		}
		if err != nil {
			errs = append(errs, d+": "+err.Error())
			continue
		}
		changed = append(changed, d)
	}
	if len(st.Owned[p]) == 0 {
		delete(st.Owned, p)
	}
	if err := pr.save(st); err != nil {
		return nil, err
	}
	if len(changed) > 0 {
		desc := "Turn off iCloud Private Relay"
		if !block {
			desc = "Allow iCloud Private Relay again"
		}
		desc += " for NextDNS profile " + p
		a.logBlock(by, AppBlock{Profiles: []string{p}}, desc, map[string]any{"profile": p, "blocked": !block}, map[string]any{"profile": p, "blocked": block, "changed": changed})
	}
	if len(errs) > 0 {
		return nil, errors.New("NextDNS: " + strings.Join(errs, "; "))
	}
	return a.privateRelayView(), nil
}

func without(l []string, s string) []string {
	out := l[:0:0]
	for _, x := range l {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
