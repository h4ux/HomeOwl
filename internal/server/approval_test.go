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

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/h4ux/homeowl/internal/config"
)

func TestDeviceApproval(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	now := time.Now().Unix()
	known := fmt.Sprintf(`[{"mac":"00:11:22:00:00:01","name":"Living room TV","last_seen":%d},{"mac":"00:11:22:00:00:02","hostname":"alexs-laptop","last_seen":%d},
		{"mac":"00:11:22:00:00:03","hostname":"old-tablet","last_seen":%d}]`, now-600, now-3600*20, now-86400*2)
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/user"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":` + known + `}`))
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
	if err := a.An.OpenDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.SSID = "Home Setup" })
	got := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), cmds...) }

	stranger := map[string]any{"mac": "00:11:22:00:00:99", "hostname": "unknown-tv", "essid": "Example Home"}
	// off: nothing happens
	a.approvalTick([]map[string]any{stranger})
	if len(got()) != 0 {
		t.Fatal("approval is off")
	}
	if _, err := a.approvalAPI("set", map[string]any{"on": true}, "admin"); err != nil {
		t.Fatal(err)
	}
	if v := a.approvalView(); v["on"] != true || v["approved"] != 2 {
		t.Fatalf("seeded from the devices seen in the last day: %v", v)
	}
	// seen two days ago: not approved, so it waits when it comes back
	a.approvalTick([]map[string]any{{"mac": "00:11:22:00:00:03", "hostname": "old-tablet", "essid": "Example Home"}})
	if c := got(); len(c) != 1 || c[0] != "block-sta 00:11:22:00:00:03" {
		t.Fatalf("a device not seen for two days waits for approval: %v", c)
	}
	mu.Lock()
	cmds = nil
	mu.Unlock()
	ast := a.approval.load()
	delete(ast.Blocked, "00:11:22:00:00:03")
	_ = a.approval.save(ast)
	// a checked-in phone moving to the home Wi-Fi (same host name) isn't blocked while HomeOwl follows it
	_ = a.checkins.Add(CheckIn{MAC: "aa:bb:cc:00:00:20", Name: "Alex's Phone"})
	x := a.checkins.List()[0]
	_ = a.checkins.Decide(x.ID, "approved", "admin")
	a.checkins.Edit(x.ID, func(y *CheckIn) { y.Onboard, y.FixedFrom, y.FixedHost = true, "aa:bb:cc:00:00:20", "alexs-phone" })

	a.approvalTick([]map[string]any{
		{"mac": "00:11:22:00:00:01", "name": "Living room TV", "essid": "Example Home"}, // known
		{"mac": "00:11:22:00:00:50", "hostname": "printer", "is_wired": true},           // wired: not gated
		{"mac": "00:11:22:00:00:51", "hostname": "friend", "essid": "Example Guests", "is_guest": true},
		{"mac": "aa:bb:cc:00:00:52", "hostname": "new-phone", "essid": "Home Setup", "is_guest": true}, // the setup Wi-Fi
		{"mac": "06:bb:cc:00:00:20", "hostname": "Alexs-Phone", "essid": "Example Home"},               // moving here
		stranger,
	})
	if c := got(); len(c) != 1 || c[0] != "block-sta 00:11:22:00:00:99" {
		t.Fatalf("only the stranger is blocked: %v", c)
	}
	alerted := false
	for _, al := range a.Alerts.List() {
		if al.Kind == "approval" && al.Data["mac"] == "00:11:22:00:00:99" {
			alerted = true
		}
	}
	if !alerted || !a.approvalBlocked("00:11:22:00:00:99") {
		t.Fatal("the admin gets an alert to approve it")
	}
	a.approvalTick([]map[string]any{stranger})
	if len(got()) != 1 {
		t.Fatal("blocked once, not every minute")
	}
	// a registered check-in is approved without asking
	_ = a.checkins.Add(CheckIn{MAC: "00:11:22:00:00:30", Name: "Sam's iPad"})
	for _, y := range a.checkins.List() {
		if y.Status == "pending" {
			_ = a.checkins.Decide(y.ID, "approved", "admin")
		}
	}
	a.approvalTick([]map[string]any{{"mac": "00:11:22:00:00:30", "hostname": "ipad", "essid": "Example Home"}})
	if len(got()) != 1 {
		t.Fatal("checked-in devices aren't blocked")
	}

	// approve: unblocked, given to Alex, and it stays approved
	alex, _ := a.An.SavePerson(analytics.Person{Name: "Alex"})
	if _, err := a.approvalAPI("approve", map[string]any{"mac": "00:11:22:00:00:99", "person_id": alex}, "admin"); err != nil {
		t.Fatal(err)
	}
	if c := got(); c[len(c)-1] != "unblock-sta 00:11:22:00:00:99" {
		t.Fatalf("approve unblocks: %v", c)
	}
	a.approvalTick([]map[string]any{stranger})
	if len(got()) != 2 || a.approvalBlocked("00:11:22:00:00:99") {
		t.Fatal("an approved device stays approved")
	}
	// keep blocked: no unblock, no more alerts
	other := map[string]any{"mac": "00:11:22:00:00:98", "hostname": "neighbour", "essid": "Example Home"}
	a.approvalTick([]map[string]any{other})
	if _, err := a.approvalAPI("keep", map[string]any{"mac": "00:11:22:00:00:98"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if v := a.approvalView(); len(v["kept"].([]pendingDevice)) != 1 || len(v["waiting"].([]pendingDevice)) != 0 {
		t.Fatalf("kept: %v", v)
	}
	// a flood blocks nobody
	var flood []map[string]any
	for i := 0; i < 11; i++ {
		flood = append(flood, map[string]any{"mac": fmt.Sprintf("00:11:22:00:01:%02x", i), "essid": "Example Home"})
	}
	n := len(got())
	a.approvalTick(flood)
	if len(got()) != n {
		t.Fatal("more than 10 at once: block none and alert")
	}
	// every block is in the change log
	logged := 0
	for _, c := range a.Changes.List() {
		if strings.HasPrefix(c.Description, "Device approval") {
			logged++
		}
	}
	if logged != len(got())+1 { // + the old tablet's block, cleared from the list above
		t.Fatalf("logged %d of %d", logged, len(got())+1)
	}
}

func TestApprovalTrimOldSeed(t *testing.T) {
	now := time.Now().Unix()
	known := fmt.Sprintf(`[{"mac":"00:11:22:00:00:01","last_seen":%d},{"mac":"00:11:22:00:00:03","last_seen":%d},{"mac":"00:11:22:00:00:04","last_seen":%d}]`, now-60, now-86400*5, now-86400*9)
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/rest/user"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":` + known + `}`))
		case strings.HasSuffix(r.URL.Path, "/stat/sta"):
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
		default:
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
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
	// an older seed approved everything at once; 00:04 was approved by hand later
	seed := now - 86400*30
	_ = a.approval.save(approvalState{Seeded: seed, Approved: map[string]int64{"00:11:22:00:00:01": seed, "00:11:22:00:00:03": seed, "00:11:22:00:00:04": seed + 100}})
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.Approve = true })
	a.approvalTick(nil) // trims once
	st := a.approval.load()
	if !st.SeedRecent || st.Approved["00:11:22:00:00:01"] == 0 || st.Approved["00:11:22:00:00:03"] != 0 || st.Approved["00:11:22:00:00:04"] == 0 {
		t.Fatalf("old seed trimmed, recent and hand approvals kept: %+v", st)
	}
}
