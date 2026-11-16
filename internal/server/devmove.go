package server

import (
	"errors"
	"log"
	"strings"
)

// deviceMove gives one device (all its names: Wi-Fi addresses and NextDNS names) to another person or group, and
// moves its check-in along, so a later address change or setup doesn't hand it back to the old owner.
// body: {person, keys: [{kind, key}]}
func (a *App) deviceMove(body map[string]any) (any, error) {
	pid := s(body, "person")
	var to *struct{ name, kind string }
	for _, p := range a.An.People() {
		if p.ID == pid {
			to = &struct{ name, kind string }{p.Name, p.Kind}
		}
	}
	if to == nil {
		return nil, errors.New("no such person or group")
	}
	l, _ := body["keys"].([]any)
	if len(l) == 0 || len(l) > 64 {
		return nil, errors.New("pick a device")
	}
	macs := map[string]bool{}
	for _, x := range l {
		m, _ := x.(map[string]any)
		kind, key := s(m, "kind"), s(m, "key")
		if err := a.An.AssignDevice(pid, kind, key); err != nil {
			return nil, err
		}
		if kind == "mac" {
			macs[strings.ToLower(key)] = true
		}
	}
	for _, c := range a.checkins.List() {
		if macs[strings.ToLower(c.MAC)] && c.PersonID != pid {
			a.checkins.Edit(c.ID, func(x *CheckIn) {
				x.PersonID, x.PersonName = pid, to.name
				x.Main = false // the old owner's main device isn't the new one's
			})
		}
	}
	log.Printf("people: a device moved to %s", to.name)
	return map[string]any{"ok": true}, nil
}
