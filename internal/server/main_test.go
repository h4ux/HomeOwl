package server

import (
	"context"
	"os"
	"testing"
)

// Tests keep secrets in their temporary data folders: the system keychain is shared with a real HomeOwl on this computer.
// Devices are never asked for their model over the network (their addresses are made up).
func TestMain(m *testing.M) {
	os.Setenv("HOMEOWL_NO_KEYCHAIN", "1")
	deviceModel = func(context.Context, string) string { return "" }
	os.Exit(m.Run())
}
