package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	a := s.Add(Alert{Kind: "new_device", Title: "New device", Body: "Phone on Main"})
	b := s.Add(Alert{Kind: "test", Title: "Test"})
	if b.ID <= a.ID || s.Unread() != 2 {
		t.Fatalf("ids %d %d, unread %d", a.ID, b.ID, s.Unread())
	}
	if l := s.List(); l[0].ID != b.ID {
		t.Fatal("newest first")
	}
	s.MarkRead(a.ID)
	if s.Unread() != 1 {
		t.Fatal("mark one read")
	}
	if again := Open(dir); len(again.List()) != 2 || again.Unread() != 1 {
		t.Fatal("alerts must survive a restart")
	}
	s.MarkRead(0)
	s.Clear()
	if len(s.List()) != 0 {
		t.Fatal("clear")
	}
}

func TestDeliver(t *testing.T) {
	var ntfyTitle, ntfyBody string
	var hook map[string]any
	nt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ntfyTitle, ntfyBody = r.Header.Get("Title"), string(b)
	}))
	defer nt.Close()
	wh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewDecoder(r.Body).Decode(&hook) }))
	defer wh.Close()
	errs := Deliver(context.Background(), Channels{Ntfy: nt.URL + "/topic", Webhook: wh.URL}, Alert{Kind: "new_device", Title: "New device", Body: "Phone"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if ntfyTitle != "New device" || ntfyBody != "Phone" || hook["text"] != "New device — Phone" || hook["kind"] != "new_device" {
		t.Fatalf("ntfy %q %q, hook %v", ntfyTitle, ntfyBody, hook)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer bad.Close()
	if errs := Deliver(context.Background(), Channels{Webhook: bad.URL}, Alert{Title: "x"}); len(errs) != 1 {
		t.Fatal("a refused webhook must be reported")
	}
	if CheckURL("file:///etc/passwd") == nil || CheckURL("https://ntfy.sh/x") != nil {
		t.Fatal("CheckURL")
	}
}
