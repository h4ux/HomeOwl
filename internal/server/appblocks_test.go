package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
)

// fakeDeny is a NextDNS denylist per profile.
type fakeDeny struct {
	lists map[string]map[string]bool
	calls []string
	fail  bool
}

func (f *fakeDeny) NDDenylist(p string) ([]analytics.NDDeny, error) {
	if f.fail {
		return nil, errors.New("NextDNS API 503")
	}
	var out []analytics.NDDeny
	for d, on := range f.lists[p] {
		out = append(out, analytics.NDDeny{ID: d, Active: on})
	}
	return out, nil
}
func (f *fakeDeny) NDDenyAdd(p, d string, on bool) error {
	if f.lists[p] == nil {
		f.lists[p] = map[string]bool{}
	}
	f.lists[p][d] = on
	f.calls = append(f.calls, "add "+p+" "+d)
	return nil
}
func (f *fakeDeny) NDDenySet(p, d string, on bool) error {
	f.lists[p][d] = on
	f.calls = append(f.calls, "set "+p+" "+d)
	return nil
}
func (f *fakeDeny) NDDenyRemove(p, d string) error {
	delete(f.lists[p], d)
	f.calls = append(f.calls, "remove "+p+" "+d)
	return nil
}

func TestBlockedNow(t *testing.T) {
	at := func(day time.Weekday, h, m int) time.Time { // 2026-11-01 is a Sunday
		return time.Date(2026, 11, 1+int(day), h, m, 0, 0, time.Local)
	}
	night := AppBlock{Mode: "schedule", Days: []int{0, 1, 2, 3, 4}, From: "21:00", To: "07:00"} // school nights
	for _, c := range []struct {
		t    time.Time
		want bool
	}{
		{at(time.Sunday, 20, 59), false}, {at(time.Sunday, 21, 0), true}, {at(time.Monday, 6, 59), true},
		{at(time.Monday, 7, 0), false}, {at(time.Friday, 22, 0), false}, {at(time.Friday, 6, 0), true}, // Thursday night
		{at(time.Saturday, 6, 0), false},
	} {
		if got := night.blockedNow(c.t); got != c.want {
			t.Errorf("%s: %v, want %v", c.t.Format("Mon 15:04"), got, c.want)
		}
	}
	day := AppBlock{Mode: "schedule", Days: []int{1}, From: "08:00", To: "15:00"}
	if !day.blockedNow(at(time.Monday, 9, 0)) || day.blockedNow(at(time.Monday, 15, 0)) || day.blockedNow(at(time.Tuesday, 9, 0)) {
		t.Error("daytime window")
	}
	if !(AppBlock{Mode: "always"}).blockedNow(time.Now()) {
		t.Error("always")
	}
}

func TestAppBlocks(t *testing.T) {
	a := checkinApp(t)
	f := &fakeDeny{lists: map[string]map[string]bool{"abc123": {"tiktok.com": true}}} // already blocked by hand
	old := appDenyFor
	appDenyFor = func(*App) ndDeny { return f }
	defer func() { appDenyFor = old }()

	if _, err := a.saveAppBlock(map[string]any{"app": "nope", "profiles": []any{"abc123"}}, "admin"); err == nil {
		t.Fatal("unknown app accepted")
	}
	if _, err := a.saveAppBlock(map[string]any{"app": "tiktok", "profiles": []any{"../x"}}, "admin"); err == nil {
		t.Fatal("bad profile accepted")
	}
	if _, err := a.saveAppBlock(map[string]any{"app": "tiktok", "profiles": []any{"abc123"}, "mode": "schedule", "days": []any{}, "from": "21:00", "to": "07:00"}, "admin"); err == nil {
		t.Fatal("schedule without days accepted")
	}
	if _, err := a.saveAppBlock(map[string]any{"app": "tiktok", "profiles": []any{"abc123", "def456"}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if !f.lists["def456"]["tiktokv.com"] || !f.lists["abc123"]["musical.ly"] {
		t.Fatalf("domains not added: %v", f.lists)
	}
	b := a.appBlocks.load()[0]
	if inList(b.Owned["abc123"], "tiktok.com") || !inList(b.Owned["def456"], "tiktok.com") {
		t.Fatalf("a pre-existing entry must not be taken over: %v", b.Owned)
	}
	if _, err := a.saveAppBlock(map[string]any{"app": "tiktok", "profiles": []any{"def456"}}, "admin"); err == nil {
		t.Fatal("two blocks of one app on one profile")
	}

	// a schedule that's off now switches HomeOwl's entries off; the hand-made one stays on
	now := time.Now()
	off := now.Add(2 * time.Hour).Format("15:04")
	off2 := now.Add(3 * time.Hour).Format("15:04")
	days := []any{0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0} // every day, so the test passes at any hour
	if _, err := a.saveAppBlock(map[string]any{"id": b.ID, "app": "tiktok", "profiles": []any{"abc123"}, "mode": "schedule", "days": days, "from": off, "to": off2}, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(f.lists["def456"]) != 0 {
		t.Fatalf("a profile taken off the block keeps entries: %v", f.lists["def456"])
	}
	if !f.lists["abc123"]["tiktok.com"] || f.lists["abc123"]["musical.ly"] {
		t.Fatalf("schedule off: %v", f.lists["abc123"])
	}
	// the scheduler switches it on when the time comes
	b = a.appBlocks.load()[0]
	f.calls = nil
	a.appBlocksTick(now.Add(2*time.Hour + time.Minute))
	if !f.lists["abc123"]["musical.ly"] || len(f.calls) == 0 {
		t.Fatalf("tick: %v %v", f.lists, f.calls)
	}
	f.calls = nil
	a.appBlocksTick(now.Add(2*time.Hour + 2*time.Minute))
	if len(f.calls) != 0 {
		t.Fatalf("nothing to do, yet it called NextDNS: %v", f.calls)
	}
	// failures are kept and retried
	f.fail = true
	a.appBlocksTick(now.Add(3*time.Hour + time.Minute))
	if b = a.appBlocks.load()[0]; b.Applied != nil || !strings.Contains(b.Error, "503") {
		t.Fatalf("error not kept: %+v", b)
	}
	f.fail = false
	a.appBlocksTick(now.Add(3*time.Hour + 10*time.Minute))
	if b = a.appBlocks.load()[0]; b.Applied == nil || *b.Applied || f.lists["abc123"]["musical.ly"] {
		t.Fatalf("retry: %+v %v", b, f.lists)
	}

	// deleting removes only what HomeOwl added
	if err := a.deleteAppBlock(b.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(f.lists["abc123"]) != 1 || !f.lists["abc123"]["tiktok.com"] || len(a.appBlocks.load()) != 0 {
		t.Fatalf("delete: %v", f.lists)
	}
	n := 0
	for _, c := range a.Changes.List() {
		if c.Host == "api.nextdns.io" {
			n++
		}
	}
	if n < 4 {
		t.Fatalf("changes not logged: %d", n)
	}
}
