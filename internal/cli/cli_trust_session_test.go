package cli

import (
	"os"
	"path/filepath"
	"testing"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

// TestSessionCreateAdhocHostRequiresTrustAck pins the ad-hoc arm of 0070 B1 (specs/0072 F1):
// `session create --agent … --environment host` has no safeslop.cue to approve, so it must
// refuse without an explicit --trust-host acknowledgement that the agent runs unconfined.
func TestSessionCreateAdhocHostRequiresTrustAck(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "host",
	)
	if err == nil {
		t.Fatalf("ad-hoc host session without --trust-host unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 || env.Errors[0].Code != jsoncontract.CodeTrustRequired {
		t.Fatalf("expected CodeTrustRequired error envelope, got: %+v", env)
	}
}

// TestSessionCreateAdhocHostWithTrustAck proves the ack flag lets the ad-hoc host session
// through — the gate is a comprehension checkpoint, not a hard block.
func TestSessionCreateAdhocHostWithTrustAck(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "host", "--trust-host",
	)
	if err != nil {
		t.Fatalf("ad-hoc host session with --trust-host failed: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("expected ok envelope, got: %+v", env.Errors)
	}
}

// TestSessionCreateAdhocContainerUngated proves the trust ack is a host-only concern: a
// container ad-hoc session (the Emacs cockpit default) is unaffected by 0072.
func TestSessionCreateAdhocContainerUngated(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "container",
	)
	if err != nil {
		t.Fatalf("ad-hoc container session failed: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("expected ok envelope, got: %+v", env.Errors)
	}
}

// TestSessionCreateFromProfileRefusesUntrusted pins the core of 0070 B1 (specs/0072 F1):
// creating a session from a profile whose safeslop.cue is not host-approved is refused with
// CodeTrustRequired — the Emacs cockpit launches on this lane, which previously skipped the gate.
func TestSessionCreateFromProfileRefusesUntrusted(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // trust store isolated + empty -> the fixture is untrusted
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		dev: {
			agent: "claude"
			environment: "container"
			network: "deny"
			workspace: "."
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatalf("write safeslop.cue: %v", err)
	}

	out, err := runRootForTest(t, ws, "session", "create", "--profile", "dev", "--output", "json")
	if err == nil {
		t.Fatalf("untrusted profile session unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 || env.Errors[0].Code != jsoncontract.CodeTrustRequired {
		t.Fatalf("expected CodeTrustRequired error envelope, got: %+v", env)
	}
}

// TestVerifySessionTrustDetectsDrift pins the run-time re-verify (specs/0072 F1, closing 0070 B3):
// session run/supervise rebuild the profile from the record, so verifySessionTrust must refuse a
// session whose policy was edited (or re-trusted to different bytes) since create.
func TestVerifySessionTrustDetectsDrift(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cuePath := filepath.Join(t.TempDir(), "safeslop.cue")
	if err := os.WriteFile(cuePath, []byte(`profiles: { dev: { agent: "claude" } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := enforceTrust(cuePath, true); err != nil {
		t.Fatalf("approve fixture: %v", err)
	}
	abs, hash, status, err := checkTrust(cuePath)
	if err != nil || status.String() != "trusted" {
		t.Fatalf("checkTrust after approve = (%q, %v), want trusted", status, err)
	}
	sess := engsession.Session{PolicyPath: abs, PolicyHash: hash}

	// Approved, unchanged -> passes.
	if err := verifySessionTrust(sess); err != nil {
		t.Fatalf("trusted, unchanged session must pass: %v", err)
	}

	// Policy edited after create, not re-trusted -> Changed -> refuse.
	if err := os.WriteFile(cuePath, []byte(`profiles: { dev: { network: "allow" } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySessionTrust(sess); err == nil {
		t.Fatal("edited-since-create policy must be refused")
	}

	// Edited policy re-trusted to NEW bytes -> Trusted but hash != recorded -> still refuse,
	// because the session captured the old profile.
	if err := enforceTrust(cuePath, true); err != nil {
		t.Fatalf("re-approve changed fixture: %v", err)
	}
	if err := verifySessionTrust(sess); err == nil {
		t.Fatal("session recorded against old bytes must be refused after re-trust to new bytes")
	}
}
