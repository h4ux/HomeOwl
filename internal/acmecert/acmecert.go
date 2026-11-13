// Package acmecert gets a free, publicly trusted certificate from Let's Encrypt for one name on the user's own
// domain, proving the domain with a DNS TXT record (DNS-01), so nothing has to be reachable from the internet.
// HomeOwl uses it for HTTPS on the home network and to sign the device setup profiles.
package acmecert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

// Directory is the ACME server (tests and the staging switch replace it).
var Directory = acme.LetsEncryptURL

// Prover publishes the TXT record that proves the domain, and removes it again.
type Prover interface {
	Present(ctx context.Context, fqdn, value string) (cleanup func(), err error)
	// Wait returns once the record can be seen where Let's Encrypt will look for it.
	Wait(ctx context.Context, fqdn, value string) error
}

// NewAccountKey makes the key that identifies HomeOwl's Let's Encrypt account (PEM).
func NewAccountKey() (string, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	b, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})), nil
}

func parseAccountKey(p string) (crypto.Signer, error) {
	blk, _ := pem.Decode([]byte(p))
	if blk == nil {
		return nil, errors.New("the saved Let's Encrypt account key is damaged")
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}

// Steps, in order, as reported to the step callback.
const (
	StepAccount  = "account"  // signing in to Let's Encrypt
	StepOrder    = "order"    // asking for the certificate
	StepRecord   = "record"   // adding the check record
	StepWait     = "wait"     // waiting until the domain's name servers show it
	StepValidate = "validate" // Let's Encrypt checks it
	StepIssue    = "issue"    // making and downloading the certificate
)

// Obtain gets a certificate for name. It returns the chain (leaf first) and the new private key, both PEM.
// The key is RSA so the certificate can also sign Apple configuration profiles. step (may be nil) hears about
// each step as it starts, with an optional detail.
func Obtain(ctx context.Context, accountPEM, name string, p Prover, step func(step, detail string)) (chainPEM, keyPEM []byte, err error) {
	if step == nil {
		step = func(string, string) {}
	}
	step(StepAccount, "")
	ak, err := parseAccountKey(accountPEM)
	if err != nil {
		return nil, nil, err
	}
	cl := &acme.Client{Key: ak, DirectoryURL: Directory, UserAgent: "HomeOwl"}
	if _, err := cl.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, nil, fmt.Errorf("Let's Encrypt account: %w", err)
	}
	step(StepOrder, "")
	order, err := cl.AuthorizeOrder(ctx, acme.DomainIDs(name))
	if err != nil {
		return nil, nil, fmt.Errorf("Let's Encrypt order: %w", err)
	}
	for _, u := range order.AuthzURLs {
		z, err := cl.GetAuthorization(ctx, u)
		if err != nil {
			return nil, nil, err
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var ch *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "dns-01" {
				ch = c
			}
		}
		if ch == nil {
			return nil, nil, errors.New("Let's Encrypt didn't offer the DNS check")
		}
		val, err := cl.DNS01ChallengeRecord(ch.Token)
		if err != nil {
			return nil, nil, err
		}
		fqdn := "_acme-challenge." + z.Identifier.Value
		step(StepRecord, fqdn)
		cleanup, err := p.Present(ctx, fqdn, val)
		if err != nil {
			return nil, nil, fmt.Errorf("couldn't add the check record: %w", err)
		}
		err = func() error {
			defer cleanup()
			step(StepWait, "")
			if err := p.Wait(ctx, fqdn, val); err != nil {
				return err
			}
			step(StepValidate, "")
			if _, err := cl.Accept(ctx, ch); err != nil {
				return fmt.Errorf("Let's Encrypt: %w", err)
			}
			if _, err := cl.WaitAuthorization(ctx, z.URI); err != nil {
				return fmt.Errorf("Let's Encrypt couldn't confirm the domain: %w", err)
			}
			return nil
		}()
		if err != nil {
			return nil, nil, err
		}
	}
	step(StepIssue, "")
	if order, err = cl.WaitOrder(ctx, order.URI); err != nil {
		return nil, nil, fmt.Errorf("Let's Encrypt order: %w", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}}, key)
	if err != nil {
		return nil, nil, err
	}
	ders, _, err := cl.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, nil, fmt.Errorf("Let's Encrypt certificate: %w", err)
	}
	for _, d := range ders {
		chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return chainPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), nil
}

// DoH are public DNS-over-HTTPS resolvers used as a second opinion (tests replace them).
var DoH = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve"}

// WaitTXT waits until the check record can be seen, so Let's Encrypt doesn't look too early. It asks the
// domain's own name servers directly and, as a second opinion, public DNS over HTTPS. Home networks often block,
// redirect or filter direct DNS (NextDNS, a router that intercepts port 53, broken IPv6), so not being able to
// confirm is not an error: Cloudflare already said the record exists, and Let's Encrypt checks with its own
// resolvers. After maxWait it carries on. report (may be nil) hears what the checks see.
func WaitTXT(ctx context.Context, nameservers []string, fqdn, value string, report func(detail string)) error {
	if report == nil {
		report = func(string) {}
	}
	start := time.Now()
	for {
		seen, reached := 0, 0
		var problems []string
		for _, ns := range nameservers {
			found, err := askNS(ctx, strings.TrimSuffix(ns, "."), fqdn, value)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			reached++
			if found {
				seen++
			}
		}
		if reached > 0 && seen == reached {
			report(fmt.Sprintf("%d of %d name servers show it", seen, len(nameservers)))
			return nil
		}
		if dohSees(ctx, fqdn, value) {
			report("public DNS shows it")
			return nil
		}
		waited := time.Since(start)
		switch {
		case reached == 0 && len(nameservers) > 0:
			report(fmt.Sprintf("can't ask the name servers from here (%s) — %ds", firstOf(problems), int(waited.Seconds())))
		default:
			report(fmt.Sprintf("%d of %d name servers show it — %ds", seen, len(nameservers), int(waited.Seconds())))
		}
		if waited > maxWait {
			report("couldn't confirm it from this network; Let's Encrypt checks for itself")
			return nil
		}
		select {
		case <-time.After(4 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// maxWait is how long WaitTXT waits for a confirmation before carrying on.
var maxWait = 90 * time.Second

func firstOf(l []string) string {
	if len(l) == 0 {
		return "no answer"
	}
	return l[0]
}

// askNS asks one name server (by IPv4 first, then IPv6) for the TXT record. An error means it couldn't be
// asked at all; (false, nil) means it answered without the record.
func askNS(ctx context.Context, ns, fqdn, value string) (bool, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	ips, err := net.DefaultResolver.LookupIPAddr(c, ns)
	cancel()
	if err != nil {
		return false, fmt.Errorf("%s: can't find its address", ns)
	}
	sort.SliceStable(ips, func(i, j int) bool { return ips[i].IP.To4() != nil && ips[j].IP.To4() == nil })
	var last error
	for _, ip := range ips {
		addr := net.JoinHostPort(ip.IP.String(), "53")
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 4 * time.Second}
			return d.DialContext(ctx, network, addr)
		}}
		c, cancel := context.WithTimeout(ctx, 6*time.Second)
		txts, err := r.LookupTXT(c, fqdn+".")
		cancel()
		var de *net.DNSError
		if err != nil && !(errors.As(err, &de) && de.IsNotFound) {
			last = err
			continue
		}
		for _, t := range txts {
			if t == value {
				return true, nil
			}
		}
		return false, nil
	}
	if last == nil {
		last = errors.New("no address")
	}
	var de *net.DNSError
	if errors.As(last, &de) && de.IsTimeout {
		return false, fmt.Errorf("%s: no answer on port 53", ns)
	}
	return false, fmt.Errorf("%s: %v", ns, last)
}

// dohSees asks the public DNS-over-HTTPS resolvers.
func dohSees(ctx context.Context, fqdn, value string) bool {
	hc := &http.Client{Timeout: 8 * time.Second}
	for _, u := range DoH {
		req, err := http.NewRequestWithContext(ctx, "GET", u+"?name="+url.QueryEscape(fqdn)+"&type=TXT", nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/dns-json")
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		var out struct {
			Answer []struct {
				Data string `json:"data"`
			} `json:"Answer"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out)
		resp.Body.Close()
		for _, a := range out.Answer {
			if strings.ReplaceAll(strings.Trim(a.Data, `"`), `" "`, "") == value {
				return true
			}
		}
	}
	return false
}
