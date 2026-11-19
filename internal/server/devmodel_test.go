package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectModel(t *testing.T) {
	a := checkinApp(t)
	asked := make(chan string, 4)
	old := deviceModel
	defer func() { deviceModel = old; modelAsked.Lock(); modelAsked.at = nil; modelAsked.Unlock() }()
	deviceModel = func(_ context.Context, ip string) string { asked <- ip; return "iPhone18,1" }
	modelAsked.Lock()
	modelAsked.at = nil
	modelAsked.Unlock()
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's iPhone","kind":"phone","person_name":"Alex"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	select {
	case ip := <-asked:
		if ip != "192.168.1.40" {
			t.Fatalf("asked %s", ip)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the device wasn't asked for its model")
	}
	var x CheckIn
	for i := 0; i < 50; i++ {
		if x = a.checkins.List()[0]; x.DetModel != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if x.DetModel != "iPhone18,1" {
		t.Fatalf("found model: %+v", x)
	}
	// approved with the dialog's "Automatic": the profile uses the found model
	if _, err := a.approveCheckIn(map[string]any{"id": x.ID, "name": "Alex's iPhone", "nd_profile": "abc123", "nd_model": ""}, "admin"); err != nil {
		t.Fatal(err)
	}
	_, out := hi(a, "GET", "192.168.1.40", "", nil)
	if nd, _ := out["registered"].(map[string]any)["nextdns"].(map[string]any); nd == nil {
		t.Fatalf("setup: %v", out)
	}
	if got := appleDoH(a.checkins.List()[0], a.checkins.List()[0].Name); !strings.HasSuffix(got, "/Apple%20iPhone%2017%20Pro") { // the marketing name
		t.Fatalf("address %s", got)
	}
	// asked at most once every 10 minutes
	hi(a, "GET", "192.168.1.40", "", nil)
	select {
	case <-asked:
		t.Fatal("asked again too soon")
	case <-time.After(100 * time.Millisecond):
	}
	// what the device says must be a model identifier
	deviceModel = func(context.Context, string) string { return "evil/../x" }
	modelAsked.Lock()
	modelAsked.at = nil
	modelAsked.Unlock()
	a.detectModel("aa:bb:cc:00:00:40", "192.168.1.40", true)
	if m := a.checkins.List()[0].DetModel; m != "iPhone18,1" {
		t.Fatalf("junk replaced it: %q", m)
	}
}

func TestDeviceModelChoice(t *testing.T) {
	a := checkinApp(t)
	defer func() { modelAsked.Lock(); modelAsked.at = nil; modelAsked.Unlock() }()
	old := deviceModel
	defer func() { deviceModel = old }()
	deviceModel = func(context.Context, string) string { return "Mac16,12" }
	modelAsked.Lock()
	modelAsked.at = nil
	modelAsked.Unlock()
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's Mac","kind":"laptop","person_name":"Alex"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	id := a.checkins.List()[0].ID
	for i := 0; i < 50 && a.checkins.List()[0].DetModel == ""; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := a.approveCheckIn(map[string]any{"id": id, "name": "Alex's Mac", "nd_profile": "abc123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	pick := func(model string) (int, map[string]any) {
		r := httptest.NewRequest("POST", "http://homeowl.lan:8555/api/hi/model", strings.NewReader(`{"model":"`+model+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://homeowl.lan:8555")
		r.RemoteAddr = "192.168.1.40:51234"
		w := httptest.NewRecorder()
		a.hiModel(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	// the device's page ticks the model found on it, by its marketing name
	_, out := hi(a, "GET", "192.168.1.40", "", nil)
	m := out["registered"].(map[string]any)["model"].(map[string]any)
	if m["current"] != "Mac16,12" || m["by"] != "found" || m["name"] != "MacBook Air 13-inch (M4)" {
		t.Fatalf("model view: %v", m)
	}
	if got := appleDoH(a.checkins.List()[0], a.checkins.List()[0].Name); got != "https://apple.dns.nextdns.io/abc123/Alex%27s%20Mac/Apple%20MacBook%20Air%2013-inch%20%28M4%29" {
		t.Fatalf("address %s", got)
	}
	// it's changed there
	if code, out := pick("Mac16,13"); code != 200 || out["registered"].(map[string]any)["model"].(map[string]any)["by"] != "device" {
		t.Fatalf("pick: %d %v", code, out)
	}
	if code, _ := pick("../evil"); code != 400 {
		t.Fatal("junk accepted")
	}
	if code, _ := pick("-"); code != 200 || ndModelOf(a.checkins.List()[0]) != "" {
		t.Fatal("don't say")
	}
	// an admin's choice wins
	pick("Mac16,13")
	if _, err := a.changeSetup(map[string]any{"id": id, "nd_profile": "abc123", "nd_model": "Mac16,1"}); err != nil {
		t.Fatal(err)
	}
	if x := a.checkins.List()[0]; ndModelOf(x) != "Mac16,1" || modelView(x)["by"] != "admin" {
		t.Fatalf("admin: %+v", x)
	}
	// unlisted identifiers go out as they are
	if ndModelLabel("iPhone19,9") != "Apple iPhone19,9" || ndModelLabel("Mac16,12") != "Apple MacBook Air 13-inch (M4)" ||
		ndModelLabel("iPhone19,2") != "Apple iPhone 18 Pro" || ndModelLabel("iPhone19,7") != "Apple iPhone 18 Pro Max" || ndModelLabel("iPhone19,4") != "Apple iPhone Duo" {
		t.Fatal("labels")
	}
}

func TestModelGroups(t *testing.T) {
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, g := range modelGroups() {
		for _, m := range g["models"].([]map[string]string) {
			if seen[m["id"]] || names[m["name"]] || !ndModelRE.MatchString(m["id"]) {
				t.Errorf("duplicate or bad: %v", m)
			}
			seen[m["id"]], names[m["name"]] = true, true
		}
	}
	if !seen["iPhone19,3"] || seen["iPhone19,7"] {
		t.Error("the international Pro Max is named but not offered twice")
	}
	// older iPad Pros and the MacBook Neo: offered once, every variant named
	for id, want := range map[string]string{"Mac17,5": "MacBook Neo", "iPad6,8": "iPad Pro 12.9-inch (1st gen)",
		"iPad7,2": "iPad Pro 12.9-inch (2nd gen)", "iPad8,4": "iPad Pro 11-inch (1st gen)", "iPad8,10": "iPad Pro 11-inch (2nd gen)"} {
		if modelName(id) != want {
			t.Errorf("%s: %q", id, modelName(id))
		}
	}
	if !seen["iPad6,7"] || seen["iPad6,8"] || !seen["Mac17,5"] {
		t.Error("each model offered once")
	}
}

func TestCheckInFormModel(t *testing.T) {
	a := checkinApp(t)
	old := deviceModel
	defer func() {
		deviceModel = old
		modelAsked.Lock()
		modelAsked.at, modelAsked.found = nil, nil
		modelAsked.Unlock()
	}()
	deviceModel = func(context.Context, string) string { return "Mac16,12" }
	modelAsked.Lock()
	modelAsked.at, modelAsked.found = nil, nil
	modelAsked.Unlock()
	// the form asks the device first (an iPhone, iPad or Mac page), and suggests what it says
	r := httptest.NewRequest("GET", "http://homeowl.lan:8555/api/hi?probe=1", nil)
	r.RemoteAddr = "192.168.1.40:51234"
	w := httptest.NewRecorder()
	a.hiAPI(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	m, _ := out["model"].(map[string]any)
	if m == nil || m["found"] != "Mac16,12" || len(m["groups"].([]any)) == 0 {
		t.Fatalf("form model: %v", out["model"])
	}
	// they pick another: the admin gets both
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's Mac","kind":"laptop","person_name":"Alex","model":"Mac16,13"}`, nil); code != 200 {
		t.Fatal("check-in failed")
	}
	x := a.checkins.List()[0]
	if x.DetModel != "Mac16,12" || x.UserModel != "Mac16,13" || ndModelOf(x) != "Mac16,13" {
		t.Fatalf("both models: %+v", x)
	}
	// junk is dropped, not refused
	if code, _ := hi(a, "POST", "192.168.1.40", `{"name":"Alex's Mac","kind":"laptop","person_name":"Alex","model":"<x>"}`, nil); code != 200 || a.checkins.List()[0].UserModel != "" {
		t.Fatalf("junk model: %+v", a.checkins.List()[0])
	}
}
