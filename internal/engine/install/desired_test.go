package install

import "testing"

// The embedded manifest must always satisfy the fail-closed contract, empty or populated. This is
// the Go-manifest equivalent of the slop-pinning gate (which only scans *.cue): a bad pin breaks
// the build, never ships a "latest" or an unchecksummed artifact.
func TestDesiredStateIsFailClosed(t *testing.T) {
	if err := ValidateDesired(DesiredState()); err != nil {
		t.Fatalf("the embedded desired-state manifest must be fail-closed valid: %v", err)
	}
}
