package server

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/protect"
	"github.com/h4ux/homeowl/internal/unifi"
)

// Protect may run on a different console than Network: a Cloud Gateway has no storage for video, so the cameras
// live on a UNVR, a Cloud Key or another UniFi OS console, with its own API key (kept in the keychain).
const protectKeySlot = "unifi-protect-key"

// protectClient is the console to ask about cameras.
func (a *App) protectClient() *unifi.Client {
	if a.Cfg.Get().Cameras.ProtectHost != "" {
		return a.ProtectUniFi
	}
	return a.UniFi
}

// connectProtectSaved reconnects a separate Protect console at start-up, retrying while it's unreachable.
func (a *App) connectProtectSaved(ctx context.Context) {
	for wait := 30 * time.Second; ctx.Err() == nil; wait = min(wait*2, 10*time.Minute) {
		c := a.Cfg.Get().Cameras
		key := a.Sec.Get(protectKeySlot)
		if c.ProtectHost == "" || key == "" || a.ProtectUniFi.Connected() {
			return
		}
		_, err := a.ProtectUniFi.ConnectApp(c.ProtectHost, key, c.ProtectTLS, protect.InfoPath)
		if err == nil {
			log.Printf("connected to UniFi Protect on %s", c.ProtectHost)
			a.Protect.Kick()
			return
		}
		log.Printf("Protect console %s: %v", c.ProtectHost, err)
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// protectConnect uses another console for Protect. An empty key keeps the saved one.
func (a *App) protectConnect(body map[string]any) (any, error) {
	host, err := unifi.NormaliseHost(s(body, "host"))
	if err != nil {
		return nil, err
	}
	key := s(body, "key")
	if key == "" {
		key = a.Sec.Get(protectKeySlot)
	}
	if key == "" {
		return nil, errors.New("paste an API key created on that console (UniFi OS › Settings › Control Plane › Integrations)")
	}
	verify := b(body, "verify_tls")
	test := unifi.New()
	if _, err := test.ConnectApp(host, key, verify, protect.InfoPath); err != nil {
		return nil, err
	}
	if err := a.Sec.Set(protectKeySlot, key); err != nil {
		return nil, err
	}
	if err := a.Cfg.Update(func(c *config.Config) { c.Cameras.ProtectHost, c.Cameras.ProtectTLS = host, verify }); err != nil {
		return nil, err
	}
	if _, err := a.ProtectUniFi.ConnectApp(host, key, verify, protect.InfoPath); err != nil {
		return nil, err
	}
	a.snaps.reset()
	a.Protect.Kick()
	return a.camerasView(1), nil
}

// protectDisconnect goes back to Protect on the Network console.
func (a *App) protectDisconnect() (any, error) {
	if err := a.Cfg.Update(func(c *config.Config) { c.Cameras.ProtectHost, c.Cameras.ProtectTLS = "", false }); err != nil {
		return nil, err
	}
	a.Sec.Delete(protectKeySlot)
	a.ProtectUniFi.Disconnect()
	a.snaps.reset()
	a.Protect.Kick()
	return a.camerasView(1), nil
}
