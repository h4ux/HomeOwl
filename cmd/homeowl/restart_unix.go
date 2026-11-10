//go:build !windows

package main

import (
	"log"
	"os"
	"syscall"

	"github.com/h4ux/homeowl/internal/update"
)

// restartService exits so launchd (KeepAlive) or systemd (Restart=always) starts the new program.
func restartService() {
	log.Printf("update: exiting so the service manager starts the new version")
	os.Exit(1)
}

// restartSelf replaces this process with the new program, same arguments.
func restartSelf() {
	exe, err := update.Executable()
	if err != nil {
		log.Fatalf("restart: %v", err)
	}
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		log.Fatalf("restart: %v — start HomeOwl again by hand", err)
	}
}
