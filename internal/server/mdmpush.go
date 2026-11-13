package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/h4ux/homeowl/internal/alerts"
)

// The MDM push certificate (Settings › Device profiles › Device management). Apple sends MDM "wake up" pushes only
// with a certificate from its Push Certificates Portal, and the portal takes only requests signed by an MDM vendor.
// mdmcert.download signs them for organizations using open-source MDM servers:
//
//  1. HomeOwl makes the push key and a request, and sends the request to mdmcert.download with the organization's
//     email, plus a certificate to encrypt the answer to.
//  2. mdmcert.download emails the signed request, encrypted; the admin uploads that file and HomeOwl decrypts it.
//  3. The admin uploads the decrypted request at identity.apple.com and downloads the push certificate.
//  4. The admin uploads the certificate here; HomeOwl checks it matches the key and reminds before it expires.
//
// The push key and the encryption key live in the secrets store; the rest (public) in mdm-push.json.

var (
	mdmcertURL = "https://mdmcert.download/api/v1/signrequest"
	// the key every open-source MDM sends (from Commandment, by mdmcert.download's author); Settings can change it
	mdmcertKey = "f847aea2ba06b41264d587b229e2712c89b1490a1208b7ff1aafab5bb40d47bc"
)

const (
	slotPushKey = "mdm-push-key"    // pairs with Apple's push certificate
	slotPKIKey  = "mdm-mdmcert-key" // decrypts mdmcert.download's answer
	slotMdmcert = "mdm-mdmcert-api-key"
)

type mdmPush struct {
	Email       string `json:"email,omitempty"`
	Country     string `json:"country,omitempty"`
	RequestedAt int64  `json:"requested_at,omitempty"` // sent to mdmcert.download
	PKICert     string `json:"pki_cert,omitempty"`     // PEM: what mdmcert.download encrypts to
	Request     string `json:"request,omitempty"`      // the decrypted signed request, for identity.apple.com
	DecryptedAt int64  `json:"decrypted_at,omitempty"`
	Cert        string `json:"cert,omitempty"` // PEM: Apple's push certificate
	Topic       string `json:"topic,omitempty"`
	NotAfter    int64  `json:"not_after,omitempty"`
	UploadedAt  int64  `json:"uploaded_at,omitempty"`
	Warned      int    `json:"warned,omitempty"`   // days-left threshold last alerted about (30, 7, 1)
	Renewing    bool   `json:"renewing,omitempty"` // steps 1-4 again while the current certificate keeps working
}

var mdmPushMu sync.Mutex

func (a *App) mdmPushPath() string { return filepath.Join(a.DataDir, "mdm-push.json") }

func (a *App) mdmPushLoad() mdmPush {
	var p mdmPush
	if b, err := os.ReadFile(a.mdmPushPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func (a *App) mdmPushUpdate(fn func(*mdmPush)) (mdmPush, error) {
	mdmPushMu.Lock()
	defer mdmPushMu.Unlock()
	p := a.mdmPushLoad()
	fn(&p)
	b, _ := json.MarshalIndent(p, "", "  ")
	return p, os.WriteFile(a.mdmPushPath(), b, 0o600)
}

func (a *App) mdmView() map[string]any {
	p := a.mdmPushLoad()
	step := 1
	switch {
	case p.Cert != "" && !p.Renewing:
		step = 5
	case p.Request != "":
		step = 3
	case p.RequestedAt > 0:
		step = 2
	}
	res := map[string]any{"step": step, "email": p.Email, "country": orDefault(p.Country, "US"), "requested_at": p.RequestedAt, "decrypted_at": p.DecryptedAt,
		"topic": p.Topic, "not_after": p.NotAfter, "uploaded_at": p.UploadedAt, "custom_key": a.Sec.Get(slotMdmcert) != "", "renewing": p.Renewing}
	if p.Cert != "" {
		res["cert_sha"] = certSHA(p.Cert)
	}
	if p.NotAfter > 0 {
		res["days_left"] = int(time.Until(time.Unix(p.NotAfter, 0)).Hours() / 24)
	}
	return res
}

// pemKey / parseKey keep RSA keys in the secrets store as PEM.
func pemKey(k *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

func parseKey(s string) (*rsa.PrivateKey, error) {
	b, _ := pem.Decode([]byte(s))
	if b == nil {
		return nil, errors.New("no key")
	}
	return x509.ParsePKCS1PrivateKey(b.Bytes)
}

var countryRE = regexp.MustCompile(`^[A-Z]{2}$`)

// mdmRequest makes the push key (once: renewals keep it), the request, and sends it to mdmcert.download.
func (a *App) mdmRequest(ctx context.Context, body map[string]any) (map[string]any, error) {
	email := strings.ToLower(strings.TrimSpace(s(body, "email")))
	country := strings.ToUpper(strings.TrimSpace(orDefault(s(body, "country"), "US")))
	if ad, err := mail.ParseAddress(email); err != nil || ad.Address != email {
		return nil, errors.New("that isn't an email address")
	}
	if !countryRE.MatchString(country) {
		return nil, errors.New("the country is two letters, e.g. US, GB, IL")
	}
	if k := strings.TrimSpace(s(body, "api_key")); k != "" { // "-" goes back to the shared key
		if k == "-" {
			a.Sec.Delete(slotMdmcert)
		} else if err := a.Sec.Set(slotMdmcert, k); err != nil {
			return nil, err
		}
	}
	push, err := parseKey(a.Sec.Get(slotPushKey))
	if err != nil {
		if push, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			return nil, err
		}
		if err := a.Sec.Set(slotPushKey, pemKey(push)); err != nil {
			return nil, err
		}
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{
		Country: []string{country}, CommonName: "HomeOwl MDM push",
		ExtraNames: []pkix.AttributeTypeAndValue{{Type: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}, Value: email}},
	}}, push)
	if err != nil {
		return nil, err
	}
	// a fresh key for mdmcert.download to encrypt its answer to
	pki, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "mdmcert.download"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageDataEncipherment}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &pki.PublicKey, pki)
	if err != nil {
		return nil, err
	}
	pkiPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
	key := orDefault(a.Sec.Get(slotMdmcert), mdmcertKey)
	reqBody, _ := json.Marshal(map[string]string{"csr": base64.StdEncoding.EncodeToString(csrPEM), "email": email, "key": key,
		"encrypt": base64.StdEncoding.EncodeToString(pkiPEM)})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", mdmcertURL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HomeOwl")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach mdmcert.download: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var r struct{ Result, Reason string }
	_ = json.Unmarshal(rb, &r)
	if resp.StatusCode != 200 || r.Result != "success" {
		msg := strings.TrimSpace(orDefault(r.Reason, r.Result))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, errors.New("mdmcert.download said: " + msg + ". The email must belong to an organization registered (and verified) at mdmcert.download.")
	}
	if err := a.Sec.Set(slotPKIKey, pemKey(pki)); err != nil {
		return nil, err
	}
	_, err = a.mdmPushUpdate(func(p *mdmPush) {
		p.Email, p.Country, p.RequestedAt, p.PKICert, p.Request, p.DecryptedAt = email, country, time.Now().Unix(), string(pkiPEM), "", 0
	})
	return a.mdmView(), err
}

// mdmDecrypt opens mdmcert.download's emailed file (hex text of a CMS envelope encrypted to our certificate).
func (a *App) mdmDecrypt(body map[string]any) (map[string]any, error) {
	p := a.mdmPushLoad()
	key, err := parseKey(a.Sec.Get(slotPKIKey))
	if err != nil || p.PKICert == "" {
		return nil, errors.New("send the request to mdmcert.download first")
	}
	blk, _ := pem.Decode([]byte(p.PKICert))
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	raw := strings.Join(strings.Fields(s(body, "file")), "")
	der, err := hex.DecodeString(raw)
	if err != nil || len(der) == 0 {
		return nil, errors.New("that isn't the file from mdmcert.download (it ends in .plist.b64.p7)")
	}
	env, err := pkcs7.Parse(der)
	if err != nil {
		return nil, errors.New("that isn't the file from mdmcert.download: " + err.Error())
	}
	out, err := env.Decrypt(cert, key)
	if err != nil {
		return nil, errors.New("couldn't decrypt it: it was made for another request (send a new one, then use the newest email)")
	}
	if len(out) == 0 || len(out) > 256<<10 {
		return nil, errors.New("the decrypted request looks wrong")
	}
	_, err = a.mdmPushUpdate(func(p *mdmPush) { p.Request, p.DecryptedAt = string(out), time.Now().Unix() })
	return a.mdmView(), err
}

var oidUID = asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}

// mdmPushCert takes Apple's push certificate: it must match the push key and carry an MDM topic. A renewal must
// keep the topic, or every enrolled device would have to enrol again.
func (a *App) mdmPushCert(body map[string]any) (map[string]any, error) {
	key, err := parseKey(a.Sec.Get(slotPushKey))
	if err != nil {
		return nil, errors.New("there's no push key: start with step 1")
	}
	var cert *x509.Certificate
	rest := []byte(s(body, "pem"))
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if c, err := x509.ParseCertificate(b.Bytes); err == nil && b.Type == "CERTIFICATE" {
			cert = c
			break
		}
	}
	if cert == nil { // the portal's download is PEM; accept DER too
		if c, err := x509.ParseCertificate([]byte(s(body, "pem"))); err == nil {
			cert = c
		}
	}
	if cert == nil {
		return nil, errors.New("that isn't a certificate: upload the .pem file from identity.apple.com")
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || pub.N.Cmp(key.PublicKey.N) != 0 || pub.E != key.PublicKey.E {
		return nil, errors.New("this certificate wasn't made from HomeOwl's request (its key doesn't match)")
	}
	topic := ""
	for _, n := range cert.Subject.Names {
		if n.Type.Equal(oidUID) {
			topic, _ = n.Value.(string)
		}
	}
	if !strings.HasPrefix(topic, "com.apple.mgmt.") {
		return nil, errors.New("this isn't an MDM push certificate (no com.apple.mgmt topic)")
	}
	if time.Now().After(cert.NotAfter) {
		return nil, errors.New("this certificate has expired")
	}
	old := a.mdmPushLoad()
	if old.Topic != "" && old.Topic != topic && !b(body, "new_topic") {
		return nil, errors.New("this certificate has another topic than the current one: it was created instead of renewed. Renew the existing certificate at identity.apple.com (same Apple ID), or confirm to switch — enrolled devices then have to enrol again")
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	_, err = a.mdmPushUpdate(func(p *mdmPush) {
		p.Cert, p.Topic, p.NotAfter, p.UploadedAt, p.Warned, p.Renewing = certPEM, topic, cert.NotAfter.Unix(), time.Now().Unix(), 0, false
		p.RequestedAt, p.Request, p.DecryptedAt, p.PKICert = 0, "", 0, ""
	})
	return a.mdmView(), err
}

// mdmRestart goes back to step 1 keeping any working certificate: to send the request again, to renew ("renew"),
// or to give up a renewal ("cancel").
func (a *App) mdmRestart(body map[string]any) map[string]any {
	a.Sec.Delete(slotPKIKey)
	_, _ = a.mdmPushUpdate(func(p *mdmPush) {
		p.RequestedAt, p.Request, p.DecryptedAt, p.PKICert = 0, "", 0, ""
		if b(body, "renew") && p.Cert != "" {
			p.Renewing = true
		}
		if b(body, "cancel") {
			p.Renewing = false
		}
	})
	return a.mdmView()
}

// mdmReset starts over. Without "all" the push key stays, so a renewal keeps pairing with it.
func (a *App) mdmReset(all bool) map[string]any {
	mdmPushMu.Lock()
	_ = os.Remove(a.mdmPushPath())
	mdmPushMu.Unlock()
	a.Sec.Delete(slotPKIKey)
	if all {
		a.Sec.Delete(slotPushKey)
	}
	return a.mdmView()
}

// mdmPushWatch alerts 30, 7 and 1 days before the push certificate expires: after that Apple refuses its pushes,
// and a certificate that expired can't be renewed (devices then have to enrol again).
func (a *App) mdmPushWatch(now time.Time) {
	p := a.mdmPushLoad()
	if p.NotAfter == 0 {
		return
	}
	left := int(time.Unix(p.NotAfter, 0).Sub(now).Hours() / 24)
	for _, th := range []int{1, 7, 30} {
		if left <= th && (p.Warned == 0 || th < p.Warned) {
			a.Alerts.Add(alerts.Alert{Kind: "mdm_push", Level: "warn", Title: fmt.Sprintf("MDM push certificate expires in %d days", max(left, 0)),
				Body: "Renew it at identity.apple.com with the same Apple ID (Renew, not Create), using a new signed request from Settings › Device profiles. An expired one can't be renewed."})
			_, _ = a.mdmPushUpdate(func(p *mdmPush) { p.Warned = th })
			return
		}
	}
}

func (a *App) mdmLoop(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		a.mdmPushWatch(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// fingerprint for the UI
func certSHA(pemText string) string {
	b, _ := pem.Decode([]byte(pemText))
	if b == nil {
		return ""
	}
	h := sha256.Sum256(b.Bytes)
	return hex.EncodeToString(h[:8])
}
