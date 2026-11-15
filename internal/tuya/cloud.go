// Package tuya talks to Tuya (Smart Life) devices: through the Tuya Cloud API (a Tuya IoT Platform project linked to
// the family's Smart Life app) or directly on the home network with each device's local key.
package tuya

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Regions: the Tuya data centre the project lives in (Tuya IoT Platform › Cloud › Project › Overview).
var Regions = map[string]string{
	"eu":   "https://openapi.tuyaeu.com",
	"eu-w": "https://openapi-weaz.tuyaeu.com",
	"us":   "https://openapi.tuyaus.com",
	"us-e": "https://openapi-ueaz.tuyaus.com",
	"cn":   "https://openapi.tuyacn.com",
	"in":   "https://openapi.tuyain.com",
}

// Status is one data point of a device: a code ("switch_1", "cur_power") or, locally, a DP number ("1", "19").
type Status struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

// Device is a Tuya device as HomeOwl keeps it. LocalKey never leaves the server (the secrets store keeps it).
type Device struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category,omitempty"` // Tuya category: cz/pc plugs, kg switches, dj lights, wsdcg sensors…
	Product  string   `json:"product,omitempty"`
	Online   bool     `json:"online"`
	IP       string   `json:"ip,omitempty"` // on the home network (local mode or discovery)
	Version  string   `json:"version,omitempty"`
	Status   []Status `json:"status,omitempty"`
	LocalKey string   `json:"-"`
	UID      string   `json:"-"` // cloud: the app account it belongs to
	Owner    string   `json:"-"` // cloud: the home it's in, when Tuya says
}

// Cloud is a Tuya Cloud API client (OpenAPI, HMAC-SHA256 signed requests).
type Cloud struct {
	Base, ID, Secret string
	HTTP             *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewCloud(region, id, secret string) (*Cloud, error) {
	base, ok := Regions[region]
	if !ok {
		return nil, errors.New("pick the Tuya data centre your project is in")
	}
	if id == "" || secret == "" {
		return nil, errors.New("the Access ID and Access Secret are both needed")
	}
	return &Cloud{Base: base, ID: id, Secret: secret, HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sign: Tuya's request signature — HMAC-SHA256 over client id (+ token) + time + nonce + the request.
func (c *Cloud) sign(method, path string, body []byte, token, t, n string) string {
	sum := sha256.Sum256(body)
	u, _ := url.Parse(path)
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p := u.Path
	if len(keys) > 0 {
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, k+"="+q.Get(k))
		}
		p += "?" + strings.Join(parts, "&")
	}
	str := c.ID + token + t + n + method + "\n" + hex.EncodeToString(sum[:]) + "\n\n" + p
	m := hmac.New(sha256.New, []byte(c.Secret))
	m.Write([]byte(str))
	return strings.ToUpper(hex.EncodeToString(m.Sum(nil)))
}

type apiResp struct {
	Success bool            `json:"success"`
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Result  json.RawMessage `json:"result"`
}

func (c *Cloud) do(method, path string, body any, token string) (json.RawMessage, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	t, n := fmt.Sprint(time.Now().UnixMilli()), nonce()
	req, err := http.NewRequest(method, c.Base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("client_id", c.ID)
	req.Header.Set("t", t)
	req.Header.Set("nonce", n)
	req.Header.Set("sign_method", "HMAC-SHA256")
	req.Header.Set("sign", c.sign(method, path, raw, token, t, n))
	if token != "" {
		req.Header.Set("access_token", token)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("can't reach Tuya: " + err.Error())
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	var r apiResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("Tuya answered HTTP %d", res.StatusCode)
	}
	if !r.Success {
		return nil, fmt.Errorf("Tuya: %s (code %d)", r.Msg, r.Code)
	}
	return r.Result, nil
}

func (c *Cloud) tok() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	res, err := c.do("GET", "/v1.0/token?grant_type=1", nil, "")
	if err != nil {
		return "", err
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpireTime  int    `json:"expire_time"`
	}
	if err := json.Unmarshal(res, &t); err != nil || t.AccessToken == "" {
		return "", errors.New("Tuya didn't give a token")
	}
	c.token, c.expires = t.AccessToken, time.Now().Add(time.Duration(t.ExpireTime-60)*time.Second)
	return c.token, nil
}

func (c *Cloud) call(method, path string, body any) (json.RawMessage, error) {
	t, err := c.tok()
	if err != nil {
		return nil, err
	}
	res, err := c.do(method, path, body, t)
	if err != nil && strings.Contains(err.Error(), "token invalid") { // expired early: once more with a new one
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		if t, err = c.tok(); err == nil {
			res, err = c.do(method, path, body, t)
		}
	}
	return res, err
}

type cloudDevice struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	ProductName string          `json:"product_name"`
	Online      bool            `json:"online"`
	LocalKey    string          `json:"local_key"`
	Status      []Status        `json:"status"`
	UID         string          `json:"uid"`
	OwnerID     json.RawMessage `json:"owner_id"`
}

// Devices lists every device of the app accounts linked to the project, with their status and local keys.
func (c *Cloud) Devices() ([]Device, error) {
	out := []Device{}
	last := ""
	for page := 0; page < 40; page++ {
		res, err := c.call("GET", "/v1.0/iot-01/associated-users/devices?size=50&last_row_key="+url.QueryEscape(last), nil)
		if err != nil {
			return nil, err
		}
		var r struct {
			Devices    []cloudDevice `json:"devices"`
			HasMore    bool          `json:"has_more"`
			LastRowKey string        `json:"last_row_key"`
		}
		if err := json.Unmarshal(res, &r); err != nil {
			return nil, errors.New("couldn't read Tuya's device list")
		}
		for _, d := range r.Devices {
			out = append(out, Device{ID: d.ID, Name: d.Name, Category: d.Category, Product: d.ProductName, Online: d.Online, Status: d.Status, LocalKey: d.LocalKey,
				UID: d.UID, Owner: anyID(d.OwnerID)})
		}
		if !r.HasMore || r.LastRowKey == "" || r.LastRowKey == last {
			break
		}
		last = r.LastRowKey
	}
	return out, nil
}

// Command sends one data point to a device ({"code": "switch_1", "value": true}).
func (c *Cloud) Command(id, code string, value any) error {
	_, err := c.call("POST", "/v1.0/iot-03/devices/"+url.PathEscape(id)+"/commands", map[string]any{"commands": []map[string]any{{"code": code, "value": value}}})
	return err
}

// Unit is how a numeric data point reads: its unit and how many decimals the integer carries.
type Unit struct {
	Unit  string `json:"unit,omitempty"`
	Scale int    `json:"scale,omitempty"`
}

// Spec: the units of a device's data points, by code.
func (c *Cloud) Spec(id string) (map[string]Unit, error) {
	res, err := c.call("GET", "/v1.0/iot-03/devices/"+url.PathEscape(id)+"/specification", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Status []struct {
			Code   string `json:"code"`
			Type   string `json:"type"`
			Values string `json:"values"`
		} `json:"status"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	out := map[string]Unit{}
	for _, s := range r.Status {
		var u Unit
		if s.Type == "Integer" && json.Unmarshal([]byte(s.Values), &u) == nil {
			out[s.Code] = u
		}
	}
	return out, nil
}

// anyID reads an ID Tuya sends as a number or as a string.
func anyID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// Home is one Smart Life home (the app's "home" switcher) with its rooms.
type Home struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Rooms []Room `json:"rooms,omitempty"`
}

type Room struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Devices []string `json:"devices,omitempty"`
}

// Homes lists the homes of one linked app account.
func (c *Cloud) Homes(uid string) ([]Home, error) {
	res, err := c.call("GET", "/v1.0/users/"+url.PathEscape(uid)+"/homes", nil)
	if err != nil {
		return nil, err
	}
	var l []struct {
		HomeID json.RawMessage `json:"home_id"`
		Name   string          `json:"name"`
	}
	if err := json.Unmarshal(res, &l); err != nil {
		return nil, errors.New("couldn't read Tuya's home list")
	}
	out := []Home{}
	for _, h := range l {
		if id := anyID(h.HomeID); id != "" {
			out = append(out, Home{ID: id, Name: h.Name})
		}
	}
	return out, nil
}

// HomeDevices: the IDs of the devices in a home.
func (c *Cloud) HomeDevices(home string) ([]string, error) {
	return c.deviceIDs("/v1.0/homes/" + url.PathEscape(home) + "/devices")
}

func (c *Cloud) deviceIDs(path string) ([]string, error) {
	res, err := c.call("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var l []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res, &l); err != nil {
		return nil, errors.New("couldn't read Tuya's device list")
	}
	out := []string{}
	for _, d := range l {
		out = append(out, d.ID)
	}
	return out, nil
}

// Rooms: a home's rooms and the devices in each.
func (c *Cloud) Rooms(home string) ([]Room, error) {
	res, err := c.call("GET", "/v1.0/homes/"+url.PathEscape(home)+"/rooms", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Rooms []struct {
			RoomID json.RawMessage `json:"room_id"`
			Name   string          `json:"name"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, errors.New("couldn't read Tuya's rooms")
	}
	out := []Room{}
	for _, x := range r.Rooms {
		id := anyID(x.RoomID)
		if id == "" {
			continue
		}
		devs, err := c.deviceIDs("/v1.0/homes/" + url.PathEscape(home) + "/rooms/" + url.PathEscape(id) + "/devices")
		if err != nil {
			return nil, err
		}
		out = append(out, Room{ID: id, Name: x.Name, Devices: devs})
	}
	return out, nil
}

// Scene is a Smart Life "Tap-to-Run" scene or an automation.
type Scene struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (c *Cloud) scenes(path, idKey string) ([]Scene, error) {
	res, err := c.call("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var l []map[string]json.RawMessage
	if err := json.Unmarshal(res, &l); err != nil {
		return nil, errors.New("couldn't read Tuya's scenes")
	}
	out := []Scene{}
	for _, m := range l {
		var name string
		_ = json.Unmarshal(m["name"], &name)
		en := true
		if raw, ok := m["enabled"]; ok {
			_ = json.Unmarshal(raw, &en)
		}
		if id := anyID(m[idKey]); id != "" {
			out = append(out, Scene{ID: id, Name: name, Enabled: en})
		}
	}
	return out, nil
}

// Scenes: a home's Tap-to-Run scenes (needs the project's "Smart Home Scene Linkage" API service).
func (c *Cloud) Scenes(home string) ([]Scene, error) {
	return c.scenes("/v1.0/homes/"+url.PathEscape(home)+"/scenes", "scene_id")
}

// Automations: a home's automations (same API service).
func (c *Cloud) Automations(home string) ([]Scene, error) {
	return c.scenes("/v1.0/homes/"+url.PathEscape(home)+"/automations", "automation_id")
}

// RunScene runs a Tap-to-Run scene.
func (c *Cloud) RunScene(home, id string) error {
	_, err := c.call("POST", "/v1.0/homes/"+url.PathEscape(home)+"/scenes/"+url.PathEscape(id)+"/trigger", nil)
	return err
}

// SetAutomation turns an automation on or off.
func (c *Cloud) SetAutomation(home, id string, on bool) error {
	act := "disable"
	if on {
		act = "enable"
	}
	_, err := c.call("PUT", "/v1.0/homes/"+url.PathEscape(home)+"/automations/"+url.PathEscape(id)+"/actions/"+act, nil)
	return err
}
