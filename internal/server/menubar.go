package server

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/h4ux/homeowl/internal/config"
)

// Menubar is the macOS menu bar item. The command line fills it in when it can show one (a Mac with someone
// signed in); nil elsewhere. Its popover is /menubar.html, which proves itself with Token: a random value made
// at start-up that only the popover knows, accepted from this computer only. It unlocks a summary and a few
// actions (open HomeOwl, updates, start at login, the icon), never the rest of the API.
type Menubar struct {
	Token      string
	Styles     []string
	SetVisible func(bool)
	SetIcon    func(style string, level, count int)
	Close      func()
	SetHeight  func(int)

	mu           sync.Mutex
	level, count int
}

func (m *Menubar) icon(style string) {
	m.mu.Lock()
	l, c := m.level, m.count
	m.mu.Unlock()
	m.SetIcon(style, l, c)
}

func (a *App) menubarStyle() string {
	st := a.Cfg.Get().Menubar.Icon
	for _, x := range a.Menubar.Styles {
		if x == st {
			return st
		}
	}
	return "outline"
}

// menubarSettings is the Settings › System card (admins).
func (a *App) menubarSettings() map[string]any {
	if a.Menubar == nil {
		return map[string]any{"available": false}
	}
	return map[string]any{"available": true, "show": !a.Cfg.Get().Menubar.Hide, "icon": a.menubarStyle(), "styles": a.Menubar.Styles}
}

func (a *App) setMenubar(body map[string]any) (any, error) {
	m := a.Menubar
	if m == nil {
		return nil, errors.New("the menu bar icon is only for a Mac with someone signed in")
	}
	if st := s(body, "icon"); st != "" {
		ok := false
		for _, x := range m.Styles {
			ok = ok || x == st
		}
		if !ok {
			return nil, errors.New("unknown icon style")
		}
		if err := a.Cfg.Update(func(c *config.Config) { c.Menubar.Icon = st }); err != nil {
			return nil, err
		}
		m.icon(st)
	}
	if has(body, "show") {
		show := b(body, "show")
		if err := a.Cfg.Update(func(c *config.Config) { c.Menubar.Hide = !show }); err != nil {
			return nil, err
		}
		m.SetVisible(show)
	}
	return a.menubarSettings(), nil
}

var menubarViewRE = regexp.MustCompile(`^[a-z]{1,20}$`)

// menubarAPI serves /api/menubar and /api/menubar/action for the popover.
func (a *App) menubarAPI(w http.ResponseWriter, r *http.Request, p string) {
	m := a.Menubar
	if m == nil || m.Token == "" || !isLoopback(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-HomeOwl-Menubar")), []byte(m.Token)) != 1 {
		fail(w, 403, "only HomeOwl's menu bar item may use this")
		return
	}
	switch {
	case p == "menubar" && r.Method == http.MethodGet:
		send(w, 200, a.menubarView(r.URL.Query().Get("guest") == "1"))
	case p == "menubar/action" && r.Method == http.MethodPost:
		if !sameOrigin(r) {
			fail(w, 403, "cross-origin request blocked")
			return
		}
		res, err := a.menubarAction(r, readJSON(r))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, res)
	default:
		fail(w, 405, "not allowed")
	}
}

func (a *App) menubarView(guest bool) map[string]any {
	c := a.Cfg.Get()
	out := map[string]any{"version": config.Version, "update": a.Updates.Status(), "startup": a.startupView(), "icon": a.menubarStyle(),
		"connected": a.UniFi.Connected(), "checkins_pending": a.checkins.Pending(), "read_only": a.ReadOnly,
		"url": fmt.Sprintf("http://127.0.0.1:%d/", c.LAN.Port)}
	var bundle map[string]any
	if a.UniFi.Connected() {
		bundle = a.Cache.Get(false, a.maxAge())
		out["bundle"] = bundle
	}
	if c.UPS.Enabled && c.UPS.Host != "" {
		out["ups"] = a.UPS.Status()
	}
	out["people"] = a.peopleHome(bundle)
	if guest {
		if g, err := a.guestWiFi(false); err == nil {
			out["guest"] = g
		} else {
			out["guest_error"] = err.Error()
		}
	}
	return out
}

// peopleHome: everyone with a UniFi device, and whether one of those devices is connected right now.
func (a *App) peopleHome(bundle map[string]any) []map[string]any {
	if len(a.presencePeople()) > 0 { // tagged devices (Family › Who's home) decide
		now := a.presence.Now()
		out := []map[string]any{}
		for _, p := range a.An.People() {
			if st, ok := now[p.ID]; ok {
				out = append(out, map[string]any{"name": p.Name, "color": p.Color, "home": st.Home, "since": st.Since})
			}
		}
		return out
	}
	online := map[string]bool{}
	for _, cl := range section(bundle, "legacy", "clients") {
		if mac, _ := cl["mac"].(string); mac != "" {
			online[strings.ToLower(mac)] = true
		}
	}
	out := []map[string]any{}
	for _, p := range a.An.People() {
		macs, on := 0, 0
		for _, d := range p.Devices {
			if d.Kind == "mac" {
				macs++
				if online[strings.ToLower(d.Key)] {
					on++
				}
			}
		}
		if macs > 0 {
			out = append(out, map[string]any{"name": p.Name, "color": p.Color, "home": on > 0, "online": on, "devices": macs})
		}
	}
	return out
}

func (a *App) menubarAction(r *http.Request, body map[string]any) (any, error) {
	m := a.Menubar
	switch s(body, "do") {
	case "status": // the popover page worked out how things are; the icon shows it
		lvl, _ := n(body, "level")
		cnt, _ := n(body, "count")
		m.mu.Lock()
		m.level, m.count = min(max(lvl, 0), 2), max(cnt, 0)
		m.mu.Unlock()
		m.icon(a.menubarStyle())
		if h, ok := n(body, "height"); ok {
			m.SetHeight(h)
		}
		return map[string]any{"ok": true}, nil
	case "open":
		u := fmt.Sprintf("http://127.0.0.1:%d/", a.Cfg.Get().LAN.Port)
		if v := s(body, "view"); v != "" {
			if !menubarViewRE.MatchString(v) {
				return nil, errors.New("unknown page")
			}
			u += "#" + v
		}
		m.Close()
		return map[string]any{"ok": true}, exec.Command("open", u).Start()
	case "update-check":
		return a.Updates.Check(r.Context()), nil
	case "update-apply":
		if a.ReadOnly {
			return nil, errors.New("HomeOwl was started in read-only mode")
		}
		if err := a.applyUpdate(r.Context()); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "restarting": a.Restart != nil}, nil
	case "startup":
		return a.setStartup(b(body, "on"))
	case "icon", "hide":
		if s(body, "do") == "hide" {
			body = map[string]any{"show": false}
		}
		return a.setMenubar(body)
	}
	return nil, errors.New("unknown action")
}
