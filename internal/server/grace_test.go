package server

import (
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
	"github.com/h4ux/homeowl/internal/config"
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
	// the push gets Approve and Block links (not stored in the alert)
	_ = a.Cfg.Update(func(c *config.Config) { c.LAN.Enabled = true })
	acts := a.pushActions(al)
	if len(acts) != 2 || acts[0].Label != "Approve" || acts[1].Label != "Block" {
		t.Fatalf("push actions: %v", acts)
	}
	if _, ok := al.Data["actions"]; ok {
		t.Fatal("links aren't stored with the alert")
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
	// the push's Approve link: works once, only with a genuine token
	tok := a.approvalToken("approve", "02:00:00:00:00:30", time.Now().Add(time.Hour))
	call := func(method, tk string) int {
		w := httptest.NewRecorder()
		a.approvalLinkAPI(w, httptest.NewRequest(method, "/api/approval-link?t="+tk, nil))
		return w.Code
	}
	if c := call("GET", tok); c != 200 || len(got()) != 1 {
		t.Fatalf("GET only asks: %d %v", c, got())
	}
	if c := call("POST", tok[:len(tok)-2]+"xx"); c != 403 {
		t.Fatalf("tampered: %d", c)
	}
	if c := call("POST", a.approvalToken("approve", "02:00:00:00:00:30", time.Now().Add(-time.Minute))); c != 403 {
		t.Fatalf("expired: %d", c)
	}
	if c := call("POST", tok); c != 200 {
		t.Fatalf("approved: %d", c)
	}
	if c := got(); c[len(c)-1] != "unblock-sta 02:00:00:00:00:30" || a.approval.load().Approved["02:00:00:00:00:30"] == 0 {
		t.Fatalf("unblocked and approved: %v", c)
	}
	if c := call("POST", tok); c != 410 {
		t.Fatalf("used once: %d", c)
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
