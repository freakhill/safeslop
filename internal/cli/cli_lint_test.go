package cli

import (
	"os"
	"path/filepath"
	"testing"
)

const riskyCue = `package safeslop
safeslop: {
	version: 1
	profiles: risky: {
		agent: "claude"
		environment: "container"
		network: "allow"
		egress: ["evil.example.com"]
	}
}
`

func TestValidateAndLintSurfacesWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(riskyCue), 0o644); err != nil {
		t.Fatal(err)
	}
	warns, err := validateAndLint(path)
	if err != nil {
		t.Fatalf("validateAndLint: %v", err)
	}
	if len(warns) != 1 || warns[0].Code != "egress-ignored" || warns[0].Profile != "risky" {
		t.Fatalf("expected the egress-ignored warning, got %+v", warns)
	}
}
