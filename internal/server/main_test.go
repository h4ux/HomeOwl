package server

import (
	"os"
	"testing"
)

// Tests keep secrets in their temporary data folders: the system keychain is shared with a real HomeOwl on this computer.
func TestMain(m *testing.M) {
	os.Setenv("HOMEOWL_NO_KEYCHAIN", "1")
	os.Exit(m.Run())
}
