package sandbox

import (
	"strings"
	"testing"
)

func TestProfileAllowsToolchainStores(t *testing.T) {
	p := Profile("/tmp/ws", "deny", "", Scope{})
	for _, want := range []string{`(subpath "/nix")`, "mise"} {
		if !strings.Contains(p, want) {
			t.Fatalf("seatbelt profile missing toolchain read %q:\n%s", want, p)
		}
	}
}
