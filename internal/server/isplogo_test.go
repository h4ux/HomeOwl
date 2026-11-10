package server

import (
	"net"
	"net/url"
	"reflect"
	"testing"
)

func TestSiteHost(t *testing.T) {
	ok := map[string]string{"example.net": "example.net", "https://www.Example.net/about": "www.example.net", " example.co.uk. ": "example.co.uk"}
	for in, want := range ok {
		if got, err := siteHost(in); err != nil || got != want {
			t.Errorf("siteHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "192.168.1.1", "http://10.0.0.1/", "localhost", "[::1]", "exa mple.net"} {
		if got, err := siteHost(in); err == nil {
			t.Errorf("siteHost(%q) = %q, want an error", in, got)
		}
	}
}

func TestPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{"192.168.1.10": false, "10.1.2.3": false, "127.0.0.1": false, "100.64.0.1": false,
		"169.254.1.1": false, "::1": false, "fd00::1": false, "203.0.113.5": true, "2001:db8::1": true} {
		if got := publicIP(net.ParseIP(ip)); got != want {
			t.Errorf("publicIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestIconCandidates(t *testing.T) {
	page := []byte(`<html><head>
<link rel="stylesheet" href="/a.css">
<link rel="icon" type="image/png" href="/favicon-32.png">
<link href='https://cdn.example.net/touch.png' rel='apple-touch-icon'>
<link rel="mask-icon" href="/mask.svg"><link rel="icon" href="/logo.svg"><link rel="icon" href="data:image/png;base64,AAAA">
<link rel=shortcut\ icon href=/old.ico>
</head></html>`)
	base, _ := url.Parse("https://www.example.net/home/")
	got := iconCandidates(page, base)
	want := []string{"https://cdn.example.net/touch.png", "https://www.example.net/favicon-32.png"}
	if !reflect.DeepEqual(got[:2], want) {
		t.Fatalf("iconCandidates = %v, want first %v", got, want)
	}
	for _, u := range got {
		if u == "https://www.example.net/logo.svg" || u == "https://www.example.net/mask.svg" {
			t.Fatalf("SVG icons must be skipped: %v", got)
		}
	}
}
