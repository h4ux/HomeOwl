// Package access reads UniFi Access (doors, locks, entry log) through its developer API on port 12445,
// which uses its own API token (UniFi Access › Settings › General › Advanced › API Token).
// The only write is a remote unlock, which the server runs through the logged, guarded change path.
package access

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

const DefaultPort = 12445

type Client struct {
	mu    sync.RWMutex
	base  string // https://host:12445
	token string
	http  *http.Client
}

// Configure sets the address and token ("" token disconnects).
func (c *Client) Configure(base, token string, verify bool) {
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: !verify}, //nolint:gosec // self-signed consoles
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
	c.mu.Lock()
	c.base, c.token, c.http = strings.TrimRight(base, "/"), token, &http.Client{Transport: tr, Timeout: 20 * time.Second}
	c.mu.Unlock()
}

func (c *Client) Configured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base != "" && c.token != ""
}

func (c *Client) Base() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base
}

// BaseFor turns the UniFi console address into the Access API address (same host, port 12445).
func BaseFor(consoleHost string, port int) (string, error) {
	u, err := url.Parse(consoleHost)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("connect HomeOwl to the UniFi console first")
	}
	if port <= 0 {
		port = DefaultPort
	}
	return "https://" + net.JoinHostPort(u.Hostname(), fmt.Sprint(port)), nil
}

// call returns HTTP status and the "data" of Access's {"code":"SUCCESS","data":…} envelope.
func (c *Client) call(ctx context.Context, method, path string, body any) (int, any) {
	c.mu.RLock()
	base, token, hc := c.base, c.token, c.http
	c.mu.RUnlock()
	if base == "" || token == "" {
		return 0, map[string]any{"error": "UniFi Access isn't connected"}
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", config.AppSlug+"/"+config.Version)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, map[string]any{"error": "can't reach UniFi Access: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var env struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data any    `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return resp.StatusCode, map[string]any{"error": "UniFi Access rejected the API token — create a new one in Access › Settings › General › Advanced"}
	}
	if resp.StatusCode >= 400 || (env.Code != "" && env.Code != "SUCCESS") {
		msg := env.Msg
		if msg == "" {
			msg = env.Code
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		st := resp.StatusCode
		if st < 400 {
			st = 400
		}
		return st, map[string]any{"error": "UniFi Access: " + msg}
	}
	return resp.StatusCode, env.Data
}

func errText(d any) string {
	if m, ok := d.(map[string]any); ok {
		if e, ok := m["error"].(string); ok {
			return e
		}
	}
	return "unexpected answer from UniFi Access"
}

// Door is what the Doors page shows.
type Door struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name,omitempty"`
	Lock     string `json:"lock"`     // locked | unlocked | unknown
	Position string `json:"position"` // open | closed | unknown (no door sensor)
	Hub      bool   `json:"hub"`      // has a hub (can be unlocked remotely)
}

func (c *Client) Doors(ctx context.Context) ([]Door, error) {
	st, d := c.call(ctx, http.MethodGet, "/api/v1/developer/doors", nil)
	if st != 200 {
		return nil, errors.New(errText(d))
	}
	arr, _ := d.([]any)
	out := []Door{}
	for _, x := range arr {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		s := func(k string) string { v, _ := m[k].(string); return v }
		dr := Door{ID: s("id"), Name: s("name"), FullName: s("full_name"), Lock: "unknown", Position: "unknown"}
		dr.Hub, _ = m["is_bind_hub"].(bool)
		switch s("door_lock_relay_status") {
		case "lock":
			dr.Lock = "locked"
		case "unlock":
			dr.Lock = "unlocked"
		}
		switch s("door_position_status") {
		case "open":
			dr.Position = "open"
		case "close":
			dr.Position = "closed"
		}
		if dr.Name == "" {
			dr.Name = dr.FullName
		}
		out = append(out, dr)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Entry is one line of the door log.
type Entry struct {
	Time    int64  `json:"time"` // ms
	Door    string `json:"door"`
	DoorID  string `json:"door_id"`
	Who     string `json:"who"`
	WhoType string `json:"who_type,omitempty"`
	Result  string `json:"result"` // ACCESS | BLOCKED | …
	Method  string `json:"method,omitempty"`
	Message string `json:"message,omitempty"`
}

// Log fetches door openings (granted and refused) since the given time, newest first (at most 200).
func (c *Client) Log(ctx context.Context, since time.Time) ([]Entry, error) {
	body := map[string]any{"topic": "door_openings", "since": since.Unix(), "until": time.Now().Unix()}
	st, d := c.call(ctx, http.MethodPost, "/api/v1/developer/system/logs?page_num=1&page_size=200", body)
	if st != 200 {
		return nil, errors.New(errText(d))
	}
	return parseLog(d), nil
}

func parseLog(d any) []Entry {
	m, _ := d.(map[string]any)
	hits, _ := m["hits"].([]any)
	out := []Entry{}
	for _, h := range hits {
		hm, _ := h.(map[string]any)
		src, _ := hm["_source"].(map[string]any)
		if src == nil {
			continue
		}
		obj := func(k string) map[string]any { v, _ := src[k].(map[string]any); return v }
		str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
		actor, ev, auth := obj("actor"), obj("event"), obj("authentication")
		e := Entry{Who: str(actor, "display_name"), WhoType: str(actor, "type"), Result: str(ev, "result"),
			Method: str(auth, "credential_provider"), Message: str(ev, "display_message")}
		if p, ok := ev["published"].(float64); ok {
			e.Time = int64(p)
		} else if ts, err := time.Parse(time.RFC3339, str(hm, "@timestamp")); err == nil {
			e.Time = ts.UnixMilli()
		}
		if tl, ok := src["target"].([]any); ok {
			for _, t := range tl {
				tm, _ := t.(map[string]any)
				if str(tm, "type") == "door" {
					e.Door, e.DoorID = str(tm, "display_name"), str(tm, "id")
				}
			}
		}
		if e.Who == "N/A" {
			e.Who = ""
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out
}

// UnlockPath is the API path of a remote unlock (for the change log).
func UnlockPath(id string) string { return "/api/v1/developer/doors/" + url.PathEscape(id) + "/unlock" }

// Unlock opens a door once (it locks again by itself after the door's unlock time).
func (c *Client) Unlock(ctx context.Context, id, actor string) (int, any) {
	if id == "" || strings.ContainsAny(id, "/?#") {
		return 400, map[string]any{"error": "bad door id"}
	}
	return c.call(ctx, http.MethodPut, UnlockPath(id), map[string]any{"actor_id": "homeowl", "actor_name": "HomeOwl (" + actor + ")"})
}

// ErrText exposes the error text of a failed call.
func ErrText(d any) string { return errText(d) }
