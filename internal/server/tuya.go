package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/tuya"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Tuya (Smart Life) devices under Smart: what they are, online or not, their readings, and on/off. Through the
// Tuya Cloud API or, in local mode, straight to each device on the home network. Keys stay in the secrets store.

const (
	slotTuyaID     = "tuya-access-id"
	slotTuyaSecret = "tuya-access-secret"
	tuyaEvery      = 2 * time.Minute
	tuyaHomesEvery = time.Hour
)

func tuyaKeySlot(id string) string { return "tuya-key-" + id }

var tuyaIDRE = regexp.MustCompile(`^[A-Za-z0-9]{8,40}$`)

// tuyaDev is a device as HomeOwl keeps it (tuya-devices.json; never the local key).
type tuyaDev struct {
	tuya.Device
	MAC    string               `json:"mac,omitempty"`   // the UniFi client it is
	Units  map[string]tuya.Unit `json:"units,omitempty"` // from the cloud: how numbers read
	DPS    map[string]any       `json:"dps,omitempty"`   // local mode: the raw data points
	Seen   int64                `json:"seen,omitempty"`
	Err    string               `json:"error,omitempty"`
	Manual bool                 `json:"manual,omitempty"`  // added by hand (local mode)
	Home   string               `json:"home,omitempty"`    // the Smart Life home it's in (cloud)
	Room   string               `json:"room,omitempty"`    // its room there (cloud)
	Place  string               `json:"place,omitempty"`   // the house an admin put it in (overrides Home)
	MyRoom string               `json:"my_room,omitempty"` // the room an admin gave it (overrides Room)
	Type   string               `json:"type,omitempty"`    // what an admin says it is (overrides the category)
}

// tuyaHome: a Smart Life home from the cloud, or a house an admin made (Custom) to group devices.
type tuyaHome struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Custom      bool         `json:"custom,omitempty"`
	Rooms       []tuya.Room  `json:"rooms,omitempty"`
	Scenes      []tuya.Scene `json:"scenes,omitempty"`
	Automations []tuya.Scene `json:"automations,omitempty"`
	SceneErr    string       `json:"scene_error,omitempty"`
}

// where: the house and room a device shows under.
func (d tuyaDev) where() (home, room string) {
	return orStr(d.Place, d.Home), orStr(d.MyRoom, d.Room)
}

// kind: what the device is, in words.
func (d tuyaDev) kind() string { return orStr(d.Type, tuyaCategory[d.Category]) }

type tuyaStore struct {
	mu      sync.Mutex
	path    string
	Devices []tuyaDev    `json:"devices"`
	Found   []tuya.Found `json:"found,omitempty"` // the last scan
	Synced  int64        `json:"synced,omitempty"`
	Error   string       `json:"error,omitempty"`
	Scanned int64        `json:"scanned,omitempty"`
	Nets    []string     `json:"nets,omitempty"` // the networks the last scan tried
	Homes   []tuyaHome   `json:"homes,omitempty"`
	HomesAt int64        `json:"homes_at,omitempty"` // when the cloud's homes, rooms and scenes were read
}

func (t *tuyaStore) home(id string) int {
	for i, h := range t.Homes {
		if h.ID == id {
			return i
		}
	}
	return -1
}

func newTuyaStore(dir string) *tuyaStore {
	t := &tuyaStore{path: filepath.Join(dir, "tuya-devices.json")}
	if b, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(b, t)
	}
	return t
}

// saveLocked writes the store; the caller holds mu.
func (t *tuyaStore) saveLocked() {
	b, _ := json.MarshalIndent(t, "", " ")
	tmp := t.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, t.path)
	}
}

func (t *tuyaStore) find(id string) int {
	for i, d := range t.Devices {
		if d.ID == id {
			return i
		}
	}
	return -1
}

func (a *App) tuyaCloud() (*tuya.Cloud, error) {
	return tuya.NewCloud(a.Cfg.Get().Tuya.Region, a.Sec.Get(slotTuyaID), a.Sec.Get(slotTuyaSecret))
}

// tuyaSync refreshes every device: the device list and status from the cloud, or each device on the network.
func (a *App) tuyaSync() error { return a.tuyaSyncNow(false) }

// tuyaSyncNow: force also reads the homes, rooms and scenes again (they're read hourly otherwise).
func (a *App) tuyaSyncNow(force bool) error {
	c := a.Cfg.Get().Tuya
	switch c.Mode {
	case "cloud":
		cl, err := a.tuyaCloud()
		if err != nil {
			return a.tuyaFailed(err)
		}
		list, err := cl.Devices()
		if err != nil {
			return a.tuyaFailed(err)
		}
		for _, d := range list {
			if d.LocalKey != "" && a.Sec.Get(tuyaKeySlot(d.ID)) != d.LocalKey {
				_ = a.Sec.Set(tuyaKeySlot(d.ID), d.LocalKey) // for local mode later
			}
		}
		s := a.tuya
		s.mu.Lock()
		need := []string{}
		keep := s.Devices
		s.Devices = nil
		for _, d := range list {
			n := tuyaDev{Device: d, Seen: time.Now().Unix()}
			for _, o := range keep {
				if o.ID == d.ID {
					n.MAC, n.Units, n.IP, n.Version = o.MAC, o.Units, o.IP, o.Version
					n.Home, n.Room, n.Place, n.MyRoom, n.Type = o.Home, o.Room, o.Place, o.MyRoom, o.Type
				}
			}
			n.LocalKey, n.UID, n.Owner = "", "", ""
			if n.Home == "" && d.Owner != "" {
				n.Home = d.Owner
			}
			if n.Units == nil {
				need = append(need, d.ID)
			}
			s.Devices = append(s.Devices, n)
		}
		s.Synced, s.Error = time.Now().Unix(), ""
		s.saveLocked()
		s.mu.Unlock()
		if force || time.Since(time.Unix(s.HomesAt, 0)) > tuyaHomesEvery {
			a.tuyaHomes(cl, list)
		}
		for i, id := range need { // units once per device, a few at a time (the API has a quota)
			if i == 5 {
				break
			}
			if u, err := cl.Spec(id); err == nil {
				s.mu.Lock()
				if j := s.find(id); j >= 0 {
					s.Devices[j].Units = u
				}
				s.saveLocked()
				s.mu.Unlock()
			}
		}
	case "local":
		s := a.tuya
		s.mu.Lock()
		devs := append([]tuyaDev{}, s.Devices...)
		s.mu.Unlock()
		var wg sync.WaitGroup
		res := make([]tuyaDev, len(devs))
		for i, d := range devs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := a.Sec.Get(tuyaKeySlot(d.ID))
				switch {
				case d.IP == "":
					d.Online, d.Err = false, "No address yet: scan the network, or add its address."
				case key == "":
					d.Online, d.Err = false, "No local key yet."
				default:
					dps, err := tuya.LocalStatus(d.IP, d.ID, key, d.Version)
					if err != nil {
						d.Online, d.Err = false, tuyaErrText(err, d.IP)
					} else {
						d.Online, d.Err, d.DPS, d.Seen = true, "", dps, time.Now().Unix()
					}
				}
				res[i] = d
			}()
		}
		wg.Wait()
		s.mu.Lock()
		for _, d := range res {
			if j := s.find(d.ID); j >= 0 {
				s.Devices[j].Online, s.Devices[j].Err, s.Devices[j].DPS, s.Devices[j].Seen = d.Online, d.Err, d.DPS, d.Seen
			}
		}
		s.Synced, s.Error = time.Now().Unix(), ""
		s.saveLocked()
		s.mu.Unlock()
	}
	a.tuyaMatchClients()
	return nil
}

func (a *App) tuyaFailed(err error) error {
	s := a.tuya
	s.mu.Lock()
	s.Error = err.Error()
	s.saveLocked()
	s.mu.Unlock()
	return err
}

// tuyaMatchClients: which UniFi client each device is (by its address on the home network).
func (a *App) tuyaMatchClients() {
	if !a.UniFi.Connected() {
		return
	}
	list, err := a.onlineClients(time.Minute)
	if err != nil {
		return
	}
	s := a.tuya
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.Devices {
		if d.IP == "" {
			continue
		}
		if c := findClientIP(list, d.IP); c != nil {
			s.Devices[i].MAC = strings.ToLower(str(c, "mac"))
		}
	}
	s.saveLocked()
}

func (a *App) tuyaLoop(ctx context.Context) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if a.Cfg.Get().Tuya.Mode != "" {
			if err := a.tuyaSync(); err != nil {
				log.Printf("tuya: %v", err)
			}
		}
		t.Reset(tuyaEvery)
	}
}

// tuyaScan looks for devices: broadcasts on this computer's network, port 6668 on the chosen networks, and the
// addresses of clients marked as Tuya devices. Devices that say who they are get their address and version.
func (a *App) tuyaScan(ctx context.Context, nets []string) ([]tuya.Found, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var mu sync.Mutex
	byIP := map[string]tuya.Found{}
	add := func(l []tuya.Found) {
		mu.Lock()
		defer mu.Unlock()
		for _, f := range l {
			if o, ok := byIP[f.IP]; ok && o.ID != "" {
				continue
			}
			byIP[f.IP] = f
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); add(tuya.Listen(ctx, 8*time.Second)) }()
	var perr error
	for _, n := range nets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := tuya.Probe(ctx, n)
			if err != nil {
				mu.Lock()
				perr = fmt.Errorf("%s: %w", n, err)
				mu.Unlock()
				return
			}
			add(l)
		}()
	}
	marked := a.Cfg.Get().Tuya.Marked
	if len(marked) > 0 && a.UniFi.Connected() {
		if list, err := a.onlineClients(time.Minute); err == nil {
			for _, c := range list {
				if contains(marked, strings.ToLower(str(c, "mac"))) && str(c, "ip") != "" {
					if p, err := tuya.Probe(ctx, str(c, "ip")+"/32"); err == nil {
						add(p)
					}
				}
			}
		}
	}
	wg.Wait()
	out := make([]tuya.Found, 0, len(byIP))
	for _, f := range byIP {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	s := a.tuya
	s.mu.Lock()
	for _, f := range out { // a known device seen at an address: remember where and which version
		if f.ID == "" {
			continue
		}
		if j := s.find(f.ID); j >= 0 {
			s.Devices[j].IP = f.IP
			if f.Version != "" {
				s.Devices[j].Version = f.Version
			}
		} else if a.Cfg.Get().Tuya.Mode == "local" {
			s.Devices = append(s.Devices, tuyaDev{Device: tuya.Device{ID: f.ID, Name: "Tuya device " + f.IP, IP: f.IP, Version: f.Version, Product: f.Product}})
		}
	}
	s.Found, s.Scanned, s.Nets = out, time.Now().Unix(), append([]string{}, nets...)
	s.saveLocked()
	s.mu.Unlock()
	a.tuyaMatchClients()
	if len(out) == 0 && perr != nil {
		return out, perr
	}
	return out, nil
}

// tuyaSwitches: the on/off data points of a device ({code, label, on}).
func tuyaSwitches(d tuyaDev) []map[string]any {
	out := []map[string]any{}
	if d.DPS != nil { // local: DPs 1–6 that are on/off, plus 7 (often a USB port)
		ks := []string{}
		for k, v := range d.DPS {
			if _, ok := v.(bool); ok {
				if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= 7 {
					ks = append(ks, k)
				}
			}
		}
		sort.Slice(ks, func(i, j int) bool { a, _ := strconv.Atoi(ks[i]); b, _ := strconv.Atoi(ks[j]); return a < b })
		for _, k := range ks {
			label := "Switch " + k
			if len(ks) == 1 {
				label = "Power"
			}
			out = append(out, map[string]any{"code": k, "label": label, "on": d.DPS[k]})
		}
		return out
	}
	n := 0
	for _, s := range d.Status {
		if _, ok := s.Value.(bool); ok && (s.Code == "switch" || s.Code == "switch_led" || strings.HasPrefix(s.Code, "switch_")) && !strings.HasSuffix(s.Code, "_backlight") {
			n++
		}
	}
	for _, s := range d.Status {
		on, ok := s.Value.(bool)
		if !ok || !(s.Code == "switch" || s.Code == "switch_led" || strings.HasPrefix(s.Code, "switch_")) || strings.HasSuffix(s.Code, "_backlight") {
			continue
		}
		label := map[string]string{"switch": "Power", "switch_led": "Light", "switch_usb1": "USB"}[s.Code]
		if label == "" {
			label = "Switch " + strings.TrimPrefix(s.Code, "switch_")
		}
		if n == 1 {
			label = "Power"
		}
		out = append(out, map[string]any{"code": s.Code, "label": label, "on": on})
	}
	return out
}

// tuyaReadings: the numbers worth showing (power, temperature, humidity, battery…), with units.
var tuyaReadingCodes = map[string]string{"cur_power": "Power", "cur_voltage": "Voltage", "cur_current": "Current",
	"add_ele": "Energy", "va_temperature": "Temperature", "temp_current": "Temperature", "va_humidity": "Humidity",
	"humidity_value": "Humidity", "battery_percentage": "Battery", "bright_value": "Brightness", "bright_value_v2": "Brightness"}

func tuyaReadings(d tuyaDev) []map[string]any {
	out := []map[string]any{}
	num := func(v any) (float64, bool) {
		switch x := v.(type) {
		case float64:
			return x, true
		case int:
			return float64(x), true
		}
		return 0, false
	}
	if d.DPS != nil { // local: the usual plug data points
		for _, r := range []struct {
			dp, label, unit string
			div             float64
		}{{"19", "Power", "W", 10}, {"20", "Voltage", "V", 10}, {"18", "Current", "mA", 1}} {
			if v, ok := num(d.DPS[r.dp]); ok {
				out = append(out, map[string]any{"label": r.label, "value": trimNum(v / r.div), "unit": r.unit})
			}
		}
		return out
	}
	for _, s := range d.Status {
		label, ok := tuyaReadingCodes[s.Code]
		v, isNum := num(s.Value)
		if !ok || !isNum {
			continue
		}
		u, has := d.Units[s.Code]
		if !has { // the usual scales when the cloud didn't say
			u = map[string]tuya.Unit{"cur_power": {Unit: "W", Scale: 1}, "cur_voltage": {Unit: "V", Scale: 1}, "cur_current": {Unit: "mA"},
				"va_temperature": {Unit: "°C", Scale: 1}, "temp_current": {Unit: "°C"}, "va_humidity": {Unit: "%"}, "humidity_value": {Unit: "%"},
				"battery_percentage": {Unit: "%"}, "add_ele": {Unit: "kWh", Scale: 3}}[s.Code]
		}
		out = append(out, map[string]any{"label": label, "value": trimNum(v / math.Pow10(u.Scale)), "unit": u.Unit})
	}
	return out
}

func trimNum(v float64) string { return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64) }

// tuyaCategory: Tuya's category code in words.
var tuyaCategory = map[string]string{"cz": "Plug", "pc": "Power strip", "kg": "Switch", "tdq": "Switch", "dj": "Light", "dd": "Light strip",
	"xdd": "Ceiling light", "fwd": "Light", "dc": "Light string", "wsdcg": "Temperature & humidity", "mcs": "Door sensor",
	"pir": "Motion sensor", "ywbj": "Smoke alarm", "rqbj": "Gas alarm", "sj": "Water leak", "cl": "Curtain", "ckmkzq": "Garage door",
	"wk": "Thermostat", "kt": "Air conditioner", "fs": "Fan", "cs": "Dehumidifier", "kj": "Air purifier", "sp": "Camera", "ms": "Lock",
	"wnykq": "IR remote", "wg2": "Gateway", "zndb": "Energy meter", "dlq": "Circuit breaker", "qn": "Heater", "sd": "Robot vacuum"}

// tuyaView: settings (never the secrets themselves) and the devices.
func (a *App) tuyaView(admin bool) map[string]any {
	c := a.Cfg.Get().Tuya
	s := a.tuya
	s.mu.Lock()
	devs := append([]tuyaDev{}, s.Devices...)
	homes := append([]tuyaHome{}, s.Homes...)
	res := map[string]any{"mode": c.Mode, "region": c.Region, "synced": s.Synced, "error": s.Error}
	if admin {
		res["found"], res["scanned"] = nz(s.Found), s.Scanned
	}
	s.mu.Unlock()
	names := map[string]string{}
	if a.UniFi.Connected() {
		if list, err := a.onlineClients(time.Minute); err == nil {
			for _, cl := range list {
				names[strings.ToLower(str(cl, "mac"))] = orStr(str(cl, "name"), str(cl, "hostname"))
				names["ip:"+str(cl, "ip")] = strings.ToLower(str(cl, "mac"))
			}
		}
	}
	hidden := map[string]bool{}
	for _, h := range c.Hidden {
		hidden[h] = true
	}
	count := map[string]int{}
	out := []map[string]any{}
	for _, d := range devs {
		home, room := d.where()
		if a.tuyaHomeIdx(homes, home) < 0 {
			home = ""
		}
		count[home]++
		if hidden[home] {
			continue
		}
		out = append(out, map[string]any{"id": d.ID, "name": d.Name, "category": d.Category, "kind": d.kind(), "auto_kind": tuyaCategory[d.Category],
			"home": home, "room": room, "place": d.Place, "my_room": d.MyRoom, "type": d.Type, "product": d.Product,
			"online": d.Online, "ip": d.IP, "version": d.Version, "mac": d.MAC, "unifi_name": names[d.MAC], "seen": d.Seen, "error": d.Err,
			"switches": tuyaSwitches(d), "readings": tuyaReadings(d), "has_key": admin && a.Sec.Get(tuyaKeySlot(d.ID)) != ""})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]["name"].(string)) < strings.ToLower(out[j]["name"].(string))
	})
	res["devices"] = out
	hl := []map[string]any{}
	for _, h := range homes {
		x := map[string]any{"id": h.ID, "name": h.Name, "custom": h.Custom, "hidden": hidden[h.ID], "count": count[h.ID]}
		if !hidden[h.ID] {
			x["scenes"], x["automations"], x["scene_error"] = nz(h.Scenes), nz(h.Automations), h.SceneErr
			rooms := []string{}
			for _, r := range h.Rooms {
				rooms = append(rooms, r.Name)
			}
			x["rooms"] = rooms
		}
		hl = append(hl, x)
	}
	res["homes"], res["unplaced"], res["homes_at"] = hl, count[""], s.HomesAt
	res["kinds"] = tuyaKinds()
	if admin {
		res["cloud_ready"] = a.Sec.Get(slotTuyaID) != "" && a.Sec.Get(slotTuyaSecret) != ""
		res["networks"], res["marked"] = nz(c.Networks), nz(c.Marked)
		if nets, err := a.issueNets(); err == nil {
			l := []map[string]any{}
			for _, n := range nets {
				l = append(l, map[string]any{"name": n.Name, "subnet": n.Subnet})
			}
			res["unifi_networks"] = l
		}
		found := []map[string]any{}
		for _, f := range nz(res["found"].([]tuya.Found)) {
			mac := names["ip:"+f.IP]
			found = append(found, map[string]any{"ip": f.IP, "id": f.ID, "version": f.Version, "how": f.How, "mac": mac, "unifi_name": names[mac]})
		}
		res["found"] = found
		res["unreached"] = a.tuyaUnreached(found)
		res["regions"] = []string{"eu", "eu-w", "us", "us-e", "cn", "in"}
	}
	return res
}

// tuyaAPI: the admin's Tuya actions.
func (a *App) tuyaAPI(ctx context.Context, op string, body map[string]any, by string) (map[string]any, error) {
	switch op {
	case "settings":
		mode, region := s(body, "mode"), s(body, "region")
		if mode != "" && mode != "cloud" && mode != "local" {
			return nil, errors.New("pick cloud or local")
		}
		if _, ok := tuya.Regions[region]; region != "" && !ok {
			return nil, errors.New("pick the Tuya data centre")
		}
		nets := []string{}
		for _, n := range strList(body["networks"]) {
			if !strings.Contains(n, "/") {
				return nil, errors.New(n + " isn't a network (like 192.168.1.0/24)")
			}
			nets = append(nets, n)
		}
		if id := strings.TrimSpace(s(body, "access_id")); id != "" {
			if err := a.Sec.Set(slotTuyaID, id); err != nil {
				return nil, err
			}
		}
		if sec := strings.TrimSpace(s(body, "access_secret")); sec != "" {
			if err := a.Sec.Set(slotTuyaSecret, sec); err != nil {
				return nil, err
			}
		}
		if err := a.Cfg.Update(func(c *config.Config) {
			c.Tuya.Mode, c.Tuya.Region, c.Tuya.Networks = mode, region, nets
		}); err != nil {
			return nil, err
		}
		if mode != "" {
			if err := a.tuyaSync(); err != nil {
				v := a.tuyaView(true)
				v["error"] = err.Error()
				return v, nil
			}
		}
	case "forget-cloud":
		a.Sec.Delete(slotTuyaID)
		a.Sec.Delete(slotTuyaSecret)
	case "sync":
		if err := a.tuyaSyncNow(true); err != nil {
			return nil, err
		}
	case "home-add":
		name := clean(s(body, "name"), 40)
		if name == "" {
			return nil, errors.New("give the house a name")
		}
		st := a.tuya
		st.mu.Lock()
		st.Homes = append(st.Homes, tuyaHome{ID: "c" + strconv.FormatInt(time.Now().UnixNano(), 36), Name: name, Custom: true})
		st.saveLocked()
		st.mu.Unlock()
	case "home-edit", "home-remove": // houses made in HomeOwl only; Smart Life homes are renamed in the app
		st := a.tuya
		st.mu.Lock()
		i := st.home(s(body, "id"))
		if i < 0 || !st.Homes[i].Custom {
			st.mu.Unlock()
			return nil, errors.New("that house comes from Smart Life: rename or remove it in the app")
		}
		if op == "home-edit" {
			if name := clean(s(body, "name"), 40); name != "" {
				st.Homes[i].Name = name
			}
		} else {
			id := st.Homes[i].ID
			st.Homes = append(st.Homes[:i], st.Homes[i+1:]...)
			for j := range st.Devices {
				if st.Devices[j].Place == id {
					st.Devices[j].Place = ""
				}
			}
		}
		st.saveLocked()
		st.mu.Unlock()
	case "home-show": // hide a house's devices (another family's home on the same account) or show them again
		id, on := s(body, "id"), b(body, "on")
		if err := a.Cfg.Update(func(c *config.Config) {
			l := []string{}
			for _, h := range c.Tuya.Hidden {
				if h != id {
					l = append(l, h)
				}
			}
			if !on {
				l = append(l, id)
			}
			c.Tuya.Hidden = l
		}); err != nil {
			return nil, err
		}
	case "tag": // put a device in a house and room, and say what it is
		st := a.tuya
		st.mu.Lock()
		j := st.find(s(body, "id"))
		place := s(body, "place")
		if j < 0 || (place != "" && st.home(place) < 0) {
			st.mu.Unlock()
			return nil, errors.New("that device or house is gone")
		}
		st.Devices[j].Place, st.Devices[j].MyRoom, st.Devices[j].Type = place, clean(s(body, "room"), 40), clean(s(body, "type"), 40)
		if n := clean(s(body, "name"), 60); n != "" && a.Cfg.Get().Tuya.Mode == "local" {
			st.Devices[j].Name = n
		}
		st.saveLocked()
		st.mu.Unlock()
	case "scene", "automation":
		if err := a.tuyaScene(op, s(body, "home"), s(body, "id"), b(body, "on"), by); err != nil {
			return nil, err
		}
	case "scan": // the networks ticked on screen (kept for next time), else the saved ones
		nets := []string{}
		for _, n := range strList(body["networks"]) {
			if _, _, err := net.ParseCIDR(n); err != nil {
				return nil, errors.New(n + " isn't a network (like 192.168.1.0/24)")
			}
			nets = append(nets, n)
		}
		if _, ok := body["networks"]; ok {
			if err := a.Cfg.Update(func(c *config.Config) { c.Tuya.Networks = nets }); err != nil {
				return nil, err
			}
		} else {
			nets = a.Cfg.Get().Tuya.Networks
		}
		if _, err := a.tuyaScan(ctx, nets); err != nil {
			return nil, err
		}
		if a.Cfg.Get().Tuya.Mode == "local" {
			_ = a.tuyaSync()
		}
	case "mark":
		mac := strings.ToLower(s(body, "mac"))
		if !macRE.MatchString(mac) {
			return nil, errors.New("not a device address")
		}
		on := b(body, "on")
		if err := a.Cfg.Update(func(c *config.Config) {
			l := []string{}
			for _, m := range c.Tuya.Marked {
				if m != mac {
					l = append(l, m)
				}
			}
			if on {
				l = append(l, mac)
			}
			c.Tuya.Marked = l
		}); err != nil {
			return nil, err
		}
	case "device": // local mode: add or edit one by hand
		id := strings.TrimSpace(s(body, "id"))
		if !tuyaIDRE.MatchString(id) {
			return nil, errors.New("the device ID is 8–40 letters and digits (Smart Life › the device › ✎ › Device information › Virtual ID)")
		}
		key := strings.TrimSpace(s(body, "local_key"))
		if key != "" && len(key) != 16 {
			return nil, errors.New("a local key is 16 characters")
		}
		ver := s(body, "version")
		if ver != "" && ver != "3.3" && ver != "3.4" && ver != "3.5" {
			return nil, errors.New("pick 3.3, 3.4 or 3.5")
		}
		ip := strings.TrimSpace(s(body, "ip"))
		if key != "" {
			if err := a.Sec.Set(tuyaKeySlot(id), key); err != nil {
				return nil, err
			}
		}
		st := a.tuya
		st.mu.Lock()
		if j := st.find(id); j >= 0 {
			if n := clean(s(body, "name"), 60); n != "" {
				st.Devices[j].Name = n
			}
			if ip != "" {
				st.Devices[j].IP = ip
			}
			if ver != "" {
				st.Devices[j].Version = ver
			}
		} else {
			st.Devices = append(st.Devices, tuyaDev{Device: tuya.Device{ID: id, Name: orStr(clean(s(body, "name"), 60), "Tuya device"), IP: ip, Version: orStr(ver, "3.3")}, Manual: true})
		}
		st.saveLocked()
		st.mu.Unlock()
		_ = a.tuyaSync()
	case "remove":
		id := s(body, "id")
		st := a.tuya
		st.mu.Lock()
		if j := st.find(id); j >= 0 {
			st.Devices = append(st.Devices[:j], st.Devices[j+1:]...)
		}
		st.saveLocked()
		st.mu.Unlock()
		a.Sec.Delete(tuyaKeySlot(id))
	case "firewall-check", "firewall-fix": // let HomeOwl reach Tuya's port on another network (an IoT VLAN)
		view, acts, err := a.tuyaFwPlan(s(body, "network"))
		if err != nil {
			return nil, err
		}
		if op == "firewall-check" {
			if view["fix"], err = a.fwPreview(acts, by); err != nil {
				return nil, err
			}
			return view, nil
		}
		done, err := a.fwApply(acts, by)
		if err != nil {
			return nil, err
		}
		return map[string]any{"done": done}, nil
	case "switch":
		if err := a.tuyaSwitch(s(body, "id"), s(body, "code"), b(body, "on"), by); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown Tuya action")
	}
	return a.tuyaView(true), nil
}

// tuyaSwitch turns one switch of a device on or off, logged in History › Changes.
func (a *App) tuyaSwitch(id, code string, on bool, by string) error {
	st := a.tuya
	st.mu.Lock()
	j := st.find(id)
	var d tuyaDev
	if j >= 0 {
		d = st.Devices[j]
	}
	st.mu.Unlock()
	if j < 0 {
		return errors.New("that Tuya device is gone")
	}
	okCode := false
	for _, sw := range tuyaSwitches(d) {
		if sw["code"] == code {
			okCode = true
		}
	}
	if !okCode {
		return errors.New("that device has no such switch")
	}
	mode := a.Cfg.Get().Tuya.Mode
	word := map[bool]string{true: "on", false: "off"}[on]
	host := "Tuya cloud"
	if mode == "local" {
		host = d.IP
	}
	pub := unifi.Preview{Method: "POST", Path: "/tuya/" + id + "/" + code, Target: "Tuya · " + d.Name,
		Description: "Turn " + word + " " + d.Name, After: map[string]any{code: on}}
	var err error
	if _, lerr := a.Changes.External(host, by, pub, false, func() (int, any) {
		if mode == "local" {
			err = tuya.LocalSet(d.IP, d.ID, a.Sec.Get(tuyaKeySlot(d.ID)), d.Version, code, on)
		} else {
			var cl *tuya.Cloud
			if cl, err = a.tuyaCloud(); err == nil {
				err = cl.Command(d.ID, code, on)
			}
		}
		if err != nil {
			return 502, map[string]any{"error": err.Error()}
		}
		return 200, nil
	}); lerr != nil {
		return lerr
	}
	if err != nil {
		return err
	}
	st.mu.Lock() // show the new state straight away
	if j := st.find(id); j >= 0 {
		if st.Devices[j].DPS != nil {
			st.Devices[j].DPS[code] = on
		}
		for k := range st.Devices[j].Status {
			if st.Devices[j].Status[k].Code == code {
				st.Devices[j].Status[k].Value = on
			}
		}
	}
	st.saveLocked()
	st.mu.Unlock()
	return nil
}

// tuyaErrText: a local error in words.
func tuyaErrText(err error, ip string) string {
	m := err.Error()
	switch {
	case strings.Contains(m, "i/o timeout"), strings.Contains(m, "no route"), strings.Contains(m, "host is down"):
		return "Not answering at " + ip + ". Battery sensors sleep between readings and can't be reached locally."
	case strings.Contains(m, "connection refused"):
		return "It refused the connection at " + ip + ": another app may hold its one local connection, or it isn't a Tuya device."
	case strings.Contains(m, "EOF"), strings.Contains(m, "connection reset"):
		return "It hung up: check the local key and the protocol version."
	}
	return m
}

// ownIPs: this computer's IPv4 addresses (swapped in tests).
var ownIPs = func() []net.IP {
	var out []net.IP
	if l, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range l {
			if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				out = append(out, n.IP.To4())
			}
		}
	}
	return out
}

// homeOwlNet: the UniFi network HomeOwl's computer is on.
func homeOwlNet(nets []issueNet) *issueNet {
	for _, ip := range ownIPs() {
		for i := range nets {
			if nets[i].sn.Contains(ip) {
				return &nets[i]
			}
		}
	}
	return nil
}

// tuyaUnreached: the networks of the last scan where nothing answered and HomeOwl sits elsewhere — most often the
// firewall keeps HomeOwl's network from reaching them (an IoT network).
func (a *App) tuyaUnreached(found []map[string]any) []map[string]any {
	out := []map[string]any{}
	s := a.tuya
	s.mu.Lock()
	scanned := append([]string{}, s.Nets...)
	s.mu.Unlock()
	if len(scanned) == 0 || !a.UniFi.Connected() {
		return out
	}
	nets, err := a.issueNets()
	if err != nil {
		return out
	}
	own := homeOwlNet(nets)
	for _, sub := range scanned {
		_, sn, err := net.ParseCIDR(sub)
		if err != nil || (own != nil && own.Subnet == sn.String()) {
			continue
		}
		hit := false
		for _, f := range found {
			if ip := net.ParseIP(str(f, "ip")); ip != nil && sn.Contains(ip) {
				hit = true
			}
		}
		if hit {
			continue
		}
		for _, n := range nets {
			if n.Subnet == sn.String() {
				out = append(out, map[string]any{"network": n.Subnet, "name": n.Name})
			}
		}
	}
	return out
}

// tuyaFwLabel: the rule's name, so it's recognisable in UniFi and HomeOwl can tell it's there.
func tuyaFwLabel(network string) string { return unifi.FwName("HomeOwl: Tuya on " + network) }

// tuyaFwPlan: one narrow firewall rule that lets HomeOwl's network reach Tuya's port (TCP 6668) on the network
// the devices are on. Nothing else of that network opens up; it can be undone from the Changes page.
func (a *App) tuyaFwPlan(subnet string) (map[string]any, []unifi.Action, error) {
	if !a.UniFi.Connected() {
		return nil, nil, errors.New("connect UniFi first")
	}
	nets, err := a.issueNets()
	if err != nil {
		return nil, nil, err
	}
	var dst *issueNet
	for i := range nets {
		if nets[i].Subnet == subnet {
			dst = &nets[i]
		}
	}
	if dst == nil {
		return nil, nil, errors.New("that isn't one of the console's networks")
	}
	src := homeOwlNet(nets)
	if src == nil {
		return nil, nil, errors.New("couldn't tell which network HomeOwl's computer is on")
	}
	if src.ID == dst.ID {
		return nil, nil, errors.New("HomeOwl is on " + dst.Name + " already, so no firewall is in between")
	}
	label := tuyaFwLabel(dst.Name)
	view := map[string]any{"network": dst.Subnet, "name": dst.Name, "from": src.Name,
		"text": "Let " + src.Name + " (where HomeOwl runs) reach Tuya's port, TCP 6668, on " + dst.Name + ". Nothing else on " + dst.Name + " opens up."}
	desc := "Tuya: let " + src.Name + " reach Tuya devices on " + dst.Name + " (TCP 6668)"
	zoneOf, zoned := a.fwZones()
	if zoned {
		sz, dz := zoneOf(src.ID), zoneOf(dst.ID)
		if sz == nil || dz == nil {
			return nil, nil, errors.New("couldn't find the firewall zones of " + src.Name + " and " + dst.Name)
		}
		if exists, err := a.fwPolicyExists("", label); err == nil && exists {
			view["exists"] = true
			return view, nil, nil
		}
		return view, []unifi.Action{{"kind": "fwpolicy", "src_zone": str(sz, "_id"), "dst_zone": str(dz, "_id"), "network_ids": []string{src.ID},
			"ip": dst.Subnet, "protocol": "tcp", "ports": "6668", "name": label, "from": src.Name, "description": desc}}, nil
	}
	return view, []unifi.Action{
		{"kind": "fwportgroup", "ports": "6668", "name": "HomeOwl Tuya port", "description": "Tuya: the port Tuya devices answer on"},
		{"kind": "fwrule", "network_id": src.ID, "ip": dst.Subnet, "protocol": "tcp", "ports": "6668", "name": label, "from": src.Name,
			"description": desc, "port_group": portGroupPending}}, nil
}

// tuyaHomeIdx: where a home is in the list (-1: not there).
func (a *App) tuyaHomeIdx(homes []tuyaHome, id string) int {
	for i, h := range homes {
		if id != "" && h.ID == id {
			return i
		}
	}
	return -1
}

// tuyaKinds: the device types an admin can pick, in words.
func tuyaKinds() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range tuyaCategory {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// tuyaHomes reads the Smart Life homes of every linked app account: their rooms, which device is where, and their
// scenes and automations. A home whose scenes can't be read (the project lacks that API service) says why.
func (a *App) tuyaHomes(cl *tuya.Cloud, list []tuya.Device) {
	uids := []string{}
	for _, d := range list {
		if d.UID != "" && !contains(uids, d.UID) {
			uids = append(uids, d.UID)
		}
	}
	var homes []tuyaHome
	devHome, devRoom := map[string]string{}, map[string]string{}
	for _, uid := range uids {
		hl, err := cl.Homes(uid)
		if err != nil {
			log.Printf("tuya: homes: %v", err)
			continue
		}
		for _, h := range hl {
			th := tuyaHome{ID: h.ID, Name: h.Name}
			if ids, err := cl.HomeDevices(h.ID); err == nil {
				for _, id := range ids {
					devHome[id] = h.ID
				}
			}
			if rooms, err := cl.Rooms(h.ID); err == nil {
				th.Rooms = rooms
				for _, r := range rooms {
					for _, id := range r.Devices {
						devHome[id], devRoom[id] = h.ID, r.Name
					}
				}
			}
			var serr error
			if th.Scenes, serr = cl.Scenes(h.ID); serr == nil {
				th.Automations, serr = cl.Automations(h.ID)
			}
			if serr != nil {
				th.SceneErr = serr.Error()
			}
			homes = append(homes, th)
		}
	}
	s := a.tuya
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(uids) > 0 && len(homes) == 0 { // couldn't read them now: keep what was there
		s.HomesAt = time.Now().Unix()
		s.saveLocked()
		return
	}
	for _, h := range s.Homes {
		if h.Custom {
			homes = append(homes, h)
		}
	}
	s.Homes = homes
	for i, d := range s.Devices {
		if h, ok := devHome[d.ID]; ok {
			s.Devices[i].Home, s.Devices[i].Room = h, devRoom[d.ID]
		}
	}
	s.HomesAt = time.Now().Unix()
	s.saveLocked()
}

// tuyaScene runs a Tap-to-Run scene or turns an automation on or off, logged in History › Changes.
func (a *App) tuyaScene(kind, home, id string, on bool, by string) error {
	st := a.tuya
	st.mu.Lock()
	name := ""
	if i := st.home(home); i >= 0 {
		l := st.Homes[i].Scenes
		if kind == "automation" {
			l = st.Homes[i].Automations
		}
		for _, x := range l {
			if x.ID == id {
				name = x.Name
			}
		}
	}
	st.mu.Unlock()
	if name == "" {
		return errors.New("that scene is gone — refresh")
	}
	cl, err := a.tuyaCloud()
	if err != nil {
		return err
	}
	desc := "Run the scene " + name
	if kind == "automation" {
		desc = map[bool]string{true: "Turn on", false: "Turn off"}[on] + " the automation " + name
	}
	pub := unifi.Preview{Method: "POST", Path: "/tuya/" + kind + "/" + id, Target: "Tuya · " + name, Description: desc}
	if _, lerr := a.Changes.External("Tuya cloud", by, pub, false, func() (int, any) {
		if kind == "automation" {
			err = cl.SetAutomation(home, id, on)
		} else {
			err = cl.RunScene(home, id)
		}
		if err != nil {
			return 502, map[string]any{"error": err.Error()}
		}
		return 200, nil
	}); lerr != nil {
		return lerr
	}
	if err != nil {
		return err
	}
	if kind == "automation" {
		st.mu.Lock()
		if i := st.home(home); i >= 0 {
			for k := range st.Homes[i].Automations {
				if st.Homes[i].Automations[k].ID == id {
					st.Homes[i].Automations[k].Enabled = on
				}
			}
		}
		st.saveLocked()
		st.mu.Unlock()
	}
	return nil
}
