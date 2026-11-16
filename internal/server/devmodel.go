package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/mdns"
)

// The device's model for its NextDNS setup (devprofile.go): iPhones, iPads and Macs announce their model identifier
// over mDNS, so HomeOwl asks the device directly — when it checks in, when its page is opened, and when it downloads
// its profile — while it's awake on the home network. An admin's choice wins over what was found.

// deviceModel asks the device at ip for its model (swapped in tests).
var deviceModel = func(ctx context.Context, ip string) string { return mdns.Model(ctx, net.ParseIP(ip)) }

var modelAsked struct {
	sync.Mutex
	at    map[string]time.Time
	found map[string]string // what each device answered, for its check-in form before there's a check-in
}

// foundModel is what the device at mac last said it is ("" = nothing yet).
func foundModel(mac string) string {
	modelAsked.Lock()
	defer modelAsked.Unlock()
	return modelAsked.found[mac]
}

// detectModel asks the device at ip for its model and keeps it on its check-ins. At most once every 10 minutes
// per device; wait=true waits for the answer (up to 1.5 s), else it's asked in the background.
func (a *App) detectModel(mac, ip string, wait bool) {
	modelAsked.Lock()
	if modelAsked.at == nil {
		modelAsked.at = map[string]time.Time{}
	}
	if t, ok := modelAsked.at[mac]; ok && time.Since(t) < 10*time.Minute {
		modelAsked.Unlock()
		return
	}
	modelAsked.at[mac] = time.Now()
	modelAsked.Unlock()
	run := func() {
		d := 3 * time.Second
		if wait {
			d = 1500 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		m := deviceModel(ctx, ip)
		if !ndModelRE.MatchString(m) {
			return
		}
		modelAsked.Lock()
		if modelAsked.found == nil {
			modelAsked.found = map[string]string{}
		}
		modelAsked.found[mac] = m
		modelAsked.Unlock()
		for _, x := range a.checkins.List() {
			if x.MAC == mac && x.DetModel != m && (x.Status == "pending" || x.Status == "approved") {
				a.checkins.Edit(x.ID, func(c *CheckIn) { c.DetModel = m })
			}
		}
	}
	if wait {
		run()
	} else {
		go run()
	}
}

// ndModelOf is the model the device's setup reports: an admin's choice, else the one picked on the device, else the
// one found on it.
func ndModelOf(x CheckIn) string {
	switch {
	case x.NDModel == "-":
		return ""
	case ndModelRE.MatchString(x.NDModel):
		return x.NDModel
	case x.UserModel == "-":
		return ""
	case ndModelRE.MatchString(x.UserModel):
		return x.UserModel
	case ndModelRE.MatchString(x.DetModel):
		return x.DetModel
	}
	return ""
}

// hiModel: POST /api/hi/model {model} from the device's own page — which model it is ("" = the one found, "-" = none).
func (a *App) hiModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "not allowed")
		return
	}
	_, mac, ok := a.hiDevice(w, r)
	if !ok {
		return
	}
	x, ok := a.checkins.Registered(mac)
	if !ok {
		fail(w, 404, "This device isn't approved yet.")
		return
	}
	if !a.checkins.allow(remoteIP(r)) {
		fail(w, 429, "That's a lot of requests — try again later.")
		return
	}
	m := strings.TrimSpace(s(readJSON(r), "model"))
	if m != "" && m != "-" && !ndModelRE.MatchString(m) {
		fail(w, 400, "That isn't a model this list knows.")
		return
	}
	a.checkins.Edit(x.ID, func(c *CheckIn) { c.UserModel = m })
	x, _ = a.checkins.Get(x.ID)
	send(w, 200, map[string]any{"registered": a.deviceSetup(x)})
}

// modelView is the device page's model picker: the list, what's used now, and who decided.
func modelView(x CheckIn) map[string]any {
	by := "found"
	switch {
	case x.NDModel != "":
		by = "admin"
	case x.UserModel != "":
		by = "device"
	case x.DetModel == "":
		by = ""
	}
	return map[string]any{"current": ndModelOf(x), "found": x.DetModel, "by": by, "name": modelName(ndModelOf(x)), "groups": modelGroups()}
}
