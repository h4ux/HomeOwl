package server

import (
	"testing"

	"github.com/h4ux/homeowl/internal/config"
)

func TestTailscaleTags(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "",
		"homeowl":                          "tag:homeowl",
		"tag:homeowl, server; tag:homeowl": "tag:homeowl,tag:server",
	} {
		if got, err := tsTags(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"tag:", "tag:-x", "tag:a b!", "x,y,z,a,b,c,d,e,f"} {
		if _, err := tsTags(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if l := (config.Tailscale{Tags: "tag:a,tag:b"}).TagList(); len(l) != 2 || l[1] != "tag:b" {
		t.Fatalf("list: %v", l)
	}
	if l := (config.Tailscale{}).TagList(); l != nil {
		t.Fatalf("none: %v", l)
	}
}
