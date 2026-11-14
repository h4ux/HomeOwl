package analytics

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Owner suggestions (Family › People & groups): UniFi clients nobody owns yet, with a guess at whose they are and
// why. Nothing is linked until an admin accepts. The clues, strongest first:
//
//   - same device: the client has the name of a NextDNS device already linked to someone ("Sam's iPhone");
//   - new address: a phone or tablet with a private Wi-Fi address came back under a new MAC — same host name as
//     someone's linked device, and the two were never online together;
//   - name: the client's name or host name holds one person's first name ("Alexs-iPhone", "alex-mbp");
//   - shared: a TV, speaker, printer or smart-home thing, offered to the matching group.

type OwnerClient struct {
	MAC, Name, Hostname, Vendor string
	First, Last                 time.Time
}

type OwnerHint struct {
	MAC      string `json:"mac"`
	Device   string `json:"device"`
	Hostname string `json:"hostname,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Last     int64  `json:"last,omitempty"` // unix seconds
	PersonID string `json:"person_id"`
	Person   string `json:"person"`
	Group    bool   `json:"group,omitempty"`
	Signal   string `json:"signal"`   // same-device | new-address | name | shared
	Strength string `json:"strength"` // strong | likely
	Reason   string `json:"reason"`
}

// OwnerSuggestions lists a guess for each recent unowned UniFi client. kinds are the admin-chosen client types
// (MAC → "tv", "printer"…); skip says an admin turned that guess down.
func (e *Engine) OwnerSuggestions(kinds map[string]string, skip func(mac, personID string) bool) ([]OwnerHint, error) {
	if e.db == nil {
		return nil, nil
	}
	var cls []OwnerClient
	rs, err := e.db.Query(`SELECT mac, coalesce(name,''), coalesce(hostname,''), coalesce(vendor,''),
		coalesce(first_seen, last_seen, now()::TIMESTAMP), coalesce(last_seen, now()::TIMESTAMP) FROM clients WHERE last_seen >= ?`, utcnow().Add(-90*24*time.Hour))
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var c OwnerClient
		if rs.Scan(&c.MAC, &c.Name, &c.Hostname, &c.Vendor, &c.First, &c.Last) == nil {
			cls = append(cls, c)
		}
	}
	rs.Close()
	return ownerHints(e.People(), cls, kinds, skip), nil
}

var (
	nonAlnum   = regexp.MustCompile(`[^a-z0-9]+`)
	genericHST = regexp.MustCompile(`^(iphone|ipad|ipod|android|galaxy|pixel|macbook|macbookair|macbookpro|imac|macmini|watch|applewatch|localhost|unknown|espressif|esp|amazon|kindle|echo)[a-z0-9]{0,8}$`)
	deviceWord = regexp.MustCompile(`^(iphone|ipad|ipod|phone|mobile|macbook|mbp|mba|mac|imac|laptop|pc|desktop|tablet|pixel|galaxy|watch|kindle|switch|xbox|ps\d?)`)
)

// squash folds a device name for comparing: "Sam's iPhone", "Sams-iPhone" and "sams_iphone" are the same.
func squash(s string) string {
	s = strings.ToLower(strings.NewReplacer("'", "", "’", "", "`", "").Replace(s))
	return nonAlnum.ReplaceAllString(s, "")
}

func tokens(s string) []string {
	s = strings.ToLower(strings.NewReplacer("'", "", "’", "", "`", "").Replace(s))
	return strings.FieldsFunc(nonAlnum.ReplaceAllString(s, " "), func(r rune) bool { return r == ' ' })
}

// firstName is the part of a person's name to look for in device names ("" when it's too short to be sure).
func firstName(n string) string {
	t := tokens(n)
	if len(t) == 0 || len(t[0]) < 3 {
		return ""
	}
	return t[0]
}

// nameIn says whether a device name holds the first name f: as a word ("alex"), possessive ("alexs"), or stuck to a
// device word ("alexiphone", "alexsmbp"). strong when it's clearly a possessive or a prefix ("Alex's", "alex-mbp").
func nameIn(name, f string) (hit, strong bool) {
	t := tokens(name)
	for i, w := range t {
		switch {
		case w == f+"s":
			return true, true
		case w == f:
			return true, i == 0 && len(t) > 1
		case strings.HasPrefix(w, f) && deviceWord.MatchString(strings.TrimPrefix(strings.TrimPrefix(w, f), "s")):
			return true, true
		}
	}
	return false, false
}

// privateMAC: the "locally administered" bit — phones' private Wi-Fi addresses.
func privateMAC(mac string) bool {
	return len(mac) >= 2 && strings.ContainsRune("26ae", rune(strings.ToLower(mac)[1]))
}

var sharedKinds = []struct {
	kinds []string
	group *regexp.Regexp
	words *regexp.Regexp
}{
	{[]string{"tv", "speaker", "console"}, regexp.MustCompile(`tv|stream|media|living|lounge|speaker|music`),
		regexp.MustCompile(`\b(tv|appletv|roku|chromecast|firetv|shield|sonos|homepod|bravia|webos|tizen|playstation|ps[345]|xbox|nintendo|switch)\b`)},
	{[]string{"printer"}, regexp.MustCompile(`print`), regexp.MustCompile(`\b(printer|print|epson|brother|canon|hp[a-z0-9]*|laserjet|officejet|deskjet)\b`)},
	{[]string{"nas", "server"}, regexp.MustCompile(`server|nas|lab|storage|backup`), regexp.MustCompile(`\b(nas|synology|diskstation|qnap|truenas|unraid|server|proxmox|pihole)\b`)},
	{[]string{"iot", "plug", "bulb", "thermostat", "vacuum", "camera", "doorbell"}, regexp.MustCompile(`smart|iot|home|light|sensor`),
		regexp.MustCompile(`\b(hue|tuya|shelly|tasmota|esp[0-9a-z]*|espressif|tplink|kasa|tapo|meross|wiz|lifx|nest|ecobee|roomba|roborock|ring|wyze|eufy|smartthings|homey|ikea|tradfri|sonoff|myq)\b`)},
}

func ownerHints(people []Person, cls []OwnerClient, kinds map[string]string, skip func(mac, pid string) bool) []OwnerHint {
	linked := map[string]string{} // MAC → person
	pname := map[string]Person{}
	devNames := map[string]string{} // squashed NextDNS device name → person ("" when two people have one)
	for _, p := range people {
		pname[p.ID] = p
		for _, d := range p.Devices {
			switch d.Kind {
			case "mac":
				linked[strings.ToLower(d.Key)] = p.ID
			case "ndev", "dns":
				n := d.Key
				if d.Kind == "ndev" {
					_, n, _ = strings.Cut(d.Key, "/")
				}
				if k := squash(n); len(k) >= 4 {
					if o, seen := devNames[k]; seen && o != p.ID {
						devNames[k] = ""
					} else {
						devNames[k] = p.ID
					}
				}
			}
		}
	}
	var mine []OwnerClient // linked clients, for following a device to its new address
	for _, c := range cls {
		if linked[strings.ToLower(c.MAC)] != "" {
			mine = append(mine, c)
		}
	}
	var out []OwnerHint
	for _, c := range cls {
		mac := strings.ToLower(c.MAC)
		if linked[mac] != "" {
			continue
		}
		label := c.Name
		if label == "" {
			label = c.Hostname
		}
		if label == "" {
			label = c.MAC
		}
		var cands []OwnerHint
		hint := func(pid, signal, strength, reason string) {
			if p, ok := pname[pid]; ok && (skip == nil || !skip(mac, pid)) {
				cands = append(cands, OwnerHint{PersonID: pid, Person: p.Name, Group: p.Kind == "group", Signal: signal, Strength: strength, Reason: reason})
			}
		}
		// the same device as a NextDNS device someone owns
		for _, n := range []string{c.Name, c.Hostname} {
			if pid := devNames[squash(n)]; n != "" && pid != "" {
				hint(pid, "same-device", "strong", "Same name as "+pname[pid].Name+"'s NextDNS device “"+n+"”")
				break
			}
		}
		// a device someone owns, back with a new private address
		if c.Hostname != "" && privateMAC(mac) {
			owners := map[string]OwnerClient{}
			for _, m := range mine {
				if squash(m.Hostname) == squash(c.Hostname) && !m.Last.After(c.First.Add(time.Hour)) {
					owners[linked[strings.ToLower(m.MAC)]] = m
				}
			}
			if len(owners) == 1 {
				for pid, m := range owners {
					st := "strong"
					if genericHST.MatchString(squash(c.Hostname)) {
						st = "likely"
					}
					hint(pid, "new-address", st, "Looks like "+pname[pid].Name+"'s “"+orStr(m.Name, m.Hostname)+"” with a new private Wi-Fi address (same host name “"+c.Hostname+"”, never online at the same time)")
				}
			}
		}
		// a person's name in the device's name
		var who []string
		strong := false
		for _, p := range people {
			f := firstName(p.Name)
			if p.Kind == "group" || f == "" {
				continue
			}
			for _, n := range []string{c.Name, c.Hostname} {
				if hit, st := nameIn(n, f); hit {
					who = append(who, p.ID)
					strong = strong || st
					break
				}
			}
		}
		if len(who) == 1 {
			st := "likely"
			if strong {
				st = "strong"
			}
			hint(who[0], "name", st, "“"+label+"” has "+pname[who[0]].Name+"'s name in it")
		}
		// shared things → a matching group
		k, text := kinds[mac], strings.ToLower(c.Name+" "+c.Hostname+" "+c.Vendor)
		for _, s := range sharedKinds {
			if !(k != "" && inStrs(s.kinds, k) || k == "" && s.words.MatchString(text)) {
				continue
			}
			for _, p := range people {
				if p.Kind == "group" && s.group.MatchString(strings.ToLower(p.Name)) {
					hint(p.ID, "shared", "likely", "“"+label+"” looks like a shared "+s.kinds[0]+" thing, not one person's")
					break
				}
			}
			break
		}
		if len(cands) == 0 {
			continue
		}
		best := cands[0]
		for _, x := range cands[1:] { // a strong clue beats a likely one; same strength: the order above
			if x.Strength == "strong" && best.Strength != "strong" {
				best = x
			}
		}
		best.MAC, best.Device, best.Hostname, best.Vendor, best.Last = mac, label, c.Hostname, c.Vendor, c.Last.Unix()
		if best.Hostname == best.Device {
			best.Hostname = ""
		}
		out = append(out, best)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Strength == "strong") != (out[j].Strength == "strong") {
			return out[i].Strength == "strong"
		}
		return out[i].Last > out[j].Last
	})
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func inStrs(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
