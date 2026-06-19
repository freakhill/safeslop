package install

import (
	"fmt"
	"regexp"
)

// Pin is one tool's pinned desired-state entry. Plan diffs the live Status against these; apply
// (SP7b-3) downloads URL, verifies SHA256, installs Version. The manifest is fail-closed: every
// field is mandatory and Version is never "latest" (specs/0012 §5).
type Pin struct {
	Name    string `json:"name"`    // matches Tool.Name from Status (e.g. "mise", "tart")
	Kind    string `json:"kind"`    // "toolchain" | "runtime" — informs apply's provisioner
	Version string `json:"version"` // exact pinned version, never "latest"
	SHA256  string `json:"sha256"`  // sha256 of the darwin-arm64 artifact (provenance)
	URL     string `json:"url"`     // download source for that artifact
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateDesired enforces the fail-closed contract: every pin is fully specified and exact. An
// invalid manifest is an error, never a silent skip (specs/0012 §5: "fails closed").
func ValidateDesired(pins []Pin) error {
	seen := map[string]bool{}
	for _, p := range pins {
		if p.Name == "" {
			return fmt.Errorf("install: pin with empty name")
		}
		if seen[p.Name] {
			return fmt.Errorf("install: duplicate pin %q", p.Name)
		}
		seen[p.Name] = true
		if p.Kind != "toolchain" && p.Kind != "runtime" {
			return fmt.Errorf("install: pin %q has invalid kind %q (want toolchain|runtime)", p.Name, p.Kind)
		}
		if p.Version == "" || p.Version == "latest" {
			return fmt.Errorf("install: pin %q must declare an exact version, got %q", p.Name, p.Version)
		}
		if !sha256Re.MatchString(p.SHA256) {
			return fmt.Errorf("install: pin %q must declare a 64-hex sha256", p.Name)
		}
		if p.URL == "" {
			return fmt.Errorf("install: pin %q must declare a source url", p.Name)
		}
	}
	return nil
}
