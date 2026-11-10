//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/h4ux/homeowl/internal/update"
)

const detached = 0x00000008 | 0x00000200 // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP

// restartService asks the Service Control Manager (through a helper process) to restart the service,
// which then runs the new program.
func restartService() {
	exe, err := update.Executable()
	if err == nil {
		cmd := exec.Command(exe, "service", "restart")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detached}
		err = cmd.Start()
	}
	if err != nil {
		log.Printf("update: couldn't restart the service: %v", err)
	}
	os.Exit(1)
}

// restartSelf starts the new program with the same arguments and exits.
func restartSelf() {
	exe, err := update.Executable()
	if err != nil {
		log.Fatalf("restart: %v", err)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("restart: %v — start HomeOwl again by hand", err)
	}
	os.Exit(0)
}
