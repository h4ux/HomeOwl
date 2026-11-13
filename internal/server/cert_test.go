package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/h4ux/homeowl/internal/acmecert"
	"github.com/h4ux/homeowl/internal/cloudflare"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/smallstep/pkcs7"
)

// fakeLE stands in for Let's Encrypt: it checks that the TXT record is published, then issues a certificate
// from a throwaway CA.
func fakeLE(t *testing.T, f *fakeCF) func(context.Context, string, string, acmecert.Prover) ([]byte, []byte, error) {
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caT := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	caDER, _ := x509.CreateCertificate(rand.Reader, caT, caT, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	return func(_ context.Context, acct, name string, p acmecert.Prover) ([]byte, []byte, error) {
		if !strings.Contains(acct, "PRIVATE KEY") {
			t.Errorf("account key: %q", acct)
		}
		cleanup, err := p.Present(context.Background(), "_acme-challenge."+name, "proof")
		if err != nil {
			return nil, nil, err
		}
		if !strings.Contains(f.summary(), "dns _acme-challenge."+name) {
			t.Errorf("TXT record missing: %s", f.summary())
		}
		cleanup()
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		lt := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, _ := x509.CreateCertificate(rand.Reader, lt, ca, &key.PublicKey, caKey)
		kb, _ := x509.MarshalPKCS8PrivateKey(key)
		chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
		return chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), nil
	}
}

func TestLetsEncryptCertificate(t *testing.T) {
	f := &fakeCF{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	oldAPI, oldIP, oldObtain := cloudflare.API, homeIP, leObtain
	defer func() { cloudflare.API, homeIP, leObtain = oldAPI, oldIP, oldObtain }()
	cloudflare.API = srv.URL
	ip := "192.168.1.10"
	homeIP = func() string { return ip }
	leObtain = fakeLE(t, f)

	a, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.LAN.Enabled, c.LAN.TLS, c.LAN.Port = true, true, 8555 })
	if _, err := a.certAPI("cert/settings", "alex", map[string]any{"label": "homeowl"}); err == nil {
		t.Fatal("needs Cloudflare first")
	}
	if _, err := a.cloudflareAPI("cloudflare/connect", "alex", map[string]any{"token": "good-token"}); err != nil {
		t.Fatal(err)
	}
	_ = a.Cfg.Update(func(c *config.Config) { c.Cloudflare.ZoneID, c.Cloudflare.Zone = "z1", "example.com" })
	if _, err := a.certAPI("cert/settings", "alex", map[string]any{"label": "Not valid!"}); err == nil {
		t.Fatal("bad label accepted")
	}
	// set directly (the API would start the request in the background)
	_ = a.Cfg.Update(func(c *config.Config) { c.Cert = config.Cert{Label: "homeowl", Public: true} })
	if !a.certDue() {
		t.Fatal("a certificate should be due")
	}
	if err := a.issueCert("alex"); err != nil {
		t.Fatal(err)
	}
	if a.certDue() {
		t.Fatal("nothing due right after issuing")
	}
	if strings.Contains(f.summary(), "_acme-challenge") {
		t.Fatalf("the check record should be gone: %s", f.summary())
	}
	v := a.certView()
	if v["valid"] != true || v["url"] != "https://homeowl.example.com:8555" {
		t.Fatalf("view: %v", v)
	}
	if strings.Contains(string(mustRead(t, a.chainPath())), "PRIVATE") || a.Sec.Get(leKeySlot) == "" {
		t.Fatal("the key belongs in secrets, not next to the chain")
	}
	// HTTPS: the trusted certificate for its name only
	c, _ := a.pickCert(&tls.ClientHelloInfo{ServerName: "homeowl.example.com"})
	if c == nil || c.Leaf == nil || c.Leaf.Subject.CommonName != "homeowl.example.com" {
		t.Fatal("the name should get the Let's Encrypt certificate")
	}
	if c, _ := a.pickCert(&tls.ClientHelloInfo{}); c == a.le.Load() {
		t.Fatal("an IP visit should get the self-signed certificate")
	}
	if r := httptest.NewRequest("GET", "https://homeowl.example.com:8555/", nil); !a.hostAllowed(r) {
		t.Fatal("the certificate's name must be allowed")
	}
	if n := homeDNSNames(a.Cfg.Get()); len(n) != 1 || n[0] != "homeowl.example.com" {
		t.Fatalf("home DNS names: %v", n)
	}
	// the public name follows HomeOwl's address
	a.syncCertRecord("alex")
	if s := f.summary(); !strings.Contains(s, "dns homeowl.example.com") || a.certLoad().RecordIP != ip {
		t.Fatalf("public record: %s %+v", s, a.certLoad())
	}
	ip = "192.168.1.11"
	a.syncCertRecord("alex")
	if a.certLoad().RecordIP != "192.168.1.11" || f.recs[0]["content"] != "192.168.1.11" {
		t.Fatalf("address change: %+v %v", a.certLoad(), f.recs)
	}
	// profiles are signed with it
	body := []byte("<plist>test</plist>")
	signed, err := signProfile(body, a.signer())
	if err != nil {
		t.Fatal(err)
	}
	p7, err := pkcs7.Parse(signed)
	if err != nil || string(p7.Content) != string(body) || len(p7.Certificates) != 2 {
		t.Fatalf("signed profile: %v", err)
	}
	if err := p7.Verify(); err != nil {
		t.Fatalf("signature: %v", err)
	}
	// a restart finds it again
	a.le.Store(nil)
	a.loadLE()
	if a.le.Load() == nil {
		t.Fatal("reload")
	}
	// removing takes the public name away and forgets the key
	if _, err := a.certAPI("cert/remove", "alex", nil); err != nil {
		t.Fatal(err)
	}
	if f.summary() != "" || a.le.Load() != nil || a.Sec.Get(leKeySlot) != "" {
		t.Fatalf("after remove: %q", f.summary())
	}
	// someone else's record of that name is left alone
	f.recs = []map[string]any{{"id": "theirs", "name": "homeowl.example.com", "type": "A", "content": "203.0.113.9"}}
	_ = a.Cfg.Update(func(c *config.Config) { c.Cert = config.Cert{Label: "homeowl", Public: true} })
	a.syncCertRecord("alex")
	if st := a.certLoad(); st.Record != "" || !strings.Contains(st.RecordErr, "didn't make it") || len(f.recs) != 1 {
		t.Fatalf("foreign record: %+v %v", st, f.recs)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
