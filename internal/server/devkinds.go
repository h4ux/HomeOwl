package server

import (
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/protect"
)

// Device kinds HomeOwl knows from elsewhere than the client's name: UniFi Protect's cameras (a camera is a plain
// Ubiquiti client to UniFi Network, named after its room) and Tuya devices. Keyed by MAC; the browser's icons use
// them before guessing from names (icons.js clientKind).

var detected struct {
	sync.Mutex
	at    time.Time
	kinds map[string]string
}

const detectedFresh = 10 * time.Minute

// normMAC writes any MAC spelling (AABBCC…, aa-bb-…) as aa:bb:cc:dd:ee:ff, or "" if it isn't one.
func normMAC(s string) string {
	h := make([]byte, 0, 12)
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			h = append(h, byte(c))
		case c == ':' || c == '-' || c == '.':
		default:
			return ""
		}
	}
	if len(h) != 12 {
		return ""
	}
	out := make([]byte, 0, 17)
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, h[i], h[i+1])
	}
	return string(out)
}

// tuyaIcon: the icon kind for a Tuya category.
func tuyaIcon(cat string) string {
	switch cat {
	case "cz", "pc", "kg", "tdq", "dlq", "zndb":
		return "plug"
	case "dj", "dd", "xdd", "fwd", "dc":
		return "bulb"
	case "wk", "kt", "qn", "wsdcg":
		return "thermostat"
	case "sp":
		return "camera"
	case "sd":
		return "vacuum"
	}
	return "iot"
}

// detectedKinds: MAC → icon kind (camera, doorbell, plug…), Protect's list read at most every 10 minutes.
func (a *App) detectedKinds() map[string]string {
	detected.Lock()
	defer detected.Unlock()
	if detected.kinds == nil || time.Since(detected.at) > detectedFresh {
		k := map[string]string{}
		if pc := a.protectClient(); pc.Connected() {
			if cams, err := protect.Cameras(pc); err == nil {
				for _, c := range cams {
					if m := normMAC(c.MAC); m != "" {
						k[m] = map[bool]string{true: "doorbell", false: "camera"}[c.Doorbell]
					}
				}
			}
		}
		detected.kinds, detected.at = k, time.Now()
	}
	out := map[string]string{}
	for m, v := range detected.kinds {
		out[m] = v
	}
	if a.tuya != nil {
		a.tuya.mu.Lock()
		for _, d := range a.tuya.Devices {
			if m := normMAC(d.MAC); m != "" && out[m] == "" {
				out[m] = tuyaIcon(d.Category)
			}
		}
		a.tuya.mu.Unlock()
	}
	return out
}
