package server

import (
	"errors"
	"log"
	"time"
)

// Startup lets Settings turn "start HomeOwl with this computer" on or off. The command line fills it in
// (it owns the service definition); nil when HomeOwl can't manage it (tests, unusual builds).
type Startup struct {
	Kind      string               // "at login" (macOS LaunchAgent) or "when the computer starts" (systemd, Windows service)
	AsService bool                 // this HomeOwl was started by the service manager
	CanChange bool                 // this process may install/remove it itself (macOS: yes; Linux/Windows: only as root/admin)
	Command   string               // what to run when it can't
	Installed func() (bool, error) // is the service (LaunchAgent) set up?
	Enable    func() error
	Disable   func() error
}

func (a *App) startupView() map[string]any {
	st := a.Startup
	if st == nil {
		return map[string]any{"available": false}
	}
	on, err := st.Installed()
	out := map[string]any{"available": true, "enabled": on, "kind": st.Kind, "as_service": st.AsService, "can_change": st.CanChange, "command": st.Command}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}

// setStartup installs or removes the background service. Removing it while HomeOwl runs as that service also
// stops this HomeOwl, so that happens a moment after the answer is sent.
func (a *App) setStartup(enable bool) (any, error) {
	st := a.Startup
	if st == nil {
		return nil, errors.New("HomeOwl can't manage starting by itself here")
	}
	if !st.CanChange {
		return nil, errors.New("HomeOwl needs administrator rights for this; run the command shown instead")
	}
	on, _ := st.Installed()
	switch {
	case enable && !on:
		if err := st.Enable(); err != nil {
			return nil, err
		}
	case !enable && on && st.AsService:
		go func() {
			time.Sleep(1500 * time.Millisecond)
			if err := st.Disable(); err != nil {
				log.Printf("startup: %v", err)
			}
		}()
		v := a.startupView()
		v["enabled"], v["stopping"] = false, true
		return v, nil
	case !enable && on:
		if err := st.Disable(); err != nil {
			return nil, err
		}
	}
	return a.startupView(), nil
}
