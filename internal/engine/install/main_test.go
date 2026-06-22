package install

import (
	"os"
	"testing"
)

// TestMain points HOME at a throwaway dir for the whole package so Apply's receipt write
// (receipt.DefaultPath under ~/.config/safeslop) never touches the developer's real home. Individual
// tests that assert on the receipt set dirs.ReceiptPath (or t.Setenv("HOME", ...)) explicitly.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "safeslop-install-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
