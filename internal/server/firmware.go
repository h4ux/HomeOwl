package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/history"
)

// Firmware watch: remembers since when each device has had an update waiting, and reads recent firmware
// changes out of the configuration history.

type fwPending struct {
	To    string `json:"to"`
	Since int64  `json:"since"`
}

type firmwareWatch struct {
	mu   sync.Mutex
	path string
}

func (f *firmwareWatch) load() map[string]fwPending {
	m := map[string]fwPending{}
	if b, err := os.ReadFile(f.path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// Observe notes the updates offered in a collected bundle.
func (f *firmwareWatch) Observe(bundle map[string]any) {
	legacy, _ := bundle["legacy"].(map[string]any)
	devs, ok := legacy["devices"].([]any)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old, now := f.load(), map[string]fwPending{}
	for _, x := range devs {
		d, _ := x.(map[string]any)
		mac, _ := d["mac"].(string)
		up, _ := d["upgradable"].(bool)
		if mac == "" || !up {
			continue
		}
		to, _ := d["upgrade_to_firmware"].(string)
		mac = strings.ToLower(mac)
		if p, ok := old[mac]; ok && p.To == to {
			now[mac] = p
		} else {
			now[mac] = fwPending{To: to, Since: time.Now().Unix()}
		}
	}
	b, _ := json.MarshalIndent(now, "", " ")
	_ = config.WriteFileAtomic(f.path, b, 0o600)
}

func (f *firmwareWatch) Pending() map[string]fwPending {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}

type fwChange struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Before any    `json:"before"`
	After  any    `json:"after"`
	When   int64  `json:"when"`
}

// recentFirmware lists devices whose firmware changed in the last `days` days, snapshot by snapshot.
func (a *App) recentFirmware(days int) []fwChange {
	out := []fwChange{}
	l := a.History.List()
	start, _ := history.At(l, time.Now().Unix()-int64(days)*86400)
	var prev *history.Snapshot
	for _, e := range l {
		if e.Time < start.Time {
			continue
		}
		s, err := a.History.Load(e.File)
		if err != nil {
			continue
		}
		if prev != nil {
			for _, c := range history.Diff(*prev, s) {
				if c.Area != "UniFi devices" {
					continue
				}
				for _, fl := range c.Fields {
					if fl.Path == "version" {
						out = append(out, fwChange{ID: c.ID, Name: c.Name, Before: fl.Before, After: fl.After, When: e.Time})
					}
				}
			}
		}
		prev = &s
	}
	return out
}

func newFirmwareWatch(dataDir string) *firmwareWatch {
	return &firmwareWatch{path: filepath.Join(dataDir, "firmware-pending.json")}
}
