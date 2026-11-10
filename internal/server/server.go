// Package server is HomeOwl's web server: login, the JSON API used by the browser UI, the MCP endpoint,
// and the listeners (home network with HTTPS, and optionally HomeOwl's own Tailscale device).
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/auth"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mcp"
	"github.com/h4ux/homeowl/internal/secrets"
	"github.com/h4ux/homeowl/internal/tsnetx"
	"github.com/h4ux/homeowl/internal/unifi"
	"github.com/h4ux/homeowl/internal/update"
	"github.com/h4ux/homeowl/web"
)

const cookieName = "homeowl_session"
const unifiKeySlot = "unifi-api-key"

type App struct {
	DataDir  string
	Cfg      *config.Store
	Sec      *secrets.Store
	Users    *auth.Store
	UniFi    *unifi.Client
	Cache    *unifi.Cache
	Changes  *unifi.Changes
	An       *analytics.Engine
	TS       *tsnetx.Node
	MCP      *mcp.Server
	Updates  *update.Checker
	ReadOnly bool
	// Restart is set by the command line: it restarts HomeOwl (after an update replaced the program file).
	Restart func()

	mu       sync.Mutex
	lan      *http.Server
	lanAddr  string
	lanErr   string
	cert     tls.Certificate
	handler  http.Handler
	started  time.Time
	tsErr    string
	logos    *ispLogos
	checkins *checkins
}

func New(dataDir string, readOnly bool) (*App, error) {
	cfg, err := config.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	users, err := auth.Open(dataDir)
	if err != nil {
		return nil, err
	}
	a := &App{DataDir: dataDir, Cfg: cfg, Sec: secrets.New(dataDir), Users: users, UniFi: unifi.New(), Changes: unifi.NewChanges(dataDir),
		TS: tsnetx.New(), ReadOnly: readOnly, started: time.Now(), logos: &ispLogos{dir: filepath.Join(dataDir, "isp-logos")},
		checkins: &checkins{path: filepath.Join(dataDir, "checkins.json")}}
	a.Changes.ReadOnly = readOnly
	a.Updates = &update.Checker{Current: config.Version}
	a.UniFi.GuestSSIDs = func() []string { return a.Cfg.Get().Guest.SSIDs }
	a.Cache = unifi.NewCache(a.UniFi)
	a.An = analytics.New(cfg, dataDir, a.UniFi, a.Sec)
	a.MCP = &mcp.Server{Name: "homeowl", Version: config.Version, Tools: a.mcpTools,
		Instructions: "HomeOwl manages a home network built on UniFi (with NextDNS and Tailscale). Use the read tools to look up devices, " +
			"clients, Wi-Fi, firewall, events and DNS/usage analytics. Changes (when allowed) always take a dry run first: call the tool " +
			"with dry_run=true, show the user the preview, and only then call it again with dry_run=false. Every change is logged and most can be undone."}
	a.handler = a.routes()
	return a, nil
}

// Run starts everything and blocks until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	if a.Users.NeedsSetup() {
		log.Printf("No users yet. Open HomeOwl on this computer to create the admin account, or use setup code %s from another device.", a.Users.SetupCode)
	}
	a.autoConnect()
	a.An.Start()
	go a.backgroundRefresh(ctx)
	go a.updateLoop(ctx)
	go a.watchExecutable(ctx)
	if err := a.StartLAN(); err != nil {
		return err
	}
	a.ApplyTailscale()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.mu.Lock()
	if a.lan != nil {
		_ = a.lan.Shutdown(sctx)
	}
	a.mu.Unlock()
	a.TS.Stop()
	return nil
}

// StartLAN (re)binds the home-network listener according to the settings.
func (a *App) StartLAN() error {
	c := a.Cfg.Get().LAN
	host := "127.0.0.1"
	if c.Enabled {
		host = ""
	}
	addr := net.JoinHostPort(host, strconv.Itoa(c.Port))
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lan != nil && a.lanAddr == addr {
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		a.lanErr = err.Error()
		return fmt.Errorf("can't listen on %s: %w (is HomeOwl already running? change the port with --port)", addr, err)
	}
	var tcfg *tls.Config
	if c.TLS {
		if a.cert.Certificate == nil {
			cert, err := loadOrCreateCert(a.DataDir)
			if err != nil {
				log.Printf("HTTPS certificate: %v (serving plain HTTP only)", err)
			} else {
				a.cert = cert
			}
		}
		if a.cert.Certificate != nil {
			cert := a.cert
			tcfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		}
	}
	if a.lan != nil {
		old := a.lan
		go func() { _ = old.Close() }()
	}
	srv := &http.Server{Handler: a.handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(io.Discard, "", 0)}
	a.lan, a.lanAddr, a.lanErr = srv, addr, ""
	go func() {
		if err := srv.Serve(newSniffListener(ln, tcfg)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("listener %s: %v", addr, err)
		}
	}()
	log.Printf("HomeOwl %s listening on %s (open http://127.0.0.1:%d on this computer)", config.Version, addr, c.Port)
	return nil
}

// ApplyTailscale starts or stops HomeOwl's own tailnet device.
func (a *App) ApplyTailscale() {
	c := a.Cfg.Get().Tailscale
	if !c.Enabled {
		a.TS.Stop()
		return
	}
	go func() {
		key := a.Sec.Get("tailscale-auth-key")
		if err := a.TS.Start(c.Hostname, filepath.Join(a.DataDir, "tailscale"), key, a.handler); err != nil {
			a.mu.Lock()
			a.tsErr = err.Error()
			a.mu.Unlock()
			log.Printf("tailscale: %v", err)
		}
	}()
}

func (a *App) autoConnect() {
	c := a.Cfg.Get()
	if !c.AutoConnect || c.Host == "" {
		return
	}
	key := a.Sec.Get(unifiKeySlot)
	if key == "" {
		a.UniFi.LastError = "Auto-connect is on but no saved UniFi API key was found."
		return
	}
	if _, err := a.UniFi.Connect(c.Host, key, c.VerifyTLS, c.Site); err != nil {
		log.Printf("auto-connect to %s: %v", c.Host, err)
		return
	}
	log.Printf("connected to %s", c.Host)
	go a.Cache.Get(true, 0)
}

func (a *App) maxAge() time.Duration {
	return time.Duration(max(1, a.Cfg.Get().RefreshMinutes)) * time.Minute
}

func (a *App) backgroundRefresh(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if age, ok := a.Cache.Age(); a.UniFi.Connected() && (!ok || age > a.maxAge()) {
				a.Cache.Get(true, 0)
			}
		}
	}
}

// ------------------------------------------------------------------ identity

// Who is the signed-in user of a request.
type Who struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Via      string `json:"via"`    // browser | tailscale | mcp
	Remote   bool   `json:"remote"` // not on this computer
	csrf     string
}

func (w *Who) Admin() bool { return w != nil && w.Role == auth.RoleAdmin }

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func isLoopback(r *http.Request) bool {
	if tsnetx.FromTailnet(r) {
		return false
	}
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

// hostAllowed blocks DNS-rebinding: the Host header must be one of this computer's names/addresses.
func (a *App) hostAllowed(r *http.Request) bool {
	if tsnetx.FromTailnet(r) {
		return true
	}
	h := strings.ToLower(r.Host)
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || isLocalIP(ip) || strings.HasPrefix(h, "100.")
	}
	if ex := strings.ToLower(a.Cfg.Get().LAN.ExtraHost); ex != "" && h == ex {
		return true
	}
	names, _ := localNames()
	for _, n := range names {
		if h == n {
			return true
		}
	}
	return strings.HasSuffix(h, ".ts.net") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".lan") || strings.HasSuffix(h, ".home.arpa")
}

func isLocalIP(ip net.IP) bool {
	_, ips := localNames()
	return hasIP(ips, ip)
}

func (a *App) who(r *http.Request) *Who {
	if c, err := r.Cookie(cookieName); err == nil {
		if u, csrf, ok := a.Users.Check(c.Value); ok {
			return &Who{Username: u.Username, Name: u.Name, Role: u.Role, Via: "browser", Remote: !isLoopback(r), csrf: csrf}
		}
	}
	return nil
}

// tailnetLogin signs a tailnet user in without a password when their login is linked to a HomeOwl user.
func (a *App) tailnetLogin(w http.ResponseWriter, r *http.Request) *Who {
	if !tsnetx.FromTailnet(r) || !a.Cfg.Get().Tailscale.AutoLogin {
		return nil
	}
	id, err := a.TS.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil || id.Tagged || id.Login == "" {
		return nil
	}
	u, ok := a.Users.ByTailscale(id.Login)
	if !ok {
		return nil
	}
	token, csrf := a.Users.SessionFor(u.Username, "tailscale")
	setCookie(w, r, token)
	return &Who{Username: u.Username, Name: u.Name, Role: u.Role, Via: "tailscale", Remote: true, csrf: csrf}
}

func setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil,
		SameSite: http.SameSiteStrictMode, MaxAge: int(auth.SessionTTL.Seconds())})
}

func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// ------------------------------------------------------------------ plumbing

func send(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		code, b = 500, []byte(`{"error":"could not encode response"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func fail(w http.ResponseWriter, code int, msg string) { send(w, code, map[string]any{"error": msg}) }

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.TLS != nil {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", a.MCP.HTTPHandler(a.mcpAuth))
	mux.HandleFunc("/api/", a.api)
	static, _ := fs.Sub(web.Static, "static")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fail(w, 405, "not allowed")
			return
		}
		if tsnetx.FromTailnet(r) && a.who(r) == nil {
			a.tailnetLogin(w, r)
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if p == "hi" {
			p = "hi.html"
		}
		b, err := fs.ReadFile(static, p)
		if err != nil {
			fail(w, 404, "not found")
			return
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		if p == "manifest.webmanifest" {
			ct = "application/manifest+json"
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(b)
	})
	return a.guard(securityHeaders(mux))
}

// The device check-in page also works over plain HTTP, so a phone can open it without a certificate warning.
// It only shows the device itself and the family's first names, and only queues a request for an admin.
var plainHTTPOK = map[string]bool{"/hi": true, "/hi.js": true, "/style.css": true, "/icon.png": true, "/api/hi": true}

// guard applies the Host check and sends plain-HTTP visitors from other devices to HTTPS.
func (a *App) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.hostAllowed(r) {
			http.Error(w, "Unexpected host name. Open HomeOwl by this computer's IP address or name.", http.StatusMisdirectedRequest)
			return
		}
		if r.TLS == nil && !isLoopback(r) && !tsnetx.FromTailnet(r) && a.cert.Certificate != nil && a.Cfg.Get().LAN.TLS && !plainHTTPOK[r.URL.Path] {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusFound)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// sameOrigin rejects cross-site POSTs (in addition to SameSite cookies and the per-session token).
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func readJSON(r *http.Request) map[string]any {
	body := map[string]any{}
	b, err := io.ReadAll(io.LimitReader(r.Body, 512<<10))
	if err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	return body
}

func s(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return strings.TrimSpace(v)
}

func b(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func n(m map[string]any, k string) (int, bool) {
	switch v := m[k].(type) {
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(v)
		return i, err == nil
	}
	return 0, false
}
