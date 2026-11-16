package server

import (
	"context"
	"net"
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
	at map[string]time.Time
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

// ndModelOf is the model the device's setup reports: the admin's choice, else the one found on the device.
func ndModelOf(x CheckIn) string {
	switch {
	case x.NDModel == "-":
		return ""
	case ndModelRE.MatchString(x.NDModel):
		return x.NDModel
	case ndModelRE.MatchString(x.DetModel):
		return x.DetModel
	}
	return ""
}
