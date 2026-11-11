package ups

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Scripts are the programs HomeOwl may run on UPS events. They live in one folder that only the person with a
// shell on this computer can fill: the web page can pick a script, never write one (except the fixed suggestions).
type Scripts struct {
	Dir     string
	Timeout time.Duration
}

// ScriptFile is one file in the scripts folder.
type ScriptFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // ms
	Problem  string `json:"problem,omitempty"`
}

// Result is how a script run went.
type Result struct {
	Exit   int    `json:"exit"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
	Ms     int64  `json:"ms"`
}

var scriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,100}$`)

// EnsureDir creates the scripts folder (only the owner can write it).
func (s *Scripts) EnsureDir() error { return os.MkdirAll(s.Dir, 0o700) }

// List returns the scripts in the folder, each with the reason it can't run (if any).
func (s *Scripts) List() []ScriptFile {
	ents, err := os.ReadDir(s.Dir)
	out := []ScriptFile{}
	if err != nil {
		return out
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") || e.IsDir() {
			continue
		}
		f := ScriptFile{Name: e.Name()}
		if fi, err := e.Info(); err == nil {
			f.Size, f.Modified = fi.Size(), fi.ModTime().UnixMilli()
		}
		if _, _, err := s.command(context.Background(), e.Name()); err != nil {
			f.Problem = err.Error()
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// check makes sure name is a plain file directly in the folder that nobody else can change.
func (s *Scripts) check(name string) (string, os.FileInfo, error) {
	if !scriptName.MatchString(name) || filepath.Base(name) != name {
		return "", nil, errors.New("not a valid script name")
	}
	p := filepath.Join(s.Dir, name)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", nil, errors.New("no such script in the scripts folder")
	}
	if !fi.Mode().IsRegular() {
		return "", nil, errors.New("not a plain file (links aren't allowed)")
	}
	if fi.Size() > 1<<20 {
		return "", nil, errors.New("too big for a script")
	}
	if err := safeOwner(s.Dir, fi); err != nil {
		return "", nil, err
	}
	return p, fi, nil
}

// command builds the command for a script: by extension on Windows, directly (or via sh) elsewhere.
func (s *Scripts) command(ctx context.Context, name string) (*exec.Cmd, string, error) {
	p, fi, err := s.check(name)
	if err != nil {
		return nil, "", err
	}
	ext := strings.ToLower(filepath.Ext(name))
	if runtime.GOOS == "windows" {
		switch ext {
		case ".cmd", ".bat":
			return exec.CommandContext(ctx, "cmd.exe", "/C", p), p, nil
		case ".ps1":
			return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", p), p, nil
		case ".exe":
			return exec.CommandContext(ctx, p), p, nil
		}
		return nil, "", errors.New("use a .cmd, .bat, .ps1 or .exe file")
	}
	if ext == ".sh" && fi.Mode()&0o100 == 0 {
		return exec.CommandContext(ctx, "/bin/sh", p), p, nil
	}
	if fi.Mode()&0o100 == 0 {
		return nil, "", fmt.Errorf("not executable (run: chmod +x %q)", p)
	}
	return exec.CommandContext(ctx, p), p, nil
}

// Run runs one script with the event's environment and waits for it (at most Timeout).
func (s *Scripts) Run(ctx context.Context, name string, env []string) Result {
	t := s.Timeout
	if t <= 0 {
		t = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	cmd, _, err := s.command(ctx, name)
	if err != nil {
		return Result{Exit: -1, Error: err.Error()}
	}
	cmd.Dir = s.Dir
	cmd.Env = append(cleanEnv(os.Environ()), env...)
	var buf capped
	cmd.Stdout, cmd.Stderr = &buf, &buf
	cmd.WaitDelay = 5 * time.Second
	ownGroup(cmd) // a timeout stops the script's children too
	start := time.Now()
	err = cmd.Run()
	r := Result{Output: strings.TrimSpace(buf.String()), Ms: time.Since(start).Milliseconds()}
	if cmd.ProcessState != nil {
		r.Exit = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		r.Error, r.Exit = fmt.Sprintf("stopped after %s", t), -1
	} else if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			r.Error, r.Exit = err.Error(), -1
		}
	}
	return r
}

// cleanEnv drops anything that looks like a secret from HomeOwl's own environment before a script sees it.
func cleanEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(k)
		if strings.Contains(u, "TOKEN") || strings.Contains(u, "SECRET") || strings.Contains(u, "PASSWORD") || strings.Contains(u, "API_KEY") ||
			strings.HasPrefix(u, "NOTIFYTYPE") || strings.HasPrefix(u, "UPSNAME") || strings.HasPrefix(u, "HOMEOWL_UPS_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// capped keeps the first 8 KB of a script's output.
type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := 8192 - c.Len(); room > 0 {
		if len(p) > room {
			c.Buffer.Write(p[:room])
		} else {
			c.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// Template is a script HomeOwl suggests for the system it runs on.
type Template struct {
	Name        string `json:"name"`
	Event       string `json:"event"` // the event it's meant for
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Exists      bool   `json:"exists"`
	Same        bool   `json:"same"` // the file in the folder is exactly this template
}

// Suggestions returns the scripts HomeOwl suggests for goos (ShutdownPermission says whether the shutdown one may run).
func (s *Scripts) Suggestions(goos string) []Template {
	var t []Template
	switch goos {
	case "windows":
		t = []Template{
			{Name: "homeowl-shutdown.cmd", Event: "shutdown", Title: "Shut down this PC",
				Description: "Windows' own shutdown with a 30-second warning, logged as an unplanned power failure. As the HomeOwl service (Local System) it needs nothing else.",
				Content:     crlf(winShutdown)},
			{Name: "homeowl-notify.cmd", Event: "onbatt", Title: "Tell everyone signed in",
				Description: "Writes the event to the Windows Application log and shows a message to everyone signed in to this PC. Use it for on battery, back on mains, low battery and lost connection.",
				Content:     crlf(winNotify)},
		}
	case "darwin":
		t = []Template{
			{Name: "homeowl-shutdown.sh", Event: "shutdown", Title: "Shut down this Mac",
				Description: "Shuts the Mac down once you allow it below; otherwise asks macOS like the Apple menu does (apps may ask to save their work first, which can stop it).",
				Content:     macShutdown},
			{Name: "homeowl-notify.sh", Event: "onbatt", Title: "Show a notification",
				Description: "Shows a macOS notification and writes the system log. Use it for on battery, back on mains, low battery and lost connection.",
				Content:     macNotify},
		}
	default:
		t = []Template{
			{Name: "homeowl-shutdown.sh", Event: "shutdown", Title: "Shut down this computer",
				Description: "Powers off with systemctl (or shutdown on systems without systemd), so services stop cleanly.",
				Content:     linuxShutdown},
			{Name: "homeowl-notify.sh", Event: "onbatt", Title: "Tell everyone signed in",
				Description: "Writes the system log and sends a message to every terminal (wall). Use it for on battery, back on mains, low battery and lost connection.",
				Content:     linuxNotify},
		}
	}
	for i := range t {
		if b, err := os.ReadFile(filepath.Join(s.Dir, t[i].Name)); err == nil {
			t[i].Exists, t[i].Same = true, string(b) == t[i].Content
		}
	}
	return t
}

// SaveSuggestion writes one of the built-in suggestions into the folder (never anything else).
func (s *Scripts) SaveSuggestion(goos, name string, overwrite bool) error {
	for _, t := range s.Suggestions(goos) {
		if t.Name != name {
			continue
		}
		if err := s.EnsureDir(); err != nil {
			return err
		}
		p := filepath.Join(s.Dir, name)
		if fi, err := os.Lstat(p); err == nil {
			if !overwrite {
				return errors.New("a script with that name is already there")
			}
			if !fi.Mode().IsRegular() {
				return errors.New("not a plain file")
			}
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, []byte(t.Content), 0o700); err != nil {
			return err
		}
		_ = os.Chmod(tmp, 0o700)
		return os.Rename(tmp, p)
	}
	return errors.New("no such suggested script")
}

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// Every suggestion understands HOMEOWL_UPS_TEST=1 (the Test button) and then only says what it would do.

const macShutdown = `#!/bin/sh
# HomeOwl UPS script: shut this Mac down gracefully before the UPS battery runs out.
# HomeOwl runs it on the "shutdown" event with NOTIFYTYPE, UPSNAME, HOMEOWL_UPS_STATUS,
# HOMEOWL_UPS_CHARGE (%) and HOMEOWL_UPS_RUNTIME_MIN set; the Test button adds HOMEOWL_UPS_TEST=1.
say() { logger -t homeowl-ups "$*" >/dev/null 2>&1; echo "$*"; }

if [ "$HOMEOWL_UPS_TEST" = "1" ]; then
  if [ "$(id -u)" = "0" ] || sudo -n -l 2>/dev/null | grep -Eq 'NOPASSWD:.*(/sbin/shutdown( -h now)?(,|$)|[: ]ALL$)'; then how="with shutdown (allowed)"; else how="by asking macOS (not allowed yet: press Allow in HomeOwl)"; fi
  say "Test: would shut down this Mac now $how. UPS $UPSNAME: $HOMEOWL_UPS_STATUS, battery $HOMEOWL_UPS_CHARGE%."
  exit 0
fi

say "UPS $UPSNAME: $HOMEOWL_UPS_STATUS, battery $HOMEOWL_UPS_CHARGE% - shutting down this Mac."
if [ "$(id -u)" = "0" ]; then exec /sbin/shutdown -h now; fi
if sudo -n /sbin/shutdown -h now 2>/dev/null; then exit 0; fi
exec osascript -e 'tell application "System Events" to shut down'
`

const macNotify = `#!/bin/sh
# HomeOwl UPS script: show a notification about the UPS and write it to the system log.
case "$NOTIFYTYPE" in
  ONBATT)   msg="Power cut: the UPS is on battery ($HOMEOWL_UPS_CHARGE%, about $HOMEOWL_UPS_RUNTIME_MIN min left)." ;;
  ONLINE)   msg="Power is back: the UPS is on mains again." ;;
  LOWBATT)  msg="UPS battery low ($HOMEOWL_UPS_CHARGE%). Save your work." ;;
  FSD)      msg="The NUT server ordered a shutdown." ;;
  COMMBAD)  msg="HomeOwl lost the connection to the UPS." ;;
  COMMOK)   msg="HomeOwl reaches the UPS again." ;;
  REPLBATT) msg="The UPS says its battery needs replacing." ;;
  SHUTDOWN) msg="This computer is about to shut down (UPS battery)." ;;
  *)        msg="UPS $UPSNAME: $HOMEOWL_UPS_STATUS" ;;
esac
[ "$HOMEOWL_UPS_TEST" = "1" ] && msg="Test: $msg"
logger -t homeowl-ups "$msg" >/dev/null 2>&1
osascript -e 'on run argv' -e 'display notification (item 1 of argv) with title "HomeOwl · UPS"' -e 'end run' "$msg" >/dev/null 2>&1
echo "$msg"
`

const linuxShutdown = `#!/bin/sh
# HomeOwl UPS script: power this computer off cleanly before the UPS battery runs out.
# HomeOwl runs it on the "shutdown" event with NOTIFYTYPE, UPSNAME, HOMEOWL_UPS_STATUS,
# HOMEOWL_UPS_CHARGE (%) and HOMEOWL_UPS_RUNTIME_MIN set; the Test button adds HOMEOWL_UPS_TEST=1.
say() { logger -t homeowl-ups "$*" >/dev/null 2>&1; echo "$*"; }
if command -v systemctl >/dev/null 2>&1; then off="systemctl poweroff"; else off="/sbin/shutdown -h now"; fi
run() { if [ "$(id -u)" = "0" ]; then $off; else sudo -n $off; fi; }

if [ "$HOMEOWL_UPS_TEST" = "1" ]; then
  if [ "$(id -u)" = "0" ] || sudo -n -l 2>/dev/null | grep -Eq 'NOPASSWD:.*(shutdown|poweroff|[: ]ALL$)'; then ok="allowed"; else ok="NOT allowed yet - see Allow shutdown in HomeOwl"; fi
  say "Test: would run '$off' now ($ok). UPS $UPSNAME: $HOMEOWL_UPS_STATUS, battery $HOMEOWL_UPS_CHARGE%."
  exit 0
fi

say "UPS $UPSNAME: $HOMEOWL_UPS_STATUS, battery $HOMEOWL_UPS_CHARGE% - powering off."
command -v wall >/dev/null 2>&1 && echo "UPS battery low: this computer is powering off now." | wall >/dev/null 2>&1
run
`

const linuxNotify = `#!/bin/sh
# HomeOwl UPS script: log the UPS event and tell everyone signed in (wall).
case "$NOTIFYTYPE" in
  ONBATT)   msg="Power cut: the UPS is on battery ($HOMEOWL_UPS_CHARGE%, about $HOMEOWL_UPS_RUNTIME_MIN min left)." ;;
  ONLINE)   msg="Power is back: the UPS is on mains again." ;;
  LOWBATT)  msg="UPS battery low ($HOMEOWL_UPS_CHARGE%). Save your work." ;;
  FSD)      msg="The NUT server ordered a shutdown." ;;
  COMMBAD)  msg="HomeOwl lost the connection to the UPS." ;;
  COMMOK)   msg="HomeOwl reaches the UPS again." ;;
  REPLBATT) msg="The UPS says its battery needs replacing." ;;
  SHUTDOWN) msg="This computer is about to power off (UPS battery)." ;;
  *)        msg="UPS $UPSNAME: $HOMEOWL_UPS_STATUS" ;;
esac
[ "$HOMEOWL_UPS_TEST" = "1" ] && msg="Test: $msg"
logger -t homeowl-ups "$msg" >/dev/null 2>&1
[ "$HOMEOWL_UPS_TEST" = "1" ] || { command -v wall >/dev/null 2>&1 && echo "$msg" | wall >/dev/null 2>&1; }
echo "$msg"
`

const winShutdown = `@echo off
rem HomeOwl UPS script: shut this PC down gracefully before the UPS battery runs out.
rem HomeOwl runs it on the "shutdown" event with NOTIFYTYPE, UPSNAME, HOMEOWL_UPS_STATUS,
rem HOMEOWL_UPS_CHARGE and HOMEOWL_UPS_RUNTIME_MIN set; the Test button adds HOMEOWL_UPS_TEST=1.
if not "%HOMEOWL_UPS_TEST%"=="1" goto run
echo Test: would shut down this PC now. UPS %UPSNAME%: %HOMEOWL_UPS_STATUS%, battery %HOMEOWL_UPS_CHARGE% percent.
exit /b 0
:run
echo UPS %UPSNAME%: %HOMEOWL_UPS_STATUS% - shutting down this PC.
shutdown.exe /s /t 30 /d u:6:12 /c "UPS battery low: HomeOwl is shutting down this PC."
`

const winNotify = `@echo off
rem HomeOwl UPS script: write the UPS event to the Application log and tell everyone signed in.
set "msg=UPS %UPSNAME%: %HOMEOWL_UPS_STATUS%"
if "%NOTIFYTYPE%"=="ONBATT" set "msg=Power cut: the UPS is on battery, %HOMEOWL_UPS_CHARGE% percent, about %HOMEOWL_UPS_RUNTIME_MIN% min left."
if "%NOTIFYTYPE%"=="ONLINE" set "msg=Power is back: the UPS is on mains again."
if "%NOTIFYTYPE%"=="LOWBATT" set "msg=UPS battery low, %HOMEOWL_UPS_CHARGE% percent. Save your work."
if "%NOTIFYTYPE%"=="FSD" set "msg=The NUT server ordered a shutdown."
if "%NOTIFYTYPE%"=="COMMBAD" set "msg=HomeOwl lost the connection to the UPS."
if "%NOTIFYTYPE%"=="COMMOK" set "msg=HomeOwl reaches the UPS again."
if "%NOTIFYTYPE%"=="REPLBATT" set "msg=The UPS says its battery needs replacing."
if "%NOTIFYTYPE%"=="SHUTDOWN" set "msg=This PC is about to shut down because of the UPS battery."
if "%HOMEOWL_UPS_TEST%"=="1" set "msg=Test: %msg%"
eventcreate /t WARNING /id 300 /l APPLICATION /so HomeOwl /d "%msg%" >nul 2>&1
if not "%HOMEOWL_UPS_TEST%"=="1" msg * /time:120 "%msg%" >nul 2>&1
echo %msg%
`
