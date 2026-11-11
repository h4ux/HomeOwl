package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/protect"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Protect may run on a different console than Network: a Cloud Gateway has no storage for video, so the cameras
// live on a UNVR, a Cloud Key or another UniFi OS console, with its own API key (kept in the keychain).
const protectKeySlot = "unifi-protect-key"

// protectClient is the console to ask about cameras.
func (a *App) protectClient() *unifi.Client {
	if a.Cfg.Get().Cameras.ProtectHost != "" {
		return a.ProtectUniFi
	}
	return a.UniFi
}

// connectProtectSaved reconnects a separate Protect console at start-up, retrying while it's unreachable.
func (a *App) connectProtectSaved(ctx context.Context) {
	for wait := 30 * time.Second; ctx.Err() == nil; wait = min(wait*2, 10*time.Minute) {
		c := a.Cfg.Get().Cameras
		key := a.Sec.Get(protectKeySlot)
		if c.ProtectHost == "" || key == "" || a.ProtectUniFi.Connected() {
			return
		}
		_, err := a.ProtectUniFi.ConnectApp(c.ProtectHost, key, c.ProtectTLS, protect.InfoPath)
		if err == nil {
			log.Printf("connected to UniFi Protect on %s", c.ProtectHost)
			a.Protect.Kick()
			return
		}
		log.Printf("Protect console %s: %v", c.ProtectHost, err)
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// protectConnect uses another console for Protect. An empty key keeps the saved one.
func (a *App) protectConnect(body map[string]any) (any, error) {
	host, err := unifi.NormaliseHost(s(body, "host"))
	if err != nil {
		return nil, err
	}
	key := s(body, "key")
	if key == "" {
		key = a.Sec.Get(protectKeySlot)
	}
	if key == "" {
		return nil, errors.New("paste an API key created on that console (UniFi OS › Settings › Control Plane › Integrations)")
	}
	verify := b(body, "verify_tls")
	test := unifi.New()
	if _, err := test.ConnectApp(host, key, verify, protect.InfoPath); err != nil {
		return nil, err
	}
	if err := a.Sec.Set(protectKeySlot, key); err != nil {
		return nil, err
	}
	if err := a.Cfg.Update(func(c *config.Config) { c.Cameras.ProtectHost, c.Cameras.ProtectTLS = host, verify }); err != nil {
		return nil, err
	}
	if _, err := a.ProtectUniFi.ConnectApp(host, key, verify, protect.InfoPath); err != nil {
		return nil, err
	}
	a.snaps.reset()
	a.Protect.Kick()
	return a.camerasView(1), nil
}

// protectDisconnect goes back to Protect on the Network console.
func (a *App) protectDisconnect() (any, error) {
	if err := a.Cfg.Update(func(c *config.Config) { c.Cameras.ProtectHost, c.Cameras.ProtectTLS = "", false }); err != nil {
		return nil, err
	}
	a.Sec.Delete(protectKeySlot)
	a.ProtectUniFi.Disconnect()
	a.snaps.reset()
	a.Protect.Kick()
	return a.camerasView(1), nil
}

// ------------------------------------------------------------------ finding the console that runs Protect

// protectHint matches names of UniFi consoles that can run Protect (NVRs, Cloud Keys, Dream Machines…).
var protectHint = regexp.MustCompile(`(?i)unvr|nvr|cloud ?key|uck|udm|dream|udr|udw|ucg|envr|protect`)

// discoverBase turns a client's address into the URL to ask, or false to skip it (only the home network).
// Tests point it at their own server.
var discoverBase = func(ip string) (string, bool) {
	p := net.ParseIP(ip)
	return "https://" + ip, p != nil && p.IsPrivate()
}

// discoverProtect looks for other UniFi OS consoles among the clients UniFi knows (Ubiquiti devices on the home
// network) and asks each one, without any key, whether Protect answers there. It never logs in or changes anything.
func (a *App) discoverProtect(ctx context.Context) []map[string]any {
	out := []map[string]any{}
	if !a.UniFi.Connected() {
		return out
	}
	b := a.Cache.Get(false, a.maxAge())
	main := hostOnly(a.Cfg.Get().Host)
	current := hostOnly(a.Cfg.Get().Cameras.ProtectHost)
	type cand struct{ ip, name, mac string }
	seen := map[string]bool{main: true}
	var cands []cand
	for _, src := range []string{"clients", "known_clients"} {
		for _, c := range section(b, "legacy", src) {
			ip, _ := c["ip"].(string)
			if ip == "" {
				ip, _ = c["last_ip"].(string)
			}
			oui, _ := c["oui"].(string)
			name, _ := c["name"].(string)
			host, _ := c["hostname"].(string)
			if name == "" {
				name = host
			}
			if _, ok := discoverBase(ip); !ok || seen[ip] {
				continue
			}
			if !strings.Contains(strings.ToLower(oui), "ubiquiti") && !protectHint.MatchString(name+" "+host) {
				continue
			}
			seen[ip] = true
			mac, _ := c["mac"].(string)
			cands = append(cands, cand{ip, name, strings.ToLower(mac)})
		}
	}
	if len(cands) > 24 {
		cands = cands[:24]
	}
	hc := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // consoles use self-signed certificates; nothing secret is sent
	get := func(ctx context.Context, url string) (int, []byte) {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return 0, nil
		}
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, body
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for _, c := range cands {
		wg.Add(1)
		go func(c cand) {
			defer wg.Done()
			// a UniFi OS console says what it is at /api/system (no login needed on current versions)
			base, _ := discoverBase(c.ip)
			st, body := get(ctx, base+"/api/system")
			var sys struct {
				Name     string `json:"name"`
				Hardware struct {
					Shortname string `json:"shortname"`
					Name      string `json:"name"`
				} `json:"hardware"`
			}
			isOS := st == 200 && json.Unmarshal(body, &sys) == nil && (sys.Hardware.Shortname != "" || sys.Name != "")
			// does Protect's API answer (it asks for a key)? A console without Protect sends its web page instead.
			pst, pbody := get(ctx, base+protect.InfoPath)
			var js any
			protectAPI := (pst == 401 || pst == 403 || pst == 200) && json.Unmarshal(pbody, &js) == nil
			if !isOS && !protectAPI {
				return
			}
			model := sys.Hardware.Name
			if model == "" {
				model = sys.Hardware.Shortname
			}
			name := c.name
			if sys.Name != "" {
				name = sys.Name
			}
			likely := protectAPI && (protectHint.MatchString(name+" "+model) || !isOS)
			mu.Lock()
			out = append(out, map[string]any{"ip": c.ip, "name": name, "model": model, "mac": c.mac, "protect": protectAPI, "likely": likely, "connected": c.ip == current})
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		li, lj := out[i]["likely"].(bool), out[j]["likely"].(bool)
		if li != lj {
			return li
		}
		return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"])
	})
	return out
}

func hostOnly(h string) string {
	h = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(h), "https://"), "http://")
	if i := strings.IndexAny(h, "/"); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}
