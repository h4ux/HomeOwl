package acmecert

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The record check must never be what stops a certificate: a network that blocks direct DNS still works,
// public DNS over HTTPS can confirm the record, and without any confirmation it carries on after maxWait.
func TestWaitTXT(t *testing.T) {
	answer := ""
	doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "TXT" || r.URL.Query().Get("name") != "_acme-challenge.homeowl.example.com" {
			t.Errorf("DoH query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/dns-json")
		if answer == "" {
			_, _ = w.Write([]byte(`{"Status":3}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"name":"_acme-challenge.homeowl.example.com","type":16,"data":"\"` + answer + `\""}]}`))
	}))
	defer doh.Close()
	oldDoH, oldWait := DoH, maxWait
	defer func() { DoH, maxWait = oldDoH, oldWait }()
	DoH = []string{doh.URL}
	// a name server that can't be asked (nothing listens on port 53 here)
	ns := []string{"127.0.0.1"}

	var notes []string
	report := func(d string) { notes = append(notes, d) }
	answer = "proof-value"
	start := time.Now()
	if err := WaitTXT(context.Background(), ns, "_acme-challenge.homeowl.example.com", "proof-value", report); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second || len(notes) == 0 || notes[len(notes)-1] != "public DNS shows it" {
		t.Fatalf("public DNS should confirm it at once: %v", notes)
	}

	answer, notes, maxWait = "", nil, time.Second
	if err := WaitTXT(context.Background(), ns, "_acme-challenge.homeowl.example.com", "proof-value", report); err != nil {
		t.Fatalf("not being able to confirm must not fail the request: %v", err)
	}
	all := strings.Join(notes, " | ")
	if !strings.Contains(all, "can't ask the name servers from here") || !strings.Contains(all, "Let's Encrypt checks for itself") {
		t.Fatalf("progress should say why: %s", all)
	}
}
