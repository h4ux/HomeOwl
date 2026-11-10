package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/h4ux/homeowl/internal/protect"
)

// camerasView is the Cameras page: Protect's cameras, the event recorder's state and the recorded events.
func (a *App) camerasView(days int) map[string]any {
	if days < 1 || days > 30 {
		days = 1
	}
	out := map[string]any{"status": a.Protect.Status(), "days": days, "cameras": []protect.Camera{}, "events": []protect.Event{}}
	if !a.UniFi.Connected() {
		out["error"] = "not connected to UniFi"
		return out
	}
	cams, err := protect.Cameras(a.UniFi)
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
	if !a.UniFi.Connected() {
		fail(w, 400, "not connected to UniFi")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	q := r.URL.Query()
	img, typ, err := protect.Snapshot(ctx, a.UniFi, q.Get("id"), q.Get("hq") == "1")
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(img)
}
