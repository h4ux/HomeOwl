package server

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/smallstep/pkcs7"
)

// Device setup after check-in: once an admin approves a device, its "Hi HomeOwl" page offers to set it up so it
// keeps being recognised everywhere — not just by its (often random) Wi-Fi address at home:
//
//   - Apple (iPhone, iPad, Mac): a configuration profile with encrypted DNS to the person's NextDNS profile, under
//     the device's name. NextDNS then logs it by that name at home, on mobile data and on other Wi-Fi.
//   - Android: the same as a Private DNS host name; Windows: the DNS-over-HTTPS template.
//
// The profile carries no keys or passwords, and only the device itself (found by its address on the home
// network, like the check-in page) can download it.

var ndProfileRE = regexp.MustCompile(`^[a-z0-9]{4,16}$`)

// ndProfileChoices lists the NextDNS profiles the admin can give a device, and each person's usual one.
func (a *App) ndProfileChoices() map[string]any {
	profiles := a.ndProfilesCached()
	byPerson := map[string]string{}
	if a.An.Open() {
		for _, p := range a.An.People() {
			if id := personNDProfile(p); id != "" {
				byPerson[p.ID] = id
			}
		}
	}
	return map[string]any{"profiles": profiles, "by_person": byPerson}
}

var ndProfileCache struct {
	sync.Mutex
	at   time.Time
	list []analytics.NDProfile
}

func (a *App) ndProfilesCached() []analytics.NDProfile {
	ndProfileCache.Lock()
	defer ndProfileCache.Unlock()
	if ndProfileCache.list != nil && time.Since(ndProfileCache.at) < 10*time.Minute {
		return ndProfileCache.list
	}
	out := []analytics.NDProfile{}
	if a.An.Open() {
		got, _ := a.An.NDAccountProfiles()
		seen := map[string]bool{}
		for _, l := range got {
			for _, p := range l {
				if !seen[p.ID] && ndProfileRE.MatchString(p.ID) {
					seen[p.ID] = true
					out = append(out, analytics.NDProfile{ID: p.ID, Name: p.Name})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	ndProfileCache.list, ndProfileCache.at = out, time.Now()
	return out
}

// personNDProfile: the NextDNS profile a person's devices use — their whole profile if they have one, else the one
// most of their NextDNS devices are in.
func personNDProfile(p analytics.Person) string {
	count := map[string]int{}
	best := ""
	for _, d := range p.Devices {
		switch d.Kind {
		case "profile":
			if ndProfileRE.MatchString(d.Key) {
				return d.Key
			}
		case "ndev":
			if id, _, ok := strings.Cut(d.Key, "/"); ok && ndProfileRE.MatchString(id) {
				count[id]++
				if count[id] > count[best] || (count[id] == count[best] && id < best) {
					best = id
				}
			}
		}
	}
	return best
}

// ndDeviceLabel turns a device name into what NextDNS accepts inside a host name ("Alex's iPad" → "Alexs--iPad").
func ndDeviceLabel(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r == ' ':
			b.WriteString("--")
		case r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		}
	}
	l := strings.Trim(b.String(), "-")
	if len(l) > 40 {
		l = strings.Trim(l[:40], "-")
	}
	return l
}

// deviceSetup is what the device's own check-in page shows once it is approved.
func (a *App) deviceSetup(x CheckIn) map[string]any {
	res := map[string]any{"name": x.Name, "kind": x.Kind, "person": x.PersonName, "profile_at": x.ProfileAt}
	if x.PersonID != "" && a.An.Open() {
		for _, p := range a.An.People() {
			if p.ID == x.PersonID {
				res["person"] = p.Name
			}
		}
	}
	if x.NDProfile == "" {
		return res
	}
	label := ndDeviceLabel(x.Name)
	res["nextdns"] = map[string]any{
		"doh":     "https://dns.nextdns.io/" + x.NDProfile + "/" + url.PathEscape(x.Name),
		"android": strings.TrimPrefix(label+"-", "-") + x.NDProfile + ".dns.nextdns.io",
		"servers": []string{"45.90.28.0", "45.90.30.0", "2a07:a8c0::", "2a07:a8c1::"},
	}
	return res
}

// hiProfile sends the device its Apple configuration profile.
func (a *App) hiProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "use GET")
		return
	}
	_, mac, ok := a.hiDevice(w, r)
	if !ok {
		return
	}
	x, ok := a.checkins.Registered(mac)
	if !ok {
		fail(w, 404, "This device isn't approved yet. Check in first, then come back once an admin has approved it.")
		return
	}
	if x.NDProfile == "" {
		fail(w, 404, "An admin hasn't chosen a NextDNS profile for this device yet.")
		return
	}
	a.checkins.Touch(x.ID)
	body := mobileConfig(x)
	if sc := a.signer(); sc != nil { // signed with the Let's Encrypt certificate: iOS shows it as "Verified"
		if signed, err := signProfile(body, sc); err == nil {
			body = signed
		} else {
			log.Printf("device profile: couldn't sign it (%v); sending it unsigned", err)
		}
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="HomeOwl.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// signProfile wraps the profile in a CMS (PKCS #7) signature with the certificate and its chain.
func signProfile(body []byte, c *tls.Certificate) ([]byte, error) {
	sd, err := pkcs7.NewSignedData(body)
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	key, ok := c.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("the certificate's key can't sign")
	}
	if err := sd.AddSigner(c.Leaf, key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	for _, der := range c.Certificate[1:] {
		ic, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		sd.AddCertificate(ic)
	}
	return sd.Finish()
}

// mobileConfig builds the profile. Its identifiers come from the device's MAC address, so installing a newer one
// replaces the old one instead of adding a second.
func mobileConfig(x CheckIn) []byte {
	base := "io.github.h4ux.homeowl.device." + strings.ReplaceAll(x.MAC, ":", "")
	doh := "https://dns.nextdns.io/" + x.NDProfile + "/" + url.PathEscape(x.Name)
	var b bytes.Buffer
	str := func(k, v string) {
		b.WriteString("<key>" + k + "</key><string>")
		_ = xml.EscapeText(&b, []byte(v))
		b.WriteString("</string>\n")
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>PayloadContent</key>
<array>
<dict>
<key>DNSSettings</key>
<dict>
`)
	str("DNSProtocol", "HTTPS")
	str("ServerURL", doh)
	b.WriteString("</dict>\n")
	str("PayloadDescription", "Encrypted DNS through NextDNS, so this device is recognised by name on any network.")
	str("PayloadDisplayName", "NextDNS · "+x.Name)
	str("PayloadIdentifier", base+".dns")
	str("PayloadType", "com.apple.dnsSettings.managed")
	str("PayloadUUID", stableUUID(base+".dns"))
	b.WriteString("<key>PayloadVersion</key><integer>1</integer>\n</dict>\n</array>\n")
	str("PayloadDescription", "Set up by HomeOwl for "+x.Name+". It contains no passwords and can be removed at any time in Settings.")
	str("PayloadDisplayName", "HomeOwl · "+x.Name)
	str("PayloadIdentifier", base)
	str("PayloadOrganization", "HomeOwl")
	b.WriteString("<key>PayloadRemovalDisallowed</key><false/>\n")
	str("PayloadType", "Configuration")
	str("PayloadUUID", stableUUID(base))
	b.WriteString("<key>PayloadVersion</key><integer>1</integer>\n</dict>\n</plist>\n")
	return b.Bytes()
}

func stableUUID(s string) string {
	h := sha256.Sum256([]byte(s))
	h[6] = h[6]&0x0f | 0x50 // name-based
	h[8] = h[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16]))
}
