package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h := HashPassword("correct horse battery")
	if !strings.HasPrefix(h, "pbkdf2-sha256$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Fatal("right password rejected")
	}
	if VerifyPassword(h, "wrong password!!") {
		t.Fatal("wrong password accepted")
	}
}

func TestUsersSessionsAndLimits(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !s.NeedsSetup() || s.SetupCode == "" {
		t.Fatal("a fresh store needs setup and has a setup code")
	}
	if err := s.Save(UserInput{Username: "alex", Role: RoleAdmin, Password: "short"}, true); err == nil {
		t.Fatal("short password accepted")
	}
	if err := s.Save(UserInput{Username: "Alex", Role: RoleAdmin, Password: "a-long-password", Tailscale: "alex@github"}, true); err != nil {
		t.Fatal(err)
	}
	if s.NeedsSetup() || s.SetupCode != "" {
		t.Fatal("setup should be done")
	}
	tok, csrf, u, err := s.Login("alex", "a-long-password", "1.2.3.4")
	if err != nil || u.Username != "alex" || csrf == "" {
		t.Fatalf("login failed: %v", err)
	}
	if got, c, ok := s.Check(tok); !ok || got.Username != "alex" || c != csrf {
		t.Fatal("session not valid")
	}
	if _, ok := s.ByTailscale("ALEX@github"); !ok {
		t.Fatal("tailscale lookup should be case-insensitive")
	}
	if err := s.Delete("alex"); err == nil {
		t.Fatal("deleting the last admin must fail")
	}
	s.Logout(tok)
	if _, _, ok := s.Check(tok); ok {
		t.Fatal("session survived logout")
	}
	for i := 0; i < 8; i++ {
		_, _, _, _ = s.Login("alex", "nope", "5.6.7.8")
	}
	if _, _, _, err := s.Login("alex", "a-long-password", "5.6.7.8"); err != ErrRateLimited {
		t.Fatalf("expected rate limit, got %v", err)
	}
	// reopen: users and sessions persist
	tok2, _, _, _ := s.Login("alex", "a-long-password", "9.9.9.9")
	s2, _ := Open(s.dir)
	if _, _, ok := s2.Check(tok2); !ok {
		t.Fatal("session lost after restart")
	}
}
