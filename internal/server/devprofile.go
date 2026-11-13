package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/analytics"
	"github.com/smallstep/pkcs7"

	"github.com/h4ux/homeowl/web"
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
	res := map[string]any{"name": x.Name, "kind": x.Kind, "person": x.PersonName, "profile_at": x.ProfileAt,
		"update": x.ProfileAt > 0 && a.setupUpdate(x), "includes": a.includes(x)} // settings changed since the last install
	if x.PersonID != "" && a.An.Open() {
		for _, p := range a.An.People() {
			if p.ID == x.PersonID {
				res["person"] = p.Name
			}
		}
	}
	if x.NDProfile == "" {
		if inc := res["includes"].(map[string]any); len(inc) > 0 {
			res["apple_only"] = true // Wi-Fi and VPN only: there's something to install on Apple devices
		}
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
	ex, _ := a.extrasFor(x)
	if x.NDProfile == "" && len(ex.WiFi) == 0 && ex.VPN == nil {
		fail(w, 404, "There's nothing to set up on this device yet: an admin chooses its NextDNS profile or a profile type.")
		return
	}
	if x.ProfileND != "" && x.ProfileND != x.NDProfile && a.An.Open() {
		_ = a.An.UnassignDevice("ndev", x.ProfileND+"/"+x.Name)
	}
	a.checkins.Touch(x.ID, x.NDProfile, a.setupHash(x))
	ex.Mac = r.URL.Query().Get("p") == "mac"
	if r.URL.Query().Get("p") == "ios" && ex.Icon != "none" { // iPhone and iPad: also a Home Screen icon
		icon, _ := fs.ReadFile(web.Static, "static/icon-192.png")
		if ex.Icon == "app" { // HomeOwl itself, for an admin's or a viewer's own phone
			u, trusted := a.appURL(r)
			ex.Clip = &webClip{URL: u, Icon: icon, App: true, FullScreen: trusted}
		} else { // the device's check-in page, for updates later
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			ex.Clip = &webClip{URL: scheme + "://" + r.Host + "/hi", Icon: icon}
		}
	}
	body := mobileConfig(x, ex)
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

// webClip is a Home Screen icon (iPhone and iPad only; macOS doesn't take them in a profile).
type webClip struct {
	URL        string
	Icon       []byte // PNG
	App        bool   // opens HomeOwl itself rather than the check-in page
	FullScreen bool   // like an app, without Safari's bars (only with a trusted certificate: no warning to click through)
}

// appURL is where a Home Screen icon opens HomeOwl: its trusted name when there's a certificate (trusted is true),
// else the address the device reached HomeOwl by, on HomeOwl's own port. The check-in name (hi.local) only serves
// the check-in page, so then it's the computer's IP address.
func (a *App) appURL(r *http.Request) (u string, trusted bool) {
	if c := a.certURL(); c != "" {
		return c + "/", true
	}
	c := a.Cfg.Get().LAN
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if a.nameHost(r) != "" {
		if ip := homeIP(); ip != "" {
			host = ip
		}
	}
	scheme := "http"
	if c.TLS && a.cert.Certificate != nil {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, fmt.Sprint(c.Port)) + "/", false
}

// mobileConfig builds the profile. Its identifiers come from the device's MAC address, so installing a newer one
// replaces the old one instead of adding a second.
func mobileConfig(x CheckIn, ex profileExtras) []byte {
	clip := ex.Clip
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
`)
	p := plistW{&b}
	for _, w := range ex.WiFi {
		wifiPayload(p, base, w)
	}
	if ex.VPN != nil {
		vpnPayloadXML(p, base, ex.VPN, ex.Mac)
	}
	if x.NDProfile != "" {
		b.WriteString("<dict>\n<key>DNSSettings</key>\n<dict>\n")
		str("DNSProtocol", "HTTPS")
		str("ServerURL", doh)
		b.WriteString("</dict>\n")
		str("PayloadDescription", "Encrypted DNS through NextDNS, so this device is recognised by name on any network.")
		str("PayloadDisplayName", "NextDNS · "+x.Name)
		str("PayloadIdentifier", base+".dns")
		str("PayloadType", "com.apple.dnsSettings.managed")
		str("PayloadUUID", stableUUID(base+".dns"))
		b.WriteString("<key>PayloadVersion</key><integer>1</integer>\n</dict>\n")
	}
	if clip != nil {
		b.WriteString("<dict>\n<key>FullScreen</key><" + fmt.Sprint(clip.FullScreen) + "/>\n")
		if len(clip.Icon) > 0 {
			b.WriteString("<key>Icon</key><data>" + base64.StdEncoding.EncodeToString(clip.Icon) + "</data>\n")
		}
		b.WriteString("<key>IgnoreManifestScope</key><true/>\n<key>IsRemovable</key><true/>\n")
		str("Label", "HomeOwl")
		if clip.App {
			str("PayloadDescription", "A Home Screen icon that opens HomeOwl (at home, or over Tailscale).")
		} else {
			str("PayloadDescription", "A Home Screen icon that opens this device's HomeOwl page (at home).")
		}
		str("PayloadDisplayName", "HomeOwl icon")
		str("PayloadIdentifier", base+".webclip")
		str("PayloadType", "com.apple.webClip.managed")
		str("PayloadUUID", stableUUID(base+".webclip"))
		b.WriteString("<key>PayloadVersion</key><integer>1</integer>\n<key>Precomposed</key><true/>\n")
		str("URL", clip.URL)
		b.WriteString("</dict>\n")
	}
	b.WriteString("</array>\n")
	desc := "Set up by HomeOwl for " + x.Name + ". It contains no passwords and can be removed at any time in Settings."
	if len(ex.WiFi) > 0 || ex.VPN != nil {
		desc = "Set up by HomeOwl for " + x.Name + ": " + strings.Join(profileParts(x, ex), ", ") + ". It can be removed at any time in Settings."
	}
	str("PayloadDescription", desc)
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

// setupStatus: is the device's setup in use? The setup names the device in NextDNS, so its queries in the
// imported NextDNS logs show it working — and from where.
func (a *App) setupStatus(x CheckIn) map[string]any {
	st := map[string]any{"downloaded": x.ProfileAt, "update": x.ProfileAt > 0 && a.setupUpdate(x)}
	if t, ok := a.typeFor(x); ok {
		st["type"] = t.Name
		if t.VPN != nil && t.VPN.Type == "wireguard" {
			st["wg"] = a.Sec.Get(deviceWGSlot(x.ID)) != "" // whether it's there, never the config
		}
	}
	if x.NDProfile == "" {
		st["state"] = "no_nextdns" // Wi-Fi and VPN only: nothing NextDNS could show
		return st
	}
	if !a.An.Open() || len(a.An.NDSlots()) == 0 {
		st["state"] = "unknown" // no NextDNS logs to look in
		return st
	}
	last, _, ip, day, err := a.setupSeen(x)
	if err != nil {
		st["state"] = "unknown"
		return st
	}
	switch {
	case x.SetupLost > 0:
		st["state"] = "stopped" // it worked, then stopped (setupwatch.go)
	case last.IsZero() && x.ProfileAt > 0:
		st["state"] = "not_seen" // downloaded, but no query under its name: probably not installed (or not yet imported)
	case last.IsZero():
		st["state"] = "offered"
	case time.Since(last) < 24*time.Hour:
		st["state"] = "active"
	default:
		st["state"] = "quiet"
	}
	if !last.IsZero() {
		st["last_seen"] = last.Unix()
		st["day"] = day
		st["where"] = a.whereFrom(ip)
	}
	return st
}

// whereFrom: "home" when the address is one of this home's public addresses, "away" otherwise, "" when unknown.
func (a *App) whereFrom(ip string) string {
	pip := net.ParseIP(ip)
	home := a.homePublic()
	if pip == nil || len(home) == 0 {
		return ""
	}
	for _, h := range home {
		if _, n, err := net.ParseCIDR(h); err == nil && n.Contains(pip) {
			return "home"
		}
		if hip := net.ParseIP(h); hip != nil && hip.Equal(pip) {
			return "home"
		}
	}
	return "away"
}

// homePublic is this home's public addresses, cached (finding them takes a few seconds, so a stale list is
// refreshed in the background).
func (a *App) homePublic() []string {
	homePubCache.Lock()
	defer homePubCache.Unlock()
	if time.Since(homePubCache.at) > 30*time.Minute && !homePubCache.busy {
		homePubCache.busy = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var l []string
			if cfHome != nil {
				l = cfHome(ctx)
			} else {
				l = a.homeAddresses(ctx)
			}
			homePubCache.Lock()
			homePubCache.list, homePubCache.at, homePubCache.busy = l, time.Now(), false
			homePubCache.Unlock()
		}()
	}
	return homePubCache.list
}

var homePubCache struct {
	sync.Mutex
	list []string
	at   time.Time
	busy bool
}

// changeSetup gives an approved device another NextDNS profile (or none). Its check-in page then offers the
// new setup ("update ready"), and its NextDNS name moves to the new profile in People.
func (a *App) changeSetup(body map[string]any) (any, error) {
	id, nd := s(body, "id"), s(body, "nd_profile")
	if has(body, "type_id") {
		if t := s(body, "type_id"); t != "" && t != typeNone {
			if _, ok := a.devTypes.Get(t); !ok {
				return nil, errors.New("that profile type is gone")
			}
		}
	}
	if wg := s(body, "wg"); wg != "" && wg != "-" {
		if err := checkWG(wg); err != nil {
			return nil, err
		}
	}
	if nd != "" && !ndProfileRE.MatchString(nd) {
		return nil, errors.New("that isn't a NextDNS profile ID")
	}
	x, ok := a.checkins.Get(id)
	if !ok || x.Status != "approved" {
		return nil, errors.New("that device isn't approved")
	}
	pid := x.PersonID
	if pid == "" && a.An.Open() { // older check-ins: whoever has the device now
		for _, p := range a.An.People() {
			for _, d := range p.Devices {
				if d.Kind == "mac" && d.Key == x.MAC {
					pid = p.ID
				}
			}
		}
	}
	// what the device has installed now: it keeps using that (and its NextDNS name stays with the person) until
	// it installs the new setup
	inst := x.ProfileND
	if inst == "" && x.SetupSeen > 0 { // Android and Windows don't download anything; seen working = installed
		inst = x.NDProfile
	}
	if x.NDProfile != "" && x.NDProfile != inst && a.An.Open() { // a choice that was never installed
		_ = a.An.UnassignDevice("ndev", x.NDProfile+"/"+x.Name)
	}
	if nd != "" && pid != "" && a.An.Open() {
		if err := a.An.AssignDevice(pid, "ndev", nd+"/"+x.Name); err != nil {
			return nil, err
		}
	}
	switch wg := s(body, "wg"); wg {
	case "":
	case "-":
		a.Sec.Delete(deviceWGSlot(id))
	default:
		if err := a.Sec.Set(deviceWGSlot(id), strings.TrimSpace(wg)+"\n"); err != nil {
			return nil, err
		}
	}
	a.checkins.Edit(id, func(c *CheckIn) {
		if has(body, "type_id") {
			c.TypeID = s(body, "type_id")
		}
		c.NDProfile, c.ProfileND, c.SetupChanged = nd, inst, time.Now().Unix()
		if nd == "" {
			c.SetupLost = 0
		}
	})
	return a.checkinView(), nil
}

// profileParts names what a profile holds, for its description.
func profileParts(x CheckIn, ex profileExtras) []string {
	var parts []string
	if x.NDProfile != "" {
		parts = append(parts, "encrypted DNS through NextDNS")
	}
	for _, w := range ex.WiFi {
		parts = append(parts, "Wi-Fi "+w.SSID)
	}
	if ex.VPN != nil {
		parts = append(parts, "the "+orDefault(ex.VPN.Conf.Name, "Home VPN")+" VPN")
	}
	return parts
}
