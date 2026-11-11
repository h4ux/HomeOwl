package analytics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/h4ux/homeowl/internal/config"
)

// NextDNS "Rewrites" (a DNS name answered with an address of your choice) — used for HomeOwl's home DNS names
// (Settings › Access), added to every profile of every NextDNS account HomeOwl has a key for.

type NDRewrite struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ndSend makes one write request with the key in slot.
func (e *Engine) ndSend(method, path string, body any, slot string) (map[string]any, error) {
	key := e.sec.Get(slot)
	if key == "" {
		return nil, errors.New("no NextDNS API key saved")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, nextdnsAPI+path, rd)
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", config.AppSlug+"/"+config.Version)
	resp, err := ndHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach the NextDNS API (%v)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("NextDNS API %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if errs := anyList(out["errors"]); len(errs) > 0 {
		return nil, fmt.Errorf("NextDNS: %s", orS(str(errs[0]["detail"]), str(errs[0]["code"]), "error"))
	}
	return out, nil
}

// NDAccountProfiles lists the profile IDs of every saved NextDNS account (slot → profiles), and the accounts
// that couldn't be read.
func (e *Engine) NDAccountProfiles() (map[string][]NDProfile, map[string]string) {
	out, errs := map[string][]NDProfile{}, map[string]string{}
	for _, slot := range e.NDSlots() {
		j, err := e.ndGet("/profiles", nil, "", slot)
		if err != nil {
			errs[slot] = err.Error()
			continue
		}
		for _, x := range anyList(j["data"]) {
			if id := str(x["id"]); id != "" {
				out[slot] = append(out[slot], NDProfile{ID: id, Name: orS(str(x["name"]), id), Account: slot})
			}
		}
	}
	return out, errs
}

func (e *Engine) NDRewrites(slot, profile string) ([]NDRewrite, error) {
	j, err := e.ndGet("/profiles/"+url.PathEscape(profile)+"/rewrites", nil, "", slot)
	if err != nil {
		return nil, err
	}
	var out []NDRewrite
	for _, x := range anyList(j["data"]) {
		out = append(out, NDRewrite{ID: str(x["id"]), Name: str(x["name"]), Content: str(x["content"])})
	}
	return out, nil
}

func (e *Engine) NDAddRewrite(slot, profile, name, content string) (string, error) {
	j, err := e.ndSend("POST", "/profiles/"+url.PathEscape(profile)+"/rewrites", map[string]string{"name": name, "content": content}, slot)
	if err != nil {
		return "", err
	}
	d, _ := j["data"].(map[string]any)
	return str(d["id"]), nil
}

func (e *Engine) NDDeleteRewrite(slot, profile, id string) error {
	_, err := e.ndSend("DELETE", "/profiles/"+url.PathEscape(profile)+"/rewrites/"+url.PathEscape(id), nil, slot)
	return err
}

// SetNextDNSAPI points the NextDNS client at another address (tests).
func SetNextDNSAPI(u string) { nextdnsAPI = u }
