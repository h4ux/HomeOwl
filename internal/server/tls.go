package server

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/config"
)

// localNames returns this computer's host names and IP addresses (used for the certificate and the Host check).
func localNames() (names []string, ips []net.IP) {
	names = []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" {
		h = strings.ToLower(strings.TrimSuffix(h, ".local"))
		names = append(names, h, h+".local")
	}
	ips = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLinkLocalUnicast() {
				ips = append(ips, n.IP)
			}
		}
	}
	return names, ips
}

// loadOrCreateCert returns the HTTPS certificate for the home-network listener: tls/cert.pem + key.pem if
// present (drop your own there), otherwise a self-signed one made on first start (regenerated when the
// computer's addresses change).
func loadOrCreateCert(dataDir string) (tls.Certificate, error) {
	dir := filepath.Join(dataDir, "tls")
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
			_, ips := localNames()
			covered := true
			if leaf.Subject.CommonName == config.AppName+" (self-signed)" {
				for _, ip := range ips {
					if ip.To4() != nil && !hasIP(leaf.IPAddresses, ip) {
						covered = false
					}
				}
			}
			if covered && time.Until(leaf.NotAfter) > 30*24*time.Hour {
				return c, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	names, ips := localNames()
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: config.AppName + " (self-signed)", Organization: []string{config.AppName}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: names, IPAddresses: ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	_ = os.MkdirAll(dir, 0o700)
	_ = config.WriteFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = config.WriteFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return tls.LoadX509KeyPair(certPath, keyPath)
}

func hasIP(list []net.IP, ip net.IP) bool {
	for _, x := range list {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}

// sniffListener serves HTTPS and plain HTTP on one port: a TLS handshake starts with byte 0x16.
// Each new connection is inspected in its own goroutine so a slow client can't stall the others.
type sniffListener struct {
	net.Listener
	cfg   *tls.Config
	once  sync.Once
	conns chan net.Conn
	errs  chan error
}

func newSniffListener(l net.Listener, cfg *tls.Config) *sniffListener {
	return &sniffListener{Listener: l, cfg: cfg, conns: make(chan net.Conn, 16), errs: make(chan error, 1)}
}

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func (l *sniffListener) loop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.errs <- err
			return
		}
		go func(c net.Conn) {
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			br := bufio.NewReader(c)
			b, err := br.Peek(1)
			_ = c.SetReadDeadline(time.Time{})
			if err != nil {
				_ = c.Close()
				return
			}
			pc := &peekConn{Conn: c, r: br}
			if b[0] == 0x16 && l.cfg != nil {
				l.conns <- tls.Server(pc, l.cfg)
				return
			}
			l.conns <- pc
		}(c)
	}
}

func (l *sniffListener) Accept() (net.Conn, error) {
	l.once.Do(func() { go l.loop() })
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errs:
		return nil, err
	}
}
