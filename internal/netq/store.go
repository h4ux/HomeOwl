// Package netq is HomeOwl's record of the internet connection: a reading every minute from this computer (how long
// the gateway and the internet take to answer), the speed tests the gateway ran, and the outages put together from
// the readings, each with who it was most likely down to: the provider, or something at home.
package netq

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Sample is one reading. Gw and Net are milliseconds; nil means nothing answered. NoGw: no gateway to try yet.
type Sample struct {
	T    int64    `json:"t"` // ms
	Gw   *float64 `json:"g,omitempty"`
	Net  *float64 `json:"n,omitempty"`
	NoGw bool     `json:"ng,omitempty"`
}

// NetDown: the internet didn't answer.
func (s Sample) NetDown() bool { return s.Net == nil }

// GwDown: there was a gateway to try and it didn't answer.
func (s Sample) GwDown() bool { return !s.NoGw && s.Gw == nil }

// SpeedTest is one speed test run by the gateway (UniFi keeps them; HomeOwl copies them here).
type SpeedTest struct {
	T       int64   `json:"t"`              // ms
	Down    float64 `json:"down"`           // Mbps
	Up      float64 `json:"up"`             // Mbps
	Latency float64 `json:"latency"`        // ms
	Server  string  `json:"server"`         // where the test ran to, if UniFi says
	Source  string  `json:"source"`         // unifi (UniFi's own schedule or a button there), schedule (HomeOwl's), homeowl (someone in HomeOwl)
	By      string  `json:"by,omitempty"`   // who started it in HomeOwl
	WAN     string  `json:"wan,omitempty"`  // which connection, when UniFi says
	Note    string  `json:"note,omitempty"` // e.g. "below half your plan"
}

// Outage is a stretch when the internet didn't answer. Cause: isp (the gateway answered, nothing past it did) or
// home (the gateway didn't answer either: the home network, its power, or this computer's own connection).
type Outage struct {
	Start int64  `json:"start"`
	End   int64  `json:"end,omitempty"` // 0 = still down
	Cause string `json:"cause"`
	// minutes of each kind while it lasted (the cause is the bigger one)
	IspMin  int `json:"isp_min"`
	HomeMin int `json:"home_min"`
}

// Minutes the outage lasted (up to now if it hasn't ended).
func (o Outage) Minutes(now time.Time) float64 {
	end := o.End
	if end == 0 {
		end = now.UnixMilli()
	}
	return float64(end-o.Start) / 60000
}

// Store keeps 7 days of readings, a year of speed tests and a year of outages in a JSON file.
type Store struct {
	mu        sync.Mutex
	path      string
	Samples   []Sample    `json:"samples"`
	Tests     []SpeedTest `json:"tests"`
	Outages   []Outage    `json:"outages"`
	Triggered []Trigger   `json:"triggered,omitempty"` // speed tests HomeOwl asked for, to label them when they show up
	dirty     bool
}

// Trigger is a speed test HomeOwl started.
type Trigger struct {
	T      int64  `json:"t"`
	Source string `json:"source"`
	By     string `json:"by,omitempty"`
}

const (
	sampleDays = 7
	keepDays   = 365
	maxTests   = 3000
	maxOutages = 2000
)

// Open loads the store at path (a missing or broken file starts empty).
func Open(path string) *Store {
	s := &Store{path: path}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, s); err != nil {
			log.Printf("internet: history file unreadable, starting fresh: %v", err)
			s.Samples, s.Tests, s.Outages, s.Triggered = nil, nil, nil, nil
		}
	}
	return s
}

// Since returns the readings, speed tests and outages newer than t (ms), oldest first. An outage that started
// earlier but was still going at t counts.
func (s *Store) Since(t int64) ([]Sample, []SpeedTest, []Outage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := []Sample{}
	for _, x := range s.Samples {
		if x.T >= t {
			ss = append(ss, x)
		}
	}
	ts := []SpeedTest{}
	for _, x := range s.Tests {
		if x.T >= t {
			ts = append(ts, x)
		}
	}
	out := []Outage{}
	for _, o := range s.Outages {
		if o.End == 0 || o.End >= t {
			out = append(out, o)
		}
	}
	return ss, ts, out
}

// Open outage, if the internet is down right now.
func (s *Store) current() *Outage {
	if n := len(s.Outages); n > 0 && s.Outages[n-1].End == 0 {
		return &s.Outages[n-1]
	}
	return nil
}

// AddTrigger remembers that HomeOwl started a speed test.
func (s *Store) AddTrigger(t Trigger) {
	s.mu.Lock()
	s.Triggered = append(s.Triggered, t)
	if len(s.Triggered) > 50 {
		s.Triggered = s.Triggered[len(s.Triggered)-50:]
	}
	s.dirty = true
	s.mu.Unlock()
}

// LastTrigger is when HomeOwl last started a speed test from this source (0 = never).
func (s *Store) LastTrigger(source string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.Triggered) - 1; i >= 0; i-- {
		if s.Triggered[i].Source == source {
			return s.Triggered[i].T
		}
	}
	return 0
}

// MergeTests adds speed tests that aren't kept yet (the same test is within a minute of one already kept) and
// labels the ones HomeOwl started (a test within 5 minutes after a trigger). Returns the new ones.
func (s *Store) MergeTests(in []SpeedTest) []SpeedTest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var added []SpeedTest
	for _, t := range in {
		if t.T <= 0 || (t.Down <= 0 && t.Up <= 0) {
			continue
		}
		dup := false
		for _, k := range s.Tests {
			if abs64(k.T-t.T) < 60000 {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if t.Source == "" {
			t.Source = "unifi"
		}
		for _, tr := range s.Triggered {
			if t.T >= tr.T-30000 && t.T <= tr.T+5*60000 {
				t.Source, t.By = tr.Source, tr.By
			}
		}
		s.Tests = append(s.Tests, t)
		added = append(added, t)
	}
	if len(added) > 0 {
		sort.Slice(s.Tests, func(i, j int) bool { return s.Tests[i].T < s.Tests[j].T })
		s.dirty = true
	}
	return added
}

// Save trims old entries and writes the file when something changed.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	now := time.Now()
	sc := now.AddDate(0, 0, -sampleDays).UnixMilli()
	i := sort.Search(len(s.Samples), func(i int) bool { return s.Samples[i].T >= sc })
	s.Samples = append([]Sample(nil), s.Samples[i:]...)
	kc := now.AddDate(0, 0, -keepDays).UnixMilli()
	j := sort.Search(len(s.Tests), func(i int) bool { return s.Tests[i].T >= kc })
	if len(s.Tests)-j > maxTests {
		j = len(s.Tests) - maxTests
	}
	s.Tests = append([]SpeedTest(nil), s.Tests[j:]...)
	k := 0
	for k < len(s.Outages) && s.Outages[k].End != 0 && s.Outages[k].End < kc {
		k++
	}
	if len(s.Outages)-k > maxOutages {
		k = len(s.Outages) - maxOutages
	}
	s.Outages = append([]Outage(nil), s.Outages[k:]...)
	b, err := json.Marshal(s)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(s.path, b, 0o600)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
