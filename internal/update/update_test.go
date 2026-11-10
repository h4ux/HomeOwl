package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, cur string
		want        bool
	}{
		{"v0.1.2", "v0.1.1", true}, {"v0.1.1", "v0.1.1", false}, {"v0.1.1", "v0.1.2", false},
		{"v0.2.0", "v0.1.9", true}, {"v1.0.0", "v0.99.99", true}, {"v0.1.10", "v0.1.9", true},
		{"v0.1.2", "v0.1.1-3-gabc1234", true}, {"v0.1.1", "v0.1.1-3-gabc1234-dirty", false},
		{"v0.1.2", "dev", false}, {"nightly", "v0.1.1", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.cur); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.cur, got, c.want)
		}
	}
}

func tgz(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		n string
		b []byte
	}{{"README.md", []byte("hi")}, {name, body}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.n, Mode: 0o755, Size: int64(len(f.b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(f.b)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestExtract(t *testing.T) {
	bin := []byte("program")
	got, err := Extract(tgz(t, "homeowl", bin), "linux")
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("tar.gz: %q %v", got, err)
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("homeowl.exe")
	_, _ = w.Write(bin)
	_ = zw.Close()
	got, err = Extract(zb.Bytes(), "windows")
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("zip: %q %v", got, err)
	}
	if _, err := Extract(tgz(t, "other", bin), "linux"); err == nil {
		t.Fatal("expected an error when the program is missing")
	}
}

func TestDownloadChecksAndReplace(t *testing.T) {
	name := Asset(runtime.GOOS, runtime.GOARCH)
	bin := bytes.Repeat([]byte("x"), 2<<20)
	var arc []byte
	if runtime.GOOS == "windows" {
		var zb bytes.Buffer
		zw := zip.NewWriter(&zb)
		w, _ := zw.Create("homeowl.exe")
		_, _ = w.Write(bin)
		_ = zw.Close()
		arc = zb.Bytes()
	} else {
		arc = tgz(t, "homeowl", bin)
	}
	sum := sha256.Sum256(arc)
	good := hex.EncodeToString(sum[:])
	sums := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprint(w, `{"tag_name":"v9.9.9","html_url":"https://example.invalid/r","body":"notes"}`)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", sums, name, strings.Repeat("0", 64))
		case strings.HasSuffix(r.URL.Path, "/"+name):
			_, _ = w.Write(arc)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	APIBase, DownloadBase = srv.URL, srv.URL
	ctx := context.Background()

	c := &Checker{Current: "v0.1.1"}
	if st := c.Check(ctx); !st.Available || st.Latest != "v9.9.9" || st.Notes != "notes" {
		t.Fatalf("check: %+v", st)
	}
	if _, err := Download(ctx, "v9.9.9"); err != nil {
		t.Fatalf("download: %v", err)
	}
	sums = strings.Repeat("a", 64)
	if _, err := Download(ctx, "v9.9.9"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a wrong checksum must be refused, got %v", err)
	}
	if _, err := Download(ctx, "../../evil"); err == nil {
		t.Fatal("a bad tag must be refused")
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "homeowl")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	if err := Replace(exe, bin); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	fi, _ := os.Stat(exe)
	if !bytes.Equal(got, bin) || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0) {
		t.Fatal("program not replaced or not executable")
	}
}
