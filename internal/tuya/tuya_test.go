package tuya

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testKey = "0123456789abcdef"

// fakeDevice answers one connection the way a Tuya device of that version does, holding dps.
func fakeDevice(t *testing.T, version string, dps map[string]any) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFake(c, version, dps)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func serveFake(c net.Conn, version string, dps map[string]any) {
	defer c.Close()
	lk := []byte(testKey)
	var sess []byte
	key := func() []byte {
		if sess != nil {
			return sess
		}
		return lk
	}
	read := func() (frame, bool) {
		hdr := make([]byte, 16)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return frame{}, false
		}
		if version == "3.5" {
			more := make([]byte, 2)
			_, _ = io.ReadFull(c, more)
			h := append(hdr, more...)
			rest := make([]byte, int(binary.BigEndian.Uint32(h[14:]))+4)
			_, _ = io.ReadFull(c, rest)
			f, err := unpack66(append(h, rest...), key())
			return f, err == nil
		}
		rest := make([]byte, binary.BigEndian.Uint32(hdr[12:]))
		_, _ = io.ReadFull(c, rest)
		var hk []byte
		if version == "3.4" {
			hk = key()
		}
		f, err := unpack55(append(hdr, rest...), hk)
		return f, err == nil
	}
	write := func(cmd uint32, payload []byte) {
		p := append([]byte{0, 0, 0, 0}, payload...) // return code 0
		var b []byte
		switch version {
		case "3.5":
			b, _ = pack66(frame{seq: 1, cmd: cmd, payload: p}, key())
		case "3.4":
			b = pack55(frame{seq: 1, cmd: cmd, payload: p}, key())
		default:
			b = pack55(frame{seq: 1, cmd: cmd, payload: p}, nil)
		}
		_, _ = c.Write(b)
	}
	if version != "3.3" { // the handshake
		f, ok := read()
		if !ok || f.cmd != cmdSessStart {
			return
		}
		local := f.payload
		if version == "3.4" {
			local, _ = ecb(lk, local, false)
		}
		remote := make([]byte, 16)
		_, _ = rand.Read(remote)
		res := append(append([]byte{}, remote...), hmac256(lk, local)...)
		if version == "3.4" {
			res, _ = ecb(lk, res, true)
		}
		write(cmdSessRes, res)
		f, ok = read()
		if !ok || f.cmd != cmdSessFinish {
			return
		}
		fin := f.payload
		if version == "3.4" {
			fin, _ = ecb(lk, fin, false)
		}
		if !hmac.Equal(fin, hmac256(lk, remote)) {
			return
		}
		x := make([]byte, 16)
		for i := range x {
			x[i] = local[i] ^ remote[i]
		}
		if version == "3.4" {
			sess, _ = ecb(lk, x, true)
		} else {
			blk, _ := aes.NewCipher(lk)
			g, _ := cipher.NewGCM(blk)
			sess = g.Seal(nil, local[:12], x, nil)[:16]
		}
	}
	for {
		f, ok := read()
		if !ok {
			return
		}
		body := f.payload
		if version == "3.3" && len(body) > 15 && string(body[:3]) == "3.3" {
			body = body[15:]
		}
		if version != "3.5" {
			d, err := ecb(key(), body, false)
			if err != nil {
				return
			}
			body = unpad(d)
		}
		if len(body) > 15 && body[0] == '3' {
			body = body[15:]
		}
		var req map[string]any
		if json.Unmarshal(body, &req) != nil {
			return
		}
		set, _ := req["dps"].(map[string]any)
		if d, ok := req["data"].(map[string]any); ok {
			set, _ = d["dps"].(map[string]any)
		}
		for k, v := range set {
			dps[k] = v
		}
		js, _ := json.Marshal(map[string]any{"devId": "dev1", "dps": dps})
		if version == "3.5" {
			write(f.cmd, append(append([]byte("3.5"), make([]byte, 12)...), js...))
			continue
		}
		enc, _ := ecb(key(), pkcs7(js), true)
		if version == "3.4" {
			enc, _ = ecb(key(), pkcs7(append(append([]byte("3.4"), make([]byte, 12)...), js...)), true)
		}
		write(f.cmd, enc)
	}
}

func TestLocalVersions(t *testing.T) {
	for _, v := range []string{"3.3", "3.4", "3.5"} {
		dps := map[string]any{"1": false, "19": float64(125)}
		old := localPort
		localPort = fakeDevice(t, v, dps)
		st, err := LocalStatus("127.0.0.1", "dev1", testKey, v)
		if err != nil || st["19"] != float64(125) || st["1"] != false {
			t.Fatalf("%s status: %v %v", v, st, err)
		}
		if err := LocalSet("127.0.0.1", "dev1", testKey, v, "1", true); err != nil {
			t.Fatalf("%s set: %v", v, err)
		}
		if st, _ := LocalStatus("127.0.0.1", "dev1", testKey, v); st["1"] != true {
			t.Fatalf("%s switched on: %v", v, st)
		}
		if v != "3.3" {
			if _, err := LocalStatus("127.0.0.1", "dev1", "fedcba9876543210", v); err == nil {
				t.Fatalf("%s: a wrong key must fail", v)
			}
		}
		localPort = old
	}
	if _, err := DialLocal("127.0.0.1", "dev1", "short", "3.3", 0); err == nil {
		t.Fatal("key length")
	}
}

func TestBroadcast(t *testing.T) {
	js := []byte(`{"ip":"192.168.1.50","gwId":"bf0123456789abcdef","active":2,"encrypt":true,"productKey":"keyabc","version":"3.3"}`)
	enc, _ := ecb(udpKey, pkcs7(js), true)
	b := pack55(frame{seq: 0, cmd: 19, payload: append([]byte{0, 0, 0, 0}, enc...)}, nil)
	f, ok := decodeBroadcast(b)
	if !ok || f.IP != "192.168.1.50" || f.ID != "bf0123456789abcdef" || f.Version != "3.3" {
		t.Fatalf("3.3 broadcast: %+v %v", f, ok)
	}
	b35, _ := pack66(frame{cmd: 19, payload: append([]byte{0, 0, 0, 0}, []byte(strings.Replace(string(js), "3.3", "3.5", 1))...)}, udpKey)
	if f, ok := decodeBroadcast(b35); !ok || f.Version != "3.5" {
		t.Fatalf("3.5 broadcast: %+v %v", f, ok)
	}
	if _, ok := decodeBroadcast([]byte("hello")); ok {
		t.Fatal("garbage")
	}
}

func TestCloud(t *testing.T) {
	var cmds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &Cloud{ID: "cid", Secret: "secret"}
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		if r.Header.Get("sign") != c.sign(r.Method, r.URL.RequestURI(), body, r.Header.Get("access_token"), r.Header.Get("t"), r.Header.Get("nonce")) {
			_, _ = w.Write([]byte(`{"success":false,"code":1004,"msg":"sign invalid"}`))
			return
		}
		switch {
		case r.URL.Path == "/v1.0/token":
			_, _ = w.Write([]byte(`{"success":true,"result":{"access_token":"tok","expire_time":7200}}`))
		case strings.HasSuffix(r.URL.Path, "/associated-users/devices"):
			if r.Header.Get("access_token") != "tok" {
				t.Error("no token")
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"has_more":false,"devices":[{"id":"dev1","name":"Desk plug","category":"cz","online":true,"local_key":"k","status":[{"code":"switch_1","value":true},{"code":"cur_power","value":125}]}]}}`))
		case strings.HasSuffix(r.URL.Path, "/commands"):
			cmds = append(cmds, string(body))
			_, _ = w.Write([]byte(`{"success":true,"result":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := NewCloud("eu", "cid", "secret")
	if err != nil {
		t.Fatal(err)
	}
	c.Base = srv.URL
	l, err := c.Devices()
	if err != nil || len(l) != 1 || l[0].Name != "Desk plug" || l[0].LocalKey != "k" || len(l[0].Status) != 2 {
		t.Fatalf("devices: %+v %v", l, err)
	}
	if err := c.Command("dev1", "switch_1", false); err != nil || len(cmds) != 1 || !strings.Contains(cmds[0], `"switch_1"`) {
		t.Fatalf("command: %v %v", cmds, err)
	}
	if _, err := NewCloud("mars", "a", "b"); err == nil {
		t.Fatal("unknown region")
	}
}

func TestSwitchNames(t *testing.T) {
	for in, want := range map[string]string{
		`[{"identifier":"switch_1","name":"Boiler"},{"identifier":"switch_2","name":"Porch"}]`: "Boiler|Porch",
		`{"switch_1":"Boiler","switch_2":"Porch"}`:                                             "Boiler|Porch",
		`{"dp_name":{"switch_1":"Boiler","switch_2":"Porch"}}`:                                 "Boiler|Porch",
		`[{"dp_id":1,"name":"Boiler"},{"dp_id":2,"name":"Porch"}]`:                             "Boiler|Porch",
		`"nothing"`: "|",
	} {
		m := parseSwitchNames(json.RawMessage(in))
		if got := m["switch_1"] + "|" + m["switch_2"]; got != want {
			t.Errorf("%s: %v", in, m)
		}
	}
}
