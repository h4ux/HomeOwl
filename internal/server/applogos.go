package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/apps"
	"github.com/h4ux/homeowl/internal/config"
)

// App logos (Family › Apps, People cards, the Apps report). HomeOwl ships no vendor artwork: the first time a logo
// is wanted it fetches the icons of every app in the catalogue, at once, through DuckDuckGo's icon service, keeps
// them in <data>/app-logos/ and serves them from its own origin. Asking DuckDuckGo instead of the apps' own sites
// keeps HomeOwl's computer from looking up tiktok.com and the like, which would show in NextDNS as if it used them;
// fetching the whole catalogue tells it nothing about which apps the family uses.

type appLogos struct {
	dir     string
	once    sync.Mutex // one fetch round at a time
	mu      sync.Mutex
	tried   time.Time // the last fetch round (failed icons are tried again a day later)
	missing map[string]bool
}

func newAppLogos(dataDir string) *appLogos {
	return &appLogos{dir: filepath.Join(dataDir, "app-logos"), missing: map[string]bool{}}
}

// appIconURL is where an app's icon comes from (a variable for tests).
var appIconURL = func(site string) string { return "https://icons.duckduckgo.com/ip3/" + url.PathEscape(site) + ".ico" }

var appLogoClient = logoClient

func (l *appLogos) file(id string) string { return filepath.Join(l.dir, id+".img") }

// Get returns the app's logo, fetching the catalogue's icons first if this is the first time (or a day after a
// failed try).
func (l *appLogos) Get(ctx context.Context, id string) ([]byte, string, bool) {
	if _, ok := apps.Get(id); !ok {
		return nil, "", false
	}
	if b, err := os.ReadFile(l.file(id)); err == nil {
		return b, http.DetectContentType(b), true
	}
	l.mu.Lock()
	due := l.tried.IsZero() || l.missing[id] && time.Since(l.tried) > 24*time.Hour
	l.mu.Unlock()
	if !due {
		return nil, "", false
	}
	l.fetchAll(ctx)
	if b, err := os.ReadFile(l.file(id)); err == nil {
		return b, http.DetectContentType(b), true
	}
	return nil, "", false
}

// fetchAll gets every catalogue app's icon it doesn't have yet, a few at a time.
func (l *appLogos) fetchAll(ctx context.Context) {
	l.once.Lock()
	defer l.once.Unlock()
	l.mu.Lock()
	again := !l.tried.IsZero() && time.Since(l.tried) < 24*time.Hour
	l.mu.Unlock()
	if again { // another request just did it
		return
	}
	_ = os.MkdirAll(l.dir, 0o700)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	c := appLogoClient()
	missing := map[string]bool{}
	var mu sync.Mutex
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, a := range apps.Catalog {
		if _, err := os.Stat(l.file(a.ID)); err == nil {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			err := l.fetch(ctx, c, id)
			if err != nil {
				mu.Lock()
				missing[id] = true
				mu.Unlock()
			}
		}(a.ID)
	}
	wg.Wait()
	l.mu.Lock()
	l.tried, l.missing = time.Now(), missing
	l.mu.Unlock()
}

func (l *appLogos) fetch(ctx context.Context, c *http.Client, id string) error {
	site := apps.Site(id)
	if site == "" {
		return errors.New("no site")
	}
	b, _, err := get(ctx, c, appIconURL(site), 256<<10)
	if err != nil {
		return err
	}
	if !logoTypes[http.DetectContentType(b)] {
		return errors.New("not an image")
	}
	return config.WriteFileAtomic(l.file(id), b, 0o600)
}
