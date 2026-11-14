package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/auth"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mcp"
	"github.com/h4ux/homeowl/internal/tsnetx"
	"github.com/h4ux/homeowl/internal/unifi"
)

func (a *App) api(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/")
	defer func() {
		if rec := recover(); rec != nil {
			fail(w, 500, fmt.Sprint("internal error: ", rec))
		}
	}()
	if p == "ping" {
		send(w, 200, map[string]any{"app": config.AppSlug, "version": config.Version})
		return
	}
	// ---- device check-in page (no session: the device itself, on the home network)
	if p == "hi" {
		a.hiAPI(w, r)
		return
	}
	if p == "hi/profile" { // the device's own setup profile (Apple), same rules as the check-in page
		a.hiProfile(w, r)
		return
	}
	// ---- the guest Wi-Fi card at http://wifi.local (no session; only on that name, only when turned on)
	if p == "wifi" {
		a.wifiAPI(w, r)
		return
	}
	// ---- the macOS menu bar popover (its own start-up token, this computer only)
	if p == "menubar" || p == "menubar/action" {
		a.menubarAPI(w, r, p)
		return
	}
	// ---- login (no session needed)
	if strings.HasPrefix(p, "auth/") {
		a.authAPI(w, r, strings.TrimPrefix(p, "auth/"))
		return
	}
	who := a.who(r)
	if who == nil {
		who = a.tailnetLogin(w, r)
	}
	if who == nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if r.Method == http.MethodPost {
		if !sameOrigin(r) {
			fail(w, 403, "cross-origin request blocked")
			return
		}
		if t := r.Header.Get("X-Audit-Token"); subtle.ConstantTimeCompare([]byte(t), []byte(who.csrf)) != 1 {
			fail(w, 403, "missing app token — reload the page")
			return
		}
	} else if r.Method != http.MethodGet {
		fail(w, 405, "not allowed")
		return
	}
	body := map[string]any{}
	if r.Method == http.MethodPost {
		body = readJSON(r)
	}
	by := who.Username
	switch p {
	case "status":
		age, ok := a.Cache.Age()
		var dataAge any
		if ok {
			dataAge = int(age.Seconds())
		}
		c := a.Cfg.Get()
		send(w, 200, map[string]any{"connected": a.UniFi.Connected(), "host": a.UniFi.Host(), "last_error": nilIfEmpty(a.UniFi.LastError),
			"configured": c.Host != "", "write": a.Changes.Write, "read_only": a.ReadOnly, "token": who.csrf, "version": config.Version,
			"user": who, "data_age": dataAge, "refresh_minutes": c.RefreshMinutes, "mcp_mode": c.MCP.Mode, "checkins_pending": a.pendingFor(who), "alerts_unread": a.Alerts.Unread()})
		return
	case "changes":
		send(w, 200, a.Changes.List())
		return
	case "analytics/status":
		st := a.An.Status()
		send(w, 200, st)
		return
	case "me/password":
		if err := a.changeOwnPassword(who, body); err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, map[string]any{"ok": true})
		return
	case "collect":
		if !a.UniFi.Connected() {
			fail(w, 400, "not connected to UniFi")
			return
		}
		age, ok := a.Cache.Age()
		fresh := b(body, "fresh") && (who.Admin() || !ok || age > time.Minute) // viewers: at most once a minute
		send(w, 200, a.Cache.Get(fresh, a.maxAge()))
		return
	case "nextdns-test":
		send(w, 200, nextdnsTest())
		return
	case "client-types": // the device types admins chose (they set the icons), for everyone signed in
		send(w, 200, map[string]any{"types": a.cliTypes.All()})
		return
	case "analytics/query":
		params, _ := body["params"].(map[string]any)
		res, err := a.An.Query(s(body, "name"), params)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, res)
		return
	case "analytics/explore":
		var req analytics.ExploreRequest
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, &req)
		res, err := a.An.Explore(req)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, res)
		return
	case "update":
		st := a.Updates.Status()
		if !who.Admin() {
			st.Commands, st.CanApply = nil, false
		}
		send(w, 200, st)
		return
	case "alerts":
		send(w, 200, a.alertsView(who.Admin()))
		return
	case "alerts/read":
		if r.Method == http.MethodPost {
			id, _ := n(body, "id")
			a.Alerts.MarkRead(int64(id))
		}
		send(w, 200, map[string]any{"unread": a.Alerts.Unread()})
		return
	case "guest-wifi":
		res, err := a.guestWiFi(who.Admin())
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, res)
		return
	case "history":
		send(w, 200, map[string]any{"snapshots": a.History.List(), "checked": a.History.Checked})
		return
	case "history/diff":
		q := r.URL.Query()
		res, err := a.historyDiff(map[string]string{"from": q.Get("from"), "to": q.Get("to"), "since": q.Get("since")})
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, res)
		return
	case "firmware":
		send(w, 200, map[string]any{"pending": a.firmware.Pending(), "recent": a.recentFirmware(30)})
		return
	case "cameras", "cameras/snapshot":
		if !who.Admin() && !a.Cfg.Get().Cameras.Viewers {
			fail(w, 403, "Only admins can see the cameras. An admin can share them with viewers in Settings.")
			return
		}
		if p == "cameras" {
			days, _ := strconv.Atoi(r.URL.Query().Get("days"))
			send(w, 200, a.camerasView(days))
		} else {
			a.cameraSnapshot(w, r)
		}
		return
	case "presence":
		if r.Method == http.MethodGet {
			days, _ := strconv.Atoi(r.URL.Query().Get("days"))
			send(w, 200, a.presenceView(who.Admin(), days))
			return
		}
	case "ups":
		if r.Method == http.MethodGet {
			days, _ := strconv.Atoi(r.URL.Query().Get("days"))
			send(w, 200, a.upsView(who.Admin(), days))
			return
		}
	case "isp-logos":
		send(w, 200, map[string]any{"logos": a.logos.Versions()})
		return
	case "isp-logo":
		if r.Method == http.MethodGet {
			img, typ, ok := a.logos.Get(r.URL.Query().Get("name"))
			if !ok {
				fail(w, 404, "no logo")
				return
			}
			w.Header().Set("Content-Type", typ)
			w.Header().Set("Cache-Control", "private, max-age=86400")
			_, _ = w.Write(img)
			return
		}
	case "app-logo":
		if r.Method == http.MethodGet {
			img, typ, ok := a.appLogos.Get(r.Context(), r.URL.Query().Get("app"))
			if !ok {
				w.Header().Set("Cache-Control", "private, max-age=3600")
				fail(w, 404, "no logo")
				return
			}
			w.Header().Set("Content-Type", typ)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "private, max-age=604800")
			_, _ = w.Write(img)
			return
		}
	case "analytics/meta":
		res, err := a.An.Meta()
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		send(w, 200, res)
		return
	}
	// ---- admins only from here
	if !who.Admin() {
		fail(w, 403, "You have view-only access. Ask an admin to make you an admin.")
		return
	}
	if r.Method == http.MethodGet && p == "settings" {
		send(w, 200, a.settingsView(who, r))
		return
	}
	var err error
	var res any = map[string]any{"ok": true}
	switch {
	case r.Method != http.MethodPost && !getOK[p]:
		err = errors.New("use POST")
	case p == "connect":
		res, err = a.connect(body)
	case p == "disconnect":
		a.UniFi.Disconnect()
		a.Cache.Clear()
		a.Changes.Write = false
		_ = a.Cfg.Update(func(c *config.Config) { c.AutoConnect = false })
	case p == "write-mode":
		if a.ReadOnly && b(body, "enabled") {
			err = errors.New("HomeOwl was started in read-only mode")
		} else {
			a.Changes.Write = b(body, "enabled")
			res = map[string]any{"write": a.Changes.Write}
		}
	case p == "action":
		if !a.UniFi.Connected() {
			err = errors.New("not connected to UniFi")
			break
		}
		act, _ := body["action"].(map[string]any)
		dry := true
		if v, ok := body["dry_run"].(bool); ok {
			dry = v
		}
		res, err = a.Changes.Run(a.UniFi, unifi.Action(act), dry, by, true)
		if err == nil && !dry {
			a.Cache.Invalidate()
		}
	case p == "undo":
		id, _ := n(body, "id")
		err = a.Changes.Undo(a.UniFi, id, by, true)
		a.Cache.Invalidate()
	case p == "speedtest-status":
		st, d := a.UniFi.Request("POST", a.UniFi.SitePath("cmd/devmgr", false), map[string]any{"cmd": "speedtest-status"})
		res = map[string]any{"status": st, "data": unifi.Redact(unifi.Data(d))}
	case p == "doors":
		res = a.doorsView(r.Context())
	case p == "doors/connect":
		res, err = a.doorsConnect(r.Context(), body)
	case p == "doors/disconnect":
		a.doorsDisconnect()
		res = a.doorsView(r.Context())
	case p == "doors/settings":
		if r.Method != http.MethodPost {
			err = errors.New("use POST")
			break
		}
		err = a.Cfg.Update(func(c *config.Config) { c.Access.Unlock = b(body, "unlock") })
		res = map[string]any{"unlock_allowed": a.Cfg.Get().Access.Unlock}
	case p == "doors/unlock":
		res, err = a.doorUnlock(r.Context(), body, by)
	case p == "ups/connect":
		res, err = a.upsConnect(r.Context(), body)
	case p == "ups/disconnect":
		a.upsDisconnect()
		res = a.upsView(true, 1)
	case p == "ups/settings":
		if err = a.upsSave(body); err == nil {
			res = a.upsView(true, 1)
		}
	case p == "ups/script/save":
		if err = a.UPS.Scripts.SaveSuggestion(runtime.GOOS, s(body, "name"), b(body, "overwrite")); err == nil {
			res = a.upsView(true, 1)
		}
	case p == "ups/shutdown-permission":
		if r.Method != http.MethodPost {
			err = errors.New("use POST")
			break
		}
		res, err = a.upsShutdownPermission(r.Context(), body, by)
	case p == "ups/script/test":
		res, err = a.upsTestScript(r.Context(), body, by)
	case p == "checkins":
		res = a.checkinView()
	case p == "client-types/set":
		if err = a.cliTypes.Set(s(body, "mac"), s(body, "kind"), false); err == nil {
			res = map[string]any{"types": a.cliTypes.All()}
		}
	case p == "mdm":
		res = a.mdmView()
	case p == "mdm/request":
		res, err = a.mdmRequest(r.Context(), body)
	case p == "mdm/decrypt":
		res, err = a.mdmDecrypt(body)
	case p == "mdm/push-request":
		if q := a.mdmPushLoad().Request; q != "" {
			res = map[string]any{"request": q}
		} else {
			err = errors.New("there's no decrypted request yet")
		}
	case p == "mdm/pushcert":
		res, err = a.mdmPushCert(body)
	case p == "mdm/restart":
		res = a.mdmRestart(body)
	case p == "mdm/reset":
		res = a.mdmReset(b(body, "all"))
	case p == "wifi-ident":
		res, err = a.wifiIdentCheck()
	case p == "owners":
		res, err = a.ownersView()
	case p == "owners/link" || p == "owners/dismiss":
		res, err = a.ownersAPI(strings.TrimPrefix(p, "owners/"), body)
	case p == "appblocks":
		res = a.appBlocksView()
	case p == "appblocks/save":
		res, err = a.saveAppBlock(body, by)
	case p == "appblocks/delete":
		if err = a.deleteAppBlock(s(body, "id"), by); err == nil {
			res = a.appBlocksView()
		}
	case p == "devtypes":
		res, err = a.devTypesView()
	case p == "devtypes/save":
		if res, err = a.saveDevType(body); err == nil {
			res, err = a.devTypesView()
		}
	case p == "devtypes/delete":
		if err = a.deleteDevType(s(body, "id")); err == nil {
			res, err = a.devTypesView()
		}
	case p == "checkins/coverage":
		res = a.setupCoverage()
	case p == "checkins/setup":
		res, err = a.changeSetup(body)
	case p == "checkins/ndprofiles":
		res = a.ndProfileChoices()
	case p == "checkins/approve":
		res, err = a.approveCheckIn(body, by)
	case p == "checkins/reject":
		err = a.checkins.Decide(s(body, "id"), "rejected", by)
		res = a.checkinView()
	case p == "guest-wifi/pick":
		err = a.pickGuestSSIDs(body)
		res = map[string]any{"ssids": a.Cfg.Get().Guest.SSIDs}
	case p == "startup":
		if r.Method == http.MethodPost {
			res, err = a.setStartup(b(body, "enabled"))
		} else {
			res = a.startupView()
		}
	case p == "presence/tag":
		if err = a.presenceTag(body); err == nil {
			res = a.presenceView(true, 1)
		}
	case p == "presence/settings":
		if err = a.presenceSettings(body); err == nil {
			res = a.presenceView(true, 1)
		}
	case p == "presence/clear":
		a.presence.Clear()
		_ = a.presence.Save()
		res = a.presenceView(true, 1)
	case p == "cameras/discover":
		res = map[string]any{"found": a.discoverProtect(r.Context())}
	case p == "cameras/console":
		if r.Method != http.MethodPost {
			err = errors.New("use POST")
		} else if b(body, "disconnect") {
			res, err = a.protectDisconnect()
		} else {
			res, err = a.protectConnect(body)
		}
	case p == "menubar-settings":
		if r.Method == http.MethodPost {
			res, err = a.setMenubar(body)
		} else {
			res = a.menubarSettings()
		}
	case p == "alerts/settings":
		if err = a.alertsSettings(body); err == nil {
			res = a.alertsView(true)
		}
	case p == "alerts/test":
		err = a.testAlert(r.Context())
	case p == "alerts/clear":
		a.Alerts.Clear()
	case p == "alerts/assign":
		err = a.An.AssignDevice(s(body, "person"), "mac", s(body, "mac"))
	case p == "guest-wifi/schedule":
		if r.Method == http.MethodPost {
			res, err = a.guestSchedule(body)
		} else {
			res = a.guestScheduleView()
		}
	case strings.HasPrefix(p, "cloudflare/"):
		res, err = a.cloudflareAPI(p, by, body)
	case strings.HasPrefix(p, "cert/"):
		res, err = a.certAPI(p, by, body)
	case p == "names/sync":
		a.syncHomeDNS(by)
		res = a.homeDNSView()
	case p == "guest-wifi/rotate-now":
		var k int
		if k, err = a.rotateGuestPasswords(by); err == nil {
			res = map[string]any{"rotated": k}
		}
	case p == "guest-wifi/theme":
		res, err = a.setQRTheme(body)
	case p == "guest-wifi/suggest":
		res = map[string]any{"password": unifi.NewGuestPassword()}
	case p == "update/check":
		res = a.Updates.Check(r.Context())
	case p == "update/apply":
		if a.ReadOnly {
			err = errors.New("HomeOwl was started in read-only mode")
		} else if err = a.applyUpdate(r.Context()); err == nil {
			res = map[string]any{"ok": true, "restarting": a.Restart != nil}
		}
	case p == "isp-logo":
		if s(body, "site") == "" {
			err = a.logos.Remove(s(body, "name"))
		} else {
			err = a.logos.Set(r.Context(), s(body, "name"), s(body, "site"))
		}
		res = map[string]any{"logos": a.logos.Versions()}
	case p == "settings":
		res, err = a.saveSettings(who, r, body)
	case p == "users/save":
		err = a.saveUser(who, body)
		res = map[string]any{"ok": err == nil, "users": a.Users.Users()}
	case p == "users/delete":
		if s(body, "username") == who.Username {
			err = errors.New("you can't delete yourself")
		} else {
			err = a.Users.Delete(s(body, "username"))
		}
		res = map[string]any{"ok": err == nil, "users": a.Users.Users()}
	case p == "mcp/token":
		res, err = a.createMCPToken(body)
	case p == "mcp/revoke":
		id := s(body, "id")
		err = a.Cfg.Update(func(c *config.Config) {
			var keep []config.MCPToken
			for _, t := range c.MCP.Tokens {
				if t.ID != id {
					keep = append(keep, t)
				}
			}
			c.MCP.Tokens = keep
		})
	case p == "tailscale/logout":
		err = a.TS.Logout()
		a.TS.Stop()
		_ = a.Cfg.Update(func(c *config.Config) { c.Tailscale.Enabled = false })
	case strings.HasPrefix(p, "analytics/"):
		res, err = a.analyticsAdmin(strings.TrimPrefix(p, "analytics/"), body)
	default:
		fail(w, 404, "not found")
		return
	}
	if err != nil {
		code := 400
		var ae unifi.ActionError
		if !errors.As(err, &ae) && strings.Contains(err.Error(), "internal") {
			code = 500
		}
		fail(w, code, err.Error())
		return
	}
	send(w, 200, res)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ------------------------------------------------------------------ login

func (a *App) authAPI(w http.ResponseWriter, r *http.Request, op string) {
	if r.Method == http.MethodPost && !sameOrigin(r) {
		fail(w, 403, "cross-origin request blocked")
		return
	}
	switch op {
	case "state":
		who := a.who(r)
		if who == nil {
			who = a.tailnetLogin(w, r)
		}
		st := map[string]any{"needs_setup": a.Users.NeedsSetup(), "local": isLoopback(r), "version": config.Version,
			"tailnet": tsnetx.FromTailnet(r), "user": who}
		if a.Users.NeedsSetup() {
			st["code_required"] = !isLoopback(r)
			c := a.Cfg.Get()
			st["suggest_tailscale"] = firstOr(c.LegacyAdmins)
		}
		if tsnetx.FromTailnet(r) && who == nil {
			if id, err := a.TS.WhoIs(r.Context(), r.RemoteAddr); err == nil && !id.Tagged {
				st["tailscale_login"] = id.Login
			}
		}
		send(w, 200, st)
	case "setup":
		if r.Method != http.MethodPost {
			fail(w, 405, "use POST")
			return
		}
		body := readJSON(r)
		if !a.Users.NeedsSetup() {
			fail(w, 400, "HomeOwl is already set up — sign in instead.")
			return
		}
		if !isLoopback(r) && !a.Users.CheckSetupCode(s(body, "setup_code")) {
			fail(w, 403, "Wrong setup code. Find it in HomeOwl's log or run `homeowl setup-code` on the computer running it.")
			return
		}
		in := auth.UserInput{Username: s(body, "username"), Name: s(body, "name"), Role: auth.RoleAdmin, Password: s(body, "password"), Tailscale: s(body, "tailscale")}
		if err := a.Users.Save(in, true); err != nil {
			fail(w, 400, err.Error())
			return
		}
		token, _, u, err := a.Users.Login(in.Username, in.Password, clientIP(r))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		setCookie(w, r, token)
		send(w, 200, map[string]any{"ok": true, "user": u.Public()})
	case "login":
		if r.Method != http.MethodPost {
			fail(w, 405, "use POST")
			return
		}
		body := readJSON(r)
		token, _, u, err := a.Users.Login(s(body, "username"), s(body, "password"), clientIP(r))
		if err != nil {
			code := 401
			if errors.Is(err, auth.ErrRateLimited) {
				code = 429
			}
			fail(w, code, err.Error())
			return
		}
		setCookie(w, r, token)
		send(w, 200, map[string]any{"ok": true, "user": u.Public()})
	case "logout":
		if c, err := r.Cookie(cookieName); err == nil {
			a.Users.Logout(c.Value)
		}
		clearCookie(w, r)
		send(w, 200, map[string]any{"ok": true})
	default:
		fail(w, 404, "not found")
	}
}

func firstOr(l []string) string {
	if len(l) > 0 {
		return l[0]
	}
	return ""
}

func (a *App) changeOwnPassword(who *Who, body map[string]any) error {
	u, ok := a.Users.Get(who.Username)
	if !ok || !auth.VerifyPassword(u.Hash, s(body, "current")) {
		return errors.New("the current password is wrong")
	}
	return a.Users.SetPassword(u.Username, s(body, "new"))
}

func (a *App) saveUser(who *Who, body map[string]any) error {
	in := auth.UserInput{Username: s(body, "username"), Name: s(body, "name"), Role: s(body, "role"), Password: s(body, "password"), Tailscale: s(body, "tailscale")}
	create := b(body, "create")
	if !create && strings.EqualFold(in.Username, who.Username) && in.Role != auth.RoleAdmin {
		return errors.New("you can't remove your own admin role")
	}
	return a.Users.Save(in, create)
}

// ------------------------------------------------------------------ UniFi connection

func (a *App) connect(body map[string]any) (any, error) {
	host, err := unifi.NormaliseHost(s(body, "host"))
	if err != nil {
		return nil, err
	}
	key := s(body, "key")
	if key == "" {
		key = a.Sec.Get(unifiKeySlot)
	}
	if key == "" {
		return nil, errors.New("enter the UniFi API key")
	}
	verify, site := b(body, "verify_tls"), s(body, "site")
	info, err := a.UniFi.Connect(host, key, verify, site)
	if err != nil {
		return nil, err
	}
	a.Cache.Clear()
	a.Changes.Write = false
	warning := ""
	if err := a.Sec.Set(unifiKeySlot, key); err != nil {
		warning = "Connected, but the key couldn't be saved: " + err.Error()
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Host, c.Site, c.VerifyTLS, c.AutoConnect = host, site, verify, true })
	a.configureAccess()
	go a.Cache.Get(true, 0)
	return map[string]any{"ok": true, "info": info, "warning": nilIfEmpty(warning)}, nil
}

// ------------------------------------------------------------------ settings

func (a *App) urls(r *http.Request) []string {
	c := a.Cfg.Get().LAN
	scheme := "http"
	if c.TLS && a.cert.Certificate != nil {
		scheme = "https"
	}
	var out []string
	if !c.Enabled {
		return []string{fmt.Sprintf("http://127.0.0.1:%d", c.Port)}
	}
	_, ips := localNames()
	for _, ip := range ips {
		if ip.To4() != nil && !ip.IsLoopback() && (ip.IsPrivate() || strings.HasPrefix(ip.String(), "100.")) {
			out = append(out, fmt.Sprintf("%s://%s:%d", scheme, ip, c.Port))
		}
	}
	names, _ := localNames()
	if len(names) > 2 {
		out = append(out, fmt.Sprintf("%s://%s:%d", scheme, names[2], c.Port))
	}
	sort.Strings(out)
	return out
}

func (a *App) settingsView(who *Who, r *http.Request) map[string]any {
	c := a.Cfg.Get()
	toks := []map[string]any{}
	for _, t := range c.MCP.Tokens {
		toks = append(toks, map[string]any{"id": t.ID, "name": t.Name, "created": t.Created, "last_used": t.LastUsed})
	}
	ts := a.TS.Status()
	a.mu.Lock()
	if a.tsErr != "" && ts["error"] == "" {
		ts["error"] = a.tsErr
	}
	lanErr := a.lanErr
	a.mu.Unlock()
	exe, _ := os.Executable()
	return map[string]any{
		"unifi": map[string]any{"host": c.Host, "site": c.Site, "verify_tls": c.VerifyTLS, "auto_connect": c.AutoConnect,
			"key_saved": a.Sec.Get(unifiKeySlot) != "", "connected": a.UniFi.Connected(), "last_error": nilIfEmpty(a.UniFi.LastError)},
		"refresh_minutes": c.RefreshMinutes,
		"lan":             map[string]any{"enabled": c.LAN.Enabled, "port": c.LAN.Port, "tls": c.LAN.TLS, "extra_host": c.LAN.ExtraHost, "urls": a.urls(r), "error": nilIfEmpty(lanErr)},
		"names":           a.namesView(),
		"tailscale":       map[string]any{"enabled": c.Tailscale.Enabled, "hostname": c.Tailscale.Hostname, "auto_login": c.Tailscale.AutoLogin, "status": ts, "built_in": tsnetx.Available},
		"mcp":             map[string]any{"mode": c.MCP.Mode, "tokens": toks, "tools": a.mcpToolSummary()},
		"users":           a.Users.Users(),
		"legacy_logins":   append(c.LegacyAdmins, c.LegacyViewers...),
		"me":              who,
		"files": map[string]any{"data": a.DataDir, "config": a.Cfg.Path(), "change_log": filepath.Join(a.DataDir, "changes-log.jsonl"),
			"analytics": a.An.DBPath, "log": config.LogDir(a.DataDir), "secrets": a.Sec.Where(), "binary": exe},
		"system":  map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": config.Version, "uptime_s": int(time.Since(a.started).Seconds())},
		"version": config.Version,
		"updates": map[string]any{"check": c.Updates.Check},
		"cameras": map[string]any{"viewers": c.Cameras.Viewers},
		"startup": a.startupView(), "menubar": a.menubarSettings(),
	}
}

var hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (a *App) saveSettings(who *Who, r *http.Request, body map[string]any) (any, error) {
	var restartLAN, restartTS bool
	var verr error
	c := a.Cfg.Get()
	if v, ok := n(body, "refresh_minutes"); ok {
		c.RefreshMinutes = max(1, min(120, v))
	}
	if u, ok := body["updates"].(map[string]any); ok && has(u, "check") {
		c.Updates.Check = b(u, "check")
	}
	if u, ok := body["cameras"].(map[string]any); ok && has(u, "viewers") {
		c.Cameras.Viewers = b(u, "viewers")
	}
	if l, ok := body["lan"].(map[string]any); ok {
		nl := c.LAN
		if has(l, "enabled") {
			nl.Enabled = b(l, "enabled")
		}
		if v, ok := n(l, "port"); ok {
			if v < 1024 || v > 65535 {
				return nil, errors.New("pick a port between 1024 and 65535")
			}
			nl.Port = v
		}
		if has(l, "tls") {
			nl.TLS = b(l, "tls")
		}
		if has(l, "extra_host") {
			nl.ExtraHost = strings.ToLower(s(l, "extra_host"))
		}
		if !nl.Enabled && who.Remote && !tsnetx.FromTailnet(r) {
			return nil, errors.New("turning off home-network access would lock you out — do it on the computer running HomeOwl, or over Tailscale")
		}
		restartLAN = nl != c.LAN
		c.LAN = nl
	}
	restartNames := false
	if nm, ok := body["names"].(map[string]any); ok {
		nn, err := parseNames(nm, c.Names)
		if err != nil {
			return nil, err
		}
		restartNames = nn != c.Names
		c.Names = nn
	}
	if t, ok := body["tailscale"].(map[string]any); ok {
		nt := c.Tailscale
		if has(t, "enabled") {
			nt.Enabled = b(t, "enabled")
		}
		if has(t, "hostname") {
			h := strings.ToLower(s(t, "hostname"))
			if !hostRE.MatchString(h) {
				return nil, errors.New("tailnet name: letters, numbers and dashes")
			}
			nt.Hostname = h
		}
		if has(t, "auto_login") {
			nt.AutoLogin = b(t, "auto_login")
		}
		if key := s(t, "auth_key"); key != "" {
			if err := a.Sec.Set("tailscale-auth-key", key); err != nil {
				return nil, err
			}
		}
		if nt.Enabled && !tsnetx.Available {
			return nil, errors.New("this build of HomeOwl has no built-in Tailscale")
		}
		if !nt.Enabled && tsnetx.FromTailnet(r) {
			return nil, errors.New("you're connected over Tailscale — turn it off from your home network or this computer")
		}
		restartTS = nt != c.Tailscale
		c.Tailscale = nt
	}
	if m, ok := body["mcp"].(map[string]any); ok {
		mode := s(m, "mode")
		switch mode {
		case "off", "read", "guarded", "god":
			c.MCP.Mode = mode
		default:
			verr = errors.New("unknown MCP mode")
		}
	}
	if verr != nil {
		return nil, verr
	}
	if err := a.Cfg.Update(func(x *config.Config) {
		x.RefreshMinutes, x.LAN, x.Tailscale, x.MCP.Mode, x.Updates, x.Cameras, x.Names = c.RefreshMinutes, c.LAN, c.Tailscale, c.MCP.Mode, c.Updates, c.Cameras, c.Names
	}); err != nil {
		return nil, err
	}
	if restartLAN {
		go func() {
			time.Sleep(500 * time.Millisecond) // let this response go out first
			if err := a.StartLAN(); err != nil {
				a.mu.Lock()
				a.lanErr = err.Error()
				a.mu.Unlock()
			}
		}()
	}
	if restartNames || restartLAN {
		go func() { time.Sleep(700 * time.Millisecond); a.ApplyNames() }()
	}
	if restartTS {
		a.ApplyTailscale()
	}
	return a.settingsView(who, r), nil
}

// ------------------------------------------------------------------ MCP tokens

func (a *App) createMCPToken(body map[string]any) (any, error) {
	name := s(body, "name")
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	token, hash := auth.NewToken()
	id := hash[:10]
	err := a.Cfg.Update(func(c *config.Config) {
		c.MCP.Tokens = append(c.MCP.Tokens, config.MCPToken{ID: id, Name: name, Hash: hash, Created: time.Now().Unix()})
		if c.MCP.Mode == "off" {
			c.MCP.Mode = "read"
		}
	})
	return map[string]any{"ok": true, "id": id, "token": token}, err
}

func (a *App) mcpAuth(r *http.Request) string {
	c := a.Cfg.Get()
	if c.MCP.Mode == "off" || !a.hostAllowed(r) {
		return ""
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	hash := auth.TokenHash(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
	for _, t := range c.MCP.Tokens {
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(hash)) == 1 {
			if time.Now().Unix()-t.LastUsed > 300 {
				id := t.ID
				_ = a.Cfg.Update(func(c *config.Config) {
					for i := range c.MCP.Tokens {
						if c.MCP.Tokens[i].ID == id {
							c.MCP.Tokens[i].LastUsed = time.Now().Unix()
						}
					}
				})
			}
			return "mcp:" + t.Name
		}
	}
	return ""
}

func (a *App) mcpToolSummary() []map[string]any {
	var out []map[string]any
	for _, t := range a.allTools() {
		out = append(out, map[string]any{"name": t.Name, "level": t.Level, "title": t.Title})
	}
	return out
}

func (a *App) mcpTools() []mcp.Tool {
	mode := a.Cfg.Get().MCP.Mode
	var out []mcp.Tool
	for _, t := range a.allTools() {
		if mcp.Allowed(mode, t.Level) {
			out = append(out, t)
		}
	}
	return out
}

// ------------------------------------------------------------------ analytics (admin)

func (a *App) analyticsAdmin(op string, body map[string]any) (any, error) {
	A := a.An
	switch op {
	case "settings":
		return a.analyticsSettings(body)
	case "profiles":
		return map[string]any{"profiles": A.RefreshNextDNSProfiles()}, nil
	case "sync":
		if !A.Open() {
			return nil, errors.New("analytics isn't running")
		}
		A.Trigger(orStr(s(body, "source"), "all"))
		return map[string]any{"ok": true}, nil
	case "person-save":
		var p analytics.Person
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, &p)
		id, err := A.SavePerson(p)
		return map[string]any{"ok": err == nil, "id": id}, err
	case "person-delete":
		return map[string]any{"ok": true}, A.DeletePerson(s(body, "id"))
	case "rule-save":
		pats := []string{}
		if l, ok := body["patterns"].([]any); ok {
			for _, x := range l {
				pats = append(pats, fmt.Sprint(x))
			}
		} else {
			pats = append(pats, s(body, "pattern"))
		}
		for _, p := range pats {
			if err := A.SaveRule(p, s(body, "category")); err != nil {
				return nil, err
			}
		}
		return map[string]any{"ok": true}, nil
	case "rule-delete":
		return map[string]any{"ok": true}, A.DeleteRule(s(body, "pattern"))
	case "recategorize":
		_, err := A.RefreshCategories(true)
		return map[string]any{"ok": true}, err
	case "sql":
		return A.RunSQL(s(body, "sql"), 5000)
	case "install":
		return map[string]any{"ok": true, "install": map[string]any{"state": "done"}}, nil
	}
	return nil, errors.New("not found")
}

var profileRE = regexp.MustCompile(`^[0-9a-f]{6}$`)
var ipRE = regexp.MustCompile(`^[0-9a-fA-F.:]+$`)

func (a *App) analyticsSettings(body map[string]any) (any, error) {
	A := a.An
	c := a.Cfg.Get().Analytics
	accounts := c.NextDNSAccounts
	if A.HasLegacyKey() && !hasSlot(accounts, "nextdns-api-key") {
		accounts = append([]config.NextDNSAccount{{Slot: "nextdns-api-key", Label: "NextDNS account 1"}}, accounts...)
	}
	trigger := false
	if key := s(body, "nextdns_key"); key != "" {
		profs, err := A.ValidateNextDNSKey(key)
		if err != nil {
			return nil, err
		}
		slot := s(body, "nextdns_slot")
		if slot == "" {
			for _, ac := range accounts {
				if a.Sec.Get(ac.Slot) == key {
					slot = ac.Slot
				}
			}
		}
		if slot == "" {
			used := map[string]bool{}
			for _, ac := range accounts {
				used[ac.Slot] = true
			}
			for i := 1; i < 50; i++ {
				cand := "nextdns-api-key"
				if i > 1 {
					cand = fmt.Sprintf("nextdns-api-key-%d", i)
				}
				if !used[cand] {
					slot = cand
					break
				}
			}
		}
		label := s(body, "nextdns_label")
		if label == "" {
			var names []string
			for i, p := range profs {
				if i < 3 {
					names = append(names, p.Name)
				}
			}
			label = orStr(strings.Join(names, ", "), fmt.Sprintf("NextDNS account %d", len(accounts)+1))
		}
		if err := a.Sec.Set(slot, key); err != nil {
			return nil, err
		}
		if !hasSlot(accounts, slot) {
			accounts = append(accounts, config.NextDNSAccount{Slot: slot, Label: label})
		}
		trigger = true
	}
	if slot := s(body, "nextdns_remove"); slot != "" {
		a.Sec.Delete(slot)
		var keep []config.NextDNSAccount
		for _, ac := range accounts {
			if ac.Slot != slot {
				keep = append(keep, ac)
			}
		}
		accounts = keep
		A.UnlinkAccount(slot)
	}
	tzChanged := false
	var verr error
	err := a.Cfg.Update(func(cfg *config.Config) {
		x := &cfg.Analytics
		x.NextDNSAccounts = accounts
		if has(body, "nextdns_all") {
			if b(body, "nextdns_all") && !x.NextDNSAll {
				trigger = true
			}
			x.NextDNSAll = b(body, "nextdns_all")
		}
		for k, p := range map[string]*bool{"enabled": &x.Enabled, "nextdns_raw": &x.NextDNSRaw, "syslog_enabled": &x.SyslogEnabled} {
			if has(body, k) {
				*p = b(body, k)
			}
		}
		if l, ok := body["nextdns_profiles"].([]any); ok {
			var ids []string
			for _, v := range l {
				if id := fmt.Sprint(v); profileRE.MatchString(id) {
					ids = append(ids, id)
				}
			}
			x.NextDNSProfiles = ids
			trigger = true
		}
		if v, ok := n(body, "syslog_port"); ok {
			x.SyslogPort = max(1024, min(65535, v))
		}
		if l, ok := body["syslog_sources"].([]any); ok {
			var src []string
			for _, v := range l {
				if ip := strings.TrimSpace(fmt.Sprint(v)); ipRE.MatchString(ip) {
					src = append(src, ip)
				}
			}
			x.SyslogSources = src
		}
		if v, ok := n(body, "raw_days"); ok {
			x.RawDays = max(7, min(3650, v))
		}
		if has(body, "timezone") {
			tz := s(body, "timezone")
			if !analytics.ValidTimezone(tz) {
				verr = fmt.Errorf("unknown time zone '%s' — use a name like Europe/London", tz)
				return
			}
			tzChanged = tz != x.Timezone
			x.Timezone = tz
		}
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, err
	}
	A.Start()
	if trigger && A.Open() {
		go func() { A.RefreshNextDNSProfiles(); A.Trigger("nextdns") }()
	}
	if tzChanged && A.Open() {
		go A.ApplyLocalTime(true)
	}
	return map[string]any{"ok": true, "status": A.Status()}, nil
}

func hasSlot(l []config.NextDNSAccount, slot string) bool {
	for _, a := range l {
		if a.Slot == slot {
			return true
		}
	}
	return false
}

func orStr(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

// getOK: the admin routes that only read, so a GET may run them. Every other admin route changes something and
// needs a POST, which carries the session token (X-Audit-Token) and passes the same-origin check: a link, an
// <img> or a page on another site can't make HomeOwl act.
var getOK = map[string]bool{"appblocks": true, "owners": true, "wifi-ident": true, "devtypes": true, "mdm": true, "checkins": true, "checkins/coverage": true,
	"checkins/ndprofiles": true, "doors": true, "guest-wifi/schedule": true, "cameras/discover": true}
