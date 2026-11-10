// Package unifi talks to a UniFi OS console: the official Integration API plus the classic and v2
// Network APIs, collects a configuration "bundle" for the UI, and applies allow-listed changes.
package unifi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Client holds the live connection. Safe for concurrent use.
type Client struct {
	mu         sync.RWMutex
	host       string
	key        string
	verifyTLS  bool
	site       string // what the user asked for (id, name or internal reference)
	legacySite string // classic API site name, learned while collecting
	http       *http.Client
	LastError  string
	// GuestSSIDs lists Wi-Fi ids an admin marked as guest networks (in addition to UniFi's own guest flag).
	GuestSSIDs func() []string
}

func New() *Client { return &Client{legacySite: "default"} }

func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.host != "" && c.key != ""
}

func (c *Client) Host() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.host
}

func (c *Client) LegacySite() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.legacySite
}

func (c *Client) setLegacySite(s string) {
	c.mu.Lock()
	c.legacySite = s
	c.mu.Unlock()
}

// SitePath builds a classic (/api/s/<site>/…) or v2 (/v2/api/site/<site>/…) Network path.
func (c *Client) SitePath(path string, v2 bool) string {
	if v2 {
		return "/proxy/network/v2/api/site/" + c.LegacySite() + "/" + strings.TrimPrefix(path, "/")
	}
	return "/proxy/network/api/s/" + c.LegacySite() + "/" + strings.TrimPrefix(path, "/")
}

func NormaliseHost(h string) (string, error) {
	h = strings.TrimRight(strings.TrimSpace(h), "/")
	if h == "" {
		return "", errors.New("enter the console IP or hostname")
	}
	if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
		h = "https://" + h
	}
	return h, nil
}

func (c *Client) configure(host, key string, verify bool, site string) {
	tr := &http.Transport{
		Proxy:               nil,                                      // always talk to the console directly, never through a system proxy
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: !verify}, //nolint:gosec // UniFi consoles use self-signed certificates
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 4,
	}
	c.mu.Lock()
	c.host, c.key, c.verifyTLS, c.site = host, key, verify, site
	c.http = &http.Client{Transport: tr, Timeout: 30 * time.Second}
	c.mu.Unlock()
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	c.host, c.key = "", ""
	c.mu.Unlock()
}

// Connect checks the key against /integration/v1/info and keeps the connection on success.
func (c *Client) Connect(host, key string, verify bool, site string) (any, error) {
	c.configure(host, key, verify, site)
	st, info := c.Get("/proxy/network/integration/v1/info")
	if st == 200 {
		c.LastError = ""
		return info, nil
	}
	c.mu.Lock()
	c.key = ""
	c.mu.Unlock()
	c.LastError = ConnectHint(st, info)
	return nil, errors.New(c.LastError)
}

// Request performs one API call and returns the HTTP status (0 = network error) and decoded JSON.
func (c *Client) Request(method, path string, body any) (int, any) {
	return c.RequestCtx(context.Background(), method, path, body)
}

func (c *Client) RequestCtx(ctx context.Context, method, path string, body any) (int, any) {
	c.mu.RLock()
	host, key, hc := c.host, c.key, c.http
	c.mu.RUnlock()
	if host == "" || key == "" || hc == nil {
		return 0, map[string]any{"error": "not connected"}
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, host+path, rd)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	req.Header.Set("X-API-KEY", key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", config.AppSlug+"/"+config.Version)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	var out any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			out = map[string]any{"text": truncate(string(raw), 2000)}
		}
	} else {
		out = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		msg := ""
		if m, ok := out.(map[string]any); ok {
			if meta, ok := m["meta"].(map[string]any); ok {
				msg, _ = meta["msg"].(string)
			}
			if msg == "" {
				msg, _ = m["message"].(string)
			}
			if msg == "" {
				msg, _ = m["error"].(string)
			}
		}
		e := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if msg != "" {
			e += ": " + msg
		}
		return resp.StatusCode, map[string]any{"error": e, "body": truncate(string(raw), 1500)}
	}
	return resp.StatusCode, out
}

func (c *Client) Get(path string) (int, any) { return c.Request("GET", path, nil) }

// Data unwraps the classic API's {"meta":…, "data":[…]} envelope.
func Data(d any) any {
	if m, ok := d.(map[string]any); ok {
		if v, ok := m["data"]; ok {
			return v
		}
	}
	return d
}

// List returns Data(d) as a slice of objects.
func List(d any) []map[string]any {
	arr, _ := Data(d).([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func ErrText(d any) string {
	if m, ok := d.(map[string]any); ok {
		if e, ok := m["error"].(string); ok {
			return e
		}
	}
	return fmt.Sprint(d)
}

func (c *Client) pagedIntegration(path string) (int, any) {
	var items []any
	offset, limit := 0, 200
	var last any
	st := 0
	for {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		st, last = c.Get(fmt.Sprintf("%s%soffset=%d&limit=%d", path, sep, offset, limit))
		m, ok := last.(map[string]any)
		if st != 200 || !ok {
			break
		}
		data, ok := m["data"].([]any)
		if !ok {
			break
		}
		items = append(items, data...)
		total := len(items)
		if t, ok := m["totalCount"].(float64); ok {
			total = int(t)
		}
		offset += limit
		if offset >= total || len(data) == 0 || len(items) >= 5000 {
			break
		}
	}
	if st == 200 {
		if items == nil {
			items = []any{}
		}
		return 200, items
	}
	return st, last
}

func ConnectHint(status int, info any) string {
	switch status {
	case 401:
		return "The console rejected the API key (401). Check the key, or create a new one in UniFi Network › Settings › Control Plane › Integrations."
	case 403:
		return "The key was accepted but lacks permission (403)."
	case 404:
		return "Reached the host but found no UniFi Network integration API (404). Is this a UniFi OS console (UDM/UCG/UDR/UX/CK-G2) running Network 9 or newer?"
	case 0:
		e := ErrText(info)
		low := strings.ToLower(e)
		hint := "Could not reach the console."
		switch {
		case strings.Contains(low, "no route to host") || strings.Contains(low, "operation not permitted") || strings.Contains(low, "permission denied"):
			hint = "This computer blocked HomeOwl from the local network. On macOS: System Settings › Privacy & Security › Local Network › turn on HomeOwl, then restart it."
		case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
			hint = "Timed out reaching the console. Check the IP, and that no VPN is routing your home network elsewhere."
		case strings.Contains(low, "tls") || strings.Contains(low, "certificate") || strings.Contains(low, "x509"):
			hint = "The TLS handshake with the console failed. Leave “Verify TLS certificate” off for self-signed consoles."
		case strings.Contains(low, "refused"):
			hint = "Connection refused. UniFi OS consoles listen on 443 — remove any :port you added."
		case strings.Contains(low, "no such host") || strings.Contains(low, "lookup"):
			hint = "Could not resolve that hostname — use the IP address instead."
		}
		return hint + "  [" + e + "]"
	}
	return fmt.Sprintf("Unexpected response (%d).", status)
}

var secretKeyRE = regexp.MustCompile(`(?i)(pass(word|phrase)?|secret|psk|private|token|x_.*key|auth_key|shared_key|wep_key|preshared|x_iapp)`)

// RedactKey, when set, adds a short keyed fingerprint to redacted strings, so a changed password can be noticed
// (e.g. in the configuration history) without keeping or revealing it. The key never leaves this computer.
var RedactKey []byte

func redacted(s string) map[string]any {
	m := map[string]any{"__redacted__": true, "length": len(s)}
	if len(RedactKey) > 0 {
		h := hmac.New(sha256.New, RedactKey)
		h.Write([]byte(s))
		m["fp"] = hex.EncodeToString(h.Sum(nil)[:4])
	}
	return m
}

// Redact replaces passwords, PSKs and keys with {"__redacted__":true,"length":n} before data leaves the server.
func Redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if secretKeyRE.MatchString(k) {
				switch s := x.(type) {
				case string:
					if s != "" {
						out[k] = redacted(s)
						continue
					}
				case []any:
					if len(s) > 0 {
						out[k] = map[string]any{"__redacted__": true, "length": len(s)}
						continue
					}
				}
			}
			out[k] = Redact(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Redact(x)
		}
		return out
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
