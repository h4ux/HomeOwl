// Package protect reads UniFi Protect through its official integration API (the same API key as Network):
// cameras and their health, snapshots, and a timeline of motion, smart detections and doorbell rings that
// HomeOwl records from Protect's event stream. Read-only: it never changes anything in Protect.
package protect

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

const base = "/proxy/protect/integration/v1"

// Event is one detection or ring, as kept by HomeOwl.
type Event struct {
	ID     string   `json:"id"`
	Type   string   `json:"type"`   // motion | smart | audio | ring | line | other
	Kinds  []string `json:"kinds"`  // person, vehicle, package, animal, face, licensePlate, smoke_cmonx, …
	Camera string   `json:"camera"` // device id
	Start  int64    `json:"start"`  // ms
	End    int64    `json:"end,omitempty"`
}

// Store keeps the last `Days` days of events (at most Max) in a JSON file.
type Store struct {
	mu    sync.Mutex
	path  string
	ev    map[string]*Event
	dirty bool
	Days  int
	Max   int
}

func NewStore(path string) *Store {
	s := &Store{path: path, ev: map[string]*Event{}, Days: 30, Max: 20000}
	if b, err := os.ReadFile(path); err == nil {
		var l []*Event
		if json.Unmarshal(b, &l) == nil {
			for _, e := range l {
				if e != nil && e.ID != "" {
					s.ev[e.ID] = e
				}
			}
		}
	}
	return s
}

// Apply merges one message from the event stream: {"type":"add"|"update","item":{…}}.
func (s *Store) Apply(raw []byte) bool {
	var m struct {
		Type string         `json:"type"`
		Item map[string]any `json:"item"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Item == nil {
		return false
	}
	id, _ := m.Item["id"].(string)
	if id == "" {
		return false
	}
	if mk, _ := m.Item["modelKey"].(string); mk != "" && mk != "event" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.ev[id]
	if e == nil {
		if m.Type == "remove" {
			return false
		}
		e = &Event{ID: id}
		s.ev[id] = e
	}
	if m.Type == "remove" {
		delete(s.ev, id)
		s.dirty = true
		return true
	}
	if t, ok := m.Item["type"].(string); ok && t != "" {
		e.Type = normType(t)
	}
	if d, ok := m.Item["device"].(string); ok && d != "" {
		e.Camera = d
	}
	if v, ok := num(m.Item["start"]); ok {
		e.Start = v
	}
	if v, ok := num(m.Item["end"]); ok {
		e.End = v
	}
	if k, ok := m.Item["smartDetectTypes"].([]any); ok {
		e.Kinds = e.Kinds[:0]
		for _, x := range k {
			if v, ok := x.(string); ok && v != "" {
				e.Kinds = append(e.Kinds, v)
			}
		}
	}
	if e.Start == 0 {
		e.Start = time.Now().UnixMilli()
	}
	s.dirty = true
	return true
}

func normType(t string) string {
	switch t {
	case "motion":
		return "motion"
	case "ring", "doorbell":
		return "ring"
	case "smartDetectZone", "smartDetectLoiterZone":
		return "smart"
	case "smartDetectLine":
		return "line"
	case "smartAudioDetect":
		return "audio"
	}
	return "other"
}

func num(v any) (int64, bool) {
	f, ok := v.(float64)
	return int64(f), ok && f > 0
}

// Since lists events that started after t (ms), newest first.
func (s *Store) Since(t int64) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for _, e := range s.ev {
		if e.Start >= t {
			c := *e
			c.Kinds = append([]string{}, e.Kinds...)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out
}

// Save prunes old events and writes the file if anything changed.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().AddDate(0, 0, -s.Days).UnixMilli()
	l := make([]*Event, 0, len(s.ev))
	for id, e := range s.ev {
		if e.Start < cut {
			delete(s.ev, id)
			s.dirty = true
			continue
		}
		l = append(l, e)
	}
	if !s.dirty {
		return nil
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Start < l[j].Start })
	if len(l) > s.Max {
		for _, e := range l[:len(l)-s.Max] {
			delete(s.ev, e.ID)
		}
		l = l[len(l)-s.Max:]
	}
	b, _ := json.Marshal(l)
	if err := config.WriteFileAtomic(s.path, b, 0o600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Watcher keeps a subscription to Protect's event stream while HomeOwl is connected to a console with Protect.
type Watcher struct {
	UniFi *unifi.Client
	Store *Store

	mu      sync.Mutex
	state   string // "", "off" (no Protect), "connecting", "live", "error"
	err     string
	since   int64
	version string
}

// Status is what the Cameras page shows about the event recorder.
type Status struct {
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
	Since   int64  `json:"since,omitempty"`
	Version string `json:"version,omitempty"`
}

func (w *Watcher) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Status{w.state, w.err, w.since, w.version}
}

func (w *Watcher) set(state, err string) {
	w.mu.Lock()
	w.state, w.err = state, err
	if state == "live" {
		w.since = time.Now().Unix()
	}
	w.mu.Unlock()
}

// Run loops until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	saved := make(chan struct{})
	defer func() { <-saved }() // the last save is done before Run returns
	go func() {                // save every minute
		defer close(saved)
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = w.Store.Save()
				return
			case <-t.C:
				if err := w.Store.Save(); err != nil {
					log.Printf("protect: saving events: %v", err)
				}
			}
		}
	}()
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		if !w.UniFi.Connected() {
			w.set("", "")
			sleep(ctx, 30*time.Second)
			continue
		}
		st, info := w.UniFi.Get(base + "/meta/info")
		if st != 200 {
			if st == 404 || st == 502 || st == 503 || st == 401 || st == 403 {
				w.set("off", unifi.ErrText(info))
			} else {
				w.set("error", unifi.ErrText(info))
			}
			sleep(ctx, 10*time.Minute)
			continue
		}
		if m, ok := info.(map[string]any); ok {
			v, _ := m["applicationVersion"].(string)
			w.mu.Lock()
			w.version = v
			w.mu.Unlock()
		}
		w.set("connecting", "")
		start := time.Now()
		err := w.stream(ctx)
		if ctx.Err() != nil {
			return
		}
		if unifi.IsWSNotFound(err) {
			w.set("off", "this Protect version has no event stream (update UniFi Protect to 5.3 or newer)")
			sleep(ctx, 30*time.Minute)
			continue
		}
		if time.Since(start) > 5*time.Minute {
			backoff = 5 * time.Second
		}
		msg := "the event stream stopped"
		if err != nil {
			msg = err.Error()
		}
		w.set("error", msg)
		sleep(ctx, backoff+time.Duration(rand.Int64N(int64(backoff/2+1))))
		backoff = min(backoff*2, 5*time.Minute)
	}
}

func (w *Watcher) stream(ctx context.Context) error {
	ws, err := w.UniFi.DialWS(ctx, base+"/subscribe/events")
	if err != nil {
		return err
	}
	defer ws.Close()
	w.set("live", "")
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	go func() { // ping every 2 minutes
		t := time.NewTicker(2 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if ws.Ping() != nil {
					return
				}
			}
		}
	}()
	for {
		msg, err := ws.Read(6*time.Minute, 1<<20)
		if err != nil {
			return err
		}
		w.Store.Apply(msg)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ------------------------------------------------------------------ cameras

// Camera is the part of Protect's camera object HomeOwl shows.
type Camera struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	State    string   `json:"state"` // CONNECTED, DISCONNECTED, …
	MAC      string   `json:"mac"`
	Model    string   `json:"model,omitempty"`
	Doorbell bool     `json:"doorbell"`
	Smart    []string `json:"smart"` // what it is set to detect
	Mic      bool     `json:"mic"`
	HDR      string   `json:"hdr,omitempty"`
	Mode     string   `json:"mode,omitempty"`
}

// Cameras lists the cameras (with Protect's version), or an error that says why there are none.
func Cameras(c *unifi.Client) ([]Camera, error) {
	st, d := c.Get(base + "/cameras")
	if st == 404 || st == 502 || st == 503 {
		return nil, ErrNoProtect
	}
	if st != 200 {
		return nil, errors.New("Protect: " + unifi.ErrText(d))
	}
	arr, _ := d.([]any)
	out := []Camera{}
	for _, x := range arr {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, cameraFrom(m))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// ErrNoProtect: the console doesn't run UniFi Protect (or its API isn't reachable with this key).
var ErrNoProtect = errors.New("UniFi Protect isn't running on this console")

func cameraFrom(m map[string]any) Camera {
	s := func(k string) string { v, _ := m[k].(string); return v }
	c := Camera{ID: s("id"), Name: s("name"), State: s("state"), MAC: strings.ToLower(s("mac")), Model: s("type"),
		HDR: s("hdrType"), Mode: s("videoMode"), Smart: []string{}}
	if c.Model == "" {
		c.Model = s("marketName")
	}
	if c.Name == "" {
		c.Name = c.Model
	}
	ff, _ := m["featureFlags"].(map[string]any)
	if v, ok := ff["isDoorbell"].(bool); ok {
		c.Doorbell = v
	} else if _, ok := m["lcdMessage"]; ok { // only doorbells have a screen
		c.Doorbell = true
	} else {
		c.Doorbell = strings.Contains(strings.ToLower(c.Model+" "+c.Name), "doorbell")
	}
	if v, ok := ff["hasMic"].(bool); ok {
		c.Mic = v
	}
	if v, ok := m["isMicEnabled"].(bool); ok {
		c.Mic = c.Mic && v
	}
	if sd, ok := m["smartDetectSettings"].(map[string]any); ok {
		for _, k := range []string{"objectTypes", "audioTypes"} {
			if l, ok := sd[k].([]any); ok {
				for _, x := range l {
					if v, ok := x.(string); ok {
						c.Smart = append(c.Smart, v)
					}
				}
			}
		}
	}
	return c
}

// Snapshot fetches a JPEG from a camera.
func Snapshot(ctx context.Context, c *unifi.Client, id string, hq bool) ([]byte, string, error) {
	if !validID(id) {
		return nil, "", errors.New("bad camera id")
	}
	q := "false"
	if hq {
		q = "true"
	}
	st, typ, b, err := c.Raw(ctx, base+"/cameras/"+id+"/snapshot?highQuality="+q, 8<<20)
	if err != nil {
		return nil, "", err
	}
	if st != 200 {
		return nil, "", errors.New("the camera didn't send a picture (HTTP " + strconv.Itoa(st) + ")")
	}
	if !strings.HasPrefix(typ, "image/") {
		typ = "image/jpeg"
	}
	return b, typ, nil
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
