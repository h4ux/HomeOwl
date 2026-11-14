package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/tsnetx"
)

// "Sign in with Tailscale" from another address (the home-network IP, a local name, the trusted domain name):
// the sign-in page links to HomeOwl's tailnet address, which knows who you are (WhoIs). It hands back a
// single-use code that lives 30 seconds and only ever goes to one of HomeOwl's own addresses; redeeming it
// there starts the session. The code is never shown to anyone else and no password or session travels.

const tsCodeTTL = 30 * time.Second

type tsCode struct {
	user string
	host string // the address the code may be redeemed on
	exp  time.Time
}

type tsHandoff struct {
	mu    sync.Mutex
	codes map[string]tsCode
}

func (h *tsHandoff) issue(user, host string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	code := hex.EncodeToString(b)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.codes == nil {
		h.codes = map[string]tsCode{}
	}
	now := time.Now()
	for k, c := range h.codes {
		if now.After(c.exp) {
			delete(h.codes, k)
		}
	}
	h.codes[code] = tsCode{user: user, host: strings.ToLower(host), exp: now.Add(tsCodeTTL)}
	return code
}

// take uses up a code: it works once, before it expires, on the address it was made for.
func (h *tsHandoff) take(code, host string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.codes[code]
	delete(h.codes, code)
	if !ok || time.Now().After(c.exp) || c.host != strings.ToLower(host) {
		return "", false
	}
	return c.user, true
}

// ownAddress: the back address is one of HomeOwl's own — this computer's names and IPs, the trusted
// name or the extra host. Not "any .lan or private IP": a code sent there could be redeemed by someone else.
func (a *App) ownAddress(back string) (*url.URL, bool) {
	u, err := url.Parse(back)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" {
		return nil, false
	}
	h := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	c := a.Cfg.Get()
	if ip := net.ParseIP(h); ip != nil {
		return u, ip.IsLoopback() || isLocalIP(ip)
	}
	if n := certName(c); n != "" && h == n {
		return u, true
	}
	if ex := strings.ToLower(c.LAN.ExtraHost); ex != "" && h == ex {
		return u, true
	}
	names, _ := localNames()
	for _, n := range names {
		if h == n {
			return u, true
		}
	}
	return nil, false
}

// tsHandoffURL: where to send a tailnet user who asked to sign in on back.
func (a *App) tsHandoffURL(user, back string) (string, error) {
	u, ok := a.ownAddress(back)
	if !ok {
		return "", errors.New("that address isn't one of HomeOwl's own")
	}
	code := a.tsCodes.issue(user, u.Host)
	return u.Scheme + "://" + u.Host + "/api/auth/ts-back?code=" + url.QueryEscape(code), nil
}

// tsGo runs on the tailnet address: who is this, and back to where they came from with a code.
func (a *App) tsGo(w http.ResponseWriter, r *http.Request) {
	if !tsnetx.FromTailnet(r) || !a.Cfg.Get().Tailscale.AutoLogin {
		http.Error(w, "Open this from the sign-in page, on a device that's on your tailnet.", http.StatusForbidden)
		return
	}
	id, err := a.TS.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil || id.Tagged || id.Login == "" {
		http.Error(w, "Tailscale couldn't say who you are.", http.StatusForbidden)
		return
	}
	u, ok := a.Users.ByTailscale(id.Login)
	if !ok {
		http.Error(w, "Your Tailscale login ("+id.Login+") isn't linked to a HomeOwl user. An admin can link it in Settings › Users.", http.StatusForbidden)
		return
	}
	to, err := a.tsHandoffURL(u.Username, r.URL.Query().Get("back"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, to, http.StatusFound)
}

// tsBack redeems the code on HomeOwl's own address and starts the session there.
func (a *App) tsBack(w http.ResponseWriter, r *http.Request) {
	name, ok := a.tsCodes.take(r.URL.Query().Get("code"), r.Host)
	if !ok {
		http.Error(w, "This sign-in link has expired or was already used. Go back and try again.", http.StatusForbidden)
		return
	}
	if _, exists := a.Users.Get(name); !exists {
		http.Error(w, "That user no longer exists.", http.StatusForbidden)
		return
	}
	token, _ := a.Users.SessionFor(name, "tailscale")
	setCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

// tailscaleURL: HomeOwl's tailnet address, for the sign-in button (empty when it isn't on a tailnet).
func (a *App) tailscaleURL() string {
	if !a.Cfg.Get().Tailscale.AutoLogin {
		return ""
	}
	st := a.TS.Status()
	if st["running"] != true {
		return ""
	}
	u, _ := st["url"].(string)
	if st["https"] != true && u != "" {
		u = "http://" + strings.TrimPrefix(u, "https://")
	}
	return u
}
