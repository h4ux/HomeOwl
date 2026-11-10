// Package config holds HomeOwl's settings, data-directory layout and migration from earlier app names.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	AppName = "HomeOwl"
	AppSlug = "homeowl"
)

// Version is set at build time with -ldflags "-X github.com/h4ux/homeowl/internal/config.Version=…".
var Version = "dev"

// Legacy names this app had before; their data folders are moved over on first start.
var Legacy = []struct{ Name, Slug string }{{"Big Mama", "big-mama"}, {"UniFi Audit", "unifi-audit"}}

type NextDNSAccount struct {
	Slot  string `json:"slot"`
	Label string `json:"label"`
}

type Analytics struct {
	Enabled         bool             `json:"enabled"`
	NextDNSProfiles []string         `json:"nextdns_profiles"`
	NextDNSAll      bool             `json:"nextdns_all"`
	NextDNSAccounts []NextDNSAccount `json:"nextdns_accounts"`
	NextDNSRaw      bool             `json:"nextdns_raw"`
	SyslogEnabled   bool             `json:"syslog_enabled"`
	SyslogPort      int              `json:"syslog_port"`
	SyslogSources   []string         `json:"syslog_sources"`
	RawDays         int              `json:"raw_days"`
	BackfillDays    int              `json:"backfill_days"`
	Timezone        string           `json:"timezone"`
}

type LAN struct {
	Enabled   bool   `json:"enabled"`    // listen on the home network (false = this computer only)
	Port      int    `json:"port"`       // one port serves HTTPS and plain HTTP (plain HTTP only from this computer)
	TLS       bool   `json:"tls"`        // self-signed HTTPS for other devices
	ExtraHost string `json:"extra_host"` // extra hostname allowed in the Host header (e.g. a DNS name you gave it)
}

type Tailscale struct {
	Enabled   bool   `json:"enabled"`    // run HomeOwl as its own device on your tailnet
	Hostname  string `json:"hostname"`   // name on the tailnet → https://<hostname>.<tailnet>.ts.net
	AutoLogin bool   `json:"auto_login"` // a tailnet login linked to a user signs in without a password
}

type MCPToken struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hash     string `json:"hash"`
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used"`
}

type MCP struct {
	Mode   string     `json:"mode"` // off | read | guarded | god
	Tokens []MCPToken `json:"tokens"`
}

type Updates struct {
	Check bool `json:"check"` // ask GitHub once in a while whether a newer release exists
}

// Access is UniFi Access (doors). Its API token lives in the secrets store.
type Access struct {
	Host   string `json:"host,omitempty"` // https://host:port when Access isn't on the UniFi console at port 12445
	Unlock bool   `json:"unlock"`         // admins may unlock doors remotely (guarded, logged)
}

type Cameras struct {
	Viewers bool `json:"viewers"` // viewers may see camera pictures and detections too (otherwise admins only)
}

type Guest struct {
	SSIDs []string `json:"ssids"` // Wi-Fi ids shown on the Guest Wi-Fi page besides UniFi's guest networks
}

type Config struct {
	Host           string    `json:"host"`
	Site           string    `json:"site"`
	VerifyTLS      bool      `json:"verify_tls"`
	AutoConnect    bool      `json:"auto_connect"`
	RefreshMinutes int       `json:"refresh_minutes"`
	LAN            LAN       `json:"lan"`
	Tailscale      Tailscale `json:"tailscale"`
	MCP            MCP       `json:"mcp"`
	Analytics      Analytics `json:"analytics"`
	Updates        Updates   `json:"updates"`
	Guest          Guest     `json:"guest"`
	Cameras        Cameras   `json:"cameras"`
	Access         Access    `json:"access"`
	// Tailscale logins that were admins/viewers in older versions — offered when creating users.
	LegacyAdmins  []string `json:"admins,omitempty"`
	LegacyViewers []string `json:"viewers,omitempty"`
}

func Defaults() Config {
	return Config{
		RefreshMinutes: 5,
		LAN:            LAN{Enabled: true, Port: 8555, TLS: true},
		Tailscale:      Tailscale{Hostname: "homeowl", AutoLogin: true},
		MCP:            MCP{Mode: "off"},
		Updates:        Updates{Check: true},
		Analytics: Analytics{NextDNSAll: true, SyslogPort: 5514, RawDays: 90, BackfillDays: 90,
			NextDNSProfiles: []string{}, SyslogSources: []string{}},
	}
}

// Store guards the config and saves it atomically.
type Store struct {
	mu   sync.RWMutex
	cfg  Config
	path string
}

func Open(dir string) (*Store, error) {
	s := &Store{cfg: Defaults(), path: filepath.Join(dir, "config.json")}
	b, err := os.ReadFile(s.path)
	if err == nil {
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	c := &s.cfg
	if c.RefreshMinutes <= 0 {
		c.RefreshMinutes = 5
	}
	if c.LAN.Port == 0 {
		c.LAN.Port = 8555
	}
	if c.Tailscale.Hostname == "" {
		c.Tailscale.Hostname = "homeowl"
	}
	if c.MCP.Mode == "" {
		c.MCP.Mode = "off"
	}
	if c.Analytics.SyslogPort == 0 {
		c.Analytics.SyslogPort = 5514
	}
	if c.Analytics.RawDays == 0 {
		c.Analytics.RawDays = 90
	}
	if c.Analytics.BackfillDays == 0 {
		c.Analytics.BackfillDays = 90
	}
	if c.Analytics.NextDNSProfiles == nil {
		c.Analytics.NextDNSProfiles = []string{}
	}
	if c.Analytics.SyslogSources == nil {
		c.Analytics.SyslogSources = []string{}
	}
	if len(b) > 0 && !strings.Contains(string(b), `"nextdns_all"`) {
		c.Analytics.NextDNSAll = true
	}
	if len(b) > 0 && !strings.Contains(string(b), `"lan"`) {
		c.LAN = LAN{Enabled: true, Port: 8555, TLS: true}
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Analytics.NextDNSProfiles = append([]string{}, s.cfg.Analytics.NextDNSProfiles...)
	c.Analytics.NextDNSAccounts = append([]NextDNSAccount{}, s.cfg.Analytics.NextDNSAccounts...)
	c.Analytics.SyslogSources = append([]string{}, s.cfg.Analytics.SyslogSources...)
	c.MCP.Tokens = append([]MCPToken{}, s.cfg.MCP.Tokens...)
	c.Guest.SSIDs = append([]string{}, s.cfg.Guest.SSIDs...)
	return c
}

// Update changes the config under lock and saves it.
func (s *Store) Update(fn func(c *Config)) error {
	s.mu.Lock()
	fn(&s.cfg)
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return WriteFileAtomic(s.path, b, 0o600)
}

func (s *Store) Path() string { return s.path }

// WriteFileAtomic writes via a temp file + rename so a crash never leaves half a file.
func WriteFileAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DataDir returns where HomeOwl keeps its files: $HOMEOWL_DATA, or the OS's per-user config folder.
// On first start it moves the folder of an earlier app name (Big Mama / UniFi Audit) into place.
func DataDir(override string) (string, error) {
	if override == "" {
		override = os.Getenv("HOMEOWL_DATA")
	}
	if override != "" {
		return override, os.MkdirAll(override, 0o700)
	}
	dir := userDir(AppName, AppSlug)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		for _, l := range Legacy {
			old := userDir(l.Name, l.Slug)
			if st, err := os.Stat(old); err == nil && st.IsDir() {
				if err := os.Rename(old, dir); err == nil {
					break
				}
			}
		}
	}
	return dir, os.MkdirAll(dir, 0o700)
}

func userDir(name, slug string) string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", name)
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, name)
		}
		return filepath.Join(home, "AppData", "Roaming", name)
	default:
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, slug)
		}
		return filepath.Join(home, ".config", slug)
	}
}

// LogDir is where the service writes its log file.
func LogDir(dataDir string) string {
	if runtime.GOOS == "darwin" && os.Getenv("HOMEOWL_DATA") == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Logs", AppName)
	}
	return filepath.Join(dataDir, "logs")
}
