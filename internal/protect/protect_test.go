package protect

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // WebSocket handshake
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/unifi"
)

func TestStoreApply(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "ev.json"))
	now := time.Now().UnixMilli()
	msgs := []string{
		`{"type":"add","item":{"id":"e1","modelKey":"event","type":"smartDetectZone","start":` + itoa64(now-5000) + `,"device":"cam1","smartDetectTypes":["person"]}}`,
		`{"type":"update","item":{"id":"e1","modelKey":"event","end":` + itoa64(now-1000) + `,"smartDetectTypes":["person","package"]}}`,
		`{"type":"add","item":{"id":"e2","modelKey":"event","type":"ring","start":` + itoa64(now) + `,"device":"bell"}}`,
		`{"type":"add","item":{"id":"c1","modelKey":"camera"}}`, // not an event
		`not json`,
	}
	for _, m := range msgs {
		s.Apply([]byte(m))
	}
	l := s.Since(0)
	if len(l) != 2 || l[0].ID != "e2" || l[0].Type != "ring" || l[1].Type != "smart" || l[1].End == 0 || len(l[1].Kinds) != 2 || l[1].Camera != "cam1" {
		t.Fatalf("events: %+v", l)
	}
	old := `{"type":"add","item":{"id":"old","modelKey":"event","type":"motion","start":` + itoa64(time.Now().AddDate(0, 0, -40).UnixMilli()) + `,"device":"cam1"}}`
	s.Apply([]byte(old))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(s.path)
	if l := s2.Since(0); len(l) != 2 {
		t.Fatalf("after reload (old one pruned): %+v", l)
	}
	s2.Apply([]byte(`{"type":"remove","item":{"id":"e2"}}`))
	if l := s2.Since(0); len(l) != 1 {
		t.Fatalf("after remove: %+v", l)
	}
}

func itoa64(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestCameraFrom(t *testing.T) {
	var m map[string]any
	_ = json.Unmarshal([]byte(`{"id":"a1","name":"Front door","state":"CONNECTED","mac":"AABBCCDDEEFF","lcdMessage":{},
	  "featureFlags":{"hasMic":true},"isMicEnabled":false,"smartDetectSettings":{"objectTypes":["person","package"],"audioTypes":["alrmSmoke"]}}`), &m)
	c := cameraFrom(m)
	if !c.Doorbell || c.Mic || len(c.Smart) != 3 || c.MAC != "aabbccddeeff" {
		t.Fatalf("%+v", c)
	}
	if validID("../x") || !validID("66b0e1c2a0f4") {
		t.Fatal("validID")
	}
}

// fakeProtect is a console with Protect: cameras, a snapshot and an event stream that sends two events.
func fakeProtect(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/integration/v1/info", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"applicationVersion":"9.0"}`) })
	mux.HandleFunc("/proxy/protect/integration/v1/meta/info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"applicationVersion":"6.1.0"}`)
	})
	mux.HandleFunc("/proxy/protect/integration/v1/cameras", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"id":"cam2","name":"Yard","state":"DISCONNECTED"},{"id":"cam1","name":"Driveway","state":"CONNECTED"}]`)
	})
	mux.HandleFunc("/proxy/protect/integration/v1/cameras/cam1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	})
	mux.HandleFunc("/proxy/protect/integration/v1/subscribe/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "k" {
			w.WriteHeader(401)
			return
		}
		h := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) //nolint:gosec
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(h[:]) + "\r\n\r\n")
		frame(rw.Writer, 0x9, nil) // a ping first
		frame(rw.Writer, 0x1, []byte(`{"type":"add","item":{"id":"e1","modelKey":"event","type":"motion","start":1700000000000,"device":"cam1"}}`))
		big := `{"type":"add","item":{"id":"e2","modelKey":"event","type":"smartDetectZone","start":1700000001000,"device":"cam1","smartDetectTypes":["vehicle"],"pad":"` + strings.Repeat("x", 300) + `"}}`
		frame(rw.Writer, 0x1, []byte(big)) // > 125 bytes: 16-bit length
		_ = rw.Flush()
		// read the client's pong (masked), then wait until the client goes away
		br := bufio.NewReader(conn)
		_, _ = br.ReadByte()
		_, _ = io.Copy(io.Discard, br)
	})
	return httptest.NewTLSServer(mux)
}

func frame(w *bufio.Writer, op byte, p []byte) {
	_ = w.WriteByte(0x80 | op)
	if len(p) < 126 {
		_ = w.WriteByte(byte(len(p)))
	} else {
		_ = w.WriteByte(126)
		_ = w.WriteByte(byte(len(p) >> 8))
		_ = w.WriteByte(byte(len(p)))
	}
	_, _ = w.Write(p)
}

func TestWatcherAndCameras(t *testing.T) {
	srv := fakeProtect(t)
	defer srv.Close()
	c := unifi.New()
	if _, err := c.Connect(srv.URL, "k", false, ""); err != nil {
		t.Fatal(err)
	}
	cams, err := Cameras(c)
	if err != nil || len(cams) != 2 || cams[0].Name != "Driveway" {
		t.Fatalf("cameras %+v %v", cams, err)
	}
	img, typ, err := Snapshot(context.Background(), c, "cam1", false)
	if err != nil || typ != "image/jpeg" || len(img) != 4 {
		t.Fatalf("snapshot %v %q %v", img, typ, err)
	}
	w := &Watcher{Client: func() *unifi.Client { return c }, Store: NewStore(filepath.Join(t.TempDir(), "ev.json"))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(w.Store.Since(0)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if l := w.Store.Since(0); len(l) != 2 || l[0].Kinds[0] != "vehicle" {
		t.Fatalf("events %+v (status %+v)", l, w.Status())
	}
	if st := w.Status(); st.State != "live" || st.Version != "6.1.0" {
		t.Fatalf("status %+v", st)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher didn't stop")
	}
}

// A Cloud Gateway has no Protect: its address answers every Protect path with the console's web page (HTTP 200).
func TestConsoleWithoutProtect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/network/integration/v1/info" {
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><html><body>UniFi OS</body></html>"))
	}))
	defer srv.Close()
	c := unifi.New()
	if _, err := c.Connect(srv.URL, "k", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Cameras(c); !errors.Is(err, ErrNoProtect) {
		t.Fatalf("cameras: %v, want ErrNoProtect", err)
	}
	w := &Watcher{Client: func() *unifi.Client { return c }, Store: NewStore(filepath.Join(t.TempDir(), "ev.json"))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for w.Status().State != "off" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if st := w.Status(); st.State != "off" || !strings.Contains(st.Error, "isn't running") {
		t.Fatalf("watcher: %+v", st)
	}
	other := unifi.New()
	if _, err := other.ConnectApp(srv.URL, "k", false, InfoPath); err == nil || !strings.Contains(err.Error(), "not as UniFi Protect") || other.Connected() {
		t.Fatalf("ConnectApp to a console without Protect: %v", err)
	}
}
