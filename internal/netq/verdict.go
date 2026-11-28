package netq

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Plan is the speed the household pays for (0 = not given).
type Plan struct {
	Down float64 `json:"down"`
	Up   float64 `json:"up"`
}

// Stats sums up the readings and outages of a stretch of time.
type Stats struct {
	Readings  int      `json:"readings"`
	Uptime    *float64 `json:"uptime"`     // % of the time the internet answered
	NetMedian *float64 `json:"net_median"` // ms to the internet
	NetP95    *float64 `json:"net_p95"`
	GwMedian  *float64 `json:"gw_median"` // ms to the gateway
	Jitter    *float64 `json:"jitter"`    // ms: how much the internet's time changes from one reading to the next
	Missed    float64  `json:"missed"`    // % of readings without an answer outside outages
	MissedGw  int      `json:"missed_gw"` // of those, how many had no gateway either
	IspMin    float64  `json:"isp_min"`   // outage minutes down to the provider
	HomeMin   float64  `json:"home_min"`  // … down to something at home
	IspCount  int      `json:"isp_count"`
	HomeCount int      `json:"home_count"`
}

// Summarize the readings and outages from `from` (ms) to now.
func Summarize(samples []Sample, outages []Outage, from int64, now time.Time) Stats {
	st := Stats{}
	nowMs := now.UnixMilli()
	inOutage := func(t int64) bool {
		for _, o := range outages {
			if t >= o.Start && (o.End == 0 || t < o.End) {
				return true
			}
		}
		return false
	}
	var net, gw []float64
	var prev *float64
	var jit []float64
	missed, missedGw := 0, 0
	for _, s := range samples {
		if s.T < from {
			continue
		}
		st.Readings++
		if s.Net != nil {
			net = append(net, *s.Net)
			if prev != nil {
				jit = append(jit, math.Abs(*s.Net-*prev))
			}
			v := *s.Net
			prev = &v
		} else {
			prev = nil
			if !inOutage(s.T) {
				missed++
				if s.GwDown() {
					missedGw++
				}
			}
		}
		if s.Gw != nil {
			gw = append(gw, *s.Gw)
		}
	}
	for _, o := range outages {
		end := o.End
		if end == 0 {
			end = nowMs
		}
		if end < from {
			continue
		}
		start := max(o.Start, from)
		m := float64(end-start) / 60000
		if o.Cause == "home" {
			st.HomeMin += m
			st.HomeCount++
		} else {
			st.IspMin += m
			st.IspCount++
		}
	}
	if st.Readings > 0 {
		covered := math.Max(float64(st.Readings), st.IspMin+st.HomeMin)
		up := 100 * (1 - (st.IspMin+st.HomeMin)/covered)
		st.Uptime = &up
		st.Missed = 100 * float64(missed) / float64(st.Readings)
		st.MissedGw = missedGw
	}
	st.NetMedian, st.NetP95, st.GwMedian = pct(net, 50), pct(net, 95), pct(gw, 50)
	if len(jit) >= 5 {
		j := mean(jit)
		st.Jitter = &j
	}
	return st
}

func pct(v []float64, p float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Round(p / 100 * float64(len(s)-1)))
	x := s[i]
	return &x
}

func mean(v []float64) float64 {
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t / float64(len(v))
}

// Verdict is the "ISP or us?" answer. Who: isp, home, mixed or "".
type Verdict struct {
	Level string   `json:"level"` // good, warn, bad, info
	Who   string   `json:"who"`
	Title string   `json:"title"`
	Lines []string `json:"lines"`
}

// Judge the connection: week and day are Summarize over 7 days and 24 hours, tests the recent speed tests
// (oldest first), cur the outage going on now.
func Judge(week, day Stats, tests []SpeedTest, plan Plan, cur *Outage, away bool, now time.Time) Verdict {
	if cur != nil {
		mins := fmtMin(cur.Minutes(now))
		if cur.Cause == "home" {
			return Verdict{Level: "bad", Who: "home", Title: "HomeOwl can't reach your gateway",
				Lines: []string{"Nothing answers for " + mins + ", not even the gateway, so the problem is inside the home: the gateway's power or cable, or this computer's own connection.",
					"Check that the gateway is on, then the cable or Wi-Fi of this computer."}}
		}
		return Verdict{Level: "bad", Who: "isp", Title: "The internet is down: it's your provider",
			Lines: []string{"Your gateway answers, but nothing past it has for " + mins + ".",
				"Restarting the modem or fibre box may help; otherwise check your provider's status page or call them."}}
	}
	if away {
		return Verdict{Level: "info", Title: "This computer is away from home",
			Lines: []string{"HomeOwl takes readings only while it's on the home network, so the internet at home isn't being watched right now."}}
	}
	if week.Readings == 0 {
		return Verdict{Level: "info", Title: "Collecting the first readings",
			Lines: []string{"HomeOwl checks the gateway and the internet every minute. The first verdict shows up in a few minutes."}}
	}
	type problem struct{ who, text string }
	var ps []problem
	if week.IspCount >= 3 || week.IspMin >= 30 {
		ps = append(ps, problem{"isp", fmt.Sprintf("Your provider dropped %s in the last 7 days (%s in all), while your gateway kept answering.", times(week.IspCount), fmtMin(week.IspMin))})
	}
	if week.HomeCount >= 2 || week.HomeMin >= 15 {
		ps = append(ps, problem{"home", fmt.Sprintf("%s in the last 7 days the gateway didn't answer either (%s in all): its power, the gateway restarting, or this computer's connection.", capital(times(week.HomeCount)), fmtMin(week.HomeMin))})
	}
	if day.GwMedian != nil && *day.GwMedian > 30 {
		ps = append(ps, problem{"home", fmt.Sprintf("This computer takes %s to reach the gateway (a cable takes 1–2 ms), so its own Wi-Fi is weak or busy. That's this computer, not the whole home.", fmtMs(*day.GwMedian))})
	}
	if day.NetMedian != nil {
		past := *day.NetMedian
		if day.GwMedian != nil {
			past -= *day.GwMedian
		}
		if past > 60 {
			ps = append(ps, problem{"isp", fmt.Sprintf("The internet takes %s past your gateway; under 30 ms is usual for a home connection.", fmtMs(past))})
		}
	}
	if day.Missed > 2 {
		who := "isp"
		if day.MissedGw*2 > int(math.Round(day.Missed*float64(day.Readings)/100)) {
			who = "home"
		}
		ps = append(ps, problem{who, fmt.Sprintf("%.0f%% of the readings in the last 24 hours got no answer from the internet%s.", day.Missed, map[string]string{"home": ", and mostly not from the gateway either", "isp": ""}[who])})
	}
	if day.Jitter != nil && *day.Jitter > 30 {
		ps = append(ps, problem{"isp", fmt.Sprintf("The internet's response time jumps around by %s from minute to minute; video calls and games may stutter.", fmtMs(*day.Jitter))})
	}
	if v, n := recentDown(tests, now); plan.Down > 0 && n > 0 && v < plan.Down/2 {
		ps = append(ps, problem{"isp", fmt.Sprintf("Speed tests average %.0f Mbps down, under half the %.0f Mbps you pay for. The gateway runs them itself, so the home Wi-Fi isn't involved.", v, plan.Down)})
	}
	if len(ps) == 0 {
		lines := []string{}
		if week.Uptime != nil {
			lines = append(lines, fmt.Sprintf("Up %s of the last 7 days.", fmtPct(*week.Uptime)))
		}
		if day.NetMedian != nil {
			l := fmt.Sprintf("%s to the internet", fmtMs(*day.NetMedian))
			if day.GwMedian != nil {
				l += fmt.Sprintf(", %s to the gateway", fmtMs(*day.GwMedian))
			}
			lines = append(lines, l+" (typical over the last 24 hours).")
		}
		if n := len(tests); n > 0 {
			t := tests[n-1]
			lines = append(lines, fmt.Sprintf("Last speed test: %.0f Mbps down, %.0f up.", t.Down, t.Up))
		}
		return Verdict{Level: "good", Title: "Your internet looks healthy", Lines: lines}
	}
	isp, home := 0, 0
	lines := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.who == "home" {
			home++
		} else {
			isp++
		}
		lines = append(lines, p.text)
	}
	v := Verdict{Level: "warn", Lines: lines}
	switch {
	case home == 0:
		v.Who, v.Title = "isp", "Mostly your provider"
	case isp == 0:
		v.Who, v.Title = "home", "Mostly at home"
	default:
		v.Who, v.Title = "mixed", "Some of each: your provider and the home"
	}
	return v
}

// recentDown: the average download of the last five speed tests in the last 30 days.
func recentDown(tests []SpeedTest, now time.Time) (float64, int) {
	from := now.AddDate(0, 0, -30).UnixMilli()
	var v []float64
	for i := len(tests) - 1; i >= 0 && len(v) < 5; i-- {
		if tests[i].T >= from && tests[i].Down > 0 {
			v = append(v, tests[i].Down)
		}
	}
	if len(v) == 0 {
		return 0, 0
	}
	return mean(v), len(v)
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	if n == 2 {
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

func capital(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

func fmtMin(m float64) string {
	switch {
	case m < 1:
		return "under a minute"
	case m < 90:
		return fmt.Sprintf("%.0f min", m)
	case m < 48*60:
		return fmt.Sprintf("%.1f hours", m/60)
	}
	return fmt.Sprintf("%.0f days", m/1440)
}

func fmtMs(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func fmtPct(v float64) string {
	if v >= 99.995 {
		return "100%"
	}
	if v >= 99 {
		return fmt.Sprintf("%.2f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}

// Bucket is the readings of a stretch (for charts): average times and the share without an answer.
type Bucket struct {
	T      int64    `json:"t"`
	Net    *float64 `json:"n"`
	Gw     *float64 `json:"g"`
	Missed float64  `json:"m"` // %
}

// Buckets groups readings into stretches of step ms.
func Buckets(samples []Sample, step int64) []Bucket {
	out := []Bucket{}
	type acc struct {
		net, gw   []float64
		n, missed int
		t         int64
	}
	var cur *acc
	flush := func() {
		if cur == nil {
			return
		}
		b := Bucket{T: cur.t, Missed: 100 * float64(cur.missed) / float64(cur.n)}
		if len(cur.net) > 0 {
			v := mean(cur.net)
			b.Net = &v
		}
		if len(cur.gw) > 0 {
			v := mean(cur.gw)
			b.Gw = &v
		}
		out = append(out, b)
	}
	for _, s := range samples {
		k := s.T / step * step
		if cur == nil || cur.t != k {
			flush()
			cur = &acc{t: k}
		}
		cur.n++
		if s.Net != nil {
			cur.net = append(cur.net, *s.Net)
		} else {
			cur.missed++
		}
		if s.Gw != nil {
			cur.gw = append(cur.gw, *s.Gw)
		}
	}
	flush()
	return out
}
