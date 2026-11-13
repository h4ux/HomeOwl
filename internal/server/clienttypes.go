package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/h4ux/homeowl/internal/config"
)

// Client types: what kind of thing a network client is (phone, TV, smart plug…), chosen by an admin when the
// guess from its name and vendor is wrong. It sets the icon everywhere. UniFi has no simple field for this, so
// HomeOwl keeps it (client-types.json); the name and note go to UniFi itself (actions.go).

var clientTypeKinds = map[string]bool{"phone": true, "tablet": true, "laptop": true, "desktop": true, "tv": true, "speaker": true,
	"console": true, "watch": true, "printer": true, "nas": true, "server": true, "plug": true, "bulb": true, "thermostat": true,
	"vacuum": true, "camera": true, "doorbell": true, "ups": true, "iot": true, "router": true, "car": true, "ap": true, "switch": true}

type clientTypes struct {
	mu   sync.Mutex
	path string
}

func (c *clientTypes) All() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load()
}

func (c *clientTypes) load() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// Set gives the client a type ("" = back to HomeOwl's guess). keep=true leaves an existing choice alone.
func (c *clientTypes) Set(mac, kind string, keep bool) error {
	mac = strings.ToLower(mac)
	if !macRE.MatchString(mac) {
		return errors.New("that isn't a MAC address")
	}
	if kind != "" && !clientTypeKinds[kind] {
		return errors.New("unknown device type")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.load()
	if keep && m[mac] != "" {
		return nil
	}
	if kind == "" {
		delete(m, mac)
	} else {
		m[mac] = kind
	}
	if len(m) > 5000 {
		return errors.New("too many device types saved")
	}
	b, _ := json.Marshal(m)
	return config.WriteFileAtomic(c.path, b, 0o600)
}

func newClientTypes(dataDir string) *clientTypes {
	return &clientTypes{path: filepath.Join(dataDir, "client-types.json")}
}

// checkinType: the type a family member picked when checking the device in.
var checkinType = map[string]string{"phone": "phone", "tablet": "tablet", "laptop": "laptop", "computer": "desktop", "tv": "tv",
	"console": "console", "speaker": "speaker", "watch": "watch", "camera": "camera", "printer": "printer"}
