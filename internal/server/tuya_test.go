package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/h4ux/homeowl/internal/tuya"
)

func TestTuyaCloud(t *testing.T) {
	a := checkinApp(t)
	var cmds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/v1.0/token":
			_, _ = w.Write([]byte(`{"success":true,"result":{"access_token":"tok","expire_time":7200}}`))
		case strings.HasSuffix(r.URL.Path, "/associated-users/devices"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"has_more":false,"devices":[{"id":"bf00000000000001","name":"Desk plug","category":"cz","online":true,"local_key":"0123456789abcdef",
				"status":[{"code":"switch_1","value":false},{"code":"cur_power","value":125},{"code":"cur_voltage","value":2301}]}]}}`))
		case strings.HasSuffix(r.URL.Path, "/specification"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"status":[{"code":"cur_power","type":"Integer","values":"{\"unit\":\"W\",\"scale\":1}"},{"code":"cur_voltage","type":"Integer","values":"{\"unit\":\"V\",\"scale\":1}"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/commands"):
			cmds = append(cmds, string(b))
			_, _ = w.Write([]byte(`{"success":true,"result":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tuya.Regions["test"] = srv.URL
	defer delete(tuya.Regions, "test")

	v, err := a.tuyaAPI(context.Background(), "settings", map[string]any{"mode": "cloud", "region": "test", "access_id": "cid", "access_secret": "sec"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	devs := v["devices"].([]map[string]any)
	if len(devs) != 1 || devs[0]["name"] != "Desk plug" || devs[0]["kind"] != "Plug" || devs[0]["online"] != true {
		t.Fatalf("devices: %v", v)
	}
	sw := devs[0]["switches"].([]map[string]any)
	if len(sw) != 1 || sw[0]["code"] != "switch_1" || sw[0]["label"] != "Power" {
		t.Fatalf("switches: %v", sw)
	}
	_ = a.tuyaSync() // units arrive after the first sync
	rd := a.tuyaView(true)["devices"].([]map[string]any)[0]["readings"].([]map[string]any)
	if len(rd) != 2 || rd[0]["value"] != "12.5" || rd[0]["unit"] != "W" || rd[1]["value"] != "230.1" {
		t.Fatalf("readings: %v", rd)
	}
	if a.Sec.Get(tuyaKeySlot("bf00000000000001")) != "0123456789abcdef" {
		t.Fatal("the local key is kept for local mode")
	}
	if v := a.tuyaView(false); v["cloud_ready"] != nil || v["found"] != nil {
		t.Fatalf("viewers see devices only: %v", v)
	}
	if err := a.tuyaSwitch("bf00000000000001", "switch_9", true, "admin"); err == nil {
		t.Fatal("no such switch")
	}
	if err := a.tuyaSwitch("bf00000000000001", "switch_1", true, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || !strings.Contains(cmds[0], `"value":true`) {
		t.Fatalf("command: %v", cmds)
	}
	logged := false
	for _, c := range a.Changes.List() {
		if strings.Contains(c.Description, "Turn on Desk plug") && c.OK {
			logged = true
		}
	}
	if !logged {
		t.Fatal("in History › Changes")
	}
	if sw := a.tuyaView(true)["devices"].([]map[string]any)[0]["switches"].([]map[string]any); sw[0]["on"] != true {
		t.Fatalf("shows on: %v", sw)
	}
	if _, err := a.tuyaAPI(context.Background(), "device", map[string]any{"id": "x"}, "admin"); err == nil {
		t.Fatal("bad id")
	}
}
