package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/trust"
)

func TestLoadPolicyForLaunchCarriesExactBytes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	pol := filepath.Join(dir, "safeslop.cue")
	v1 := []byte(`package safeslop

safeslop: {
	version: 1
	profiles: dev: {
		agent: "claude"
		environment: "container"
		network: "deny"
	}
}
`)
	v2 := []byte(`package safeslop

safeslop: {
	version: 1
	profiles: dev: {
		agent: "claude"
		environment: "container"
		network: "allow"
	}
}
`)
	if err := os.WriteFile(pol, v1, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPolicyForLaunch(pol)
	if err != nil {
		t.Fatalf("loadPolicyForLaunch: %v", err)
	}
	if loaded.hash != trust.Hash(v1) {
		t.Fatalf("loaded hash = %s, want hash of original bytes", loaded.hash)
	}
	if loaded.cfg.Profiles["dev"].Network != "deny" {
		t.Fatalf("loaded profile network = %q, want parsed original bytes", loaded.cfg.Profiles["dev"].Network)
	}

	if err := os.WriteFile(pol, v2, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := enforceLoadedPolicyTrust(loaded, true); err != nil {
		t.Fatalf("approve loaded bytes: %v", err)
	}
	if _, _, status, err := checkTrust(pol); err != nil || status != trust.Changed {
		t.Fatalf("current file trust = %v err=%v, want changed after approving loaded bytes", status, err)
	}
	if status, err := loadedPolicyTrustStatus(loaded); err != nil || status != trust.Trusted {
		t.Fatalf("loaded bytes trust = %v err=%v, want trusted", status, err)
	}
}

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
