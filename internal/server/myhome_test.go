package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAllow struct{ calls []string }

func (f *fakeAllow) NDAllowAdd(p, d string) error {
	f.calls = append(f.calls, "add "+p+" "+d)
	return nil
}
func (f *fakeAllow) NDAllowRemove(p, d string) error {
	f.calls = append(f.calls, "remove "+p+" "+d)
	return nil
}

func TestNormDomain(t *testing.T) {
	for in, want := range map[string]string{"Example.com": "example.com", "https://www.example.com/a?b": "www.example.com", "example.com.": "example.com",
		"example.com:443": "example.com", "localhost": "", "a b.com": "", "": "", "-x.com": "", "192.168.1.1": ""} {
		if got := normDomain(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestSiteRequests(t *testing.T) {
	a := checkinApp(t)
	f := &fakeAllow{}
	old := ndAllowFor
	ndAllowFor = func(*App) ndAllow { return f }
	defer func() { ndAllowFor = old }()
	_ = a.checkins.Add(CheckIn{MAC: "aa:bb:cc:00:00:40", IP: "192.168.1.40", Name: "Sam's iPad", PersonName: "Sam"})
	id := a.checkins.List()[0].ID
	_ = a.checkins.Decide(id, "approved", "admin")

	post := func(body string) (int, map[string]any) {
		r := httptest.NewRequest("POST", "http://homeowl.lan:8555/api/hi/request", strings.NewReader(body))
		r.RemoteAddr = "192.168.1.40:51234"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.hiRequest(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	// without NextDNS there's nothing to allow
	if code, _ := post(`{"domain":"example.com"}`); code != 400 {
		t.Fatalf("no NextDNS profile: %d", code)
	}
	a.checkins.Edit(id, func(x *CheckIn) { x.NDProfile = "abc123" })

	// the device's own page: status, connection, requests
	code, out := hi(a, "GET", "192.168.1.40", "", nil)
	mine, _ := out["mine"].(map[string]any)
	if code != 200 || mine == nil || mine["setup"] == nil || mine["connection"] == nil || mine["can_request"] != true {
		t.Fatalf("my device: %d %v", code, out)
	}

	if code, _ := post(`{"domain":"not a site"}`); code != 400 {
		t.Fatal("bad domain")
	}
	code, out = post(`{"domain":"https://games.example.com/play","note":"for homework"}`)
	req, _ := out["request"].(map[string]any)
	if code != 200 || req["domain"] != "games.example.com" || req["status"] != "pending" || req["mac"] != nil {
		t.Fatalf("request: %d %v", code, out)
	}
	rid := req["id"].(string)
	if _, again := post(`{"domain":"games.example.com"}`); again["request"].(map[string]any)["id"] != rid {
		t.Fatal("asking again is the same request")
	}
	var al = a.Alerts.List()[len(a.Alerts.List())-1]
	if al.Kind != "site_request" || !strings.Contains(al.Title, "Sam asks to allow games.example.com") || !strings.Contains(al.Body, "for homework") {
		t.Fatalf("alert: %+v", al)
	}
	if v := a.alertsView(true); len(v["requests"].([]SiteRequest)) != 1 {
		t.Fatal("waiting on Home")
	}
	// at most five waiting per device
	for _, d := range []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"} {
		if code, _ := post(`{"domain":"` + d + `"}`); code != 200 {
			t.Fatal(d)
		}
	}
	if code, _ := post(`{"domain":"e.example.com"}`); code != 400 {
		t.Fatal("sixth request")
	}

	// the push: Allow and Deny, decided once
	if err := a.Sec.Set(slotNtfy, "https://ntfy.example.com/family-alerts"); err != nil {
		t.Fatal(err)
	}
	acts := a.pushActions(al)
	if len(acts) != 2 || acts[0].Label != "Allow" || acts[1].Label != "Deny" {
		t.Fatalf("push actions: %v", acts)
	}
	if err := a.useDecision(acts[0].Body); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != "add abc123 games.example.com" {
		t.Fatalf("allowlisted: %v", f.calls)
	}
	if err := a.useDecision(acts[1].Body); err == nil {
		t.Fatal("already allowed: deny doesn't apply")
	}
	for _, x := range a.Alerts.List() {
		if x.ID == al.ID && !x.Read {
			t.Fatal("the request's alert is done")
		}
	}
	// logged, and an admin can take it off again
	found := false
	for _, c := range a.Changes.List() {
		if strings.Contains(c.Description, "Allow games.example.com") && c.OK {
			found = true
		}
	}
	if !found {
		t.Fatal("in the change log")
	}
	if _, err := a.decideRequest(rid, "remove", "admin"); err != nil || f.calls[1] != "remove abc123 games.example.com" {
		t.Fatalf("removed: %v %v", err, f.calls)
	}
	if _, err := a.decideRequest(rid, "deny", "admin"); err == nil {
		t.Fatal("removed: nothing to deny")
	}
	// the device sees the answers, not who decided or the profile
	_, out = hi(a, "GET", "192.168.1.40", "", nil)
	reqs := out["mine"].(map[string]any)["requests"].([]any)
	last := reqs[len(reqs)-1].(map[string]any)
	if len(reqs) != 5 || last["status"] != "removed" || last["by"] != nil || last["profile"] != nil {
		t.Fatalf("my requests: %v", reqs)
	}
}

func TestHiGuest(t *testing.T) {
	a := checkinApp(t)
	get := func(ip string) (int, map[string]any) {
		r := httptest.NewRequest("GET", "http://homeowl.lan:8555/api/hi/guest", nil)
		r.RemoteAddr = ip + ":51234"
		w := httptest.NewRecorder()
		a.hiGuest(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	_ = a.checkins.Add(CheckIn{MAC: "aa:bb:cc:00:00:40", IP: "192.168.1.40", Name: "Sam's iPad", PersonName: "Sam"})
	if code, out := get("192.168.1.40"); code != 404 || out["networks"] != nil {
		t.Fatalf("not approved yet: %d %v", code, out)
	}
	_ = a.checkins.Decide(a.checkins.List()[0].ID, "approved", "admin")
	code, out := get("192.168.1.40")
	nets, _ := out["networks"].([]any)
	if code != 200 || len(nets) != 1 || nets[0].(map[string]any)["ssid"] != "Example Guests" || nets[0].(map[string]any)["password"] != "guest-pass-456" {
		t.Fatalf("guest Wi-Fi: %d %v", code, out)
	}
	if code, _ := get("203.0.113.5"); code == 200 {
		t.Fatal("from outside")
	}
}
