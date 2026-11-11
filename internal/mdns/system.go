package mdns

import (
	"bufio"
	"context"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// On a Mac the names are handed to the system's own Bonjour service (mDNSResponder, through dns-sd): it already
// answers for the computer's name, handles sleep, Wi-Fi changes and name conflicts, and isn't held back by the
// app's Local Network permission. Elsewhere (or without dns-sd) HomeOwl answers itself.

// systemTool is the command that registers a name with the system's responder, or "".
func systemTool() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	if p, err := exec.LookPath("dns-sd"); err == nil {
		return p
	}
	return ""
}

// PrimaryIPv4 is this computer's address on the home network: the one it would use to reach the internet,
// else its first private IPv4 address.
func PrimaryIPv4() string {
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil { // no packet is sent: this only picks the route
		ip := c.LocalAddr().(*net.UDPAddr).IP
		c.Close()
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	ifs, _ := net.Interfaces()
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		v4, _ := ifaceNets(&ifi)
		for _, n := range v4 {
			if n.IP.IsPrivate() {
				return n.IP.String()
			}
		}
	}
	return ""
}

// runSystem keeps one dns-sd registration per name, and starts them again when the address changes.
func (r *Responder) runSystem(ctx context.Context, tool string, port int) {
	for ctx.Err() == nil {
		ip := PrimaryIPv4()
		if ip == "" {
			r.setErr("this computer has no home-network address yet", nil)
			if wait(ctx, 30*time.Second) {
				return
			}
			continue
		}
		r.setErr("", []string{"system Bonjour (" + ip + ")"})
		cctx, stop := context.WithCancel(ctx)
		for _, n := range r.names4() {
			go r.register(cctx, tool, n, ip, port)
		}
		for ctx.Err() == nil && PrimaryIPv4() == ip {
			if wait(ctx, 30*time.Second) {
				break
			}
		}
		stop()
		r.mu.Lock()
		r.st.Registered = 0
		r.mu.Unlock()
	}
}

func wait(ctx context.Context, d time.Duration) (done bool) {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

// register runs `dns-sd -P` for one name until ctx ends (restarting it if it quits).
func (r *Responder) register(ctx context.Context, tool, fqdn, ip string, port int) {
	label := strings.TrimSuffix(fqdn, ".local.")
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, tool, "-P", "HomeOwl "+label, "_http._tcp", "local", strconv.Itoa(port), label+".local", ip)
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			r.sendErr(err)
			return
		}
		counted := false
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := strings.ToLower(sc.Text())
			switch {
			case strings.Contains(line, "record "+label+".local") && strings.Contains(line, "registered and active") && !counted:
				counted = true
				r.mu.Lock()
				r.st.Registered++
				r.st.SendError = ""
				r.mu.Unlock()
			case strings.Contains(line, "conflict"):
				r.mu.Lock()
				r.st.SendError = "another device on the network already uses " + label + ".local — pick another name"
				r.mu.Unlock()
			}
		}
		_ = cmd.Wait()
		if counted {
			r.mu.Lock()
			r.st.Registered--
			r.mu.Unlock()
		}
		if wait(ctx, 5*time.Second) {
			return
		}
	}
}
