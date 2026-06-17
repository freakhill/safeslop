package cli

import (
	"os"
	"path/filepath"
	"testing"
)

const riskyCue = `package slop
slop: {
	version: 1
	profiles: risky: {
		agent: "claude"
		environment: "sandbox"
		network: "allow"
		secrets: {ANTHROPIC_API_KEY: "env:ANTHROPIC_API_KEY"}
	}
}
`

func TestValidateAndLintSurfacesWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(path, []byte(riskyCue), 0o644); err != nil {
		t.Fatal(err)
	}
	warns, err := validateAndLint(path)
	if err != nil {
		t.Fatalf("validateAndLint: %v", err)
	}
	if len(warns) != 1 || warns[0].Code != "sandbox-open-egress-with-creds" || warns[0].Profile != "risky" {
		t.Fatalf("expected the exfil warning, got %+v", warns)
	}
}
