package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// Internet provider logos. HomeOwl ships no vendor artwork: an admin gives the provider's website and HomeOwl
// fetches that site's own icon once, keeps it in <data>/isp-logos/, and serves it to the UI from its own origin.

type ispLogo struct {
	Site    string `json:"site"`
	File    string `json:"file"`
	Type    string `json:"type"`
	Version string `json:"version"`
}

type ispLogos struct {
	mu  sync.Mutex
	dir string
}

var siteHostRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
var iconLinkRE = regexp.MustCompile(`(?is)<link\b[^>]*>`)
var attrRE = regexp.MustCompile(`(?is)\b(rel|href)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/x-icon": true, "image/vnd.microsoft.icon": true}

func ispKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func (l *ispLogos) index() map[string]ispLogo {
	m := map[string]ispLogo{}
	b, err := os.ReadFile(filepath.Join(l.dir, "index.json"))
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (l *ispLogos) save(m map[string]ispLogo) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return config.WriteFileAtomic(filepath.Join(l.dir, "index.json"), b, 0o600)
}

// Versions maps provider name → a version string that changes when the logo does (for cache-busting).
func (l *ispLogos) Versions() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for k, v := range l.index() {
		out[k] = v.Version
	}
	return out
}

func (l *ispLogos) Get(name string) ([]byte, string, bool) {
	l.mu.Lock()
	e, ok := l.index()[ispKey(name)]
	l.mu.Unlock()
	if !ok || e.File == "" || strings.ContainsAny(e.File, `/\`) {
		return nil, "", false
	}
	b, err := os.ReadFile(filepath.Join(l.dir, e.File))
	return b, e.Type, err == nil
}

func (l *ispLogos) Remove(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.index()
	if e, ok := m[ispKey(name)]; ok {
		_ = os.Remove(filepath.Join(l.dir, e.File))
		delete(m, ispKey(name))
	}
	return l.save(m)
}

// Set fetches the icon of the provider's website and stores it for that provider name.
func (l *ispLogos) Set(ctx context.Context, name, site string) error {
	key := ispKey(name)
	if key == "" || len(key) > 200 {
		return errors.New("missing provider name")
	}
	host, err := siteHost(site)
	if err != nil {
		return err
	}
	img, typ, err := fetchSiteIcon(ctx, host)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(img)
	ver := hex.EncodeToString(sum[:6])
	nameSum := sha256.Sum256([]byte(key))
	file := hex.EncodeToString(nameSum[:8]) + ".img"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := config.WriteFileAtomic(filepath.Join(l.dir, file), img, 0o600); err != nil {
		return err
	}
	m := l.index()
	m[key] = ispLogo{Site: host, File: file, Type: typ, Version: ver}
	return l.save(m)
}

// siteHost turns "https://www.example.net/path" or "example.net" into a plain public host name.
func siteHost(site string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(site))
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("enter the provider's website, e.g. example.net")
	}
	h := strings.TrimSuffix(u.Hostname(), ".")
	if net.ParseIP(h) != nil || !siteHostRE.MatchString(h) {
		return "", errors.New("enter a website name (like example.net), not an IP address")
	}
	return h, nil
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() || isCGNAT(ip))
}

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64
}

// logoClient only talks to public addresses, so the admin-supplied website can't be used to reach the LAN.
func logoClient() *http.Client {
	d := &net.Dialer{Timeout: 6 * time.Second}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if publicIP(ip.IP) {
					return d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				}
			}
			return nil, fmt.Errorf("%s is not a public internet address", host)
		},
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" && r.URL.Scheme != "http" {
			return errors.New("unsupported redirect")
		}
		return nil
	}}
}

func get(ctx context.Context, c *http.Client, u string, max int64) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "HomeOwl/"+config.Version+" (provider icon)")
	res, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(b)) > max {
		return nil, nil, errors.New("too large")
	}
	return b, res.Request.URL, nil
}

// iconCandidates lists icon URLs from the page's <link rel=...icon...> tags, largest kinds first.
func iconCandidates(page []byte, base *url.URL) []string {
	var touch, icons []string
	for _, tag := range iconLinkRE.FindAll(page, 40) {
		var rel, href string
		for _, m := range attrRE.FindAllSubmatch(tag, -1) {
			v := string(m[3]) + string(m[4]) + string(m[5])
			if strings.EqualFold(string(m[1]), "rel") {
				rel = strings.ToLower(v)
			} else {
				href = strings.TrimSpace(v)
			}
		}
		if href == "" || strings.HasPrefix(strings.ToLower(href), "data:") || !strings.Contains(rel, "icon") || strings.Contains(rel, "mask") {
			continue
		}
		u, err := base.Parse(href)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
			continue
		}
		if strings.Contains(rel, "apple-touch-icon") {
			touch = append(touch, u.String())
		} else {
			icons = append(icons, u.String())
		}
	}
	return append(touch, icons...)
}

func fetchSiteIcon(ctx context.Context, host string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := logoClient()
	base := &url.URL{Scheme: "https", Host: host, Path: "/"}
	var cands []string
	if page, final, err := get(ctx, c, base.String(), 1<<20); err == nil {
		cands = iconCandidates(page, final)
		base = final
	}
	cands = append(cands, base.ResolveReference(&url.URL{Path: "/favicon.ico"}).String())
	var lastErr error = errors.New("no icon found")
	for _, u := range cands {
		b, _, err := get(ctx, c, u, 512<<10)
		if err != nil {
			lastErr = err
			continue
		}
		t := http.DetectContentType(b)
		if logoTypes[t] {
			return b, t, nil
		}
		lastErr = fmt.Errorf("not a supported image (%s)", t)
	}
	return nil, "", fmt.Errorf("couldn't get an icon from %s: %v", host, lastErr)
}
