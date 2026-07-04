package policy

import (
	"fmt"
	"sort"
	"strings"
)

const (
	VerificationSignedManifest   = "signed-manifest"
	VerificationSelfComputedWeak = "self-computed-WEAK"
)

// SHA256Change is the per-arch digest delta rendered in a bump plan sheet.
type SHA256Change struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// PlanSheet is the review artifact for a catalog bump (specs/0059 W4/D4; canon plan
// sheet in specs/research/2026-06-30-version-policy-flo.md). It keeps the hard-gate
// evidence (LAW-A digests, LAW-B/C/D outcome via the returned Diff) next to the soft
// soak/waiver state a human must review before the catalog edit lands.
type PlanSheet struct {
	PackageName        string                  `json:"packageName"`
	OldVersion         string                  `json:"oldVersion"`
	NewVersion         string                  `json:"newVersion"`
	Magnitude          MagnitudeKind           `json:"magnitude"`
	SHA256             map[string]SHA256Change `json:"sha256,omitempty"`
	Origin             string                  `json:"origin"`
	VerificationMethod string                  `json:"verificationMethod"`
	ChangelogURL       string                  `json:"changelogUrl,omitempty"`
	CVEID              string                  `json:"cveId,omitempty"`
	BlastRadius        []string                `json:"blastRadius,omitempty"`
	Lane               string                  `json:"lane"`
	SoakRequired       bool                    `json:"soakRequired"`
	SoakSatisfied      bool                    `json:"soakSatisfied"`
	WaivedBy           string                  `json:"waivedBy,omitempty"`
}

// String renders the human-readable plan sheet that Wave 6 prints for maintainer
// review. The order is deterministic so CLI snapshots and review diffs stay stable.
func (p PlanSheet) String() string { return p.Render() }

// Render returns a human-readable catalog bump plan sheet.
func (p PlanSheet) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Catalog bump plan\n")
	fmt.Fprintf(&b, "Package: %s\n", p.PackageName)
	fmt.Fprintf(&b, "Version: %s -> %s\n", p.OldVersion, p.NewVersion)
	fmt.Fprintf(&b, "Magnitude: %s\n", p.Magnitude)
	fmt.Fprintf(&b, "Lane: %s\n", p.Lane)
	if p.CVEID != "" {
		fmt.Fprintf(&b, "CVE: %s\n", p.CVEID)
	}
	fmt.Fprintf(&b, "Origin: %s\n", p.Origin)
	fmt.Fprintf(&b, "Verification: %s\n", p.VerificationMethod)
	if p.ChangelogURL != "" {
		fmt.Fprintf(&b, "Changelog: %s\n", p.ChangelogURL)
	}
	fmt.Fprintf(&b, "Soak: required=%t satisfied=%t\n", p.SoakRequired, p.SoakSatisfied)
	if p.WaivedBy != "" {
		fmt.Fprintf(&b, "Waived by: %s\n", p.WaivedBy)
	}
	if len(p.SHA256) > 0 {
		fmt.Fprintf(&b, "SHA256:\n")
		for _, arch := range orderedPlanSheetArches(p.SHA256) {
			change := p.SHA256[arch]
			fmt.Fprintf(&b, "  %s: %s -> %s\n", arch, change.Old, change.New)
		}
	}
	fmt.Fprintf(&b, "Blast radius:\n")
	if len(p.BlastRadius) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	} else {
		for _, name := range p.BlastRadius {
			fmt.Fprintf(&b, "  - %s\n", name)
		}
	}
	return b.String()
}

func orderedPlanSheetArches(changes map[string]SHA256Change) []string {
	arches := make([]string, 0, len(changes))
	seen := make(map[string]bool, len(changes))
	for _, arch := range buildArches {
		if _, ok := changes[arch]; ok {
			arches = append(arches, arch)
			seen[arch] = true
		}
	}
	var extra []string
	for arch := range changes {
		if !seen[arch] {
			extra = append(extra, arch)
		}
	}
	sort.Strings(extra)
	return append(arches, extra...)
}
