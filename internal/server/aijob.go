package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/h4ux/homeowl/internal/alerts"
)

// The suggestion job (aicat.go asks the model). It goes through every domain that had no category in the last 30
// days, in groups of 100, in the background, and never asks about a domain twice: each domain asked is remembered
// with what came back. A domain that got a suggestion isn't asked again (it waits for review; dismissed, it stays
// dismissed); one the model didn't know is asked again after 30 days, by when a better model may be set. Suggestions
// wait in a list until an admin reviews them, so a daily run (Settings in the model dialog) only adds new ones and
// alerts the admins. One run at a time; progress is saved after every group, so a stop or a crash loses nothing.

const (
	aiLookback     = 30 * 24 * time.Hour // uncategorized domains seen this recently
	aiRetryUnknown = 30 * 24 * time.Hour
	aiMaxPerRun    = 3000 // domains per run (cost guard): the rest are next run's
	aiDailyHour    = 3    // the daily run starts after 03:00 local time
)

type aiAsked struct {
	At  int64  `json:"at"`
	Cat string `json:"cat,omitempty"` // what it suggested ("" = didn't know)
}

type aiPending struct {
	aiSuggestion
	At    int64  `json:"at"`
	Model string `json:"model"`
}

type aiRunInfo struct {
	At    int64  `json:"at"`
	Took  int64  `json:"took"` // seconds
	Asked int    `json:"asked"`
	Found int    `json:"found"`
	Left  int    `json:"left,omitempty"` // over the per-run cap: next time
	Model string `json:"model"`
	By    string `json:"by"`
	Error string `json:"error,omitempty"`
}

type aiState struct {
	Asked   map[string]aiAsked `json:"asked"`
	Pending []aiPending        `json:"pending"`
	Last    *aiRunInfo         `json:"last,omitempty"`
}

type aiProgress struct {
	Running bool  `json:"running"`
	Done    int   `json:"done"`
	Total   int   `json:"total"`
	Found   int   `json:"found"`
	Started int64 `json:"started,omitempty"`
}

type aiJobs struct {
	mu   sync.Mutex // the file
	path string
	pmu  sync.Mutex // progress
	prog aiProgress
}

func newAIJobs(dir string) *aiJobs { return &aiJobs{path: filepath.Join(dir, "ai-categories.json")} }

func (j *aiJobs) load() aiState {
	st := aiState{}
	if b, err := os.ReadFile(j.path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Asked == nil {
		st.Asked = map[string]aiAsked{}
	}
	return st
}

func (j *aiJobs) save(st aiState) error {
	b, _ := json.MarshalIndent(st, "", " ")
	return os.WriteFile(j.path, b, 0o600)
}

func (j *aiJobs) update(fn func(*aiState)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.load()
	fn(&st)
	if err := j.save(st); err != nil {
		log.Printf("category suggestions: %v", err)
	}
}

func (j *aiJobs) progress() aiProgress {
	j.pmu.Lock()
	defer j.pmu.Unlock()
	return j.prog
}

func (j *aiJobs) setProgress(fn func(*aiProgress)) {
	j.pmu.Lock()
	fn(&j.prog)
	j.pmu.Unlock()
}

// aiCandidates: the uncategorized domains still worth asking about, the busiest first.
func aiCandidates(all []string, st aiState, now time.Time) []string {
	waiting := map[string]bool{}
	for _, p := range st.Pending {
		waiting[p.Domain] = true
	}
	var out []string
	for _, d := range all {
		d = strings.ToLower(d)
		if !aiDomainRE.MatchString(d) || waiting[d] {
			continue
		}
		if x, ok := st.Asked[d]; ok && (x.Cat != "" || now.Sub(time.Unix(x.At, 0)) < aiRetryUnknown) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// aiUncategorized lists the domains with no category (a var, so tests don't need DNS logs).
var aiUncategorized = func(a *App, since time.Time, limit int) ([]string, error) {
	return a.An.UncategorizedRoots(since, limit)
}

// aiStart runs the job in the background; false when one is already running.
func (a *App) aiStart(by string) (bool, error) {
	ac := a.aiConf()
	if !ac.ready() {
		return false, errors.New("set up a model for suggestions first")
	}
	if !a.An.Open() {
		return false, errors.New("analytics isn't running")
	}
	j := a.aiJobs
	started := false
	j.setProgress(func(p *aiProgress) {
		if !p.Running {
			*p, started = aiProgress{Running: true, Started: time.Now().Unix()}, true
		}
	})
	if !started {
		return false, nil
	}
	go a.aiRun(ac, by)
	return true, nil
}

func (a *App) aiRun(ac aiConf, by string) {
	j, now := a.aiJobs, time.Now()
	info := aiRunInfo{At: now.Unix(), Model: ac.label(), By: by}
	defer func() {
		info.Took = int64(time.Since(now).Seconds())
		j.update(func(st *aiState) { st.Last = &info })
		j.setProgress(func(p *aiProgress) { p.Running = false })
		if info.Found > 0 {
			a.Alerts.Add(alerts.Alert{Kind: "ai_categories", Level: "info", Title: fmt.Sprintf("%d category suggestions to review", info.Found),
				Body: info.Model + " suggested categories for uncategorized domains. Review them in Family › Categories."})
		}
		log.Printf("category suggestions (%s, %s): asked %d, %d suggestions%s", info.Model, by, info.Asked, info.Found, map[bool]string{true: ", " + info.Error}[info.Error != ""])
	}()
	all, err := aiUncategorized(a, now.Add(-aiLookback), 20000)
	if err != nil {
		info.Error = err.Error()
		return
	}
	j.mu.Lock()
	st := j.load()
	j.mu.Unlock()
	todo := aiCandidates(all, st, now)
	if len(todo) > aiMaxPerRun {
		info.Left, todo = len(todo)-aiMaxPerRun, todo[:aiMaxPerRun]
	}
	j.setProgress(func(p *aiProgress) { p.Total = len(todo) })
	cats := aiCategoryList(a)
	for i := 0; i < len(todo); i += aiChunk {
		part := todo[i:min(i+aiChunk, len(todo))]
		ctx, cancel := context.WithTimeout(context.Background(), aiTimeout(ac))
		asked := map[string]bool{}
		for _, d := range part {
			asked[d] = true
		}
		got, err := aiCategorize(ctx, ac, cats, part)
		cancel()
		if err != nil {
			info.Error = err.Error()
			return // what was asked so far is saved; the rest is next run's
		}
		got = aiKeepKnown(got, asked, cats)
		at := time.Now().Unix()
		j.update(func(st *aiState) {
			byDom := map[string]aiSuggestion{}
			for _, x := range got {
				byDom[x.Domain] = x
			}
			for _, d := range part {
				st.Asked[d] = aiAsked{At: at, Cat: byDom[d].Category}
				if x, ok := byDom[d]; ok {
					st.Pending = append(st.Pending, aiPending{aiSuggestion: x, At: at, Model: ac.label()})
				}
			}
		})
		info.Asked += len(part)
		info.Found += len(got)
		j.setProgress(func(p *aiProgress) { p.Done, p.Found = info.Asked, info.Found })
	}
}

// aiPendingView: the suggestions waiting, without any whose domain got a category meanwhile.
func (a *App) aiPendingView() ([]aiPending, error) {
	open := map[string]bool{}
	if a.An.Open() {
		all, err := aiUncategorized(a, time.Now().Add(-90*24*time.Hour), 50000)
		if err != nil {
			return nil, err
		}
		for _, d := range all {
			open[strings.ToLower(d)] = true
		}
	}
	var out []aiPending
	a.aiJobs.update(func(st *aiState) {
		keep := st.Pending[:0]
		for _, p := range st.Pending {
			if open[p.Domain] {
				keep = append(keep, p)
			}
		}
		st.Pending = keep
		out = append(out, keep...)
	})
	sort.SliceStable(out, func(i, k int) bool { return out[i].Category < out[k].Category })
	return nz(out), nil
}

// aiDone takes reviewed domains off the list (saved as rules, or dismissed: either way never asked again).
func (a *App) aiDone(domains []string) {
	gone := map[string]bool{}
	for _, d := range domains {
		gone[strings.ToLower(d)] = true
	}
	a.aiJobs.update(func(st *aiState) {
		keep := st.Pending[:0]
		for _, p := range st.Pending {
			if !gone[p.Domain] {
				keep = append(keep, p)
			}
		}
		st.Pending = keep
	})
}

// aiDailyLoop: with the daily run on, once a day after 03:00.
func (a *App) aiDailyLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !a.Cfg.Get().AI.Daily || !a.aiConf().ready() || !a.An.Open() {
			continue
		}
		now := time.Now()
		a.aiJobs.mu.Lock()
		last := a.aiJobs.load().Last
		a.aiJobs.mu.Unlock()
		if now.Hour() < aiDailyHour || (last != nil && time.Unix(last.At, 0).Format("2006-01-02") == now.Format("2006-01-02")) {
			continue
		}
		if _, err := a.aiStart("daily"); err != nil {
			log.Printf("category suggestions: %v", err)
		}
	}
}
