// Package secrets stores API keys in the OS keychain (macOS Keychain, Windows Credential Manager,
// Linux Secret Service) and falls back to a 0600 file in the data folder when no keychain is available
// (e.g. a headless Linux server or a Windows service account).
package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/h4ux/homeowl/internal/config"
)

type Store struct {
	dir     string
	mu      sync.Mutex
	cache   map[string]cached
	noChain bool // keychain unusable → files only
}

type cached struct {
	v  string
	at time.Time
}

var slotRE = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func New(dataDir string) *Store {
	s := &Store{dir: filepath.Join(dataDir, "secrets"), cache: map[string]cached{}}
	if os.Getenv("HOMEOWL_NO_KEYCHAIN") != "" {
		s.noChain = true
	}
	return s
}

// Where describes where secrets end up, for the Settings page.
func (s *Store) Where() string {
	if s.noChain {
		return "files in " + s.dir
	}
	return "the system keychain (or " + s.dir + " if unavailable)"
}

func (s *Store) Get(slot string) string {
	if !slotRE.MatchString(slot) {
		return ""
	}
	s.mu.Lock()
	if c, ok := s.cache[slot]; ok && time.Since(c.at) < time.Minute {
		s.mu.Unlock()
		return c.v
	}
	s.mu.Unlock()
	v := s.read(slot)
	s.mu.Lock()
	s.cache[slot] = cached{v, time.Now()}
	s.mu.Unlock()
	return v
}

func (s *Store) read(slot string) string {
	if !s.noChain {
		if v, err := keyring.Get(config.AppName, slot); err == nil && v != "" {
			return v
		}
		// saved under an earlier app name → move it
		for _, l := range config.Legacy {
			if v, err := keyring.Get(l.Name, slot); err == nil && v != "" {
				if s.Set(slot, v) == nil {
					_ = keyring.Delete(l.Name, slot)
				}
				return v
			}
		}
	}
	b, err := os.ReadFile(s.file(slot))
	if err != nil {
		// files written by the Python versions on Linux lived directly in the data dir
		if b, err = os.ReadFile(filepath.Join(filepath.Dir(s.dir), legacyFile(slot))); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(string(b))
}

func legacyFile(slot string) string {
	if slot == "unifi-api-key" {
		return "api-key"
	}
	return slot
}

func (s *Store) Set(slot, value string) error {
	if !slotRE.MatchString(slot) {
		return errors.New("bad secret name")
	}
	s.mu.Lock()
	delete(s.cache, slot)
	s.mu.Unlock()
	if !s.noChain {
		if err := keyring.Set(config.AppName, slot, value); err == nil {
			_ = os.Remove(s.file(slot))
			return nil
		}
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return config.WriteFileAtomic(s.file(slot), []byte(value), 0o600)
}

func (s *Store) Delete(slot string) {
	if !slotRE.MatchString(slot) {
		return
	}
	s.mu.Lock()
	delete(s.cache, slot)
	s.mu.Unlock()
	if !s.noChain {
		_ = keyring.Delete(config.AppName, slot)
	}
	_ = os.Remove(s.file(slot))
}

func (s *Store) file(slot string) string { return filepath.Join(s.dir, slot) }
