package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// One approval for a new device, whichever button is used: the check-in replaces the grace-period request, and
// approving it (page, notification or Grace tab) names it in UniFi, gives it to the person and lets it on.
func TestOneApproval(t *testing.T) {
	resetWlans()
	const (
		mac1, uid1 = "aa:bb:cc:00:00:41", "5f0000000000000000000041"
		mac2, uid2 = "aa:bb:cc:00:00:42", "5f0000000000000000000042"
		mac3       = "aa:bb:cc:00:00:43"
	)
	var mu sync.Mutex
	names := map[string]string{} // user id → name UniFi was given
	var cmds []string
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		ok := func(data string) { _, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":` + data + `}`)) }
		switch {
		case strings.HasSuffix(p, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(p, "/stat/sta"):
			ok(`[{"ip":"192.168.1.41","mac":"` + mac1 + `","hostname":"iPhone","oui":"Apple","user_id":"` + uid1 + `","network":"Default"},
				{"ip":"192.168.1.42","mac":"` + mac2 + `","hostname":"iPad","oui":"Apple","user_id":"` + uid2 + `","network":"Default"}]`)
		case strings.Contains(p, "/rest/user/"):
			id := p[strings.LastIndex(p, "/")+1:]
			mu.Lock()
			if r.Method == "PUT" {
				names[id], _ = m["name"].(string)
			}
			n := names[id]
			mu.Unlock()
			ok(`[{"_id":"` + id + `","name":"` + n + `"}]`)
		case strings.HasSuffix(p, "/cmd/stamgr"):
			mu.Lock()
			cmds = append(cmds, s(m, "cmd")+" "+s(m, "mac"))
			mu.Unlock()
			ok(`[]`)
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
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.Approve, c.Onboard.GraceMinutes = true, 60 })
	a.approval.mu.Lock()
	_ = a.approval.save(approvalState{Approved: map[string]int64{"aa:bb:cc:00:00:01": 1}, Blocked: map[string]pendingDevice{}, Seeded: 1, SeedRecent: true})
	a.approval.mu.Unlock()
	state := func() approvalState { a.approval.mu.Lock(); defer a.approval.mu.Unlock(); return a.approval.load() }
	online := []map[string]any{{"mac": mac1, "hostname": "iPhone"}}

	// a new phone joins: its grace period starts
	a.approvalTick(online)
	if _, ok := state().Grace[mac1]; !ok {
		t.Fatal("grace period")
	}
	// it checks in as Sam's: that's the request now — no grace, no block, no second alert
	if code, out := hi(a, "POST", "192.168.1.41", `{"name":"Sam's iPhone","kind":"phone","person_name":"Sam"}`, nil); code != 200 {
		t.Fatalf("check-in: %d %v", code, out)
	}
	a.approvalTick(online)
	if st := state(); len(st.Grace) != 0 || len(st.Blocked) != 0 {
		t.Fatalf("a checked-in device waits for its check-in only: %+v", st)
	}
	// approved from Devices › Grace period: the check-in is approved with what was entered on the phone
	if _, err := a.approvalAPI("approve", map[string]any{"mac": mac1}, "admin"); err != nil {
		t.Fatal(err)
	}
	if x, ok := a.checkins.Registered(mac1); !ok || x.Name != "Sam's iPhone" || x.PersonID == "" {
		t.Fatalf("check-in: %+v", x)
	}
	if names[uid1] != "Sam's iPhone" {
		t.Fatalf("UniFi name: %v", names)
	}
	sam := ""
	for _, p := range a.An.People() {
		if p.Name == "Sam" {
			sam = p.ID
			if len(p.Devices) == 0 || p.Devices[0].Key != mac1 {
				t.Fatalf("Sam's devices: %+v", p.Devices)
			}
		}
	}
	if sam == "" || state().Approved[mac1] == 0 {
		t.Fatal("Sam, and approved for good")
	}

	// a device HomeOwl had blocked checks in later (from the setup Wi-Fi); approving from the notification unblocks it
	a.approval.mu.Lock()
	st := a.approval.load()
	st.Blocked[mac2] = pendingDevice{MAC: mac2, At: time.Now().Unix()}
	_ = a.approval.save(st)
	a.approval.mu.Unlock()
	if err := a.checkins.Add(CheckIn{MAC: mac2, UserID: uid2, Name: "Sam's iPad", Kind: "tablet", PersonID: sam}); err != nil {
		t.Fatal(err)
	}
	x2, _ := a.checkins.pendingFor(mac2)
	if err := a.checkinDecision(x2.ID, "ci-approve"); err != nil {
		t.Fatal(err)
	}
	if _, still := state().Blocked[mac2]; still || names[uid2] != "Sam's iPad" || !contains(cmds, "unblock-sta "+mac2) {
		t.Fatalf("unblocked and named: %v %v", cmds, names)
	}

	// rejecting a check-in blocks the device (or it would ask again as a new device)
	if err := a.checkins.Add(CheckIn{MAC: mac3, Name: "Unknown laptop", Kind: "laptop", PersonName: "Someone"}); err != nil {
		t.Fatal(err)
	}
	x3, _ := a.checkins.pendingFor(mac3)
	if err := a.rejectCheckIn(x3.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, blocked := state().Blocked[mac3]; !blocked || !contains(cmds, "block-sta "+mac3) {
		t.Fatalf("rejected → blocked: %v", cmds)
	}
}

// A device blocked earlier and approved through its check-in (before HomeOwl unblocked on approval) is let on.
func TestApprovalRepairsStuck(t *testing.T) {
	a := checkinApp(t)
	_ = a.Cfg.Update(func(c *config.Config) { c.Onboard.Approve = true })
	mac := "aa:bb:cc:00:00:40"
	a.approval.mu.Lock()
	_ = a.approval.save(approvalState{Approved: map[string]int64{"aa:bb:cc:00:00:01": 1}, Seeded: 1, SeedRecent: true,
		Blocked: map[string]pendingDevice{mac: {MAC: mac, At: time.Now().Add(-time.Hour).Unix()}}})
	a.approval.mu.Unlock()
	if err := a.checkins.Add(CheckIn{MAC: mac, Name: "Alex's iPad", Kind: "tablet", PersonName: "Alex"}); err != nil {
		t.Fatal(err)
	}
	x, _ := a.checkins.pendingFor(mac)
	_ = a.checkins.Decide(x.ID, "approved", "admin")
	a.approvalTick([]map[string]any{{"mac": mac, "blocked": true}})
	a.approval.mu.Lock()
	st := a.approval.load()
	a.approval.mu.Unlock()
	if _, still := st.Blocked[mac]; still || st.Approved[mac] == 0 {
		t.Fatalf("still blocked: %+v", st)
	}
}
