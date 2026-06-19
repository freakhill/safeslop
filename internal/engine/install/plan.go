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

// ActionKind is what apply must do to one tool to reach the pinned state.
type ActionKind string

const (
	ActionInstall ActionKind = "install" // tool absent -> fetch + install
	ActionUpgrade ActionKind = "upgrade" // present but not the pinned version -> replace
	ActionOK      ActionKind = "ok"      // present at the pinned version -> no-op
)

// Action is the planned outcome for one pinned tool.
type Action struct {
	Name    string     `json:"name"`
	Kind    ActionKind `json:"kind"`
	Current string     `json:"current,omitempty"` // probed version ("" if absent)
	Desired string     `json:"desired"`           // pinned version
	SHA256  string     `json:"sha256"`            // carried through for apply
	URL     string     `json:"url"`
}

// Result is the ordered plan: one Action per pinned tool, in manifest order.
type Result struct {
	Actions []Action `json:"actions"`
}

// Pending counts the non-ok actions (install + upgrade) — the "N changes" headline.
func (r Result) Pending() int {
	n := 0
	for _, a := range r.Actions {
		if a.Kind != ActionOK {
			n++
		}
	}
	return n
}

var versionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// Plan diffs the live install state against the pinned desired manifest and returns the ordered
// actions to reconcile it. It fails closed: an invalid manifest is an error, never a partial plan.
func Plan(state State, desired []Pin) (Result, error) {
	if err := ValidateDesired(desired); err != nil {
		return Result{}, err
	}
	index := map[string]Tool{}
	for _, t := range state.Toolchains {
		index[t.Name] = t
	}
	for _, t := range state.Runtimes {
		index[t.Name] = t
	}
	var res Result
	for _, p := range desired {
		a := Action{Name: p.Name, Desired: p.Version, SHA256: p.SHA256, URL: p.URL}
		tool, found := index[p.Name]
		cur := extractVersion(tool.Version)
		switch {
		case !found || !tool.Present:
			a.Kind = ActionInstall
		case cur == p.Version:
			a.Kind = ActionOK
			a.Current = cur
		default:
			a.Kind = ActionUpgrade
			a.Current = cur
		}
		res.Actions = append(res.Actions, a)
	}
	return res, nil
}

// extractVersion pulls the first dotted-numeric token out of a `--version` line so a pinned
// "2.0.0" matches probe output like "tart version: 2.0.0 (build 7)". Returns "" if none.
func extractVersion(s string) string {
	return versionRe.FindString(s)
}
