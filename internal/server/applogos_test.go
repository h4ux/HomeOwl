package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/apps"
)

func TestAppLogos(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "zoom.us"):
			http.NotFound(w, r) // no icon for this one
		case strings.Contains(r.URL.Path, "tumblr.com"):
			_, _ = w.Write([]byte("<html>not an image</html>"))
		default:
			_, _ = w.Write(png)
		}
	}))
	defer srv.Close()
	oldURL, oldClient := appIconURL, appLogoClient
	appIconURL = func(site string) string { return srv.URL + "/ip3/" + site + ".ico" }
	appLogoClient = func() *http.Client { return srv.Client() }
	defer func() { appIconURL, appLogoClient = oldURL, oldClient }()

	l := newAppLogos(t.TempDir())
	if _, _, ok := l.Get(context.Background(), "../etc/passwd"); ok || n.Load() != 0 {
		t.Fatal("an unknown app must not be fetched")
	}
	b, typ, ok := l.Get(context.Background(), "tiktok")
	if !ok || typ != "image/png" || len(b) != len(png) {
		t.Fatalf("tiktok: %v %s", ok, typ)
	}
	if int(n.Load()) != len(apps.Catalog) {
		t.Fatalf("the whole catalogue is fetched at once (so nothing tells which apps are used): %d of %d", n.Load(), len(apps.Catalog))
	}
	for _, id := range []string{"zoom", "tumblr"} {
		if _, _, ok := l.Get(context.Background(), id); ok {
			t.Errorf("%s: no logo expected", id)
		}
	}
	if _, _, ok := l.Get(context.Background(), "x"); !ok || int(n.Load()) != len(apps.Catalog) {
		t.Fatalf("logos come from the cache: %d requests", n.Load())
	}
	// a failed icon is tried again a day later, not on every page view
	l.tried = time.Now().Add(-25 * time.Hour)
	l.Get(context.Background(), "zoom")
	if int(n.Load()) != len(apps.Catalog)+2 {
		t.Fatalf("retry: %d requests", n.Load())
	}
	if apps.Site("x") != "x.com" || apps.Site("tiktok") != "tiktok.com" || apps.Site("nope") != "" {
		t.Fatal("sites")
	}
}
