package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/h4ux/homeowl/internal/acmecert"
	"github.com/h4ux/homeowl/internal/cloudflare"
	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/mdns"
	"github.com/h4ux/homeowl/internal/unifi"
)

// A trusted certificate (Settings › Access): HomeOwl asks Let's Encrypt for a certificate for a name on the
// admin's own domain (homeowl.example.com), proving the domain with a short-lived TXT record through the
// Cloudflare token — nothing has to be reachable from the internet. The certificate gives HomeOwl HTTPS without
// warnings on the home network (the name is pointed at HomeOwl through the home DNS names, and optionally in
// public DNS) and signs the device setup profiles, so iPhones show them as "Verified". It is renewed on its own.
//
// The account key and the certificate's private key are secrets (keychain / <data>/secrets); the certificate
// chain itself is public and lives in <data>/tls/letsencrypt.pem.

const (
	leAccountSlot = "letsencrypt-account"
	leKeySlot     = "letsencrypt-key"
	leComment     = "HomeOwl: HomeOwl's address on the home network"
)

// leObtain gets the certificate (tests replace it).
var leObtain = acmecert.Obtain

// the steps of a request, in order, for the progress shown in Settings
var certSteps = []string{"start", acmecert.StepAccount, acmecert.StepOrder, acmecert.StepRecord, acmecert.StepWait, acmecert.StepValidate, acmecert.StepIssue, "save"}

type certProgress struct {
	Step    string `json:"step"`
	Detail  string `json:"detail,omitempty"`
	Started int64  `json:"started"` // ms
	StepAt  int64  `json:"step_at"` // ms
}

func (a *App) certStep(step, detail string) {
	a.certPMu.Lock()
	defer a.certPMu.Unlock()
	a.certProg.Step, a.certProg.Detail, a.certProg.StepAt = step, detail, time.Now().UnixMilli()
}

// startCert begins a request in the background. It is marked busy before this returns, so the page that asked
// sees it working straight away.
func (a *App) startCert(by string) error {
	if certName(a.Cfg.Get()) == "" {
		return errors.New("choose the name first")
	}
	if !a.certBusy.CompareAndSwap(false, true) {
		return errors.New("HomeOwl is already getting the certificate")
	}
	now := time.Now().UnixMilli()
	a.certPMu.Lock()
	a.certProg = certProgress{Step: "start", Started: now, StepAt: now}
	a.certPMu.Unlock()
	go func() {
		defer a.certBusy.Store(false)
		_ = a.issueCert(by)
	}()
	return nil
}

type certState struct {
	Name     string `json:"name,omitempty"`
	NotAfter int64  `json:"not_after,omitempty"`
	Issued   int64  `json:"issued,omitempty"`
	Error    string `json:"error,omitempty"`
	Try      int64  `json:"try,omitempty"`
	// the optional public DNS record
	Record     string `json:"record,omitempty"`
	RecordZone string `json:"record_zone,omitempty"`
	RecordIP   string `json:"record_ip,omitempty"`
	RecordErr  string `json:"record_error,omitempty"`
	// records of that name HomeOwl didn't make ("A → 203.0.113.9"): replaced only when the admin says so
	Conflict []string `json:"conflict,omitempty"`
}

// certName is the full name, or "" when the certificate is off or Cloudflare has no domain picked.
func certName(c config.Config) string {
	if c.Cert.Label == "" || c.Cloudflare.Zone == "" {
		return ""
	}
	return c.Cert.Label + "." + c.Cloudflare.Zone
}

func (a *App) certPath() string  { return filepath.Join(a.DataDir, "cert.json") }
func (a *App) chainPath() string { return filepath.Join(a.DataDir, "tls", "letsencrypt.pem") }

func (a *App) certLoad() certState {
	var st certState
	if b, err := os.ReadFile(a.certPath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (a *App) certSave(st certState) {
	if b, err := json.Marshal(st); err == nil {
		_ = config.WriteFileAtomic(a.certPath(), b, 0o600)
	}
}

// certUpdate changes the saved state in one go. The certificate request and the public-name sync run at the
// same time and each change only their own fields: saving a whole copy loaded minutes earlier would undo the
// other's work (that once made HomeOwl forget the record it had just added).
func (a *App) certUpdate(fn func(*certState)) {
	a.certSMu.Lock()
	defer a.certSMu.Unlock()
	st := a.certLoad()
	fn(&st)
	a.certSave(st)
}

// loadLE puts the saved certificate in use (when it is for the configured name and not expired).
func (a *App) loadLE() {
	name := certName(a.Cfg.Get())
	chain, err := os.ReadFile(a.chainPath())
	key := a.Sec.Get(leKeySlot)
	if name == "" || err != nil || key == "" {
		a.le.Store(nil)
		return
	}
	c, err := tls.X509KeyPair(chain, []byte(key))
	if err != nil {
		a.le.Store(nil)
		return
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil || leaf.VerifyHostname(name) != nil || time.Now().After(leaf.NotAfter) {
		a.le.Store(nil)
		return
	}
	c.Leaf = leaf
	a.le.Store(&c)
}

// pickCert: the trusted certificate for its own name, the self-signed one for everything else.
func (a *App) pickCert(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if le := a.le.Load(); le != nil && len(le.Leaf.DNSNames) > 0 && strings.EqualFold(h.ServerName, le.Leaf.DNSNames[0]) {
		return le, nil
	}
	return &a.cert, nil
}

// signer is the certificate that signs device profiles (nil when there is none).
func (a *App) signer() *tls.Certificate {
	if le := a.le.Load(); le != nil && time.Now().Before(le.Leaf.NotAfter) {
		return le
	}
	return nil
}

// certDue: a certificate is wanted and missing, for another name, or within 30 days of expiring — and the last
// failure (if any) was a while ago (Let's Encrypt limits failed tries).
func (a *App) certDue() bool {
	c, st := a.Cfg.Get(), a.certLoad()
	name := certName(c)
	if name == "" || a.Sec.Get(cfSlot) == "" {
		return false
	}
	if st.Error != "" && time.Since(time.Unix(st.Try, 0)) < 6*time.Hour {
		return false
	}
	return st.Name != name || a.le.Load() == nil || time.Until(time.Unix(st.NotAfter, 0)) < 30*24*time.Hour
}

// issueCert asks Let's Encrypt for the certificate. One at a time; it takes up to a few minutes.
func (a *App) issueCert(by string) error {
	if !a.certMu.TryLock() {
		return errors.New("HomeOwl is already getting the certificate")
	}
	defer a.certMu.Unlock()
	c := a.Cfg.Get()
	name := certName(c)
	try := time.Now().Unix()
	fail := func(err error) error {
		a.certUpdate(func(st *certState) { st.Try, st.Error = try, err.Error() })
		log.Printf("certificate for %s: %v", name, err)
		return err
	}
	if name == "" {
		return errors.New("choose the name first")
	}
	tok := a.Sec.Get(cfSlot)
	if tok == "" {
		return fail(errors.New("connect Cloudflare first: HomeOwl proves the domain through it"))
	}
	cl := cloudflare.New(tok)
	zones, err := cl.Zones()
	if err != nil {
		return fail(err)
	}
	var zone cloudflare.Zone
	for _, z := range zones {
		if z.ID == c.Cloudflare.ZoneID {
			zone = z
		}
	}
	if zone.ID == "" {
		return fail(errors.New("the Cloudflare token can't see " + c.Cloudflare.Zone + " any more"))
	}
	acct := a.Sec.Get(leAccountSlot)
	if acct == "" {
		if acct, err = acmecert.NewAccountKey(); err != nil {
			return fail(err)
		}
		if err := a.Sec.Set(leAccountSlot, acct); err != nil {
			return fail(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	p := &cfProver{cl: cl, zone: zone, seen: func(d string) { a.certStep(acmecert.StepWait, d) }}
	chain, key, err := leObtain(ctx, acct, name, p, a.certStep)
	if err != nil {
		return fail(err)
	}
	blk, err := tls.X509KeyPair(chain, key)
	if err != nil {
		return fail(err)
	}
	leaf, err := x509.ParseCertificate(blk.Certificate[0])
	if err != nil {
		return fail(err)
	}
	a.certStep("save", "")
	if err := a.Sec.Set(leKeySlot, string(key)); err != nil {
		return fail(err)
	}
	_ = os.MkdirAll(filepath.Dir(a.chainPath()), 0o700)
	if err := config.WriteFileAtomic(a.chainPath(), chain, 0o644); err != nil {
		return fail(err)
	}
	a.certUpdate(func(st *certState) {
		st.Name, st.NotAfter, st.Issued, st.Error, st.Try = name, leaf.NotAfter.Unix(), time.Now().Unix(), "", try
	})
	a.loadLE()
	a.applyPort443()
	pub := unifi.Preview{Method: "POST", Path: "/acme/new-order", Target: name, Description: "Let's Encrypt certificate for " + name,
		Before: map[string]any{}, After: map[string]any{"name": name, "expires": leaf.NotAfter.Format("2006-01-02")}}
	_, _ = a.Changes.External("acme-v02.api.letsencrypt.org", by, pub, false, func() (int, any) { return 200, nil })
	return nil
}

// cfProver adds and removes the _acme-challenge TXT record on Cloudflare.
type cfProver struct {
	cl   *cloudflare.Client
	zone cloudflare.Zone
	seen func(detail string)
}

func (p *cfProver) Present(_ context.Context, fqdn, value string) (func(), error) {
	id, err := p.cl.CreateRecord(p.zone.ID, "TXT", fqdn, value, "HomeOwl: Let's Encrypt check, removed right after")
	if err != nil {
		return nil, err
	}
	return func() { _ = p.cl.DeleteRecord(p.zone.ID, id) }, nil
}

func (p *cfProver) Wait(ctx context.Context, fqdn, value string) error {
	return acmecert.WaitTXT(ctx, p.zone.NameServers, fqdn, value, p.seen)
}

// syncCertRecord keeps the optional public name → HomeOwl's home address record in line (and removes it when
// it's turned off). A record of that name HomeOwl made earlier is taken over; one it didn't make is left alone
// and reported as a conflict, unless replace is set (the admin confirmed replacing it).
func (a *App) syncCertRecord(by string, replace bool) {
	a.cfMu.Lock()
	defer a.cfMu.Unlock()
	c, st := a.Cfg.Get(), a.certLoad()
	name, ip := certName(c), a.certAddress(c)
	want := c.Cert.Public && name != "" && ip != ""
	tok := a.Sec.Get(cfSlot)
	settled := want && st.Record != "" && st.RecordIP == ip && st.RecordZone == c.Cloudflare.ZoneID && st.RecordErr == ""
	if tok == "" || settled || (!want && st.Record == "" && st.RecordErr == "" && len(st.Conflict) == 0) {
		return
	}
	cl := cloudflare.New(tok)
	rec, zone, recIP := st.Record, st.RecordZone, st.RecordIP
	var did []string
	var problem string
	var conflict []string
	defer func() {
		a.certUpdate(func(s *certState) {
			s.Record, s.RecordZone, s.RecordIP, s.RecordErr, s.Conflict = rec, zone, recIP, problem, conflict
		})
		if len(did) > 0 {
			pub := unifi.Preview{Method: "PUT", Path: "/zones/*/dns_records", Target: name, Description: "Cloudflare: " + strings.Join(did, "; "),
				Before: map[string]any{}, After: map[string]any{"changes": did}}
			_, _ = a.Changes.External("api.cloudflare.com", by, pub, false, func() (int, any) { return 200, nil })
		}
	}()
	if rec != "" && (!want || zone != c.Cloudflare.ZoneID) {
		if err := cl.DeleteRecord(zone, rec); err != nil {
			problem = err.Error()
			return
		}
		did = append(did, "removed the public name")
		rec, zone, recIP = "", "", ""
	}
	if !want {
		return
	}
	if rec != "" { // ours already: point it at the current address
		err := cl.UpdateRecordContent(zone, rec, ip)
		if err == nil {
			if recIP != ip {
				did = append(did, "pointed "+name+" at "+ip)
			}
			recIP = ip
			return
		}
		if !cloudflare.IsNotFound(err) {
			problem = err.Error()
			return
		}
		rec, zone, recIP = "", "", "" // removed on Cloudflare meanwhile: look again below
	}
	recs, err := cl.Records(c.Cloudflare.ZoneID, name)
	if err != nil {
		problem = err.Error()
		return
	}
	var others []cloudflare.Record
	for _, r := range recs {
		if r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
			continue // other kinds (TXT, MX…) don't get in the way
		}
		if r.Comment == leComment && rec == "" && r.Type == "A" {
			rec, zone, recIP = r.ID, c.Cloudflare.ZoneID, r.Content // made by HomeOwl earlier: take it over
			continue
		}
		others = append(others, r)
	}
	if len(others) > 0 && !replace {
		for _, r := range others {
			conflict = append(conflict, r.Type+" → "+r.Content)
		}
		problem = name + " already exists on Cloudflare and HomeOwl didn't make it"
		return
	}
	for _, r := range others {
		if err := cl.DeleteRecord(c.Cloudflare.ZoneID, r.ID); err != nil {
			problem = "couldn't replace the old record: " + err.Error()
			return
		}
		did = append(did, "replaced "+name+" ("+r.Type+" → "+r.Content+")")
	}
	if rec != "" {
		if recIP != ip {
			if err := cl.UpdateRecordContent(zone, rec, ip); err != nil {
				problem = err.Error()
				return
			}
			did = append(did, "pointed "+name+" at "+ip)
			recIP = ip
		}
		return
	}
	id, err := cl.CreateRecord(c.Cloudflare.ZoneID, "A", name, ip, leComment)
	if err != nil {
		problem = err.Error()
		return
	}
	rec, zone, recIP = id, c.Cloudflare.ZoneID, ip
	did = append(did, "added "+name+" → "+ip)
}

// certAddress: HomeOwl's address at home (the one set for the Cloudflare links, else found automatically).
func (a *App) certAddress(c config.Config) string {
	ip := c.Cloudflare.Address
	if ip == "" {
		ip = homeIP()
	}
	if p := net.ParseIP(ip); p == nil || p.To4() == nil || !p.IsPrivate() {
		return ""
	}
	return ip
}

// applyPort443 answers HTTPS on port 443 while there is a trusted certificate (and HTTPS on the home network is
// on), so https://<name> works without a port. The certificate's name gets it; other visits get the self-signed
// one, like on HomeOwl's own port.
func (a *App) applyPort443() {
	c := a.Cfg.Get()
	want := c.LAN.Enabled && c.LAN.TLS && c.Cert.Port443 != "off" && a.le.Load() != nil
	a.mu.Lock()
	defer a.mu.Unlock()
	if !want {
		if a.port443 != nil {
			go func(s *http.Server) { _ = s.Close() }(a.port443)
		}
		a.port443, a.port443Err = nil, ""
		return
	}
	if a.port443 != nil {
		return
	}
	ln, err := listen443()
	if err != nil {
		a.port443Err = "Port 443 couldn't be used (" + err.Error() + "), so the address includes :" + strconv.Itoa(c.LAN.Port) + ", and it works like that."
		if errors.Is(err, syscall.EADDRINUSE) {
			a.port443Err += " Another program already uses port 443 on this computer."
		} else if runtime.GOOS == "linux" {
			a.port443Err += " To allow it on Linux, run: sudo setcap cap_net_bind_service=+ep \"$(command -v homeowl)\", then restart HomeOwl."
		}
		return
	}
	srv := &http.Server{Handler: a.handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute, ErrorLog: log.New(io.Discard, "", 0)}
	a.port443, a.port443Err, a.port443Addr = srv, "", ln.Addr().String()
	go func() {
		if err := srv.Serve(tls.NewListener(ln, &tls.Config{GetCertificate: a.pickCert, MinVersion: tls.VersionTLS12})); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("port 443: %v", err)
		}
	}()
}

// listen443 opens port 443 (tests use a free port instead).
var listen443 = func() (net.Listener, error) { return net.Listen("tcp", ":443") }

// certURL is the address to open HomeOwl by its name ("" without a certificate).
func (a *App) certURL() string {
	c := a.Cfg.Get()
	name := certName(c)
	if name == "" || a.le.Load() == nil {
		return ""
	}
	a.mu.Lock()
	p443 := a.port443 != nil
	a.mu.Unlock()
	if p443 {
		return "https://" + name
	}
	return "https://" + name + ":" + strconv.Itoa(c.LAN.Port)
}

func (a *App) certLoop(ctx context.Context) {
	a.loadLE()
	a.applyPort443()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if a.certDue() && a.certBusy.CompareAndSwap(false, true) {
			now := time.Now().UnixMilli()
			a.certPMu.Lock()
			a.certProg = certProgress{Step: "start", Started: now, StepAt: now}
			a.certPMu.Unlock()
			_ = a.issueCert("HomeOwl")
			a.certBusy.Store(false)
		}
		a.syncCertRecord("HomeOwl", false)
		a.applyPort443()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) certView() map[string]any {
	c, st := a.Cfg.Get(), a.certLoad()
	name := certName(c)
	a.mu.Lock()
	res443 := map[string]any{"on": a.port443 != nil, "error": a.port443Err}
	a.mu.Unlock()
	res := map[string]any{"label": c.Cert.Label, "zone": c.Cloudflare.Zone, "name": name, "public": c.Cert.Public, "busy": a.certBusy.Load(),
		"port443": c.Cert.Port443 != "off", "port443_state": res443,
		"cloudflare": a.Sec.Get(cfSlot) != "", "https": c.LAN.TLS && c.LAN.Enabled, "address": a.certAddress(c), "state": st}
	if res["busy"] == true {
		a.certPMu.Lock()
		res["progress"] = a.certProg
		a.certPMu.Unlock()
	}
	res["steps"] = certSteps
	res["now"] = time.Now().UnixMilli()
	if le := a.le.Load(); le != nil {
		res["valid"] = true
		res["expires"] = le.Leaf.NotAfter.Unix()
		res["url"] = a.certURL()
		res["url_port"] = "https://" + name + ":" + strconv.Itoa(c.LAN.Port)
	}
	return res
}

// certAPI: settings / issue / remove (admins only).
func (a *App) certAPI(p, by string, body map[string]any) (any, error) {
	switch p {
	case "cert/settings":
		label := strings.ToLower(strings.TrimSpace(s(body, "label")))
		if label != "" && !mdns.ValidLabel(label) {
			return nil, errors.New("the name is one word: letters, numbers and dashes (like “homeowl”)")
		}
		c := a.Cfg.Get()
		if label != "" && (c.Cloudflare.Zone == "" || a.Sec.Get(cfSlot) == "") {
			return nil, errors.New("connect Cloudflare and pick your domain first (just above)")
		}
		if label != "" && (label == c.Cloudflare.Wifi || label == c.Cloudflare.Hi) {
			return nil, errors.New("that name is already used for a Cloudflare link — pick another one")
		}
		changed := label != c.Cert.Label
		p443 := map[bool]string{true: "", false: "off"}[b(body, "port443")]
		if _, ok := body["port443"]; !ok {
			p443 = c.Cert.Port443
		}
		if err := a.Cfg.Update(func(c *config.Config) { c.Cert = config.Cert{Label: label, Public: b(body, "public"), Port443: p443} }); err != nil {
			return nil, err
		}
		a.loadLE()
		a.applyPort443()
		go a.syncCertRecord(by, false)
		go a.syncHomeDNS(by)
		if changed && label != "" {
			_ = a.startCert(by)
		}
		return a.certView(), nil
	case "cert/issue":
		if err := a.startCert(by); err != nil {
			return nil, err
		}
		return a.certView(), nil
	case "cert/view":
		return a.certView(), nil
	case "cert/replace-record": // the admin confirmed replacing the records of that name HomeOwl didn't make
		if !a.Cfg.Get().Cert.Public {
			return nil, errors.New("turn on “Also publish it in public DNS” first")
		}
		a.syncCertRecord(by, true)
		if st := a.certLoad(); st.RecordErr != "" {
			return nil, errors.New(st.RecordErr)
		}
		return a.certView(), nil
	case "cert/remove":
		if err := a.Cfg.Update(func(c *config.Config) { c.Cert = config.Cert{} }); err != nil {
			return nil, err
		}
		a.syncCertRecord(by, false)
		if st := a.certLoad(); st.Record != "" {
			return nil, fmt.Errorf("couldn't remove the public name from Cloudflare yet: %s", st.RecordErr)
		}
		a.le.Store(nil)
		a.applyPort443()
		a.Sec.Delete(leKeySlot)
		_ = os.Remove(a.chainPath())
		_ = os.Remove(a.certPath())
		go a.syncHomeDNS(by)
		return a.certView(), nil
	}
	return nil, errors.New("unknown certificate request")
}
