package ups

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// HomeOwl normally runs as a normal user (macOS: a login item; Linux: the user given at install), and a normal
// user may not power the computer off. One sudoers line allows exactly the command the suggested script runs.

// SudoersFile is where the rule goes. sudo ignores files in sudoers.d whose name contains a dot, so the
// temporary copy next to it is never read before visudo has checked it.
const SudoersFile = "/etc/sudoers.d/homeowl-ups"

var sudoUserRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$`)

// Permission says whether HomeOwl may shut this computer down, and how to allow it when not.
type Permission struct {
	Needed   bool   `json:"needed"`            // false: root/SYSTEM, or Windows (users may shut down)
	Allowed  bool   `json:"allowed"`           // sudo lets HomeOwl's user run the shutdown command
	User     string `json:"user,omitempty"`    // who HomeOwl runs as
	Rule     string `json:"rule,omitempty"`    // the sudoers line
	Command  string `json:"command,omitempty"` // one Terminal command that installs it (checked with visudo first)
	Remove   string `json:"remove,omitempty"`  // one Terminal command that removes it
	OneClick bool   `json:"one_click"`         // HomeOwl can ask for the admin password itself (macOS)
	File     string `json:"file,omitempty"`
}

// shutdownCommands are the exact commands the suggested shutdown script runs through sudo.
func shutdownCommands(goos string) []string {
	if goos == "darwin" {
		return []string{"/sbin/shutdown -h now"}
	}
	sc := "/usr/bin/systemctl"
	if p, err := exec.LookPath("systemctl"); err == nil && strings.HasPrefix(p, "/") {
		sc = p
	}
	return []string{sc + " poweroff", "/sbin/shutdown -h now"}
}

// ShutdownRule is the sudoers line for user, or "" when the name can't safely go in one.
func ShutdownRule(goos, user string) string {
	if !sudoUserRE.MatchString(user) {
		return ""
	}
	return user + " ALL=(root) NOPASSWD: " + strings.Join(shutdownCommands(goos), ", ")
}

// rootScript installs (or removes) the rule; it runs as root and contains no shell variables or double quotes,
// so it can be wrapped in sudo sh -c "…" as is. A rule that visudo rejects is never put in place.
func rootScript(goos, user string, allow bool) string {
	if !allow {
		return "rm -f " + SudoersFile
	}
	rule := ShutdownRule(goos, user)
	if rule == "" {
		return ""
	}
	tmp := SudoersFile + ".tmp"
	return "printf '%s\\n' '" + rule + "' > " + tmp + " && chmod 440 " + tmp + " && /usr/sbin/visudo -cf " + tmp +
		" >/dev/null && mv -f " + tmp + " " + SudoersFile + " || { rm -f " + tmp + "; false; }"
}

// ShutdownPermission reports the state for HomeOwl's own user (asks sudo without a password, never prompts).
func ShutdownPermission(ctx context.Context, goos, user string, root bool) Permission {
	p := Permission{User: user, File: SudoersFile}
	if root || goos == "windows" {
		p.Allowed = true
		return p
	}
	p.Needed = true
	p.Rule = ShutdownRule(goos, user)
	if p.Rule != "" {
		p.Command = `sudo sh -c "` + rootScript(goos, user, true) + `"`
		p.Remove = "sudo " + rootScript(goos, user, false)
		p.OneClick = goos == "darwin"
	}
	if goos != runtime.GOOS { // tests asking about another system
		return p
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// "sudo -l <command>" isn't enough: an administrator may run everything, but only with a password.
	// Read the rules instead and look for NOPASSWD. Listing works without a password once any rule has NOPASSWD.
	out, err := exec.CommandContext(ctx, "sudo", "-n", "-l").Output()
	p.Allowed = err == nil && nopasswdAllows(string(out), shutdownCommands(goos)[0])
	return p
}

var sudoTagRE = regexp.MustCompile(`^(?:[A-Z_]+:\s*)+`)

// nopasswdAllows reads "sudo -l" output and reports whether cmd may run as root without a password.
func nopasswdAllows(listing, cmd string) bool {
	bin := strings.Fields(cmd)[0]
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "(") {
			continue
		}
		end := strings.IndexByte(line, ')')
		if end < 0 {
			continue
		}
		runas := strings.SplitN(line[1:end], ":", 2)[0]
		if !strings.Contains(runas, "root") && !strings.Contains(runas, "ALL") {
			continue
		}
		rest := strings.TrimSpace(line[end+1:])
		tags := sudoTagRE.FindString(rest)
		if !strings.Contains(tags, "NOPASSWD:") {
			continue
		}
		for _, c := range strings.Split(rest[len(tags):], ",") {
			// a later command in the list can carry its own tags (e.g. "PASSWD: /bin/x")
			c = strings.TrimSpace(c)
			if t := sudoTagRE.FindString(c); t != "" {
				if strings.Contains(t, "PASSWD:") && !strings.Contains(t, "NOPASSWD:") {
					break
				}
				c = strings.TrimSpace(c[len(t):])
			}
			if c == "ALL" || c == bin || c == cmd {
				return true
			}
		}
	}
	return false
}

// SetShutdownPermission asks macOS for an administrator's password (its own password window, on the screen of
// the Mac running HomeOwl) and installs or removes the rule. Other systems get the command to run instead.
func SetShutdownPermission(ctx context.Context, user string, allow bool) error {
	if runtime.GOOS != "darwin" {
		return errors.New("run the command shown on the computer running HomeOwl")
	}
	script := rootScript("darwin", user, allow)
	if script == "" {
		return errors.New("HomeOwl's user name can't go in a sudoers rule; set it up by hand")
	}
	prompt := "HomeOwl wants to stop letting itself shut down this Mac."
	if allow {
		prompt = "HomeOwl wants permission to shut down this Mac when the UPS battery runs low."
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript",
		"-e", "on run argv", "-e", "do shell script (item 1 of argv) with prompt (item 2 of argv) with administrator privileges", "-e", "end run",
		script, prompt).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		switch {
		case strings.Contains(msg, "-128"):
			return errors.New("cancelled on the Mac")
		case ctx.Err() != nil:
			return errors.New("nobody answered the password window on the Mac in time")
		case msg == "":
			return err
		}
		return errors.New(msg)
	}
	return nil
}
