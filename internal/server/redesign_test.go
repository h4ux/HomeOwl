package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestQRTheme(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.setQRTheme(map[string]any{"theme": "<script>"}); err == nil {
		t.Fatal("unknown theme accepted")
	}
	if _, err := a.setQRTheme(map[string]any{"theme": "night"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Cfg.Get().Guest.QRTheme; got != "night" {
		t.Fatalf("theme = %q", got)
	}
}

func TestSnapCacheSharesOneFetch(t *testing.T) {
	var c snapCache
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(ctx context.Context, id string, hq bool) ([]byte, string, error) {
		calls.Add(1)
		<-release
		return []byte{0xff, 0xd8}, "image/jpeg", nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			img, typ, err := c.get(context.Background(), "cam1", false, fetch)
			if err != nil || len(img) != 2 || typ != "image/jpeg" {
				t.Errorf("get: %v %v %q", err, img, typ)
			}
		}()
	}
	for calls.Load() == 0 {
		// wait until the first caller is fetching
	}
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}
	// a different quality is its own picture
	if _, _, err := c.get(context.Background(), "cam1", true, fetch); err != nil || calls.Load() != 2 {
		t.Fatalf("hq: %v, calls %d", err, calls.Load())
	}
}

func TestStartup(t *testing.T) {
	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if v := a.startupView(); v["available"] != false {
		t.Fatalf("nil startup: %v", v)
	}
	installed := false
	a.Startup = &Startup{Kind: "at login", CanChange: true,
		Installed: func() (bool, error) { return installed, nil },
		Enable:    func() error { installed = true; return nil },
		Disable:   func() error { installed = false; return nil }}
	v, err := a.setStartup(true)
	if err != nil || !installed || v.(map[string]any)["enabled"] != true {
		t.Fatalf("enable: %v %v", v, err)
	}
	if _, err := a.setStartup(false); err != nil || installed {
		t.Fatalf("disable: %v", err)
	}
	a.Startup.CanChange = false
	a.Startup.Enable = func() error { return errors.New("must not run") }
	if _, err := a.setStartup(true); err == nil {
		t.Fatal("changed without rights")
	}
}
