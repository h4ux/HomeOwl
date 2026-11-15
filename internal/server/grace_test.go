package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
)

func TestApprovalGraceAndLinks(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/user"):
			_, _ = w.Write([]byte(fmt.Sprintf(`{"meta":{"rc":"ok"},"data":[{"mac":"00:11:22:00:00:01","name":"TV","last_seen":%d}]}`, time.Now().Unix())))
		case strings.HasSuffix(r.URL.Path, "/stat/sta"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/cmd/stamgr"):
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			mu.Lock()
			cmds = append(cmds, fmt.Sprint(m["cmd"], " ", m["mac"]))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer console.Close()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	got := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), cmds...) }
	if _, err := a.approvalAPI("set", map[string]any{"on": true}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.approvalAPI("set", map[string]any{"grace_minutes": float64(60)}, "admin"); err != nil {
		t.Fatal(err)
	}
	ipad := map[string]any{"mac": "02:00:00:00:00:30", "hostname": "iPad", "essid": "Example Home"}
	a.approvalTick([]map[string]any{ipad})
	if len(got()) != 0 {
		t.Fatalf("in its grace period: not blocked: %v", got())
	}
	if v := a.approvalView(); len(v["grace"].([]map[string]any)) != 1 {
		t.Fatalf("listed in grace: %v", v)
	}
	var al alerts.Alert
	for _, x := range a.Alerts.List() {
		if x.Kind == "approval" {
			al = x
		}
	}
	if !strings.Contains(al.Body, "1 hour") {
		t.Fatalf("alert says when: %+v", al)
	}
	// the push gets Approve and Block buttons that post to a private reply topic (tokens never stored in the alert)
	if err := a.Sec.Set(slotNtfy, "https://ntfy.example.com/family-alerts"); err != nil {
		t.Fatal(err)
	}
	acts := a.pushActions(al)
	if len(acts) != 2 || acts[0].Label != "Approve" || acts[1].Label != "Block" || !strings.HasPrefix(acts[0].URL, "https://ntfy.example.com/family-alerts-r") || acts[0].Body == "" {
		t.Fatalf("push actions: %v", acts)
	}
	if _, ok := al.Data["actions"]; ok {
		t.Fatal("tokens aren't stored with the alert")
	}
	// grace over: blocked
	st := a.approval.load()
	g := st.Grace["02:00:00:00:00:30"]
	g.At -= 3601
	st.Grace["02:00:00:00:00:30"] = g
	_ = a.approval.save(st)
	a.approvalTick([]map[string]any{ipad})
	if c := got(); len(c) != 1 || c[0] != "block-sta 02:00:00:00:00:30" {
		t.Fatalf("blocked after the grace period: %v", c)
	}
	// a tap on Approve: works once, only with a genuine, current token
	tok := a.approvalToken("approve", "02:00:00:00:00:30", time.Now().Add(time.Hour))
	if err := a.useDecision(tok[:len(tok)-2] + "xx"); err == nil {
		t.Fatal("tampered")
	}
	if err := a.useDecision(a.approvalToken("approve", "02:00:00:00:00:30", time.Now().Add(-time.Minute))); err == nil {
		t.Fatal("expired")
	}
	if err := a.useDecision(tok); err != nil {
		t.Fatal(err)
	}
	if c := got(); c[len(c)-1] != "unblock-sta 02:00:00:00:00:30" || a.approval.load().Approved["02:00:00:00:00:30"] == 0 {
		t.Fatalf("unblocked and approved: %v", c)
	}
	if err := a.useDecision(tok); err == nil {
		t.Fatal("used once")
	}
	// the reply stream: ntfy's JSON lines
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":"open"}` + "\n" + `{"id":"m1","event":"message","message":"hello"}` + "\n"))
	}))
	defer stream.Close()
	var seen []string
	last, err := listenReplies(context.Background(), stream.URL, "10m", func(m string) { seen = append(seen, m) })
	if err != nil || last != "m1" || len(seen) != 1 || seen[0] != "hello" {
		t.Fatalf("stream: %v %q %v", err, last, seen)
	}
	// block now, from the grace period
	tab := map[string]any{"mac": "02:00:00:00:00:31", "hostname": "tablet", "essid": "Example Home"}
	a.approvalTick([]map[string]any{tab})
	if _, err := a.approvalAPI("block", map[string]any{"mac": "02:00:00:00:00:31"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if c := got(); c[len(c)-1] != "block-sta 02:00:00:00:00:31" {
		t.Fatalf("blocked now: %v", c)
	}
}
