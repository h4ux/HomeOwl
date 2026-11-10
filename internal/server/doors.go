package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/h4ux/homeowl/internal/access"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Doors: UniFi Access door and lock state, the entry log, and a guarded remote unlock (admins only).

const accessTokenSlot = "unifi-access-token"

// configureAccess points the Access client at the console (or the address given in settings) with the saved token.
func (a *App) configureAccess() {
	c := a.Cfg.Get()
	tok := a.Sec.Get(accessTokenSlot)
	base := c.Access.Host
	if base == "" {
		base, _ = access.BaseFor(c.Host, 0)
	}
	if tok == "" || base == "" {
		a.Access.Configure("", "", false)
		return
	}
	a.Access.Configure(base, tok, c.VerifyTLS)
}

func (a *App) doorsView(ctx context.Context) map[string]any {
	c := a.Cfg.Get()
	out := map[string]any{"configured": a.Access.Configured(), "address": a.Access.Base(), "unlock_allowed": c.Access.Unlock,
		"write": a.Changes.Write && !a.ReadOnly, "doors": []access.Door{}, "log": []access.Entry{}}
	if !a.Access.Configured() {
		if c.Host != "" {
			out["address"], _ = access.BaseFor(c.Host, 0)
		}
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	doors, err := a.Access.Doors(ctx)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	out["doors"] = doors
	if l, err := a.Access.Log(ctx, time.Now().AddDate(0, 0, -7)); err != nil {
		out["log_error"] = err.Error()
	} else {
		out["log"] = l
	}
	return out
}

// doorsConnect tests and saves an Access API token (and optionally a different address).
func (a *App) doorsConnect(ctx context.Context, body map[string]any) (any, error) {
	tok, host := strings.TrimSpace(s(body, "token")), strings.TrimSpace(s(body, "host"))
	if tok == "" {
		tok = a.Sec.Get(accessTokenSlot)
	}
	if tok == "" {
		return nil, errors.New("paste the API token from UniFi Access › Settings › General › Advanced › API Token")
	}
	c := a.Cfg.Get()
	base := ""
	if host != "" {
		h, err := unifi.NormaliseHost(host)
		if err != nil {
			return nil, err
		}
		base = h
	} else if b, err := access.BaseFor(c.Host, 0); err == nil {
		base = b
	} else {
		return nil, err
	}
	test := &access.Client{}
	test.Configure(base, tok, c.VerifyTLS)
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := test.Doors(tctx); err != nil {
		return nil, err
	}
	if err := a.Sec.Set(accessTokenSlot, tok); err != nil {
		return nil, err
	}
	if err := a.Cfg.Update(func(x *config.Config) {
		x.Access.Host = ""
		if host != "" {
			x.Access.Host = base
		}
	}); err != nil {
		return nil, err
	}
	a.configureAccess()
	return a.doorsView(ctx), nil
}

func (a *App) doorsDisconnect() {
	a.Sec.Delete(accessTokenSlot)
	_ = a.Cfg.Update(func(x *config.Config) { x.Access.Host, x.Access.Unlock = "", false })
	a.configureAccess()
}

// doorUnlock previews (dry_run, the default) or performs a remote unlock: only when remote unlock is allowed in
// settings and the change lock is open; logged in Changes.
func (a *App) doorUnlock(ctx context.Context, body map[string]any, by string) (any, error) {
	if !a.Cfg.Get().Access.Unlock {
		return nil, errors.New("remote unlock is turned off — an admin can allow it on the Doors page")
	}
	id := s(body, "id")
	doors, err := a.Access.Doors(ctx)
	if err != nil {
		return nil, err
	}
	var door *access.Door
	for i := range doors {
		if doors[i].ID == id {
			door = &doors[i]
		}
	}
	if door == nil {
		return nil, errors.New("no such door")
	}
	pub := unifi.Preview{Method: "PUT", Path: access.UnlockPath(id), Target: "door " + door.Name,
		Before: map[string]any{"lock": door.Lock}, After: map[string]any{"lock": "unlocked (locks again by itself)"},
		Description: "Unlock " + door.Name + " remotely"}
	if dry, ok := body["dry_run"].(bool); !ok || dry {
		return map[string]any{"preview": pub}, nil
	}
	ch, err := a.Changes.External(a.Access.Base(), by, pub, true, func() (int, any) { return a.Access.Unlock(ctx, id, by) })
	if err != nil {
		return nil, err
	}
	if !ch.OK {
		return nil, errors.New(orDefault(ch.Error, "UniFi Access didn't unlock the door"))
	}
	return map[string]any{"result": ch}, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
