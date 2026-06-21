package buildinfo

import "testing"

// A plain `go test` / `make build` binary is NOT notarized, so Notarized must be false by default. This
// locks the honesty fix: the cockpit's "notarized binary" root-of-trust wording is only emitted when the
// release build flips Release=true via -ldflags (make sign). A regression that defaulted it true would
// make every dev build claim a notarization it doesn't have.
func TestNotNotarizedByDefault(t *testing.T) {
	if Notarized() {
		t.Fatal("a non-release build must report Notarized()==false (Release defaults to \"false\")")
	}
}
