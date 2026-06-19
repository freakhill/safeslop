package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnforceTrustGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // trust store -> {home}/.config/safeslop/trust.json
	pol := filepath.Join(t.TempDir(), "safeslop.cue")
	if err := os.WriteFile(pol, []byte("profiles: { dev: { agent: \"claude\" } }"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. fresh policy is untrusted -> blocked
	if err := enforceTrust(pol, false); err == nil {
		t.Fatal("untrusted policy must block run (fail-closed)")
	}
	// 2. --trust approves and proceeds
	if err := enforceTrust(pol, true); err != nil {
		t.Fatalf("--trust must approve: %v", err)
	}
	// 3. now trusted -> proceeds
	if err := enforceTrust(pol, false); err != nil {
		t.Fatalf("approved policy must pass: %v", err)
	}
	// 4. policy changes -> blocked again (agent-rewrite case)
	if err := os.WriteFile(pol, []byte("profiles: { dev: { network: \"allow\" } }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := enforceTrust(pol, false); err == nil {
		t.Fatal("a changed policy must block run until re-trusted")
	}
}
