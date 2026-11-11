package cloudflare

import (
	"net"
	"testing"
)

func TestPublicPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":               "203.0.113.7",
		"192.168.1.10":              "",
		"100.72.1.2":                "", // the provider's NAT
		"fe80::1":                   "",
		"fd7a:115c:a1e0::1":         "", // private (ULA)
		"2001:db8:1234:5678:9::abc": "2001:db8:1234:5600::/56",
	} {
		if got := PublicPrefix(net.ParseIP(in)); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
	r := RedirectRule("homeowl_wifi", "wifi.example.com", "http://192.168.1.10:8555/wifi", []string{"203.0.113.7", "2001:db8:1234:5600::/56"})
	if r.Expression != `(http.host eq "wifi.example.com" and ip.src in {203.0.113.7 2001:db8:1234:5600::/56})` {
		t.Fatal(r.Expression)
	}
}
