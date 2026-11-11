package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/protect"
)

// camerasView is the Cameras page: Protect's cameras, the event recorder's state and the recorded events.
func (a *App) camerasView(days int) map[string]any {
	if days < 1 || days > 30 {
		days = 1
	}
	out := map[string]any{"status": a.Protect.Status(), "days": days, "cameras": []protect.Camera{}, "events": []protect.Event{}}
	out["protect_console"] = a.Cfg.Get().Cameras.ProtectHost
	pc := a.protectClient()
	if !pc.Connected() {
		out["error"] = "not connected to UniFi"
		if out["protect_console"] != "" {
			out["error"] = "can't reach the Protect console: " + orDefault(a.ProtectUniFi.LastError, "not connected yet")
			out["no_protect"] = true
		}
		return out
	}
	cams, err := protect.Cameras(pc)
	if err != nil {
		out["error"] = err.Error()
		out["no_protect"] = errors.Is(err, protect.ErrNoProtect)
		return out
	}
	out["cameras"] = cams
	ev := a.Protect.Store.Since(time.Now().AddDate(0, 0, -days).UnixMilli())
	if len(ev) > 3000 {
		ev, out["truncated"] = ev[:3000], true
	}
	out["events"] = ev
	return out
}

func (a *App) cameraSnapshot(w http.ResponseWriter, r *http.Request) {
	pc := a.protectClient()
	if !pc.Connected() {
		fail(w, 400, "not connected to the console with Protect")
		return
	}
	q := r.URL.Query()
	img, typ, err := a.snaps.get(r.Context(), q.Get("id"), q.Get("hq") == "1", func(ctx context.Context, id string, hq bool) ([]byte, string, error) {
		return protect.Snapshot(ctx, pc, id, hq)
	})
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(img)
}

// snapCache shares one fresh picture per camera for a moment, so several open pages (or Live mode) ask Protect
// at most about once a second per camera and quality; callers that arrive while a request runs wait for it.
type snapCache struct {
	mu sync.Mutex
	m  map[string]*snapEntry
}

type snapEntry struct {
	at   time.Time
	img  []byte
	typ  string
	err  error
	done chan struct{}
}

// reset forgets every picture (after switching to another Protect console).
func (c *snapCache) reset() {
	c.mu.Lock()
	c.m = nil
	c.mu.Unlock()
}

func (c *snapCache) get(ctx context.Context, id string, hq bool, fetch func(context.Context, string, bool) ([]byte, string, error)) ([]byte, string, error) {
	key := id + "|" + strconv.FormatBool(hq)
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*snapEntry{}
	}
	e := c.m[key]
	if e != nil {
		select {
		case <-e.done:
			if time.Since(e.at) < 900*time.Millisecond {
				c.mu.Unlock()
				return e.img, e.typ, e.err
			}
			e = nil
		default: // a request is running: wait for it below
		}
	}
	if e == nil {
		if len(c.m) > 200 { // forget old pictures
			for k, v := range c.m {
				select {
				case <-v.done:
					if time.Since(v.at) > time.Minute {
						delete(c.m, k)
					}
				default:
				}
			}
		}
		e = &snapEntry{done: make(chan struct{})}
		c.m[key] = e
		c.mu.Unlock()
		fctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		e.img, e.typ, e.err = fetch(fctx, id, hq)
		cancel()
		e.at = time.Now()
		close(e.done)
		return e.img, e.typ, e.err
	}
	c.mu.Unlock()
	select {
	case <-e.done:
		return e.img, e.typ, e.err
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}
