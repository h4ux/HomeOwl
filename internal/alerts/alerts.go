// Package alerts keeps HomeOwl's alerts (a new device joined, the guest password changed…) and sends them on:
// always to the alert list in the app, and optionally as a macOS notification, to an ntfy topic (phone push)
// or to a webhook (Home Assistant, Slack, Discord…). Nothing secret is ever put in an alert.
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Alert is one thing worth telling someone about.
type Alert struct {
	ID    int64          `json:"id"`
	Time  int64          `json:"time"` // ms since 1970
	Kind  string         `json:"kind"` // new_device, guest_rotated, test…
	Level string         `json:"level"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Data  map[string]any `json:"data,omitempty"`
	Read  bool           `json:"read"`
}

// Channels says where alerts go besides the app. Ntfy and Webhook are URLs (kept in the secrets store by the caller).
type Channels struct {
	MacOS   bool
	Ntfy    string // e.g. https://ntfy.sh/<your secret topic>
	Webhook string
	Link    string // where "Open HomeOwl" should point, if anywhere
}

// Store keeps the newest alerts in a JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	list []Alert
	max  int
	// Send delivers an alert to the outside channels; set by the caller (tests leave it nil).
	Send func(Alert)
}

// Open loads the alerts saved in dir.
func Open(dir string) *Store {
	s := &Store{path: filepath.Join(dir, "alerts.json"), max: 500}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.list)
	}
	return s
}

// Add records an alert and sends it on.
func (s *Store) Add(a Alert) Alert {
	s.mu.Lock()
	if a.Time == 0 {
		a.Time = time.Now().UnixMilli()
	}
	if a.Level == "" {
		a.Level = "info"
	}
	var last int64
	if n := len(s.list); n > 0 {
		last = s.list[n-1].ID
	}
	a.ID = max(last+1, a.Time)
	s.list = append(s.list, a)
	if len(s.list) > s.max {
		s.list = s.list[len(s.list)-s.max:]
	}
	s.saveLocked()
	send := s.Send
	s.mu.Unlock()
	if send != nil {
		go send(a)
	}
	return a
}

// List returns the alerts, newest first.
func (s *Store) List() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Alert, len(s.list))
	for i, a := range s.list {
		out[len(s.list)-1-i] = a
	}
	return out
}

// Unread counts the alerts nobody has looked at.
func (s *Store) Unread() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.list {
		if !a.Read {
			n++
		}
	}
	return n
}

// MarkRead marks one alert (or all, id 0) as read.
func (s *Store) MarkRead(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if id == 0 || s.list[i].ID == id {
			s.list[i].Read = true
		}
	}
	s.saveLocked()
}

// Clear deletes every alert.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = nil
	s.saveLocked()
}

func (s *Store) saveLocked() {
	b, _ := json.MarshalIndent(s.list, "", " ")
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

// ---- delivery

var client = &http.Client{Timeout: 10 * time.Second}

// Deliver sends one alert to every configured channel and returns the problems, if any.
func Deliver(ctx context.Context, ch Channels, a Alert) []error {
	var errs []error
	if ch.MacOS && runtime.GOOS == "darwin" {
		if err := macNotify(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("macOS notification: %w", err))
		}
	}
	if ch.Ntfy != "" {
		if err := ntfy(ctx, ch, a); err != nil {
			errs = append(errs, fmt.Errorf("ntfy: %w", err))
		}
	}
	if ch.Webhook != "" {
		if err := webhook(ctx, ch, a); err != nil {
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}
	return errs
}

// macNotify shows a notification; the text goes in as arguments, never into the script itself.
func macNotify(ctx context.Context, a Alert) error {
	cmd := exec.CommandContext(ctx, "osascript", "-e", "on run argv", "-e",
		"display notification (item 2 of argv) with title \"HomeOwl\" subtitle (item 1 of argv)", "-e", "end run", a.Title, a.Body)
	return cmd.Run()
}

// CheckURL accepts http(s) URLs only.
func CheckURL(u string) error {
	if u == "" {
		return nil
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
		return errors.New("that isn't an http(s) address")
	}
	return nil
}

func ntfy(ctx context.Context, ch Channels, a Alert) error {
	if err := CheckURL(ch.Ntfy); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", ch.Ntfy, strings.NewReader(a.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", a.Title)
	req.Header.Set("Tags", map[string]string{"new_device": "new", "guest_rotated": "key", "test": "owl"}[a.Kind])
	if a.Level == "warn" {
		req.Header.Set("Priority", "high")
	}
	if ch.Link != "" {
		req.Header.Set("Click", ch.Link)
	}
	return do(req)
}

// webhook posts JSON with the common text fields too, so Slack ("text") and Discord ("content") show it as is.
func webhook(ctx context.Context, ch Channels, a Alert) error {
	if err := CheckURL(ch.Webhook); err != nil {
		return err
	}
	msg := a.Title
	if a.Body != "" {
		msg += " — " + a.Body
	}
	body, _ := json.Marshal(map[string]any{"text": msg, "content": msg, "title": a.Title, "body": a.Body, "kind": a.Kind,
		"level": a.Level, "time": a.Time, "data": a.Data, "source": "HomeOwl"})
	req, err := http.NewRequestWithContext(ctx, "POST", ch.Webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req)
}

func do(req *http.Request) error {
	req.Header.Set("User-Agent", "HomeOwl")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("the server answered %s", res.Status)
	}
	return nil
}
