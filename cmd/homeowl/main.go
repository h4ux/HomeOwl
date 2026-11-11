// HomeOwl — a friendly watcher for your home network.
//
//	homeowl                      run in the foreground (opens the browser on first start)
//	homeowl service install      run in the background at login/boot (then: start | stop | restart | status | uninstall)
//	homeowl mcp --token …        bridge an MCP client (Claude Desktop, Claude Code…) to HomeOwl over stdio
//	homeowl reset-password USER  set a new random password for USER
//	homeowl setup-code           show the one-time code for creating the first admin from another device
//	homeowl version
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/h4ux/homeowl/internal/auth"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mcp"
	"github.com/h4ux/homeowl/internal/menubar"
	"github.com/h4ux/homeowl/internal/server"
	"github.com/h4ux/homeowl/internal/update"
	"github.com/h4ux/homeowl/internal/ups"
)

func usage() {
	fmt.Fprintf(os.Stderr, `HomeOwl %s — a friendly watcher for your home network

Usage:
  homeowl [run] [--port N] [--data DIR] [--read-only] [--no-browser]
  homeowl service install|uninstall|start|stop|restart|status [--data DIR] [--user NAME]
  homeowl mcp [--url URL] [--token TOKEN] [--insecure]
  homeowl reset-password USERNAME [--data DIR]
  homeowl setup-code [--data DIR]
  homeowl update [--check]
  homeowl version
`, config.Version)
}

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		runCmd(args)
	case "service":
		serviceCmd(args)
	case "mcp":
		mcpCmd(args)
	case "reset-password":
		resetCmd(args)
	case "setup-code":
		setupCodeCmd(args)
	case "update":
		updateCmd(args)
	case "version", "--version", "-v":
		fmt.Println("HomeOwl", config.Version, runtime.GOOS+"/"+runtime.GOARCH)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

// ------------------------------------------------------------------ run

type program struct {
	data, port string
	readOnly   bool
	browser    bool
	service    bool
	cancel     context.CancelFunc
	done       chan struct{}
	restart    bool // set when the program file was replaced: start the new one after shutting down
	menubar    *server.Menubar
	mbReady    chan mbStart // run() tells the main thread how to show the menu bar item
}

type mbStart struct {
	url, style string
	show       bool
}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	select {
	case <-p.done:
	case <-time.After(8 * time.Second):
	}
	return nil
}

func (p *program) run() {
	defer close(p.done)
	dir, err := config.DataDir(p.data)
	if err != nil {
		log.Fatalf("data folder: %v", err)
	}
	if p.service {
		setupLogFile(dir)
	}
	app, err := server.New(dir, p.readOnly)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if p.port != "" {
		var port int
		if _, err := fmt.Sscan(p.port, &port); err == nil && port > 0 {
			_ = app.Cfg.Update(func(c *config.Config) { c.LAN.Port = port })
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	app.Restart = func() { p.restart = true; cancel() }
	app.Startup = startupControl(dir, p.service)
	if p.menubar != nil {
		app.Menubar = p.menubar
		c := app.Cfg.Get()
		p.mbReady <- mbStart{url: fmt.Sprintf("http://127.0.0.1:%d/menubar.html#t=%s", c.LAN.Port, p.menubar.Token), style: c.Menubar.Icon, show: !c.Menubar.Hide}
	}
	if p.browser {
		go func() {
			time.Sleep(800 * time.Millisecond)
			openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", app.Cfg.Get().LAN.Port))
		}()
	}
	if err := app.Run(ctx); err != nil {
		log.Fatalf("%v", err)
	}
	if p.restart {
		if p.service {
			restartService() // launchd / systemd / the Windows service manager starts the new program
		}
		restartSelf() // run the new program in place of this one
	}
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	data := fs.String("data", "", "data folder (default: per-user app folder; or $HOMEOWL_DATA)")
	port := fs.String("port", "", "port for the web UI (default 8555)")
	ro := fs.Bool("read-only", false, "never change anything on the UniFi console")
	noBrowser := fs.Bool("no-browser", false, "don't open the browser")
	svc := fs.Bool("service", false, "running as a background service (log to file)")
	_ = fs.Parse(args)
	p := &program{data: *data, port: *port, readOnly: *ro, service: *svc, done: make(chan struct{})}
	if menubar.Available() {
		// macOS draws the menu bar item on the main thread, so HomeOwl itself runs beside it
		p.menubar = newMenubar()
		p.mbReady = make(chan mbStart, 1)
		go func() {
			serve(p, *data, *svc, *noBrowser)
			os.Exit(0)
		}()
		select {
		case st := <-p.mbReady:
			menubar.Init(st.url, st.show)
			menubar.SetIcon(st.style, 0, 0)
			menubar.Run()
		case <-p.done:
		}
		return
	}
	serve(p, *data, *svc, *noBrowser)
}

func newMenubar() *server.Menubar {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return &server.Menubar{Token: base64.RawURLEncoding.EncodeToString(b), Styles: menubar.Styles,
		SetVisible: menubar.SetVisible, SetIcon: menubar.SetIcon, Close: menubar.ClosePopover, SetHeight: menubar.SetHeight}
}

// serve runs HomeOwl in the foreground (a terminal) or under the service manager, until it stops.
func serve(p *program, data string, svc, noBrowser bool) {
	if service.Interactive() && !svc {
		p.browser = !noBrowser
		go p.run()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sig:
			_ = p.Stop(nil)
		case <-p.done:
		}
		return
	}
	s, err := service.New(p, svcConfig(data, ""))
	if err != nil {
		log.Fatal(err)
	}
	if err := s.Run(); err != nil {
		log.Fatal(err)
	}
}

func setupLogFile(dir string) {
	ld := config.LogDir(dir)
	_ = os.MkdirAll(ld, 0o700)
	path := filepath.Join(ld, "homeowl.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		log.SetOutput(io.MultiWriter(f, os.Stderr))
	}
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return
		}
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}

// ------------------------------------------------------------------ service

func svcConfig(data, user string) *service.Config {
	a := []string{"run", "--service"}
	if data != "" {
		a = append(a, "--data", data)
	}
	opt := service.KeyValue{"RunAtLoad": true, "KeepAlive": true, "Restart": "always"}
	if runtime.GOOS == "darwin" {
		opt["UserService"] = true // a LaunchAgent: runs as you, so the keychain and local-network permission work
	}
	return &service.Config{Name: "homeowl", DisplayName: "HomeOwl", Description: "HomeOwl — home network watcher (UniFi, NextDNS, Tailscale)",
		Arguments: a, UserName: user, Option: opt}
}

// startupControl lets Settings install or remove the background service for this data folder.
func startupControl(dir string, asService bool) *server.Startup {
	s, err := service.New(&program{done: make(chan struct{})}, svcConfig(dir, ""))
	if err != nil {
		return nil
	}
	exe, _ := os.Executable()
	q := func(v string) string {
		if strings.ContainsAny(v, " '\"") {
			return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
		}
		return v
	}
	st := &server.Startup{AsService: asService, CanChange: ups.IsRoot(),
		Installed: func() (bool, error) {
			_, err := s.Status()
			if errors.Is(err, service.ErrNotInstalled) {
				return false, nil
			}
			return err == nil, err
		},
		Enable: s.Install, Disable: s.Uninstall}
	switch runtime.GOOS {
	case "darwin":
		st.Kind, st.CanChange = "at login", true // a LaunchAgent in your own Library: no admin rights needed
		st.Command = q(exe) + " service install --data " + q(dir)
	case "windows":
		st.Kind = "when Windows starts"
		st.Command = `& "` + exe + `" service install --data "` + dir + `"; & "` + exe + `" service start   # in PowerShell as Administrator`
	default:
		st.Kind = "when the computer starts"
		who := os.Getenv("USER")
		if who == "" {
			who = "$USER"
		}
		st.Command = "sudo " + q(exe) + " service install --user " + q(who) + " --data " + q(dir)
	}
	return st
}

func serviceCmd(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	data := fs.String("data", "", "data folder for the service")
	user := fs.String("user", "", "Linux: run the service as this user")
	_ = fs.Parse(args[1:])
	if runtime.GOOS == "windows" && *data == "" {
		*data = filepath.Join(os.Getenv("ProgramData"), "HomeOwl")
	}
	s, err := service.New(&program{done: make(chan struct{})}, svcConfig(*data, *user))
	if err != nil {
		log.Fatal(err)
	}
	if action == "status" {
		st, err := s.Status()
		names := map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped", service.StatusUnknown: "unknown"}
		if err != nil {
			fmt.Println("not installed:", err)
			os.Exit(1)
		}
		fmt.Println(names[st])
		return
	}
	if err := service.Control(s, action); err != nil {
		log.Fatalf("service %s: %v", action, err)
	}
	fmt.Printf("HomeOwl service: %s done\n", action)
}

// ------------------------------------------------------------------ update

func updateCmd(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	only := fs.Bool("check", false, "only check, don't install")
	_ = fs.Parse(args)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := &update.Checker{Current: config.Version}
	st := c.Check(ctx)
	if st.Error != "" {
		log.Fatalf("Couldn't check for updates: %s", st.Error)
	}
	if !st.Available {
		fmt.Printf("HomeOwl %s is up to date (latest release: %s).\n", config.Version, st.Latest)
		return
	}
	fmt.Printf("HomeOwl %s is available (you have %s).\n  %s\n", st.Latest, config.Version, st.URL)
	if *only {
		return
	}
	exe, _ := update.Executable()
	if !update.Writable() {
		fmt.Fprintf(os.Stderr, "Can't replace %s as this user. Run: %s\n", exe, update.Commands(runtime.GOOS, false)[0])
		os.Exit(1)
	}
	fmt.Println("Downloading and checking the release…")
	if err := c.Apply(ctx); err != nil {
		log.Fatalf("Update failed: %v", err)
	}
	fmt.Printf("Installed %s at %s. A running HomeOwl restarts by itself within a minute.\n", st.Latest, exe)
}

// ------------------------------------------------------------------ mcp bridge

func mcpCmd(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	url := fs.String("url", "https://127.0.0.1:8555/mcp", "HomeOwl MCP endpoint")
	token := fs.String("token", os.Getenv("HOMEOWL_MCP_TOKEN"), "MCP token from HomeOwl Settings › AI & MCP (or $HOMEOWL_MCP_TOKEN)")
	insecure := fs.Bool("insecure", false, "accept HomeOwl's self-signed certificate on another computer")
	_ = fs.Parse(args)
	if *token == "" {
		fmt.Fprintln(os.Stderr, "missing --token (create one in HomeOwl › Settings › AI & MCP)")
		os.Exit(2)
	}
	log.SetOutput(os.Stderr)
	if err := mcp.Bridge(*url, *token, *insecure); err != nil {
		log.Fatal(err)
	}
}

// ------------------------------------------------------------------ admin helpers

func dataFlag(args []string) (string, []string) {
	fs := flag.NewFlagSet("x", flag.ExitOnError)
	data := fs.String("data", "", "data folder")
	_ = fs.Parse(args)
	dir, err := config.DataDir(*data)
	if err != nil {
		log.Fatal(err)
	}
	return dir, fs.Args()
}

func resetCmd(args []string) {
	var user string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		user, args = args[0], args[1:]
	}
	dir, rest := dataFlag(args)
	if user == "" && len(rest) > 0 {
		user = rest[0]
	}
	if user == "" {
		fmt.Fprintln(os.Stderr, "usage: homeowl reset-password USERNAME")
		os.Exit(2)
	}
	st, err := auth.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	pw := base64.RawURLEncoding.EncodeToString(b)
	if err := st.SetPassword(user, pw); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("New password for %s: %s\n(change it in HomeOwl › Settings › My account; if HomeOwl is running, restart it: homeowl service restart)\n", user, pw)
}

func setupCodeCmd(args []string) {
	dir, _ := dataFlag(args)
	b, err := os.ReadFile(filepath.Join(dir, "setup-code"))
	if err != nil {
		fmt.Println("No setup code — HomeOwl already has users (or hasn't been started yet).")
		return
	}
	fmt.Println("Setup code:", strings.TrimSpace(string(b)))
}
