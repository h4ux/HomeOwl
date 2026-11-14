package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/config"
)

// Owner suggestions (Family › People & groups): HomeOwl's guesses at whose each unowned UniFi client is
// (analytics/owners.go). An admin links or turns down each one; turned-down guesses are kept in
// <data>/owner-dismissed.json (MAC → person IDs, "*" = nobody's: don't guess again).

type ownerDismissed struct {
	mu   sync.Mutex
	path string
}

func (d *ownerDismissed) load() map[string][]string {
	m := map[string][]string{}
	if b, err := os.ReadFile(d.path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (d *ownerDismissed) skip() func(mac, pid string) bool {
	d.mu.Lock()
	m := d.load()
	d.mu.Unlock()
	return func(mac, pid string) bool { return contains(m[mac], "*") || contains(m[mac], pid) }
}

func (d *ownerDismissed) add(mac, pid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.load()
	if !contains(m[mac], pid) {
		m[mac] = append(m[mac], pid)
	}
	if len(m) > 5000 {
		return errors.New("too many turned-down guesses")
	}
	b, _ := json.Marshal(m)
	return config.WriteFileAtomic(d.path, b, 0o600)
}

func (a *App) ownerDismissed() *ownerDismissed {
	return &ownerDismissed{path: filepath.Join(a.DataDir, "owner-dismissed.json")}
}

var ownerMu sync.Mutex // one dismissed-file writer at a time

func (a *App) ownersView() (map[string]any, error) {
	if !a.An.Open() {
		return map[string]any{"suggestions": []any{}, "off": true}, nil
	}
	h, err := a.An.OwnerSuggestions(a.cliTypes.All(), a.ownerDismissed().skip())
	if err != nil {
		return nil, err
	}
	if h == nil {
		h = []analytics.OwnerHint{}
	}
	return map[string]any{"suggestions": h}, nil
}

// ownersAPI: owners/link {mac, person_id} and owners/dismiss {mac, person_id | all}.
func (a *App) ownersAPI(op string, body map[string]any) (map[string]any, error) {
	mac := strings.ToLower(s(body, "mac"))
	if !macRE.MatchString(mac) {
		return nil, errors.New("that isn't a MAC address")
	}
	pid := s(body, "person_id")
	switch op {
	case "link":
		if pid == "" {
			return nil, errors.New("pick a person or group")
		}
		if err := a.An.AssignDevice(pid, "mac", mac); err != nil {
			return nil, err
		}
	case "dismiss":
		if b(body, "all") {
			pid = "*"
		}
		if pid == "" {
			return nil, errors.New("missing person")
		}
		ownerMu.Lock()
		err := a.ownerDismissed().add(mac, pid)
		ownerMu.Unlock()
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown action")
	}
	return a.ownersView()
}
