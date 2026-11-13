// Package cloudflare is the small part of the Cloudflare API HomeOwl uses for its "easy links": a proxied DNS
// name on the user's own domain (wifi.example.com) plus a redirect rule that sends visitors to HomeOwl's
// address on the home network. Only the token's own zones are touched, and only the names HomeOwl made.
package cloudflare

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// API is the Cloudflare API base (tests replace it).
var API = "https://api.cloudflare.com/client/v4"

// Phase is the ruleset phase that holds a zone's redirect rules ("Single Redirects").
const Phase = "http_request_dynamic_redirect"

type Client struct {
	Token string
	HTTP  *http.Client
}

func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	NameServers []string `json:"name_servers,omitempty"`
}

type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

type Rule struct {
	ID               string         `json:"id,omitempty"`
	Ref              string         `json:"ref,omitempty"`
	Description      string         `json:"description"`
	Expression       string         `json:"expression"`
	Action           string         `json:"action"`
	ActionParameters map[string]any `json:"action_parameters,omitempty"`
	Enabled          bool           `json:"enabled"`
}

type ruleset struct {
	ID    string `json:"id"`
	Rules []Rule `json:"rules"`
}

// Error is a failed API call; NotFound tells "nothing there yet" apart from real failures.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == 404
}

func (c *Client) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, API+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach Cloudflare (%v)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode >= 300 || !env.Success {
		msg := fmt.Sprintf("Cloudflare %d", resp.StatusCode)
		if len(env.Errors) > 0 {
			msg += ": " + env.Errors[0].Message
		}
		switch resp.StatusCode {
		case 401:
			msg = "Cloudflare refused the token — create a new one (see the steps) and paste it again"
		case 403:
			msg += " — the token is missing a permission: it needs Zone › Zone › Read, Zone › DNS › Edit and Zone › Single Redirect › Edit"
		}
		return &Error{resp.StatusCode, msg}
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// Verify checks that the token works.
func (c *Client) Verify() error {
	var r struct {
		Status string `json:"status"`
	}
	if err := c.do("GET", "/user/tokens/verify", nil, &r); err != nil {
		return err
	}
	if r.Status != "active" {
		return fmt.Errorf("this Cloudflare token is %s", r.Status)
	}
	return nil
}

// Zones lists the domains the token can see.
func (c *Client) Zones() ([]Zone, error) {
	var z []Zone
	err := c.do("GET", "/zones?per_page=50", nil, &z)
	return z, err
}

func (c *Client) Records(zone, name string) ([]Record, error) {
	var r []Record
	err := c.do("GET", "/zones/"+url.PathEscape(zone)+"/dns_records?name="+url.QueryEscape(name), nil, &r)
	return r, err
}

// CreateRedirectRecord adds a proxied name with no server behind it (100:: is Cloudflare's "originless"
// address): the redirect rule answers every visit.
func (c *Client) CreateRedirectRecord(zone, name string) (string, error) {
	var r Record
	err := c.do("POST", "/zones/"+url.PathEscape(zone)+"/dns_records", map[string]any{"type": "AAAA", "name": name,
		"content": "100::", "proxied": true, "ttl": 1, "comment": "HomeOwl: redirects to the home network"}, &r)
	return r.ID, err
}

// CreateRecord adds a DNS record (not proxied) and returns its ID: the TXT record that proves the domain to
// Let's Encrypt, or the optional public name for HomeOwl's address at home.
func (c *Client) CreateRecord(zone, typ, name, content, comment string) (string, error) {
	var r Record
	err := c.do("POST", "/zones/"+url.PathEscape(zone)+"/dns_records", map[string]any{"type": typ, "name": name,
		"content": content, "proxied": false, "ttl": 60, "comment": comment}, &r)
	return r.ID, err
}

// UpdateRecordContent changes what a record points to.
func (c *Client) UpdateRecordContent(zone, id, content string) error {
	return c.do("PATCH", "/zones/"+url.PathEscape(zone)+"/dns_records/"+url.PathEscape(id), map[string]any{"content": content}, nil)
}

func (c *Client) DeleteRecord(zone, id string) error {
	err := c.do("DELETE", "/zones/"+url.PathEscape(zone)+"/dns_records/"+url.PathEscape(id), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Redirects returns the zone's redirect ruleset ("" when it has none yet) and its rules.
func (c *Client) Redirects(zone string) (string, []Rule, error) {
	var rs ruleset
	err := c.do("GET", "/zones/"+url.PathEscape(zone)+"/rulesets/phases/"+Phase+"/entrypoint", nil, &rs)
	if IsNotFound(err) {
		return "", nil, nil
	}
	return rs.ID, rs.Rules, err
}

// AddRule adds a rule (creating the zone's redirect ruleset if it has none) and returns the ruleset ID.
func (c *Client) AddRule(zone, rsID string, r Rule) (string, error) {
	var rs ruleset
	if rsID == "" {
		err := c.do("PUT", "/zones/"+url.PathEscape(zone)+"/rulesets/phases/"+Phase+"/entrypoint", map[string]any{"rules": []Rule{r}}, &rs)
		return rs.ID, err
	}
	err := c.do("POST", "/zones/"+url.PathEscape(zone)+"/rulesets/"+url.PathEscape(rsID)+"/rules", r, &rs)
	return rsID, err
}

func (c *Client) UpdateRule(zone, rsID, id string, r Rule) error {
	r.ID = ""
	return c.do("PATCH", "/zones/"+url.PathEscape(zone)+"/rulesets/"+url.PathEscape(rsID)+"/rules/"+url.PathEscape(id), r, nil)
}

func (c *Client) DeleteRule(zone, rsID, id string) error {
	err := c.do("DELETE", "/zones/"+url.PathEscape(zone)+"/rulesets/"+url.PathEscape(rsID)+"/rules/"+url.PathEscape(id), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RedirectRule sends visits to host on to target, only from the given public addresses when there are any.
func RedirectRule(ref, host, target string, from []string) Rule {
	expr := fmt.Sprintf("(http.host eq %q", host)
	if len(from) > 0 {
		expr += " and ip.src in {" + strings.Join(from, " ") + "}"
	}
	expr += ")"
	return Rule{Ref: ref, Description: "HomeOwl: " + host + " → home network", Expression: expr, Action: "redirect", Enabled: true,
		ActionParameters: map[string]any{"from_value": map[string]any{"status_code": 302, "preserve_query_string": false,
			"target_url": map[string]any{"value": target}}}}
}

// HomeAddresses are this home's public addresses as Cloudflare sees them from this computer: the IPv4 address
// and the IPv6 network (/56, the usual size an internet provider gives a home, so every device and VLAN is in it).
func HomeAddresses(ctx context.Context) []string {
	var out []string
	for _, nw := range []string{"tcp4", "tcp6"} {
		var ip net.IP
		// by name first, then by Cloudflare's own address (works even when this computer's DNS is odd)
		for _, u := range traceURLs[nw] {
			if ip = trace(ctx, nw, u); ip != nil {
				break
			}
		}
		if s := PublicPrefix(ip); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var traceURLs = map[string][]string{
	"tcp4": {"https://cloudflare.com/cdn-cgi/trace", "https://1.1.1.1/cdn-cgi/trace", "https://1.0.0.1/cdn-cgi/trace"},
	"tcp6": {"https://cloudflare.com/cdn-cgi/trace", "https://[2606:4700:4700::1111]/cdn-cgi/trace"},
}

// PublicPrefix: an internet IPv4 address as is, an internet IPv6 address as its /56; "" for anything else
// (home, carrier-grade NAT, link-local addresses).
func PublicPrefix(ip net.IP) string {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10: the provider's NAT, not reachable from outside
			return ""
		}
		return v4.String()
	}
	m := net.CIDRMask(56, 128)
	return (&net.IPNet{IP: ip.Mask(m), Mask: m}).String()
}

func trace(ctx context.Context, nw, u string) net.IP {
	d := &net.Dialer{Timeout: 8 * time.Second}
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return d.DialContext(ctx, nw, addr) }}}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 8<<10))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			return net.ParseIP(strings.TrimSpace(v))
		}
	}
	return nil
}

// Check opens https://host/ the way a browser at home would and reports where Cloudflare sends it: "" when it
// redirects to target, else what went wrong.
func Check(ctx context.Context, host, target string) string {
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+host+"/", nil)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Sprintf("couldn't open it (%v) — Cloudflare may still be setting the name up; try again in a minute", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	switch {
	case (resp.StatusCode == 301 || resp.StatusCode == 302) && loc == target:
		return ""
	case resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 307 || resp.StatusCode == 308:
		return fmt.Sprintf("Cloudflare sends it to %s instead of %s — another redirect rule or page rule on this domain runs first", loc, target)
	case resp.StatusCode == 522 || resp.StatusCode == 521 || resp.StatusCode == 523:
		return fmt.Sprintf("Cloudflare answered %d: the redirect rule didn't apply to this visit. Check Rules › Redirect Rules on Cloudflare, or untick “Only from my home's internet address”", resp.StatusCode)
	}
	return fmt.Sprintf("Cloudflare answered %d instead of redirecting", resp.StatusCode)
}
