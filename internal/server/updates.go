package server

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/h4ux/homeowl/internal/update"
)

// updateLoop asks GitHub for a newer release a minute after start and then twice a day (unless turned off).
func (a *App) updateLoop(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 12 * time.Hour
		if !a.Cfg.Get().Updates.Check {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		st := a.Updates.Check(cctx)
		cancel()
		if st.Available {
			log.Printf("update: HomeOwl %s is available (running %s)", st.Latest, st.Current)
		}
	}
}

// watchExecutable restarts HomeOwl when its program file is replaced (by "homeowl update", the installer
// or the Update button), so the new version runs without anyone restarting the service by hand.
func (a *App) watchExecutable(ctx context.Context) {
	exe, err := update.Executable()
	if err != nil {
		return
	}
	orig, err := os.Stat(exe)
	if err != nil {
		return
	}
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now, err := os.Stat(exe)
		if err != nil || os.SameFile(orig, now) && now.ModTime().Equal(orig.ModTime()) && now.Size() == orig.Size() {
			continue // missing for a moment (being replaced) or unchanged
		}
		time.Sleep(2 * time.Second) // let the writer finish
		log.Printf("update: the program file changed — restarting")
		if a.Restart != nil {
			a.Restart()
		}
		return
	}
}

func (a *App) applyUpdate(ctx context.Context) error {
	if err := a.Updates.Apply(ctx); err != nil {
		return err
	}
	log.Printf("update: installed %s — restarting", a.Updates.Status().Latest)
	if a.Restart != nil {
		go func() { time.Sleep(time.Second); a.Restart() }()
	}
	return nil
}
