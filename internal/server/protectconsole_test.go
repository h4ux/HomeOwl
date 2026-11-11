package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/h4ux/homeowl/internal/protect"
)

// The Network console (a Cloud Gateway) has no Protect; the cameras are on an NVR with its own API key.
func TestProtectOnAnotherConsole(t *testing.T) {
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/network/integration/v1/info" {
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
			return
		}
		_, _ = w.Write([]byte("<!doctype html><title>UniFi OS</title>"))
	}))
	defer gateway.Close()
	nvr := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "nvr-key" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/proxy/protect/integration/v1/meta/info":
			_, _ = w.Write([]byte(`{"applicationVersion":"6.0.0"}`))
		case "/proxy/protect/integration/v1/cameras":
			_, _ = w.Write([]byte(`[{"id":"cam1","name":"Front door","state":"CONNECTED"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer nvr.Close()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(gateway.URL, "gw-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	if v := a.camerasView(1); v["no_protect"] != true {
		t.Fatalf("gateway without Protect: %v", v)
	}
	if _, err := a.protectConnect(map[string]any{"host": gateway.URL, "key": "gw-key"}); err == nil {
		t.Fatal("accepted a console without Protect")
	}
	if _, err := a.protectConnect(map[string]any{"host": nvr.URL, "key": "wrong"}); err == nil {
		t.Fatal("accepted a wrong key")
	}
	if a.Cfg.Get().Cameras.ProtectHost != "" {
		t.Fatal("a failed connection was saved")
	}
	res, err := a.protectConnect(map[string]any{"host": nvr.URL, "key": "nvr-key"})
	if err != nil {
		t.Fatal(err)
	}
	v := res.(map[string]any)
	if cams, _ := v["cameras"].([]protect.Camera); len(cams) != 1 || cams[0].Name != "Front door" {
		t.Fatalf("cameras: %v", v)
	}
	if v["error"] != nil || v["protect_console"] != nvr.URL || a.Sec.Get(protectKeySlot) != "nvr-key" {
		t.Fatalf("view: %v", v)
	}
	if _, err := a.protectDisconnect(); err != nil || a.Sec.Get(protectKeySlot) != "" || a.protectClient() != a.UniFi {
		t.Fatalf("disconnect: %v", err)
	}
}

func TestDiscoverProtect(t *testing.T) {
	nvr := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/system":
			_, _ = w.Write([]byte(`{"name":"Garage NVR","hardware":{"shortname":"UNVR","name":"UniFi NVR"}}`))
		case "/proxy/protect/integration/v1/meta/info":
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer nvr.Close()
	printer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>printer</html>"))
	}))
	defer printer.Close()
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case "/proxy/network/api/s/default/stat/sta":
			_, _ = w.Write([]byte(`{"data":[{"mac":"aa:bb:cc:00:00:10","ip":"192.168.1.5","oui":"Ubiquiti Inc","hostname":"UNVR"},
				{"mac":"aa:bb:cc:00:00:11","ip":"192.168.1.6","oui":"Ubiquiti Inc","hostname":"U6-Printer-Bridge"},
				{"mac":"aa:bb:cc:00:00:12","ip":"192.168.1.7","oui":"Apple","hostname":"iPhone"},
				{"mac":"aa:bb:cc:00:00:13","ip":"203.0.113.9","oui":"Ubiquiti Inc","hostname":"far-away"}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer gateway.Close()
	old := discoverBase
	defer func() { discoverBase = old }()
	discoverBase = func(ip string) (string, bool) {
		switch ip {
		case "192.168.1.5":
			return nvr.URL, true
		case "192.168.1.6":
			return printer.URL, true
		case "192.168.1.7":
			return "https://192.0.2.1", true // must never be asked: not a Ubiquiti device
		}
		return "", false
	}
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(gateway.URL, "gw-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	a.Cache.Get(true, 0)
	found := a.discoverProtect(context.Background())
	if len(found) != 1 || found[0]["ip"] != "192.168.1.5" || found[0]["name"] != "Garage NVR" || found[0]["likely"] != true {
		t.Fatalf("found: %v", found)
	}
}
