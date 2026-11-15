package server

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
)

// Approve or block from the push. A new-device push (ntfy) carries Approve and Block buttons. A tap doesn't call
// HomeOwl — the phone may be anywhere — it posts a token to a second, private ntfy topic that HomeOwl listens to
// (outgoing connections only: nothing is exposed). Each token is signed with a key only HomeOwl has, names one
// device and one decision, expires in two days and works once, so knowing the reply topic doesn't let anyone decide.
// Tokens are made when the push is sent and never stored in the alert (other HomeOwl users can read alerts).

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

// approvalToken: op ("approve" or "block") for mac — or ("allow" or "deny") for a site request's id — until exp.
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
	p, sig, ok := strings.Cut(strings.TrimSpace(t), ".")
	raw, err2 := base64.RawURLEncoding.DecodeString(p)
	key := a.linkKey()
	if !ok || err2 != nil || key == nil || !hmac.Equal([]byte(sig), []byte(linkSig(key, string(raw)))) {
		return "", "", "", 0, errors.New("not a valid decision")
	}
	f := strings.Split(string(raw), "|")
	if len(f) != 3 {
		return "", "", "", 0, errors.New("not a valid decision")
	}
	exp, _ = strconv.ParseInt(f[2], 10, 64)
	if time.Now().Unix() > exp {
		return "", "", "", 0, errors.New("that decision came after its two days — open HomeOwl to decide")
	}
	if !((f[0] == "approve" || f[0] == "block") && macRE.MatchString(f[1]) || (f[0] == "allow" || f[0] == "deny" || f[0] == "ci-approve" || f[0] == "ci-reject") && reqIDRE.MatchString(f[1])) {
		return "", "", "", 0, errors.New("not a valid decision")
	}
	return f[0], f[1], sig, exp, nil
}

// replyTopic: the private topic the buttons post to — the alert topic's name plus a suffix from HomeOwl's key, on
// the same ntfy server.
func (a *App) replyTopic() string {
	u, err := url.Parse(a.Sec.Get(slotNtfy))
	key := a.linkKey()
	if err != nil || u.Host == "" || key == nil || strings.Trim(u.Path, "/") == "" {
		return ""
	}
	u.Path = "/" + strings.Trim(u.Path, "/") + "-r" + strings.ToLower(linkSig(key, "reply:"+u.Host+u.Path)[:12])
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// pushActions: the buttons for a new-device push.
func (a *App) pushActions(al alerts.Alert) []alerts.PushAction {
	mac, _ := al.Data["mac"].(string)
	if id, _ := al.Data["request"].(string); al.Kind == "site_request" && id != "" {
		reply := a.replyTopic()
		if reply == "" {
			return nil
		}
		exp := time.Now().Add(linkTTL)
		return []alerts.PushAction{{Label: "Allow", URL: reply, Body: a.approvalToken("allow", id, exp)}, {Label: "Deny", URL: reply, Body: a.approvalToken("deny", id, exp)}}
	}
	if id, _ := al.Data["checkin"].(string); al.Kind == "checkin" && id != "" { // a device checked in and waits
		reply := a.replyTopic()
		if reply == "" {
			return nil
		}
		exp := time.Now().Add(linkTTL)
		return []alerts.PushAction{{Label: "Approve", URL: reply, Body: a.approvalToken("ci-approve", id, exp)}, {Label: "Reject", URL: reply, Body: a.approvalToken("ci-reject", id, exp)}}
	}
	if al.Kind != "approval" || mac == "" || !a.Cfg.Get().Onboard.Approve {
		return nil
	}
	reply := a.replyTopic()
	if reply == "" {
		return nil
	}
	exp := time.Now().Add(linkTTL)
	out := []alerts.PushAction{{Label: "Approve", URL: reply, Body: a.approvalToken("approve", mac, exp)}}
	if al.Data["grace"] == true {
		out = append(out, alerts.PushAction{Label: "Block", URL: reply, Body: a.approvalToken("block", mac, exp)})
	}
	return out
}

// useDecision carries out a token from a button (once), and tells everyone on the alert topic.
func (a *App) useDecision(t string) error {
	op, mac, sig, exp, err := a.checkToken(t)
	if err != nil {
		return err
	}
	s := a.approval
	s.mu.Lock()
	st := s.load()
	if st.Used == nil {
		st.Used = map[string]int64{}
	}
	if st.Used[sig] > 0 {
		s.mu.Unlock()
		return errors.New("already done")
	}
	name := mac
	if op == "ci-approve" || op == "ci-reject" {
		st.Used[sig] = exp
		_ = s.save(st)
		s.mu.Unlock()
		return a.checkinDecision(mac, op)
	}
	if op == "allow" || op == "deny" {
		st.Used[sig] = exp
		_ = s.save(st)
		s.mu.Unlock()
		r, err := a.decideRequest(mac, op, "push")
		what := orStr(r.Domain, "the site")
		if err != nil {
			a.Alerts.Add(alerts.Alert{Kind: "approval_done", Level: "warn", Title: "Couldn't " + op + " " + what, Body: err.Error()})
			return err
		}
		a.Alerts.Add(alerts.Alert{Kind: "approval_done", Title: map[string]string{"allow": "✓ " + what + " allowed", "deny": what + " stays blocked"}[op],
			Body: "For " + r.Device + ". Decided from a phone notification."})
		return nil
	}
	for _, d := range []pendingDevice{st.Grace[mac], st.Blocked[mac]} {
		if d.MAC != "" {
			name = orStr(d.Name, orStr(d.Hostname, mac))
		}
	}
	st.Used[sig] = exp
	for k, e := range st.Used {
		if e < time.Now().Unix() {
			delete(st.Used, k)
		}
	}
	_ = s.save(st)
	s.mu.Unlock()
	if _, err := a.approvalAPI(op, map[string]any{"mac": mac}, "push"); err != nil {
		a.Alerts.Add(alerts.Alert{Kind: "approval_done", Level: "warn", Title: "Couldn't " + op + " " + name, Body: err.Error()})
		return err
	}
	a.Alerts.Add(alerts.Alert{Kind: "approval_done", Title: map[string]string{"approve": "✓ " + name + " approved", "block": name + " blocked"}[op],
		Body: "Decided from a phone notification."})
	return nil
}

// replyLoop listens to the reply topic while pushes with buttons are possible.
func (a *App) replyLoop(ctx context.Context) {
	since := "10m"
	for {
		wait := time.Minute
		if topic := a.replyTopic(); topic != "" && (a.Cfg.Get().Onboard.Approve || a.requests.pending() > 0 || a.checkinsWaiting()) {
			last, err := listenReplies(ctx, topic, since, a.useDecisionLogged)
			if last != "" {
				since = last
			}
			if err == nil {
				wait = 2 * time.Second // the server closed the stream: reconnect
			} else if ctx.Err() == nil {
				log.Printf("approval replies: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (a *App) useDecisionLogged(t string) {
	if err := a.useDecision(t); err != nil && err.Error() != "already done" {
		log.Printf("approval replies: %v", err)
	}
}

var replyClient = &http.Client{} // a long-lived stream: no overall timeout

// listenReplies streams ntfy's JSON messages from the topic (since an id or a duration) and hands each one over.
func listenReplies(ctx context.Context, topic, since string, use func(string)) (last string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", topic+"/json?since="+url.QueryEscape(since), nil)
	if err != nil {
		return "", err
	}
	resp, err := replyClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("ntfy answered " + resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 4096), 64<<10)
	for sc.Scan() {
		var m struct{ ID, Event, Message string }
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Event != "message" {
			continue
		}
		last = m.ID
		if len(m.Message) < 400 {
			use(m.Message)
		}
	}
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return last, nil // our own 30-minute cut: reconnect
	}
	return last, sc.Err()
}

func (a *App) checkinsWaiting() bool {
	for _, x := range a.checkins.List() {
		if x.Status == "pending" {
			return true
		}
	}
	return false
}

// checkinDecision approves or rejects a checked-in device from a push button. Approving gives it to the person it
// named and their NextDNS profile, as the admin page suggests; the rest can be changed there later.
func (a *App) checkinDecision(id, op string) error {
	x, ok := a.checkins.Get(id)
	if !ok || x.Status != "pending" {
		return errors.New("already done")
	}
	if op == "ci-reject" {
		if err := a.rejectCheckIn(id, "push"); err != nil {
			return err
		}
		a.Alerts.Add(alerts.Alert{Kind: "approval_done", Title: x.Name + " rejected", Body: "Decided from a phone notification."})
		return nil
	}
	body := map[string]any{"id": id}
	if a.An.Open() {
		for _, p := range a.An.People() {
			if p.ID == x.PersonID || x.PersonID == "" && strings.EqualFold(p.Name, x.PersonName) && x.PersonName != "" {
				if nd := personNDProfile(p); nd != "" {
					body["nd_profile"] = nd
				}
			}
		}
	}
	res, err := a.approveCheckIn(body, "push")
	if err != nil {
		a.Alerts.Add(alerts.Alert{Kind: "approval_done", Level: "warn", Title: "Couldn't approve " + x.Name, Body: err.Error()})
		return err
	}
	if m, _ := res.(map[string]any); m != nil && m["warning"] != nil {
		a.Alerts.Add(alerts.Alert{Kind: "approval_done", Level: "warn", Title: x.Name + " approved, but", Body: str(m, "warning")})
		return nil
	}
	a.Alerts.Add(alerts.Alert{Kind: "approval_done", Title: "✓ " + x.Name + " approved", Body: "Decided from a phone notification. Its check-in page now offers its setup."})
	return nil
}
