package ups

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Events HomeOwl can run scripts for (named like upsmon's NOTIFY events).
var EventNames = map[string]string{
	"onbatt": "On battery", "online": "Back on mains", "lowbatt": "Battery low", "fsd": "Forced shutdown (from the NUT server)",
	"commbad": "Lost the UPS", "commok": "Reaches the UPS again", "replbatt": "Battery needs replacing",
	"shutdown": "Time to shut down",
}

// Rule says when the power UPS is about to run out and the "shutdown" event fires (once per power cut).
// A forced shutdown (FSD) from the NUT server, or losing the UPS while it was on battery, always counts.
type Rule struct {
	LowBattery   bool `json:"low_battery"`   // the UPS reports battery low (LB) while on battery — upsmon's rule
	AfterMinutes int  `json:"after_minutes"` // on battery for this long
	BelowCharge  int  `json:"below_charge"`  // battery charge at or below this %
	BelowRuntime int  `json:"below_runtime"` // runtime left at or below this many minutes
}

// Action runs one script when an event happens for the power UPS, optionally only if it's still true after Delay seconds.
type Action struct {
	Event  string `json:"event"`
	Script string `json:"script"`
	Delay  int    `json:"delay"`
}

// Settings is what the monitor needs; the server builds it from the config and the secrets store.
type Settings struct {
	Host      string
	Port      int
	User      string
	Pass      string
	Power     string // the UPS that powers this computer: actions and the shutdown rule use it ("" = the first one)
	Poll      time.Duration
	Secondary bool // LOGIN as a upsmon secondary
	Rule      Rule
	Actions   []Action
}

func (s Settings) key() string {
	return fmt.Sprintf("%s|%d|%s|%s|%t|%s", s.Host, s.Port, s.User, s.Pass, s.Secondary, s.Power)
}

// UPSState is the latest we know about one UPS.
type UPSState struct {
	Name        string            `json:"name"`
	Desc        string            `json:"desc,omitempty"`
	Power       bool              `json:"power"` // the UPS that powers this computer
	Status      string            `json:"status"`
	Text        string            `json:"text"`
	Charge      *float64          `json:"charge"`
	Runtime     *float64          `json:"runtime"`
	Load        *float64          `json:"load"`
	In          *float64          `json:"input_voltage"`
	Out         *float64          `json:"output_voltage"`
	Vars        map[string]string `json:"vars"`
	Updated     int64             `json:"updated"`               // ms of the last good reading
	OnBattSince int64             `json:"on_battery_since"`      // ms, 0 = on mains
	CommBad     bool              `json:"comm_bad"`              // no good reading for a while
	Error       string            `json:"error,omitempty"`       // why the last reading failed
	LoginError  string            `json:"login_error,omitempty"` // why registering as a secondary failed

	firstFail int64
	lastMin   int64
	seen      bool
}

// Status is the monitor's state for the UI.
type Status struct {
	State      string     `json:"state"` // off | connecting | live | error
	Error      string     `json:"error,omitempty"`
	Server     string     `json:"server,omitempty"`
	Secondary  bool       `json:"secondary"`
	UPS        []UPSState `json:"ups"`
	Shutdown   int64      `json:"shutdown_fired,omitempty"` // ms when the shutdown event fired in this power cut
	Pending    []string   `json:"pending,omitempty"`        // actions waiting for their delay
	Since      int64      `json:"since,omitempty"`          // ms when the monitor (re)connected
	LastChange int64      `json:"last_change,omitempty"`
}

type pending struct {
	a     Action
	event string
	due   time.Time
}

// Monitor polls upsd like upsmon does, records history and runs the chosen scripts.
type Monitor struct {
	Settings func() (Settings, bool) // false = UPS monitoring is off
	Hist     *History
	Scripts  *Scripts

	mu       sync.Mutex
	st       Status
	ups      map[string]*UPSState
	order    []string
	conn     *Conn
	key      string
	fired    bool
	pend     []pending
	running  map[string]bool
	wake     chan struct{}
	commBadT time.Duration
	now      func() time.Time
	runWG    sync.WaitGroup
}

// Wake makes the monitor re-read its settings and poll now.
func (m *Monitor) Wake() {
	m.init()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Monitor) init() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wake == nil {
		m.wake = make(chan struct{}, 1)
		m.ups = map[string]*UPSState{}
		m.running = map[string]bool{}
		if m.commBadT == 0 {
			m.commBadT = 15 * time.Second // upsmon's DEADTIME
		}
		if m.now == nil {
			m.now = time.Now
		}
		m.st.State = "off"
	}
}

// Status returns a copy of the current state.
func (m *Monitor) Status() Status {
	m.init()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	s.UPS = make([]UPSState, 0, len(m.order))
	for _, n := range m.order {
		u := *m.ups[n]
		vars := make(map[string]string, len(u.Vars))
		for k, v := range u.Vars {
			vars[k] = v
		}
		u.Vars = vars
		s.UPS = append(s.UPS, u)
	}
	for _, p := range m.pend {
		s.Pending = append(s.Pending, fmt.Sprintf("%s for %s at %s", p.a.Script, EventNames[p.event], p.due.Format("15:04:05")))
	}
	return s
}

// Run polls until ctx ends, then saves the history (after running scripts finish or 10 s pass).
func (m *Monitor) Run(ctx context.Context) {
	m.init()
	save := time.NewTicker(5 * time.Minute)
	defer save.Stop()
	t := time.NewTimer(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.closeConn()
			done := make(chan struct{})
			go func() { m.runWG.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
			if err := m.Hist.Save(); err != nil {
				log.Printf("ups: save history: %v", err)
			}
			return
		case <-save.C:
			if err := m.Hist.Save(); err != nil {
				log.Printf("ups: save history: %v", err)
			}
			continue
		case <-t.C:
		case <-m.wake:
		}
		set, on := m.Settings()
		if set.Poll < 2*time.Second {
			set.Poll = 5 * time.Second
		}
		if !on {
			m.off()
			t.Reset(10 * time.Second)
			continue
		}
		m.Tick(ctx, set)
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(set.Poll)
	}
}

func (m *Monitor) off() {
	m.closeConn()
	m.mu.Lock()
	if m.st.State != "off" {
		m.st = Status{State: "off"}
		m.ups, m.order, m.pend, m.fired, m.key = map[string]*UPSState{}, nil, nil, false, ""
	}
	m.mu.Unlock()
}

func (m *Monitor) closeConn() {
	m.mu.Lock()
	c := m.conn
	m.conn = nil
	m.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// Tick does one poll: connect if needed, read every UPS, notice changes, fire events and due actions.
func (m *Monitor) Tick(ctx context.Context, set Settings) {
	m.init()
	now := m.now()
	m.mu.Lock()
	if set.key() != m.key {
		if m.conn != nil {
			_ = m.conn.Close()
			m.conn = nil
		}
		if m.key != "" && m.st.Server != fmt.Sprintf("%s:%d", set.Host, portOr(set.Port)) {
			m.ups, m.order, m.pend, m.fired = map[string]*UPSState{}, nil, nil, false
		}
		m.key = set.key()
		m.st.Server = fmt.Sprintf("%s:%d", set.Host, portOr(set.Port))
	}
	c := m.conn
	m.mu.Unlock()

	if c == nil {
		m.mu.Lock()
		if m.st.State != "live" {
			m.st.State = "connecting"
		}
		m.mu.Unlock()
		var list []UPSInfo
		var err error
		c, list, err = m.connect(ctx, set)
		if err != nil {
			m.connFailed(now, set, err)
			return
		}
		m.mu.Lock()
		m.conn = c
		m.st.State, m.st.Error, m.st.Since, m.st.Secondary = "live", "", now.UnixMilli(), false
		names := map[string]bool{}
		for _, u := range list {
			names[u.Name] = true
			if m.ups[u.Name] == nil {
				m.ups[u.Name] = &UPSState{Name: u.Name}
				m.order = append(m.order, u.Name)
			}
			m.ups[u.Name].Desc = u.Desc
		}
		keep := m.order[:0]
		for _, n := range m.order {
			if names[n] {
				keep = append(keep, n)
			} else {
				delete(m.ups, n)
			}
		}
		m.order = keep
		power := m.powerName(set)
		for _, n := range m.order {
			m.ups[n].Power = n == power
		}
		m.mu.Unlock()
		if set.Secondary && power != "" {
			err := c.Login(power)
			m.mu.Lock()
			if p := m.ups[power]; p != nil {
				p.LoginError = ""
				if err != nil && !IsNUT(err, "ALREADY-LOGGED-IN") {
					p.LoginError = err.Error()
				} else {
					m.st.Secondary = true
				}
			}
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	names := append([]string(nil), m.order...)
	m.mu.Unlock()
	for _, n := range names {
		vars, err := c.Vars(n)
		if err != nil && !IsNUT(err, "") {
			// the connection itself broke: drop it, count it as a failed reading for every UPS, retry next tick
			m.closeConn()
			m.connFailed(now, set, err)
			return
		}
		m.observe(ctx, set, n, vars, err, now)
	}
	m.runDue(ctx, set, now)
}

func portOr(p int) int {
	if p <= 0 {
		return DefaultPort
	}
	return p
}

func (m *Monitor) powerName(set Settings) string {
	if set.Power != "" {
		if m.ups[set.Power] != nil {
			return set.Power
		}
		return ""
	}
	if len(m.order) > 0 {
		return m.order[0]
	}
	return ""
}

func (m *Monitor) connect(ctx context.Context, set Settings) (*Conn, []UPSInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := Dial(cctx, set.Host, set.Port, 5*time.Second)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Auth(set.User, set.Pass); err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	list, err := c.List()
	if err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	if len(list) == 0 {
		_ = c.Close()
		return nil, nil, errors.New("the NUT server has no UPS configured")
	}
	return c, list, nil
}

// connFailed: the server can't be reached; every known UPS gets a failed reading.
func (m *Monitor) connFailed(now time.Time, set Settings, err error) {
	m.mu.Lock()
	m.st.State, m.st.Error = "error", err.Error()
	names := append([]string(nil), m.order...)
	m.mu.Unlock()
	for _, n := range names {
		m.observe(context.Background(), set, n, nil, err, now)
	}
	m.runDue(context.Background(), set, now)
}

func pf(vars map[string]string, k string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(vars[k]), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// observe takes one reading (or failure) of a UPS and turns changes into events.
func (m *Monitor) observe(ctx context.Context, set Settings, name string, vars map[string]string, err error, now time.Time) {
	m.mu.Lock()
	u := m.ups[name]
	if u == nil {
		m.mu.Unlock()
		return
	}
	var fire []Event
	ms := now.UnixMilli()
	if err != nil {
		u.Error = err.Error()
		if u.firstFail == 0 {
			u.firstFail = ms
		}
		if !u.CommBad && ms-u.firstFail >= m.commBadT.Milliseconds() {
			u.CommBad = true
			fire = append(fire, Event{T: ms, UPS: name, Kind: "commbad", Text: "Lost the UPS: " + u.Error, Status: u.Status})
		}
	} else {
		prev, first := u.Status, !u.seen
		u.Vars, u.Error, u.firstFail, u.Updated, u.seen = vars, "", 0, ms, true
		u.Status = strings.TrimSpace(vars["ups.status"])
		u.Text = Describe(u.Status)
		u.Charge, u.Runtime, u.Load = pf(vars, "battery.charge"), pf(vars, "battery.runtime"), pf(vars, "ups.load")
		u.In, u.Out = pf(vars, "input.voltage"), pf(vars, "output.voltage")
		ev := func(kind, text string) Event {
			return Event{T: ms, UPS: name, Kind: kind, Text: text, Status: u.Status, Charge: u.Charge, Runtime: u.Runtime}
		}
		if u.CommBad {
			u.CommBad = false
			fire = append(fire, ev("commok", "Reaches the UPS again"))
		}
		ob := hasFlag(u.Status, "OB")
		if ob && u.OnBattSince == 0 {
			u.OnBattSince = ms
		}
		if first {
			if ob {
				fire = append(fire, ev("onbatt", "On battery when HomeOwl started watching"))
			}
		} else {
			if ob && !hasFlag(prev, "OB") {
				fire = append(fire, ev("onbatt", "Power cut: the UPS is on battery"))
			}
			if !ob && hasFlag(prev, "OB") {
				fire = append(fire, ev("online", "Power is back: on mains again"))
			}
			if hasFlag(u.Status, "LB") && !hasFlag(prev, "LB") {
				fire = append(fire, ev("lowbatt", "Battery low"))
			}
			if hasFlag(u.Status, "FSD") && !hasFlag(prev, "FSD") {
				fire = append(fire, ev("fsd", "The NUT server ordered a forced shutdown"))
			}
			if hasFlag(u.Status, "RB") && !hasFlag(prev, "RB") {
				fire = append(fire, ev("replbatt", "The battery needs replacing"))
			}
		}
		if !ob {
			u.OnBattSince = 0
		}
		if u.Power && !ob && !hasFlag(u.Status, "FSD") {
			m.fired, m.st.Shutdown = false, 0
		}
		if min := ms / 60000; min != u.lastMin {
			u.lastMin = min
			m.Hist.AddSample(Sample{T: ms, UPS: name, Status: u.Status, Charge: u.Charge, Runtime: u.Runtime, Load: u.Load, In: u.In, Out: u.Out})
		}
	}
	if u.Power && !m.fired {
		if why := m.shutdownReason(set.Rule, u, ms); why != "" {
			m.fired, m.st.Shutdown = true, ms
			fire = append(fire, Event{T: ms, UPS: name, Kind: "shutdown", Text: "Time to shut down: " + why, Status: u.Status, Charge: u.Charge, Runtime: u.Runtime})
		}
	}
	power := u.Power
	if len(fire) > 0 {
		m.st.LastChange = ms
	}
	m.mu.Unlock()
	for _, e := range fire {
		m.Hist.AddEvent(e)
		log.Printf("ups %s: %s", name, e.Text)
		if power {
			m.trigger(ctx, set, e, now)
		}
	}
	if len(fire) > 0 {
		if err := m.Hist.Save(); err != nil {
			log.Printf("ups: save history: %v", err)
		}
	}
}

// shutdownReason says why the power UPS needs this computer to shut down now ("" = not yet). Called with m.mu held.
func (m *Monitor) shutdownReason(r Rule, u *UPSState, ms int64) string {
	ob := hasFlag(u.Status, "OB")
	switch {
	case hasFlag(u.Status, "FSD"):
		return "the NUT server ordered a forced shutdown"
	case u.CommBad && ob:
		return "lost the UPS while it was on battery"
	case u.CommBad:
		return ""
	case r.LowBattery && ob && hasFlag(u.Status, "LB"):
		return "the battery is low"
	case ob && r.AfterMinutes > 0 && u.OnBattSince > 0 && ms-u.OnBattSince >= int64(r.AfterMinutes)*60000:
		return fmt.Sprintf("on battery for %d minutes", r.AfterMinutes)
	case ob && r.BelowCharge > 0 && u.Charge != nil && *u.Charge <= float64(r.BelowCharge):
		return fmt.Sprintf("battery at %.0f%%", *u.Charge)
	case ob && r.BelowRuntime > 0 && u.Runtime != nil && *u.Runtime <= float64(r.BelowRuntime)*60:
		return fmt.Sprintf("about %.0f minutes of battery left", *u.Runtime/60)
	}
	return ""
}

// trigger starts (or schedules) the actions for one event of the power UPS.
func (m *Monitor) trigger(ctx context.Context, set Settings, e Event, now time.Time) {
	for _, a := range set.Actions {
		if a.Event != e.Kind || a.Script == "" {
			continue
		}
		if a.Delay > 0 {
			m.mu.Lock()
			m.pend = append(m.pend, pending{a: a, event: e.Kind, due: now.Add(time.Duration(a.Delay) * time.Second)})
			m.mu.Unlock()
			continue
		}
		m.start(ctx, a.Script, e.Kind, "", false)
	}
}

// stillTrue: is the event's condition still there (for delayed actions)? Called with m.mu held.
func (m *Monitor) stillTrue(event string, u *UPSState) bool {
	if u == nil {
		return false
	}
	switch event {
	case "onbatt":
		return hasFlag(u.Status, "OB") && !u.CommBad
	case "online":
		return !hasFlag(u.Status, "OB") && !u.CommBad
	case "lowbatt":
		return hasFlag(u.Status, "LB") && !u.CommBad
	case "commbad":
		return u.CommBad
	case "commok":
		return !u.CommBad
	}
	return true
}

func (m *Monitor) runDue(ctx context.Context, set Settings, now time.Time) {
	m.mu.Lock()
	var u *UPSState
	for _, n := range m.order {
		if m.ups[n].Power {
			u = m.ups[n]
		}
	}
	var due []pending
	keep := m.pend[:0]
	for _, p := range m.pend {
		switch {
		case !m.stillTrue(p.event, u):
			// condition went away (e.g. power came back): skip it
		case !now.Before(p.due):
			due = append(due, p)
		default:
			keep = append(keep, p)
		}
	}
	m.pend = keep
	m.mu.Unlock()
	for _, p := range due {
		m.start(ctx, p.a.Script, p.event, "", false)
	}
}

// Env is the environment a script gets (upsmon's NOTIFYTYPE and UPSNAME, plus HomeOwl's own values).
func (m *Monitor) Env(event, ups string, test bool) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.ups[ups]
	if u == nil {
		for _, n := range m.order {
			if m.ups[n].Power {
				u = m.ups[n]
			}
		}
	}
	env := []string{"NOTIFYTYPE=" + strings.ToUpper(event), "HOMEOWL_UPS_EVENT=" + event}
	if test {
		env = append(env, "HOMEOWL_UPS_TEST=1")
	}
	if u != nil {
		host, _, _ := strings.Cut(m.st.Server, ":")
		f := func(p *float64, div float64) string {
			if p == nil {
				return ""
			}
			return strconv.Itoa(int(math.Round(*p / div)))
		}
		env = append(env, "UPSNAME="+u.Name+"@"+host, "HOMEOWL_UPS_STATUS="+u.Text, "HOMEOWL_UPS_FLAGS="+u.Status,
			"HOMEOWL_UPS_CHARGE="+f(u.Charge, 1), "HOMEOWL_UPS_RUNTIME="+f(u.Runtime, 1), "HOMEOWL_UPS_RUNTIME_MIN="+f(u.Runtime, 60),
			"HOMEOWL_UPS_LOAD="+f(u.Load, 1))
	}
	return env
}

// start runs a script in the background (one run of the same script at a time) and records how it went.
func (m *Monitor) start(ctx context.Context, script, event, by string, test bool) {
	m.mu.Lock()
	if m.running[script] {
		m.mu.Unlock()
		log.Printf("ups: %s is still running; not starting it again for %s", script, event)
		return
	}
	m.running[script] = true
	ups := ""
	for _, n := range m.order {
		if m.ups[n].Power {
			ups = n
		}
	}
	m.mu.Unlock()
	env := m.Env(event, ups, test)
	m.runWG.Add(1)
	go func() {
		defer m.runWG.Done()
		r := m.Scripts.Run(context.WithoutCancel(ctx), script, env)
		m.mu.Lock()
		delete(m.running, script)
		m.mu.Unlock()
		m.record(ups, script, event, by, test, r)
	}()
}

// RunNow runs a script once for a test and waits for it; the script gets HOMEOWL_UPS_TEST=1.
func (m *Monitor) RunNow(ctx context.Context, script, event, by string) Result {
	m.init()
	if event == "" {
		event = "shutdown"
	}
	m.mu.Lock()
	if m.running[script] {
		m.mu.Unlock()
		return Result{Exit: -1, Error: "this script is running right now"}
	}
	m.running[script] = true
	ups := ""
	for _, n := range m.order {
		if m.ups[n].Power {
			ups = n
		}
	}
	m.mu.Unlock()
	r := m.Scripts.Run(ctx, script, m.Env(event, ups, true))
	m.mu.Lock()
	delete(m.running, script)
	m.mu.Unlock()
	m.record(ups, script, event, by, true, r)
	return r
}

func (m *Monitor) record(ups, script, event, by string, test bool, r Result) {
	exit := r.Exit
	kind, text := "script", fmt.Sprintf("Ran %s for %q", script, EventNames[event])
	if test {
		kind, text = "test", fmt.Sprintf("Test of %s (as %q)", script, EventNames[event])
	}
	switch {
	case r.Error != "":
		text += ": " + r.Error
	case r.Exit != 0:
		text += fmt.Sprintf(": exit code %d", r.Exit)
	}
	m.Hist.AddEvent(Event{UPS: ups, Kind: kind, Text: text, Script: script, For: event, Exit: &exit, Output: trunc(r.Output, 2000), By: by})
	log.Printf("ups: %s", text)
	if err := m.Hist.Save(); err != nil {
		log.Printf("ups: save history: %v", err)
	}
}

// Test connects once with the given settings and returns the UPSes (for the Connect button).
func Test(ctx context.Context, set Settings) ([]UPSInfo, error) {
	m := &Monitor{}
	c, list, err := m.connect(ctx, set)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if set.Power != "" {
		found := false
		for _, u := range list {
			found = found || u.Name == set.Power
		}
		if !found {
			return nil, fmt.Errorf("the NUT server has no UPS named %q", set.Power)
		}
	}
	name := set.Power
	if name == "" {
		name = list[0].Name
	}
	if _, err := c.Vars(name); err != nil {
		return nil, err
	}
	return list, nil
}
