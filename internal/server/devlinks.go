package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// One device can be known by several keys: Wi-Fi addresses (a private address that changed, another network) and
// NextDNS names (an older profile, another spelling). People shows one chip per device; these are the keys that
// belong together. Some links come from check-ins (a device's address and the NextDNS names it uses), the rest are
// confirmed by an admin ("same device"); "apart" remembers pairs an admin said are different devices.

type devLinkFile struct {
	Groups [][]string `json:"groups"`          // each: keys like "mac|aa:bb:…" or "ndev|<profile>/<name>"
	Apart  []string   `json:"apart,omitempty"` // "keyA||keyB", sorted
}

type devLinks struct {
	mu   sync.Mutex
	path string
}

func newDevLinks(dir string) *devLinks {
	return &devLinks{path: filepath.Join(dir, "device-links.json")}
}

func (d *devLinks) load() devLinkFile {
	var f devLinkFile
	if b, err := os.ReadFile(d.path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	return f
}

func (d *devLinks) save(f devLinkFile) error {
	b, _ := json.MarshalIndent(f, "", " ")
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

var devKeyOK = func(k string) bool {
	kind, rest, ok := strings.Cut(k, "|")
	return ok && rest != "" && len(k) <= 200 && (kind == "mac" || kind == "ndev" || kind == "dns")
}

func apartKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "||" + b
}

// link puts keys into one group (joining any groups they're already in) and forgets "apart" between them.
func (d *devLinks) link(keys []string) error {
	clean := []string{}
	for _, k := range keys {
		if devKeyOK(k) {
			clean = append(clean, k)
		}
	}
	if len(clean) < 2 {
		return errors.New("pick at least two names of the same device")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f := d.load()
	set := map[string]bool{}
	for _, k := range clean {
		set[k] = true
	}
	keep := [][]string{}
	for _, g := range f.Groups {
		hit := false
		for _, k := range g {
			if set[k] {
				hit = true
			}
		}
		if hit {
			for _, k := range g {
				set[k] = true
			}
		} else {
			keep = append(keep, g)
		}
	}
	merged := make([]string, 0, len(set))
	for k := range set {
		merged = append(merged, k)
	}
	sort.Strings(merged)
	f.Groups = append(keep, merged)
	apart := []string{}
	for _, p := range f.Apart {
		a, b, _ := strings.Cut(p, "||")
		if !(set[a] && set[b]) {
			apart = append(apart, p)
		}
	}
	f.Apart = apart
	return d.save(f)
}

// separate takes keys out of their group and remembers they're apart from the rest of it.
func (d *devLinks) separate(keys, from []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	f := d.load()
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = true
	}
	groups := [][]string{}
	for _, g := range f.Groups {
		rest := []string{}
		for _, k := range g {
			if !out[k] {
				rest = append(rest, k)
			}
		}
		if len(rest) > 1 {
			groups = append(groups, rest)
		}
	}
	f.Groups = groups
	seen := map[string]bool{}
	for _, p := range f.Apart {
		seen[p] = true
	}
	for _, k := range keys {
		for _, o := range from {
			if o != k && !out[o] && devKeyOK(k) && devKeyOK(o) {
				if p := apartKey(k, o); !seen[p] {
					seen[p] = true
					f.Apart = append(f.Apart, p)
				}
			}
		}
	}
	if len(f.Apart) > 5000 {
		f.Apart = f.Apart[len(f.Apart)-5000:]
	}
	return d.save(f)
}

// deviceLinksView: the confirmed groups, the groups check-ins give, and the pairs kept apart.
func (a *App) deviceLinksView() map[string]any {
	f := a.devLinks.load()
	groups := append([][]string{}, f.Groups...)
	for _, x := range a.checkins.List() {
		if x.Status != "approved" || x.MAC == "" {
			continue
		}
		g := []string{"mac|" + strings.ToLower(x.MAC)}
		names := map[string]bool{}
		for _, n := range append([]string{x.Name, x.ProfileName}, x.NDNames...) {
			if n != "" {
				names[n] = true
			}
		}
		for _, y := range a.checkins.List() { // earlier addresses of the same device
			if y.Status == "merged" && y.MergedInto == x.ID && y.MAC != "" {
				g = append(g, "mac|"+strings.ToLower(y.MAC))
			}
		}
		for _, p := range ndProfilesOf(x) {
			if p == "" {
				continue
			}
			for n := range names {
				g = append(g, "ndev|"+p+"/"+n)
			}
		}
		if len(g) > 1 {
			sort.Strings(g)
			groups = append(groups, g)
		}
	}
	return map[string]any{"groups": groups, "apart": nz(f.Apart)}
}

func nz[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}

// strList: a JSON array of strings (anything else is skipped).
func strList(v any) []string {
	out := []string{}
	for _, x := range anyArr(v) {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
