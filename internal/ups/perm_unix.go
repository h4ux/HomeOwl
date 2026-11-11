//go:build !windows

package ups

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// safeOwner refuses scripts (and a scripts folder) that someone other than HomeOwl's user or root could change.
func safeOwner(dir string, fi os.FileInfo) error {
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("others can change this file (run: chmod go-w on it)")
	}
	if !ownedByUs(fi) {
		return errors.New("owned by another user")
	}
	di, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if di.Mode().Perm()&0o022 != 0 {
		return errors.New("others can change the scripts folder (run: chmod go-w on it)")
	}
	if !ownedByUs(di) {
		return errors.New("the scripts folder is owned by another user")
	}
	return nil
}

func ownedByUs(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	uid := uint32(os.Geteuid())
	return st.Uid == uid || st.Uid == 0
}

// IsRoot reports whether HomeOwl runs with full rights (root).
func IsRoot() bool { return os.Geteuid() == 0 }

// ownGroup starts the script in its own process group and, on timeout, stops the whole group.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
