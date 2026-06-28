package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/jsoncontract"
)

func TestDoctorJSONEmitsContractEnvelope(t *testing.T) {
	ws := t.TempDir()
	out, err := runRootForTest(t, ws, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("doctor returned error envelope: %+v", env.Errors)
	}
	for _, key := range []string{"os", "arch", "tools", "tiers"} {
		if _, ok := env.Data[key]; !ok {
			t.Fatalf("doctor data missing %q: %#v", key, env.Data)
		}
	}
}

func TestValidateJSONEmitsContractEnvelope(t *testing.T) {
	ws := t.TempDir()
	policyPath := filepath.Join(ws, "safeslop.cue")
	if err := os.WriteFile(policyPath, []byte(`package safeslop

safeslop: profiles: default: {
	agent: "claude"
	environment: "container"
	network: "deny"
}
`), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	out, err := runRootForTest(t, ws, "validate", "--json")
	if err != nil {
		t.Fatalf("validate --json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("validate returned error envelope: %+v", env.Errors)
	}
	if env.Data["path"] == "" {
		t.Fatalf("validate data missing path: %#v", env.Data)
	}
	if env.Warnings == nil {
		t.Fatalf("warnings array must be non-nil")
	}
}

func TestValidateWarningJSONUsesContractWarningShape(t *testing.T) {
	ws := t.TempDir()
	policyPath := filepath.Join(ws, "safeslop.cue")
	if err := os.WriteFile(policyPath, []byte(`package safeslop

safeslop: profiles: default: {
	agent: "claude"
	environment: "host"
	network: "allow"
	egress: ["example.com"]
}
`), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	out, err := runRootForTest(t, ws, "validate", "--json")
	if err != nil {
		t.Fatalf("validate --json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK || len(env.Warnings) == 0 {
		t.Fatalf("expected warning envelope: %+v", env)
	}
	if env.Warnings[0].Code != jsoncontract.CodePolicyDenied {
		t.Fatalf("warning code = %q", env.Warnings[0].Code)
	}
}
