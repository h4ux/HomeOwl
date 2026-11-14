package analytics

import (
	"sort"
	"strings"
	"time"
)

// "Comes and goes together" (owners.go): a watch, tablet or second phone joins and leaves the home Wi-Fi at the same
// moments as its owner's phone. From UniFi's Wi-Fi sessions of the last 30 days, each device's time at home is
// turned into stays (sessions less than 20 minutes apart are one stay, as phones drop off Wi-Fi while asleep). An
// unowned device's arrivals and departures are then matched against each person's linked devices: within 10
// minutes, the same direction. A guess needs at least 8 arrivals and departures, 75% of them matched, and a clear
// lead over anyone else (people who always travel together can't be told apart).

const (
	stayGap      = 20 * time.Minute
	matchWindow  = 10 * time.Minute
	togetherDays = 30
	minEvents    = 8
	minPercent   = 75
	minLead      = 20 // percentage points over the next person
)

type Together struct {
	PersonID, Anchor               string
	Percent, Matched, Events, Days int
}

type span struct{ a, b time.Time }

type comeGo struct {
	t      time.Time
	arrive bool
}

// stays merges sessions into time at home.
func stays(ss []span) []span {
	sort.Slice(ss, func(i, j int) bool { return ss[i].a.Before(ss[j].a) })
	var out []span
	for _, s := range ss {
		if n := len(out); n > 0 && !s.a.After(out[n-1].b.Add(stayGap)) {
			if s.b.After(out[n-1].b) {
				out[n-1].b = s.b
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// comings lists arrivals and departures, leaving out the ones the data window cuts (a stay already going when the
// window starts, or still going now).
func comings(st []span, from, now time.Time) []comeGo {
	var out []comeGo
	for _, s := range st {
		if s.a.After(from.Add(time.Hour)) {
			out = append(out, comeGo{s.a, true})
		}
		if s.b.Before(now.Add(-30 * time.Minute)) {
			out = append(out, comeGo{s.b, false})
		}
	}
	return out
}

func matched(xs, anchor []comeGo) int {
	n := 0
	for _, x := range xs {
		i := sort.Search(len(anchor), func(i int) bool { return !anchor[i].t.Before(x.t.Add(-matchWindow)) })
		for ; i < len(anchor) && !anchor[i].t.After(x.t.Add(matchWindow)); i++ {
			if anchor[i].arrive == x.arrive {
				n++
				break
			}
		}
	}
	return n
}

// togetherHints matches each candidate device against each person's devices. sess: MAC → Wi-Fi sessions.
func togetherHints(people []Person, cands []string, sess map[string][]span, names map[string]string, from, now time.Time) map[string]Together {
	type anchor struct {
		pid, name string
		ev        []comeGo
	}
	var anchors []anchor
	for _, p := range people {
		if p.Kind == "group" {
			continue
		}
		var all []span
		label := ""
		for _, d := range p.Devices {
			if d.Kind != "mac" {
				continue
			}
			mac := strings.ToLower(d.Key)
			if len(sess[mac]) > 0 && label == "" {
				label = names[mac]
			}
			all = append(all, sess[mac]...)
		}
		if len(all) == 0 {
			continue
		}
		ev := comings(stays(all), from, now) // a person is home while any of their devices is
		sort.Slice(ev, func(i, j int) bool { return ev[i].t.Before(ev[j].t) })
		anchors = append(anchors, anchor{p.ID, label, ev})
	}
	out := map[string]Together{}
	for _, mac := range cands {
		ev := comings(stays(sess[mac]), from, now)
		if len(ev) < minEvents {
			continue
		}
		best, second := Together{}, 0
		for _, a := range anchors {
			m := matched(ev, a.ev)
			pct := 100 * m / len(ev)
			if pct > best.Percent {
				second = best.Percent
				best = Together{PersonID: a.pid, Anchor: a.name, Percent: pct, Matched: m, Events: len(ev), Days: int(now.Sub(from).Hours() / 24)}
			} else if pct > second {
				second = pct
			}
		}
		if best.PersonID != "" && best.Percent >= minPercent && best.Percent-second >= minLead {
			out[mac] = best
		}
	}
	return out
}

// together reads the Wi-Fi sessions and runs togetherHints for the unowned wireless clients.
func (e *Engine) together(people []Person, cls []OwnerClient) map[string]Together {
	now := utcnow()
	from := now.Add(-togetherDays * 24 * time.Hour)
	rs, err := e.db.Query(`SELECT mac, start_ts, coalesce(stop_ts, start_ts + to_seconds(coalesce(duration,0))) FROM sessions
		WHERE NOT coalesce(wired, false) AND start_ts >= ? AND start_ts IS NOT NULL`, from)
	if err != nil {
		return nil
	}
	sess := map[string][]span{}
	for rs.Next() {
		var mac string
		var s span
		if rs.Scan(&mac, &s.a, &s.b) == nil {
			sess[strings.ToLower(mac)] = append(sess[strings.ToLower(mac)], s)
		}
	}
	rs.Close()
	linked := map[string]bool{}
	for _, p := range people {
		for _, d := range p.Devices {
			if d.Kind == "mac" {
				linked[strings.ToLower(d.Key)] = true
			}
		}
	}
	names := map[string]string{}
	var cands []string
	for _, c := range cls {
		mac := strings.ToLower(c.MAC)
		names[mac] = orStr(c.Name, c.Hostname)
		if !linked[mac] && len(sess[mac]) > 0 {
			cands = append(cands, mac)
		}
	}
	return togetherHints(people, cands, sess, names, from, now)
}
