package analytics

import (
	"testing"
	"time"
)

func parse(t *testing.T, s string) time.Time {
	t.Helper()
	v, ok := parseLocal(s)
	if !ok {
		t.Fatalf("bad date %s", s)
	}
	return v
}
