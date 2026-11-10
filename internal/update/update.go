// Package update checks GitHub for a newer HomeOwl release and, when the program file is writable,
// replaces it with the verified download (SHA256SUMS from the same release).
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Repo = "h4ux/HomeOwl"

// APIBase and DownloadBase can be pointed at a test server.
var (
	APIBase      = "https://api.github.com"
	DownloadBase = "https://github.com"
)

// Release is the part of a GitHub release HomeOwl needs.
type Release struct {
	Tag       string `json:"tag_name"`
	Name      string `json:"name"`
	URL       string `json:"html_url"`
	Notes     string `json:"body"`
	Published string `json:"published_at"`
	Draft     bool   `json:"draft"`
	Pre       bool   `json:"prerelease"`
}

// Version is a parsed vX.Y.Z[-N-gHASH][-dirty] (git describe) string.
type Version struct {
	Major, Minor, Patch int
	Ahead               int // commits after the tag (development builds)
	OK                  bool
}

var verRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$`)

func Parse(s string) Version {
	m := verRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	n, _ := strconv.Atoi(m[4])
	return Version{a, b, c, n, true}
}

// Newer reports whether release tag `latest` is newer than the running version `cur`. A development build
// (commits after a tag) counts as newer than that tag, so it is only offered the next release.
func Newer(latest, cur string) bool {
	l, c := Parse(latest), Parse(cur)
	if !l.OK || !c.OK {
		return false
	}
	if l.Major != c.Major {
		return l.Major > c.Major
	}
	if l.Minor != c.Minor {
		return l.Minor > c.Minor
	}
	return l.Patch > c.Patch
}

// Asset is the release archive name for this system.
func Asset(goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("homeowl_%s_%s.zip", goos, goarch)
	}
	return fmt.Sprintf("homeowl_%s_%s.tar.gz", goos, goarch)
}

// Commands are what a person can run to update by hand.
func Commands(goos string, writable bool) []string {
	if goos == "windows" {
		return []string{"homeowl update", "irm https://github.com/" + Repo + "/releases/latest/download/install.ps1 | iex"}
	}
	up := "sudo homeowl update"
	if writable {
		up = "homeowl update"
	}
	return []string{up, "curl -fsSL https://github.com/" + Repo + "/releases/latest/download/install.sh | sh"}
}

func client() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

func get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "HomeOwl-updater")
	if strings.HasPrefix(url, APIBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	res, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: too large", url)
	}
	return b, nil
}

// Latest asks GitHub for the newest published release.
func Latest(ctx context.Context) (*Release, error) {
	b, err := get(ctx, APIBase+"/repos/"+Repo+"/releases/latest", 2<<20)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Tag == "" || !Parse(r.Tag).OK {
		return nil, errors.New("GitHub returned no usable release")
	}
	return &r, nil
}

// Executable is the real path of the running program.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Writable reports whether this process may replace its own program file (needs write access to the folder).
func Writable() bool {
	exe, err := Executable()
	if err != nil || strings.Contains(exe, string(filepath.Separator)+"go-build") {
		return false // `go run` build
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".homeowl-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// Download fetches the release archive for this system and checks it against the release's SHA256SUMS.
func Download(ctx context.Context, tag string) ([]byte, error) {
	if !Parse(tag).OK {
		return nil, errors.New("bad release tag")
	}
	name := Asset(runtime.GOOS, runtime.GOARCH)
	base := DownloadBase + "/" + Repo + "/releases/download/" + tag + "/"
	sums, err := get(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("checksums: %w", err)
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return nil, fmt.Errorf("the release has no checksum for %s", name)
	}
	arc, err := get(ctx, base+name, 300<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(arc)
	if hex.EncodeToString(sum[:]) != want {
		return nil, errors.New("checksum mismatch — the download is damaged or was tampered with")
	}
	return arc, nil
}

// Extract returns the homeowl program from a release archive.
func Extract(arc []byte, goos string) ([]byte, error) {
	want := "homeowl"
	if goos == "windows" {
		want = "homeowl.exe"
		zr, err := zip.NewReader(bytes.NewReader(arc), int64(len(arc)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 500<<20))
			}
		}
		return nil, errors.New("homeowl.exe not found in the download")
	}
	gz, err := gzip.NewReader(bytes.NewReader(arc))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, errors.New("homeowl not found in the download")
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == want {
			return io.ReadAll(io.LimitReader(tr, 500<<20))
		}
	}
}

// Replace swaps the program file for bin. The old file is kept as <exe>.old until the next update
// (Windows can't delete a running program, but it can rename it).
func Replace(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".homeowl-new-*")
	if err != nil {
		return fmt.Errorf("can't write to %s: %w", dir, err)
	}
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	_ = tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		_ = os.Rename(old, exe)
		os.Remove(tmp.Name())
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(old)
	}
	return nil
}

// Install downloads, verifies and installs release tag over the running program.
func Install(ctx context.Context, tag string) error {
	exe, err := Executable()
	if err != nil {
		return err
	}
	arc, err := Download(ctx, tag)
	if err != nil {
		return err
	}
	bin, err := Extract(arc, runtime.GOOS)
	if err != nil {
		return err
	}
	if len(bin) < 1<<20 {
		return errors.New("the downloaded program looks too small")
	}
	return Replace(exe, bin)
}

// ------------------------------------------------------------------ checker

// Status is what the UI shows.
type Status struct {
	Current    string   `json:"current"`
	Latest     string   `json:"latest,omitempty"`
	Available  bool     `json:"available"`
	URL        string   `json:"url,omitempty"`
	Notes      string   `json:"notes,omitempty"`
	Published  string   `json:"published,omitempty"`
	Checked    int64    `json:"checked,omitempty"`
	Error      string   `json:"error,omitempty"`
	CanApply   bool     `json:"can_apply"`
	Commands   []string `json:"commands"`
	DevBuild   bool     `json:"dev_build"`
	Installing bool     `json:"installing"`
}

// Checker remembers the last check.
type Checker struct {
	Current string
	mu      sync.Mutex
	st      Status
	busy    bool
}

func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st
	st.Current = c.Current
	st.DevBuild = !Parse(c.Current).OK || Parse(c.Current).Ahead > 0
	w := Writable()
	st.CanApply = w && st.Available
	st.Commands = Commands(runtime.GOOS, w)
	st.Installing = c.busy
	return st
}

func (c *Checker) Check(ctx context.Context) Status {
	r, err := Latest(ctx)
	c.mu.Lock()
	c.st.Checked = time.Now().Unix()
	if err != nil {
		c.st.Error = err.Error()
	} else {
		c.st = Status{Latest: r.Tag, URL: r.URL, Notes: r.Notes, Published: r.Published, Checked: c.st.Checked,
			Available: Newer(r.Tag, c.Current)}
	}
	c.mu.Unlock()
	return c.Status()
}

// Apply installs the latest known release. Only one install runs at a time.
func (c *Checker) Apply(ctx context.Context) error {
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return errors.New("an update is already being installed")
	}
	tag, avail := c.st.Latest, c.st.Available
	c.busy = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.busy = false; c.mu.Unlock() }()
	if !avail {
		return errors.New("no newer version to install — check for updates first")
	}
	if !Writable() {
		return errors.New("HomeOwl can't replace its own program file here; run the update command instead")
	}
	return Install(ctx, tag)
}
