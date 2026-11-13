package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

func TestMDMPushCert(t *testing.T) {
	a := checkinApp(t)
	var got map[string]string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["key"] == "" || got["email"] != "it@example.com" {
			_, _ = w.Write([]byte(`{"result":"failure","reason":"email not registered"}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer fake.Close()
	old := mdmcertURL
	mdmcertURL = fake.URL
	defer func() { mdmcertURL = old }()

	if _, err := a.mdmRequest(context.Background(), map[string]any{"email": "not an email"}); err == nil {
		t.Fatal("bad email accepted")
	}
	if _, err := a.mdmRequest(context.Background(), map[string]any{"email": "other@example.com"}); err == nil || !strings.Contains(err.Error(), "email not registered") {
		t.Fatalf("the service's refusal should be shown: %v", err)
	}
	v, err := a.mdmRequest(context.Background(), map[string]any{"email": "IT@example.com", "country": "gb"})
	if err != nil {
		t.Fatal(err)
	}
	if v["step"] != 2 || a.Sec.Get(slotPushKey) == "" || a.Sec.Get(slotPKIKey) == "" {
		t.Fatalf("after the request: %v", v)
	}
	// what was sent: base64 of PEM CSR with the email, and of the PEM certificate to encrypt to
	csrPEM, _ := base64.StdEncoding.DecodeString(got["csr"])
	blk, _ := pem.Decode(csrPEM)
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil || csr.Subject.Country[0] != "GB" || !strings.Contains(csr.Subject.String(), "it@example.com") {
		t.Fatalf("csr: %v %v", err, csr.Subject)
	}
	encPEM, _ := base64.StdEncoding.DecodeString(got["encrypt"])
	eb, _ := pem.Decode(encPEM)
	encCert, err := x509.ParseCertificate(eb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	vb, _ := json.Marshal(a.mdmView())
	if strings.Contains(string(vb), "PRIVATE") {
		t.Fatal("keys must not go to the browser")
	}

	// the emailed answer: hex text of an envelope encrypted to our certificate
	signed := []byte(base64.StdEncoding.EncodeToString([]byte("<plist>signed push request</plist>")))
	env, err := pkcs7.Encrypt(signed, []*x509.Certificate{encCert})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.mdmDecrypt(map[string]any{"file": "zz-not-hex"}); err == nil {
		t.Fatal("junk accepted")
	}
	h := hex.EncodeToString(env)
	if v, err := a.mdmDecrypt(map[string]any{"file": h[:40] + "\n" + h[40:] + "\n"}); err != nil || v["step"] != 3 || a.mdmPushLoad().Request != string(signed) {
		t.Fatalf("decrypt: %v %v", err, v)
	}

	// Apple's certificate: the push key, an MDM topic
	push := csr.PublicKey.(*rsa.PublicKey)
	mk := func(pub *rsa.PublicKey, topic string) string {
		ca, _ := rsa.GenerateKey(rand.Reader, 2048)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
			Subject: pkix.Name{CommonName: "APSP:" + topic, ExtraNames: []pkix.AttributeTypeAndValue{{Type: oidUID, Value: topic}}}}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, ca)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := a.mdmPushCert(map[string]any{"pem": mk(&other.PublicKey, "com.apple.mgmt.External.aaaa")}); err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("another key accepted: %v", err)
	}
	if _, err := a.mdmPushCert(map[string]any{"pem": mk(push, "com.example.app")}); err == nil {
		t.Fatal("non-MDM topic accepted")
	}
	v, err = a.mdmPushCert(map[string]any{"pem": mk(push, "com.apple.mgmt.External.aaaa")})
	if err != nil || v["step"] != 5 || v["topic"] != "com.apple.mgmt.External.aaaa" || v["days_left"].(int) < 360 {
		t.Fatalf("push cert: %v %v", err, v)
	}
	// a "renewal" that created a new topic needs confirming
	if _, err := a.mdmPushCert(map[string]any{"pem": mk(push, "com.apple.mgmt.External.bbbb")}); err == nil {
		t.Fatal("topic change accepted without confirming")
	}
	if _, err := a.mdmPushCert(map[string]any{"pem": mk(push, "com.apple.mgmt.External.bbbb"), "new_topic": true}); err != nil {
		t.Fatal(err)
	}

	// renewing keeps the working certificate until the new one is in
	if v := a.mdmRestart(map[string]any{"renew": true}); v["step"] != 1 || v["renewing"] != true || a.mdmPushLoad().Cert == "" {
		t.Fatalf("renew: %v", v)
	}
	if v := a.mdmRestart(map[string]any{"cancel": true}); v["step"] != 5 {
		t.Fatalf("cancel renewal: %v", v)
	}

	// expiry reminders: 30, 7, 1 days, once each
	count := func() int {
		n := 0
		for _, al := range a.Alerts.List() {
			if al.Kind == "mdm_push" {
				n++
			}
		}
		return n
	}
	exp := time.Unix(a.mdmPushLoad().NotAfter, 0)
	a.mdmPushWatch(exp.Add(-40 * 24 * time.Hour))
	a.mdmPushWatch(exp.Add(-20 * 24 * time.Hour))
	a.mdmPushWatch(exp.Add(-19 * 24 * time.Hour))
	a.mdmPushWatch(exp.Add(-5 * 24 * time.Hour))
	if count() != 2 {
		t.Fatalf("reminders: %d", count())
	}

	// starting over keeps the push key (renewals pair with it) unless asked
	k := a.Sec.Get(slotPushKey)
	if a.mdmReset(false); a.Sec.Get(slotPushKey) != k || a.mdmPushLoad().Cert != "" {
		t.Fatal("reset")
	}
	if a.mdmReset(true); a.Sec.Get(slotPushKey) != "" {
		t.Fatal("reset all should remove the key")
	}
}
