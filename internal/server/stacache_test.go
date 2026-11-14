package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientByIPCache(t *testing.T) {
	var hits, fail atomic.Int32
	console := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/integration/v1/info"):
			_, _ = w.Write([]byte(`{"applicationVersion":"9.0"}`))
		case strings.HasSuffix(r.URL.Path, "/stat/sta"):
			hits.Add(1)
			if fail.Load() > 0 {
				fail.Add(-1)
				w.WriteHeader(502)
				return
			}
			_, _ = w.Write([]byte(`{"meta":{"rc":"ok"},"data":[{"mac":"00:11:22:00:00:01","ip":"192.168.1.20"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer console.Close()
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UniFi.Connect(console.URL, "test-key", false, "default"); err != nil {
		t.Fatal(err)
	}
	hits.Store(0)
	for i := 0; i < 5; i++ {
		if c, err := a.clientByIP("192.168.1.20"); err != nil || c == nil {
			t.Fatalf("found: %v %v", c, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("five look-ups, one console call: %d", hits.Load())
	}
	// an address not in the list: looked up again only with a list under a second old
	a.sta.at = time.Now().Add(-2 * time.Second)
	if c, _ := a.clientByIP("192.168.1.99"); c != nil || hits.Load() != 2 {
		t.Fatalf("unknown address: one fresh look, %d calls", hits.Load())
	}
	// the console stumbles twice: retried, not an error
	a.sta.list = nil
	fail.Store(2)
	if c, err := a.clientByIP("192.168.1.20"); err != nil || c == nil {
		t.Fatalf("retried: %v %v", c, err)
	}
}
