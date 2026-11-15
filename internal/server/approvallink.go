package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
)

// Approval links: a new-device push (ntfy) carries Approve and Block buttons that work without signing in. Each link
// is signed with a key only HomeOwl has, names one device and one decision, expires in two days and works once.
// They're made when the push is sent and never stored in the alert (other HomeOwl users can read alerts).

const (
	slotLinkKey = "approval-link-key"
	linkTTL     = 48 * time.Hour
)

func (a *App) linkKey() []byte {
	k := a.Sec.Get(slotLinkKey)
	if len(k) < 32 {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		k = hex.EncodeToString(b)
		if err := a.Sec.Set(slotLinkKey, k); err != nil {
			return nil
		}
	}
	return []byte(k)
}

func linkSig(key []byte, payload string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// approvalToken: op ("approve" or "block") for mac, until exp.
func (a *App) approvalToken(op, mac string, exp time.Time) string {
	key := a.linkKey()
	if key == nil {
		return ""
	}
	payload := op + "|" + mac + "|" + strconv.FormatInt(exp.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + linkSig(key, payload)
}

// checkToken: what the token allows, if it's genuine and current (not whether it was used).
func (a *App) checkToken(t string) (op, mac, sig string, exp int64, err error) {
	p, sig, ok := strings.Cut(t, ".")
	raw, err2 := base64.RawURLEncoding.DecodeString(p)
	key := a.linkKey()
	if !ok || err2 != nil || key == nil || !hmac.Equal([]byte(sig), []byte(linkSig(key, string(raw)))) {
		return "", "", "", 0, errors.New("this link isn't valid")
	}
	f := strings.Split(string(raw), "|")
	if len(f) != 3 {
		return "", "", "", 0, errors.New("this link isn't valid")
	}
	exp, _ = strconv.ParseInt(f[2], 10, 64)
	if time.Now().Unix() > exp {
		return "", "", "", 0, errors.New("this link has expired — open HomeOwl to decide")
	}
	if (f[0] != "approve" && f[0] != "block") || !macRE.MatchString(f[1]) {
		return "", "", "", 0, errors.New("this link isn't valid")
	}
	return f[0], f[1], sig, exp, nil
}

// linkBase: an address the admins' phones can reach HomeOwl on (the check-in page's, which is the nicest one).
func (a *App) linkBase() string {
	if l := a.checkinLinks(); len(l) > 0 {
		return strings.TrimSuffix(l[0], "/hi")
	}
	return ""
}

// pushActions: the buttons for a new-device push.
func (a *App) pushActions(al alerts.Alert) []alerts.PushAction {
	mac, _ := al.Data["mac"].(string)
	if al.Kind != "approval" || mac == "" || !a.Cfg.Get().Onboard.Approve {
		return nil
	}
	base := a.linkBase()
	if base == "" {
		return nil
	}
	exp := time.Now().Add(linkTTL)
	out := []alerts.PushAction{{Label: "Approve", URL: base + "/api/approval-link?t=" + a.approvalToken("approve", mac, exp)}}
	if al.Data["grace"] == true {
		out = append(out, alerts.PushAction{Label: "Block", URL: base + "/api/approval-link?t=" + a.approvalToken("block", mac, exp)})
	}
	return out
}

var linkHits portalLimit

// approvalLinkAPI: GET shows what the link does (with a button), POST does it — the push's button POSTs directly.
func (a *App) approvalLinkAPI(w http.ResponseWriter, r *http.Request) {
	page := func(code int, title, msg, form string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>HomeOwl</title><link rel="stylesheet" href="/style.css">` +
			`<main class="card" style="max-width:420px;margin:12vh auto;padding:24px"><h1 style="font-size:22px">` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(msg) + `</p>` + form + `</main>`))
	}
	if !linkHits.allow(remoteIP(r)) {
		page(429, "Slow down", "Too many tries — wait a few minutes.", "")
		return
	}
	t := r.URL.Query().Get("t")
	op, mac, sig, exp, err := a.checkToken(t)
	if err != nil {
		page(403, "Can't do that", err.Error(), "")
		return
	}
	s := a.approval
	s.mu.Lock()
	st := s.load()
	used := st.Used[sig] > 0
	name := mac
	for _, d := range []pendingDevice{st.Grace[mac], st.Blocked[mac]} {
		if d.MAC != "" {
			name = orStr(d.Name, orStr(d.Hostname, mac))
		}
	}
	s.mu.Unlock()
	verb := map[string]string{"approve": "Approve", "block": "Block"}[op]
	if used {
		page(410, "Already done", "This link was used already.", "")
		return
	}
	if r.Method == http.MethodGet {
		page(200, verb+" "+name+"?", map[string]string{"approve": "It joins the home Wi-Fi like your other devices.", "block": "It's cut off the home Wi-Fi until you approve it in HomeOwl."}[op],
			`<form method="post"><button class="btn primary" type="submit">`+verb+`</button></form>`)
		return
	}
	if r.Method != http.MethodPost {
		page(405, "Can't do that", "Not allowed.", "")
		return
	}
	s.mu.Lock()
	st = s.load()
	if st.Used == nil {
		st.Used = map[string]int64{}
	}
	if st.Used[sig] > 0 {
		s.mu.Unlock()
		page(410, "Already done", "This link was used already.", "")
		return
	}
	st.Used[sig] = exp
	for k, e := range st.Used {
		if e < time.Now().Unix() {
			delete(st.Used, k)
		}
	}
	_ = s.save(st)
	s.mu.Unlock()
	if _, err := a.approvalAPI(op, map[string]any{"mac": mac}, "push link"); err != nil {
		page(409, "Can't do that", err.Error(), "")
		return
	}
	page(200, "Done", map[string]string{"approve": name + " is approved.", "block": name + " is blocked."}[op], "")
}
