//go:build !notsnet

package tsnetx

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"tailscale.com/tsnet"
)

const Available = true

type Node struct {
	mu      sync.Mutex
	srv     *tsnet.Server
	servers []*http.Server
	lastErr string
	host    string
}

func New() *Node { return &Node{} }

// Start joins the tailnet (first time: Status() returns a login URL to approve the device) and serves h.
func (n *Node) Start(hostname, stateDir, authKey string, h http.Handler) error {
	n.Stop()
	n.mu.Lock()
	defer n.mu.Unlock()
	s := &tsnet.Server{Hostname: hostname, Dir: stateDir, AuthKey: authKey, Logf: func(string, ...any) {},
		UserLogf: func(f string, a ...any) { log.Printf("tailscale: "+f, a...) }}
	if err := s.Start(); err != nil {
		n.lastErr = err.Error()
		return err
	}
	n.srv, n.host, n.lastErr = s, hostname, ""
	h = mark(h)
	go func() {
		// HTTPS on :443 with the tailnet certificate; plain HTTP on :80 redirects there.
		ln, err := s.ListenTLS("tcp", ":443")
		if err != nil {
			n.setErr("HTTPS on the tailnet: " + err.Error())
			return
		}
		hs := &http.Server{Handler: h, ReadHeaderTimeout: 15 * time.Second}
		n.add(hs)
		_ = hs.Serve(ln)
	}()
	go func() {
		ln, err := s.Listen("tcp", ":80")
		if err != nil {
			return
		}
		hs := &http.Server{ReadHeaderTimeout: 15 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n.httpsReady() {
				http.Redirect(w, r, "https://"+strings.Split(r.Host, ":")[0]+r.URL.RequestURI(), http.StatusFound)
				return
			}
			h.ServeHTTP(w, r) // tailnet traffic is already encrypted by WireGuard
		})}
		n.add(hs)
		_ = hs.Serve(ln)
	}()
	return nil
}

func (n *Node) add(hs *http.Server) { n.mu.Lock(); n.servers = append(n.servers, hs); n.mu.Unlock() }
func (n *Node) setErr(e string)     { n.mu.Lock(); n.lastErr = e; n.mu.Unlock() }

func (n *Node) httpsReady() bool {
	st := n.Status()
	ok, _ := st["https"].(bool)
	return ok
}

func (n *Node) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, hs := range n.servers {
		_ = hs.Close()
	}
	n.servers = nil
	if n.srv != nil {
		_ = n.srv.Close()
		n.srv = nil
	}
}

// Status describes the tailnet connection for the Settings page.
func (n *Node) Status() map[string]any {
	n.mu.Lock()
	s, lastErr := n.srv, n.lastErr
	n.mu.Unlock()
	out := map[string]any{"available": true, "running": s != nil, "error": lastErr}
	if s == nil {
		return out
	}
	lc, err := s.LocalClient()
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	out["state"] = st.BackendState
	out["login_url"] = st.AuthURL
	if st.Self != nil {
		dns := strings.TrimSuffix(st.Self.DNSName, ".")
		out["dns_name"] = dns
		if len(st.TailscaleIPs) > 0 {
			out["ip"] = st.TailscaleIPs[0].String()
		}
		if dns != "" {
			out["url"] = "https://" + dns
		}
	}
	out["https"] = len(st.CertDomains) > 0
	if st.CurrentTailnet != nil {
		out["tailnet"] = st.CurrentTailnet.Name
	}
	return out
}

// WhoIs identifies the tailnet user behind a connection.
func (n *Node) WhoIs(ctx context.Context, remoteAddr string) (Identity, error) {
	n.mu.Lock()
	s := n.srv
	n.mu.Unlock()
	if s == nil {
		return Identity{}, errors.New("tailscale is not running")
	}
	lc, err := s.LocalClient()
	if err != nil {
		return Identity{}, err
	}
	who, err := lc.WhoIs(ctx, remoteAddr)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{}
	if who.UserProfile != nil {
		id.Login, id.Name = strings.ToLower(who.UserProfile.LoginName), who.UserProfile.DisplayName
	}
	if who.Node != nil {
		id.Tagged = who.Node.IsTagged()
	}
	return id, nil
}

// Logout removes HomeOwl from the tailnet (its device key is discarded).
func (n *Node) Logout() error {
	n.mu.Lock()
	s := n.srv
	n.mu.Unlock()
	if s == nil {
		return nil
	}
	lc, err := s.LocalClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := lc.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}
