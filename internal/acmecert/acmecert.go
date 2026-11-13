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
	"encoding/pem"
	"errors"
	"fmt"
	"net"
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

// Obtain gets a certificate for name. It returns the chain (leaf first) and the new private key, both PEM.
// The key is RSA so the certificate can also sign Apple configuration profiles.
func Obtain(ctx context.Context, accountPEM, name string, p Prover) (chainPEM, keyPEM []byte, err error) {
	ak, err := parseAccountKey(accountPEM)
	if err != nil {
		return nil, nil, err
	}
	cl := &acme.Client{Key: ak, DirectoryURL: Directory, UserAgent: "HomeOwl"}
	if _, err := cl.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, nil, fmt.Errorf("Let's Encrypt account: %w", err)
	}
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
		cleanup, err := p.Present(ctx, fqdn, val)
		if err != nil {
			return nil, nil, fmt.Errorf("couldn't add the check record: %w", err)
		}
		err = func() error {
			defer cleanup()
			if err := p.Wait(ctx, fqdn, val); err != nil {
				return err
			}
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

// WaitTXT polls the domain's own name servers until each of them answers the TXT record (they are what
// Let's Encrypt ends up asking, and asking them directly skips any cached "not there yet").
func WaitTXT(ctx context.Context, nameservers []string, fqdn, value string) error {
	if len(nameservers) == 0 {
		select { // nothing to ask: give the record a moment
		case <-time.After(20 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		seen := 0
		for _, ns := range nameservers {
			ns := strings.TrimSuffix(ns, ".")
			r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
			}}
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			txts, _ := r.LookupTXT(c, fqdn)
			cancel()
			for _, t := range txts {
				if t == value {
					seen++
					break
				}
			}
		}
		if seen == len(nameservers) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the check record didn't show up on the domain's name servers in time — try again in a few minutes")
		}
		select {
		case <-time.After(4 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
