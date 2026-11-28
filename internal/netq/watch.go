package netq

import (
	"context"
	"errors"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Targets are tried for "the internet": well-known public anycast addresses on the HTTPS port. The fastest
// answer counts, so one of them being slow or blocked doesn't look like an outage.
var Targets = []string{"1.1.1.1:443", "8.8.8.8:443", "9.9.9.9:443"}

// Event: the internet went down (after two readings in a row without an answer) or came back.
type Event struct {
	Kind   string `json:"kind"` // down, up
	Outage Outage `json:"outage"`
}

// Watcher takes a reading every minute and keeps the outages up to date.
type Watcher struct {
	Store *Store
	// Gateway: the gateway's address on the home network and the home networks' subnets ("" / nil while unknown).
	Gateway func() (string, []netip.Prefix)
	// Enabled: readings are on (Settings may turn them off).
	Enabled func() bool
	// OnEvent is told when the internet goes down or comes back.
	OnEvent func(Event)
	// Probe and LocalIP are swapped by tests.
	Probe   func(ctx context.Context, gw string) (gwMs, netMs *float64)
	LocalIP func() netip.Addr

	mu     sync.Mutex
	streak []Sample // readings without the internet, before an outage is opened
	last   Sample   // the newest reading
	away   bool     // this computer isn't on the home network right now
}

// Status is what the page shows at the top.
type Status struct {
	Last    *Sample `json:"last,omitempty"`
	Away    bool    `json:"away"`
	Down    *Outage `json:"down,omitempty"` // the outage going on now
	Gateway string  `json:"gateway,omitempty"`
	Off     bool    `json:"off"`
}

func (w *Watcher) Status() Status {
	w.mu.Lock()
	st := Status{Away: w.away}
	if w.last.T != 0 {
		l := w.last
		st.Last = &l
	}
	w.mu.Unlock()
	w.Store.mu.Lock()
	if c := w.Store.current(); c != nil {
		o := *c
		st.Down = &o
	}
	w.Store.mu.Unlock()
	if w.Gateway != nil {
		st.Gateway, _ = w.Gateway()
	}
	st.Off = w.Enabled != nil && !w.Enabled()
	return st
}

// Run reads every minute until ctx ends, and saves every five minutes.
func (w *Watcher) Run(ctx context.Context) {
	first := time.NewTimer(15 * time.Second)
	tick := time.NewTicker(time.Minute)
	save := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	defer save.Stop()
	defer func() {
		if err := w.Store.Save(); err != nil {
			log.Printf("internet: %v", err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			w.Tick(ctx, time.Now())
		case <-tick.C:
			w.Tick(ctx, time.Now())
		case <-save.C:
			if err := w.Store.Save(); err != nil {
				log.Printf("internet: %v", err)
			}
		}
	}
}

// Tick takes one reading (unless readings are off or this computer is away from home).
func (w *Watcher) Tick(ctx context.Context, now time.Time) {
	if w.Enabled != nil && !w.Enabled() {
		return
	}
	gw, home := "", []netip.Prefix(nil)
	if w.Gateway != nil {
		gw, home = w.Gateway()
	}
	local := w.localIP()
	if len(home) > 0 && local.IsValid() && !inAny(local, home) {
		// a laptop away from home: its internet says nothing about the home's
		w.mu.Lock()
		w.away = true
		w.mu.Unlock()
		return
	}
	probe := w.Probe
	if probe == nil {
		probe = ProbeOnce
	}
	pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	g, n := probe(pctx, gw)
	cancel()
	if ctx.Err() != nil {
		return
	}
	for _, e := range w.observe(Sample{T: now.UnixMilli(), Gw: g, Net: n, NoGw: gw == ""}) {
		if w.OnEvent != nil {
			w.OnEvent(e)
		}
	}
}

func (w *Watcher) localIP() netip.Addr {
	if w.LocalIP != nil {
		return w.LocalIP()
	}
	return LocalIP()
}

// readings further apart than this mean HomeOwl wasn't running or the computer slept: an open outage ends at the
// last reading rather than lasting through the gap
const maxGap = 10 * time.Minute

// observe records a reading and opens or closes an outage. Two readings in a row without the internet open one
// (starting at the first); the first answer closes it.
func (w *Watcher) observe(x Sample) []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := w.Store
	st.mu.Lock()
	var ev []Event
	prev := w.last
	w.last, w.away = x, false
	cur := st.current()
	if cur != nil && prev.T != 0 && x.T-prev.T > maxGap.Milliseconds() {
		cur.End = prev.T + time.Minute.Milliseconds()
		ev = append(ev, Event{Kind: "up", Outage: *cur})
		cur = nil
		w.streak = nil
	}
	if len(w.streak) > 0 && x.T-w.streak[len(w.streak)-1].T > maxGap.Milliseconds() {
		w.streak = nil
	}
	count := func(o *Outage, s Sample) {
		if s.GwDown() {
			o.HomeMin++
		} else {
			o.IspMin++
		}
		o.Cause = "isp"
		if o.HomeMin > o.IspMin {
			o.Cause = "home"
		}
	}
	switch {
	case !x.NetDown():
		w.streak = nil
		if cur != nil {
			cur.End = x.T
			ev = append(ev, Event{Kind: "up", Outage: *cur})
		}
	case cur != nil:
		count(cur, x)
	default:
		w.streak = append(w.streak, x)
		if len(w.streak) >= 2 {
			o := Outage{Start: w.streak[0].T}
			for _, s := range w.streak {
				count(&o, s)
			}
			st.Outages = append(st.Outages, o)
			w.streak = nil
			ev = append(ev, Event{Kind: "down", Outage: o})
		}
	}
	st.Samples = append(st.Samples, x)
	st.dirty = true
	st.mu.Unlock()
	return ev
}

func inAny(ip netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// LocalIP is the address this computer uses to reach the internet (no packet is sent: a UDP "connection" only
// picks the route).
func LocalIP() netip.Addr {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return netip.Addr{}
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		if ip, ok := netip.AddrFromSlice(a.IP.To4()); ok {
			return ip
		}
	}
	return netip.Addr{}
}

// ProbeOnce times a TCP connection to the gateway (port 443, which UniFi gateways answer) and to the internet
// targets at the same time. A refused connection still counts as an answer: something replied.
func ProbeOnce(ctx context.Context, gw string) (gwMs, netMs *float64) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	if gw != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gwMs = dialMs(ctx, net.JoinHostPort(gw, "443"), 3*time.Second)
		}()
	}
	for _, t := range Targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v := dialMs(ctx, t, 4*time.Second); v != nil {
				mu.Lock()
				if netMs == nil || *v < *netMs {
					netMs = v
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return gwMs, netMs
}

func dialMs(ctx context.Context, addr string, timeout time.Duration) *float64 {
	d := net.Dialer{Timeout: timeout}
	t0 := time.Now()
	c, err := d.DialContext(ctx, "tcp", addr)
	ms := float64(time.Since(t0).Microseconds()) / 1000
	if err == nil {
		c.Close()
		return &ms
	}
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused") {
		return &ms
	}
	return nil
}
