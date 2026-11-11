// Package presence works out who is home from the devices tagged to each person: someone is home while one of
// their devices is connected to the home network, and leaves when none has been seen for a grace period
// (phones drop Wi-Fi while asleep). Arrivals and departures can be kept as history.
package presence

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Event is an arrival or a departure.
type Event struct {
	T          int64  `json:"t"` // ms; a departure is dated when the device was last seen
	Person     string `json:"person"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // "arrive" or "leave"
	Device     string `json:"device,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

// State is where someone is now.
type State struct {
	Person     string `json:"person"`
	Home       bool   `json:"home"`
	Since      int64  `json:"since"`            // ms of the last change (or when HomeOwl started watching)
	Device     string `json:"device,omitempty"` // the device that says so (home) or was seen last (away)
	DeviceName string `json:"device_name,omitempty"`
	LastSeen   int64  `json:"last_seen,omitempty"`
}

// Person is someone with tagged devices, as the caller knows them.
type Person struct {
	ID, Name string
	Devices  []string // lower-case MACs
}

// Tracker keeps the current state, when each device was last seen, and the history.
type Tracker struct {
	path string

	mu       sync.Mutex
	States   map[string]*State `json:"states"`
	Seen     map[string]int64  `json:"seen"` // MAC → ms
	Events   []Event           `json:"events"`
	Watching int64             `json:"watching"` // ms of the last successful look
	dirty    bool
}

func Open(path string) *Tracker {
	t := &Tracker{path: path, States: map[string]*State{}, Seen: map[string]int64{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, t)
		if t.States == nil {
			t.States = map[string]*State{}
		}
		if t.Seen == nil {
			t.Seen = map[string]int64{}
		}
	}
	return t
}

// Options: how long until someone without a connected device counts as away, and the history to keep.
type Options struct {
	Grace   time.Duration
	History bool
	Days    int
}

// Observe takes the devices connected right now (MAC → name) and returns the arrivals and departures it found.
func (t *Tracker) Observe(now time.Time, online map[string]string, people []Person, o Options) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	ms := now.UnixMilli()
	t.Watching = ms
	var out []Event
	keep := map[string]bool{}
	for _, p := range people {
		keep[p.ID] = true
		var via, viaName string
		var last int64
		for _, mac := range p.Devices {
			if name, ok := online[mac]; ok {
				t.Seen[mac] = ms
				if via == "" {
					via, viaName = mac, name
				}
			}
			if s := t.Seen[mac]; s > last {
				last = s
			}
		}
		home := via != "" || (last > 0 && ms-last <= o.Grace.Milliseconds())
		st := t.States[p.ID]
		if st == nil { // the first look: no event, we don't know when they came or went
			t.States[p.ID] = &State{Person: p.ID, Home: home, Since: ms, Device: via, DeviceName: viaName, LastSeen: last}
			t.dirty = true
			continue
		}
		if via != "" {
			st.Device, st.DeviceName = via, viaName
		}
		st.LastSeen = last
		if home == st.Home {
			continue
		}
		e := Event{T: ms, Person: p.ID, Name: p.Name, Kind: "arrive", Device: st.Device, DeviceName: st.DeviceName}
		if !home {
			e.Kind = "leave"
			if last > 0 {
				e.T = last
			}
		}
		st.Home, st.Since = home, e.T
		t.dirty = true
		out = append(out, e)
		if o.History {
			t.Events = append(t.Events, e)
		}
	}
	for id := range t.States { // people who were removed or have no devices any more
		if !keep[id] {
			delete(t.States, id)
			t.dirty = true
		}
	}
	t.trim(now, o)
	return out
}

func (t *Tracker) trim(now time.Time, o Options) {
	if !o.History {
		if len(t.Events) > 0 {
			t.Events, t.dirty = nil, true
		}
		return
	}
	days := o.Days
	if days <= 0 {
		days = 90
	}
	cut := now.AddDate(0, 0, -days).UnixMilli()
	i := sort.Search(len(t.Events), func(i int) bool { return t.Events[i].T >= cut })
	if i > 0 {
		t.Events, t.dirty = append([]Event(nil), t.Events[i:]...), true
	}
	if n := len(t.Events); n > 50000 {
		t.Events = append([]Event(nil), t.Events[n-50000:]...)
	}
}

// Now returns everyone's state.
func (t *Tracker) Now() map[string]State {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]State{}
	for id, s := range t.States {
		out[id] = *s
	}
	return out
}

// Since returns the history from ms on, oldest first.
func (t *Tracker) Since(ms int64) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	i := sort.Search(len(t.Events), func(i int) bool { return t.Events[i].T >= ms })
	return append([]Event{}, t.Events[i:]...)
}

// Clear forgets the history (the current state stays).
func (t *Tracker) Clear() {
	t.mu.Lock()
	t.Events, t.dirty = nil, true
	t.mu.Unlock()
}

// Forget drops a device's last-seen time (after it's untagged).
func (t *Tracker) Forget(mac string) {
	t.mu.Lock()
	delete(t.Seen, mac)
	t.dirty = true
	t.mu.Unlock()
}

// Save writes the file when something changed.
func (t *Tracker) Save() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.dirty {
		return nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(t.path, b, 0o600); err != nil {
		return err
	}
	t.dirty = false
	return nil
}
