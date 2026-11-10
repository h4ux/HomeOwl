package server

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/h4ux/homeowl/internal/config"
	"github.com/h4ux/homeowl/internal/history"
	"github.com/h4ux/homeowl/internal/unifi"
)

// fingerprintKey is the local secret behind the short fingerprints of redacted passwords.
func fingerprintKey(dataDir string) []byte {
	p := filepath.Join(dataDir, "secrets", "fingerprint.key")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 32 {
		return b
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil
	}
	if err := config.WriteFileAtomic(p, b, 0o600); err != nil {
		return nil
	}
	return b
}

// homeowlChanges reads HomeOwl's own change log between two times (to say who changed what).
func (a *App) homeowlChanges(from, to int64) []unifi.Change {
	out := []unifi.Change{}
	f, err := os.Open(filepath.Join(a.DataDir, "changes-log.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() {
		var c unifi.Change
		if json.Unmarshal(sc.Bytes(), &c) != nil || !c.OK {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02 15:04:05", c.Time, time.Local)
		if err == nil && t.Unix() > from && t.Unix() <= to {
			out = append(out, c)
		}
	}
	return out
}

// historyDiff compares two snapshots: by file names, or "since" hours ago up to the newest.
func (a *App) historyDiff(q map[string]string) (any, error) {
	l := a.History.List()
	if len(l) == 0 {
		return map[string]any{"changes": []history.Change{}, "note": "No snapshots yet."}, nil
	}
	idx := map[string]history.Entry{}
	for _, e := range l {
		idx[e.File] = e
	}
	to, ok := idx[q["to"]]
	if !ok {
		to = l[len(l)-1]
	}
	var from history.Entry
	switch {
	case q["from"] != "":
		if from, ok = idx[q["from"]]; !ok {
			return nil, errors.New("unknown snapshot")
		}
	case q["since"] != "":
		h, _ := strconv.Atoi(q["since"])
		from, _ = history.At(l, time.Now().Unix()-int64(min(24*365, max(1, h)))*3600)
	default: // the snapshot before "to"
		from = to
		for i, e := range l {
			if e.File == to.File && i > 0 {
				from = l[i-1]
			}
		}
	}
	if from.Time > to.Time {
		from, to = to, from
	}
	sa, err := a.History.Load(from.File)
	if err != nil {
		return nil, err
	}
	sb, err := a.History.Load(to.File)
	if err != nil {
		return nil, err
	}
	ch := history.Diff(sa, sb)
	if ch == nil {
		ch = []history.Change{}
	}
	return map[string]any{"from": from, "to": to, "changes": ch, "homeowl": a.homeowlChanges(from.Time, to.Time)}, nil
}
