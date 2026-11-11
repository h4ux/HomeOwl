package ups

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Sample is one reading of a UPS, kept once a minute.
type Sample struct {
	T       int64    `json:"t"` // ms
	UPS     string   `json:"u"`
	Status  string   `json:"s"`           // raw ups.status flags, e.g. "OL CHRG"
	Charge  *float64 `json:"c,omitempty"` // %
	Runtime *float64 `json:"r,omitempty"` // seconds left on battery
	Load    *float64 `json:"l,omitempty"` // %
	In      *float64 `json:"i,omitempty"` // input voltage
	Out     *float64 `json:"o,omitempty"` // output voltage
}

// Event is something that happened: a status change, a lost connection, or a script HomeOwl ran.
type Event struct {
	T       int64    `json:"t"`             // ms
	UPS     string   `json:"ups,omitempty"` // UPS name ("" = the server connection)
	Kind    string   `json:"kind"`          // onbatt online lowbatt fsd replbatt commbad commok shutdown script test
	Text    string   `json:"text"`
	Status  string   `json:"status,omitempty"`
	Charge  *float64 `json:"charge,omitempty"`
	Runtime *float64 `json:"runtime,omitempty"`
	Script  string   `json:"script,omitempty"`
	For     string   `json:"for,omitempty"` // for a script: the event it ran for
	Exit    *int     `json:"exit,omitempty"`
	Output  string   `json:"output,omitempty"`
	By      string   `json:"by,omitempty"`
}

// History keeps 7 days of minute samples and a year of events in a JSON file.
type History struct {
	mu         sync.Mutex
	path       string
	Samples    []Sample `json:"samples"`
	Events     []Event  `json:"events"`
	dirty      bool
	SampleDays int `json:"-"`
	EventDays  int `json:"-"`
	MaxEvents  int `json:"-"`
}

func NewHistory(path string) *History {
	h := &History{path: path, SampleDays: 7, EventDays: 365, MaxEvents: 5000}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, h); err != nil {
			log.Printf("ups: history file unreadable, starting fresh: %v", err)
			h.Samples, h.Events = nil, nil
		}
	}
	return h
}

func (h *History) AddSample(s Sample) {
	h.mu.Lock()
	h.Samples = append(h.Samples, s)
	h.dirty = true
	h.mu.Unlock()
}

func (h *History) AddEvent(e Event) {
	if e.T == 0 {
		e.T = time.Now().UnixMilli()
	}
	h.mu.Lock()
	h.Events = append(h.Events, e)
	h.dirty = true
	h.mu.Unlock()
}

// Since returns samples and events newer than t (ms), oldest first; ups "" means all.
func (h *History) Since(t int64, ups string) ([]Sample, []Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ss := []Sample{}
	for _, s := range h.Samples {
		if s.T >= t && (ups == "" || s.UPS == ups) {
			ss = append(ss, s)
		}
	}
	es := []Event{}
	for _, e := range h.Events {
		if e.T >= t && (ups == "" || e.UPS == "" || e.UPS == ups) {
			es = append(es, e)
		}
	}
	return ss, es
}

// Save trims old entries and writes the file when something changed.
func (h *History) Save() error {
	h.mu.Lock()
	if !h.dirty {
		h.mu.Unlock()
		return nil
	}
	now := time.Now()
	sc := now.AddDate(0, 0, -h.SampleDays).UnixMilli()
	i := sort.Search(len(h.Samples), func(i int) bool { return h.Samples[i].T >= sc })
	h.Samples = append([]Sample(nil), h.Samples[i:]...)
	ec := now.AddDate(0, 0, -h.EventDays).UnixMilli()
	j := sort.Search(len(h.Events), func(i int) bool { return h.Events[i].T >= ec })
	if len(h.Events)-j > h.MaxEvents {
		j = len(h.Events) - h.MaxEvents
	}
	h.Events = append([]Event(nil), h.Events[j:]...)
	b, err := json.Marshal(h)
	h.dirty = false
	h.mu.Unlock()
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(h.path, b, 0o600)
}

// Outage is one stretch on battery, put together from the events.
type Outage struct {
	Start     int64    `json:"start"`
	End       int64    `json:"end,omitempty"` // 0 = still on battery
	UPS       string   `json:"ups"`
	MinCharge *float64 `json:"min_charge,omitempty"`
	Shutdown  bool     `json:"shutdown"` // HomeOwl triggered the shutdown actions
}

// Outages puts the power cuts together from the events (and the lowest charge from samples and events), newest first.
func Outages(samples []Sample, events []Event) []Outage {
	open := map[string]*Outage{}
	var out []Outage
	for _, e := range events {
		switch e.Kind {
		case "onbatt":
			if open[e.UPS] == nil {
				open[e.UPS] = &Outage{Start: e.T, UPS: e.UPS}
			}
		case "shutdown":
			if o := open[e.UPS]; o != nil {
				o.Shutdown = true
			}
		case "online":
			if o := open[e.UPS]; o != nil {
				o.End = e.T
				out = append(out, *o)
				delete(open, e.UPS)
			}
		}
	}
	for _, o := range open {
		out = append(out, *o)
	}
	for i := range out {
		o := &out[i]
		for _, s := range samples {
			if s.UPS == o.UPS && s.T >= o.Start && (o.End == 0 || s.T <= o.End) && s.Charge != nil && (o.MinCharge == nil || *s.Charge < *o.MinCharge) {
				v := *s.Charge
				o.MinCharge = &v
			}
		}
		for _, e := range events {
			if e.UPS == o.UPS && e.T >= o.Start && (o.End == 0 || e.T <= o.End) && e.Charge != nil && (o.MinCharge == nil || *e.Charge < *o.MinCharge) {
				v := *e.Charge
				o.MinCharge = &v
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out
}

// flag words in ups.status and what they mean
var flagText = map[string]string{
	"OL": "on mains", "OB": "on battery", "LB": "battery low", "HB": "battery high", "RB": "replace battery",
	"CHRG": "charging", "DISCHRG": "discharging", "BYPASS": "on bypass", "CAL": "calibrating", "OFF": "output off",
	"OVER": "overloaded", "TRIM": "lowering high voltage", "BOOST": "raising low voltage", "FSD": "forced shutdown",
	"ALARM": "alarm", "TEST": "self-test", "ECO": "eco mode",
}

// Flags splits ups.status ("OB DISCHRG LB") into its words.
func Flags(status string) []string { return strings.Fields(status) }

func hasFlag(status, f string) bool {
	for _, x := range strings.Fields(status) {
		if x == f {
			return true
		}
	}
	return false
}

// Describe turns ups.status into plain words: "on battery, discharging, battery low".
func Describe(status string) string {
	var w []string
	for _, f := range Flags(status) {
		if t, ok := flagText[f]; ok {
			w = append(w, t)
		} else {
			w = append(w, strings.ToLower(f))
		}
	}
	if len(w) == 0 {
		return "unknown"
	}
	return strings.Join(w, ", ")
}
