package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestTuyaFirewall(t *testing.T) {
	orig := ownIPs
	defer func() { ownIPs = orig }()
	ownIPs = func() []net.IP { return []net.IP{net.ParseIP("192.168.1.5").To4()} }
	for _, zoned := range []bool{true, false} {
		var mu sync.Mutex
		posts := map[string]map[string]any{}
		console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if r.Method == "POST" {
				b, _ := io.ReadAll(r.Body)
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				mu.Lock()
				posts[p[strings.LastIndex(p, "/")+1:]] = m
				mu.Unlock()
			}
			switch {
			case strings.HasSuffix(p, "/integration/v1/info"):
				_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
			case strings.HasSuffix(p, "/rest/networkconf"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"lan","name":"Home","purpose":"corporate","ip_subnet":"192.168.1.1/24","attr_hidden_id":"LAN"},
					{"_id":"iot","name":"IoT","purpose":"corporate","ip_subnet":"192.168.2.1/24"}]}`))
			case strings.HasSuffix(p, "/firewall/zone"):
				if !zoned {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`[{"_id":"zi","name":"Internal","network_ids":["lan"]},{"_id":"zt","name":"IoT","network_ids":["iot"]}]`))
			case strings.HasSuffix(p, "/firewall-policies"):
				if r.Method == "POST" {
					w.WriteHeader(201)
					_, _ = w.Write([]byte(`{"_id":"pol1"}`))
					return
				}
				_, _ = w.Write([]byte(`[]`))
			case strings.HasSuffix(p, "/rest/firewallgroup"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"grp1"}]}`))
			case strings.HasSuffix(p, "/rest/firewallrule"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"_id":"rule1","ruleset":"LAN_IN","rule_index":2000}]}`))
			case strings.HasSuffix(p, "/stat/sta"):
				_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[]}`))
			default:
				http.NotFound(w, r)
			}
		}))
		a, err := New(t.TempDir(), false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
			t.Fatal(err)
		}
		// a scan of both networks found nothing: only the other network is flagged
		a.tuya.Nets = []string{"192.168.1.0/24", "192.168.2.0/24"}
		if u := a.tuyaUnreached(nil); len(u) != 1 || u[0]["network"] != "192.168.2.0/24" {
			t.Fatalf("unreached: %v", u)
		}
		if u := a.tuyaUnreached([]map[string]any{{"ip": "192.168.2.40"}}); len(u) != 0 {
			t.Fatalf("something answered there: %v", u)
		}
		if _, err := a.tuyaAPI(context.Background(), "firewall-check", map[string]any{"network": "192.168.1.0/24"}, "admin"); err == nil {
			t.Fatal("HomeOwl's own network needs no rule")
		}
		v, err := a.tuyaAPI(context.Background(), "firewall-check", map[string]any{"network": "192.168.2.0/24"}, "admin")
		if err != nil {
			t.Fatal(err)
		}
		if l, _ := v["fix"].([]any); len(l) != map[bool]int{true: 1, false: 2}[zoned] || v["from"] != "Home" {
			t.Fatalf("zoned=%v: preview %v", zoned, v)
		}
		if _, err := a.tuyaAPI(context.Background(), "firewall-fix", map[string]any{"network": "192.168.2.0/24"}, "admin"); err == nil {
			t.Fatal("change mode is locked")
		}
		a.Changes.Write = true
		if _, err := a.tuyaAPI(context.Background(), "firewall-fix", map[string]any{"network": "192.168.2.0/24"}, "admin"); err != nil {
			t.Fatalf("zoned=%v: %v", zoned, err)
		}
		mu.Lock()
		if zoned {
			pol := posts["firewall-policies"]
			src, _ := pol["source"].(map[string]any)
			dst, _ := pol["destination"].(map[string]any)
			if pol["protocol"] != "tcp" || src["zone_id"] != "zi" || src["network_ids"].([]any)[0] != "lan" || dst["zone_id"] != "zt" ||
				dst["port"] != "6668" || dst["ips"].([]any)[0] != "192.168.2.0/24" {
				t.Fatalf("policy: %v", pol)
			}
		} else {
			rule := posts["firewallrule"]
			if rule["src_networkconf_id"] != "lan" || rule["dst_address"] != "192.168.2.0/24" || rule["dst_firewallgroup_ids"].([]any)[0] != "grp1" {
				t.Fatalf("rule: %v", rule)
			}
		}
		mu.Unlock()
		for _, c := range a.Changes.List() {
			if !c.Undoable {
				t.Fatalf("not undoable: %+v", c)
			}
		}
		console.Close()
	}
}

func TestTuyaHomes(t *testing.T) {
	a := checkinApp(t)
	var calls []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		mu.Lock()
		calls = append(calls, r.Method+" "+p)
		mu.Unlock()
		ok := func(res string) { _, _ = w.Write([]byte(`{"success":true,"result":` + res + `}`)) }
		switch {
		case p == "/v1.0/token":
			ok(`{"access_token":"tok","expire_time":7200}`)
		case strings.HasSuffix(p, "/associated-users/devices"):
			ok(`{"has_more":false,"devices":[{"id":"bf00000000000001","name":"Desk plug","category":"cz","online":true,"uid":"u1","status":[{"code":"switch_1","value":false}]},
				{"id":"bf00000000000002","name":"Porch light","category":"dj","online":true,"uid":"u1","owner_id":"202"}]}`)
		case p == "/v1.0/users/u1/homes":
			ok(`[{"home_id":101,"name":"Example Home","role":"OWNER"},{"home_id":202,"name":"Beach house","role":"ADMIN"}]`)
		case p == "/v1.0/homes/101/devices":
			ok(`[{"id":"bf00000000000001"}]`)
		case p == "/v1.0/homes/202/devices":
			ok(`[{"id":"bf00000000000002"}]`)
		case p == "/v1.0/homes/101/rooms":
			ok(`{"home_id":101,"rooms":[{"room_id":7,"name":"Kitchen"}]}`)
		case p == "/v1.0/homes/101/rooms/7/devices":
			ok(`[{"id":"bf00000000000001"}]`)
		case p == "/v1.0/homes/202/rooms":
			ok(`{"rooms":[]}`)
		case p == "/v1.0/homes/101/scenes":
			ok(`[{"scene_id":"sc1","name":"Good night","enabled":true}]`)
		case p == "/v1.0/homes/101/automations":
			ok(`[{"automation_id":"au1","name":"Lights at sunset","enabled":false}]`)
		case strings.HasPrefix(p, "/v1.0/homes/202/scenes"):
			_, _ = w.Write([]byte(`{"success":false,"code":1106,"msg":"permission deny"}`))
		case strings.HasSuffix(p, "/trigger"), strings.HasSuffix(p, "/actions/enable"):
			ok(`true`)
		default:
			ok(`{}`)
		}
	}))
	defer srv.Close()
	tuya.Regions["test"] = srv.URL
	defer delete(tuya.Regions, "test")

	v, err := a.tuyaAPI(context.Background(), "settings", map[string]any{"mode": "cloud", "region": "test", "access_id": "cid", "access_secret": "sec"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	homes := v["homes"].([]map[string]any)
	if len(homes) != 2 || homes[0]["name"] != "Example Home" || homes[0]["count"] != 1 || len(homes[0]["scenes"].([]tuya.Scene)) != 1 || homes[1]["scene_error"] == "" {
		t.Fatalf("homes: %v", homes)
	}
	where := map[string]string{}
	for _, d := range v["devices"].([]map[string]any) {
		where[d["name"].(string)] = d["home"].(string) + "/" + d["room"].(string)
	}
	if where["Desk plug"] != "101/Kitchen" || where["Porch light"] != "202/" {
		t.Fatalf("where: %v", where)
	}
	// hide the beach house: its device leaves the page, the house stays listed (to show it again)
	v, _ = a.tuyaAPI(context.Background(), "home-show", map[string]any{"id": "202", "on": false}, "admin")
	if d := v["devices"].([]map[string]any); len(d) != 1 || d[0]["name"] != "Desk plug" || v["homes"].([]map[string]any)[1]["hidden"] != true {
		t.Fatalf("hidden: %v", v)
	}
	_, _ = a.tuyaAPI(context.Background(), "home-show", map[string]any{"id": "202", "on": true}, "admin")
	// a house made here, and a device put in it with a room and a type; they survive the next sync
	v, _ = a.tuyaAPI(context.Background(), "home-add", map[string]any{"name": "Garage"}, "admin")
	var gid string
	for _, h := range v["homes"].([]map[string]any) {
		if h["name"] == "Garage" {
			gid = h["id"].(string)
		}
	}
	if _, err := a.tuyaAPI(context.Background(), "tag", map[string]any{"id": "bf00000000000002", "place": gid, "room": "Door", "type": "Garage light"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.tuyaAPI(context.Background(), "home-remove", map[string]any{"id": "101"}, "admin"); err == nil {
		t.Fatal("a Smart Life home isn't removed here")
	}
	v, _ = a.tuyaAPI(context.Background(), "sync", nil, "admin")
	for _, d := range v["devices"].([]map[string]any) {
		if d["name"] == "Porch light" && (d["home"] != gid || d["room"] != "Door" || d["kind"] != "Garage light") {
			t.Fatalf("tagged: %v", d)
		}
	}
	if _, err := a.tuyaAPI(context.Background(), "home-remove", map[string]any{"id": gid}, "admin"); err != nil {
		t.Fatal(err)
	}
	if d := a.tuya.Devices[a.tuya.find("bf00000000000002")]; d.Place != "" {
		t.Fatal("back in Smart Life's home")
	}
	// scenes and automations, logged
	if _, err := a.tuyaAPI(context.Background(), "scene", map[string]any{"home": "101", "id": "sc1"}, "admin"); err != nil {
		t.Fatal(err)
	}
	v, err = a.tuyaAPI(context.Background(), "automation", map[string]any{"home": "101", "id": "au1", "on": true}, "admin")
	if err != nil || !v["homes"].([]map[string]any)[0]["automations"].([]tuya.Scene)[0].Enabled {
		t.Fatalf("automation: %v %v", v, err)
	}
	if _, err := a.tuyaAPI(context.Background(), "scene", map[string]any{"home": "101", "id": "nope"}, "admin"); err == nil {
		t.Fatal("unknown scene")
	}
	mu.Lock()
	joined := strings.Join(calls, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "POST /v1.0/homes/101/scenes/sc1/trigger") || !strings.Contains(joined, "PUT /v1.0/homes/101/automations/au1/actions/enable") {
		t.Fatalf("calls: %s", joined)
	}
	n := 0
	for _, c := range a.Changes.List() {
		if strings.Contains(c.Description, "Good night") || strings.Contains(c.Description, "Lights at sunset") {
			n++
		}
	}
	if n != 2 {
		t.Fatal("scenes in History › Changes")
	}
}

func TestTuyaLookAndNames(t *testing.T) {
	a := checkinApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := func(res string) { _, _ = w.Write([]byte(`{"success":true,"result":` + res + `}`)) }
		switch p := r.URL.Path; {
		case p == "/v1.0/token":
			ok(`{"access_token":"tok","expire_time":7200}`)
		case strings.HasSuffix(p, "/associated-users/devices"):
			ok(`{"has_more":false,"devices":[{"id":"bf00000000000003","name":"Parking","category":"kg","online":true,"uid":"u1",
				"status":[{"code":"switch_1","value":true},{"code":"switch_2","value":false},{"code":"switch_3","value":true}]}]}`)
		case p == "/v1.0/users/u1/homes":
			ok(`[{"home_id":101,"name":"Example Home"}]`)
		case strings.HasSuffix(p, "/multiple-names"):
			ok(`[{"identifier":"switch_1","name":"Gate light"},{"identifier":"switch_2","name":"Path"}]`)
		default:
			ok(`[]`)
		}
	}))
	defer srv.Close()
	tuya.Regions["test"] = srv.URL
	defer delete(tuya.Regions, "test")
	if _, err := a.tuyaAPI(context.Background(), "settings", map[string]any{"mode": "cloud", "region": "test", "access_id": "cid", "access_secret": "sec"}, "admin"); err != nil {
		t.Fatal(err)
	}
	labels := func() []string {
		var l []string
		for _, sw := range a.tuyaView(true)["devices"].([]map[string]any)[0]["switches"].([]map[string]any) {
			l = append(l, sw["label"].(string))
		}
		return l
	}
	if l := labels(); strings.Join(l, ",") != "Gate light,Path,Switch 3" {
		t.Fatalf("names from the app: %v", l)
	}
	if _, err := a.tuyaAPI(context.Background(), "tag", map[string]any{"id": "bf00000000000003", "icon": "lantern", "names": map[string]any{"switch_3": "Garden", "bad code!": "x"}}, "admin"); err != nil {
		t.Fatal(err)
	}
	_ = a.tuyaSyncNow(true)
	if l := labels(); strings.Join(l, ",") != "Gate light,Path,Garden" {
		t.Fatalf("an admin's name wins, and survives a sync: %v", l)
	}
	if d := a.tuyaView(true)["devices"].([]map[string]any)[0]; d["icon"] != "lantern" {
		t.Fatalf("icon: %v", d["icon"])
	}
	// the house's look: a preset, then a photo (checked by its bytes, served back)
	if _, err := a.tuyaAPI(context.Background(), "home-look", map[string]any{"id": "101", "look": "sea"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.tuyaAPI(context.Background(), "home-look", map[string]any{"id": "101", "look": "photo", "photo": "data:image/png;base64,aGVsbG8="}, "admin"); err == nil {
		t.Fatal("not a picture")
	}
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	if _, err := a.tuyaAPI(context.Background(), "home-look", map[string]any{"id": "101", "look": "photo", "photo": png}, "admin"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.tuyaBg(w, httptest.NewRequest("GET", "/api/tuya/bg?home=101", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("photo: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	// the house's drawing (Plan / Dollhouse / Rooms views) and a room's own picture
	plan := map[string]any{"rooms": map[string]any{"Garden": map[string]any{"x": 10, "y": 500, "w": 5, "h": 9000, "out": true}, "Kitchen": map[string]any{"x": 0, "y": 0, "w": 300, "h": 200, "floor": 9}},
		"dots": map[string]any{"bf00000000000003/switch_1": []any{0.5, 1.7}}}
	if _, err := a.tuyaAPI(context.Background(), "home-plan", map[string]any{"id": "101", "plan": plan}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.tuyaAPI(context.Background(), "room-look", map[string]any{"id": "101", "room": "Garden", "look": "photo", "photo": png}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.tuyaAPI(context.Background(), "room-look", map[string]any{"id": "101", "room": "Bedroom", "look": "sea"}, "admin"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	a.tuyaBg(w, httptest.NewRequest("GET", "/api/tuya/bg?home=101&room=Garden", nil))
	if w.Code != 200 {
		t.Fatalf("room photo: %d", w.Code)
	}
	if _, err := a.tuyaAPI(context.Background(), "home-plan", map[string]any{"id": "101", "plan": plan}, "admin"); err != nil { // saving the layout again keeps the looks
		t.Fatal(err)
	}
	_ = a.tuyaSyncNow(true)
	if h := a.tuyaView(true)["homes"].([]map[string]any)[0]; h["look"] != "photo" || h["photo"] == int64(0) {
		t.Fatalf("the look survives a refresh of the homes: %v", h)
	}
	pl, _ := a.tuyaView(true)["homes"].([]map[string]any)[0]["plan"].(*tuyaPlan)
	if pl == nil {
		t.Fatal("the plan survives a refresh of the homes")
	}
	g, k := pl.Rooms["Garden"], pl.Rooms["Kitchen"]
	if g.Out == nil || !*g.Out || k.Out != nil || g.W != 40 || g.H != 1000 || g.Look != "photo" || g.Photo == 0 || k.Floor != 6 || pl.Dots["bf00000000000003/switch_1"] != [2]float64{0.5, 1} {
		t.Fatalf("plan clamped and looks kept: %+v", pl)
	}
	if _, err := a.tuyaAPI(context.Background(), "home-plan", map[string]any{"id": "nope", "plan": plan}, "admin"); err == nil {
		t.Fatal("plan for no house")
	}
	if _, err := a.tuyaAPI(context.Background(), "home-look", map[string]any{"id": "101", "look": "../x"}, "admin"); err == nil {
		t.Fatal("unknown look")
	}
}
