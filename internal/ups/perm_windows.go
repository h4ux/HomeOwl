//go:build windows

package ups

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// safeOwner: on Windows the scripts folder sits in HomeOwl's data folder, which only its account (and admins) can change.
func safeOwner(dir string, fi os.FileInfo) error { return nil }

// IsRoot reports whether HomeOwl runs with full rights (an administrator or Local System, as the service does).
func IsRoot() bool { return windows.GetCurrentProcessToken().IsElevated() }

func ownGroup(cmd *exec.Cmd) {}
