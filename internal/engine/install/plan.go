package install

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Artifact formats Apply knows how to install (specs/0021).
const (
	FormatBinaryTarball = "binary-tarball" // tar.gz containing the <name> binary; install to BinDir
	FormatBinaryZip     = "binary-zip"     // .zip containing the <name> binary (e.g. bun); install to BinDir
	FormatRawBinary     = "raw-binary"     // the artifact IS the <name> binary, no archive (e.g. claude); install to BinDir
	FormatAppTarball    = "app-tarball"    // tar.gz containing <name>.app; install to AppDir + symlink
)

// Sig is an optional upstream signature over the artifact's checksum file. When present, Apply
// verifies sig -> checksum-file -> artifact-sha (fail-closed). Defends a maintainer compromise that
// a copied sha256 cannot (provenance != honesty; specs/0012 §10.2).
type Sig struct {
	Scheme   string `json:"scheme"`   // "minisign"
	PubKey   string `json:"pubkey"`   // minisign public key (the single base64 key line)
	SumsURL  string `json:"sums_url"` // URL of SHASUMS256.txt
	SigURL   string `json:"sig_url"`  // URL of SHASUMS256.txt.minisig
	Artifact string `json:"artifact"` // the artifact's name as it appears in SHASUMS256.txt
}

// Pin is one tool's pinned desired-state entry. Plan diffs the live Status against these; apply
// (SP7b-3) downloads URL, verifies SHA256, installs Version. The manifest is fail-closed: every
// field is mandatory and Version is never "latest" (specs/0012 §5).
type Pin struct {
	Name    string `json:"name"`    // matches Tool.Name from Status (e.g. "mise", "tart")
	Kind    string `json:"kind"`    // "toolchain" | "runtime" — informs apply's provisioner
	Format  string `json:"format"`  // binary-tarball | app-tarball
	Version string `json:"version"` // exact pinned version, never "latest"
	SHA256  string `json:"sha256"`  // sha256 of the darwin-arm64 artifact
	URL     string `json:"url"`     // download source for that artifact
	// Provenance records how SHA256 was obtained, so the cockpit can tell a vendor-published checksum
	// apart from one safeslop computed from the download itself (trust-on-first-use). ProvenanceVendor =
	// the pin matches a checksum the vendor publishes; ProvenanceTLS/"" = no vendor checksum exists, so
	// the pin is the hash safeslop recorded over TLS (weaker provenance). It is a legibility label, NOT a
	// security gate — the SHA verification is identical either way — so it is optional and defaults to the
	// more cautious TLS reading when unset (fail-safe: an un-annotated pin never over-claims "vendor").
	Provenance string `json:"provenance,omitempty"`
	Sig        *Sig   `json:"sig,omitempty"` // optional upstream signature
	// SelfUpdating marks a tool that overwrites its own binary after install (e.g. claude), so its
	// on-disk hash diverges from this pin by design. The install receipt carries this flag so uninstall
	// does not treat the expected drift as tampering (specs/0041). Optional; defaults false.
	SelfUpdating bool `json:"self_updating,omitempty"`
}

// Provenance values for Pin.Provenance / VerifiedInstaller.Provenance.
const (
	ProvenanceVendor = "vendor" // the pinned SHA matches a checksum the vendor publishes for the release
	ProvenanceTLS    = "tls"    // no vendor checksum exists; the pin is safeslop's own hash, recorded over TLS
)

// VendorChecksum reports whether the pin's SHA256 matches a vendor-published checksum (vs trust-on-
// first-use). Unset provenance is treated as the weaker TOFU case, so the UI never over-claims.
func (p Pin) VendorChecksum() bool { return p.Provenance == ProvenanceVendor }

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
		switch p.Format {
		case FormatBinaryTarball, FormatBinaryZip, FormatRawBinary, FormatAppTarball:
		default:
			return fmt.Errorf("install: pin %q has invalid format %q", p.Name, p.Format)
		}
		// Provenance is optional (defaults to the cautious TLS reading), but a non-empty value must be a
		// known label — a typo here would silently mislabel the cockpit's trust badge, so catch it at build.
		if p.Provenance != "" && p.Provenance != ProvenanceVendor && p.Provenance != ProvenanceTLS {
			return fmt.Errorf("install: pin %q has invalid provenance %q (want vendor|tls)", p.Name, p.Provenance)
		}
		if p.Sig != nil {
			if p.Sig.Scheme != "minisign" {
				return fmt.Errorf("install: pin %q sig scheme %q unsupported (want minisign)", p.Name, p.Sig.Scheme)
			}
			if p.Sig.PubKey == "" || p.Sig.SumsURL == "" || p.Sig.SigURL == "" || p.Sig.Artifact == "" {
				return fmt.Errorf("install: pin %q sig is incomplete (need pubkey, sums_url, sig_url, artifact)", p.Name)
			}
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
	Format  string     `json:"format"`
	Sig     *Sig       `json:"sig,omitempty"`
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
		a := Action{Name: p.Name, Desired: p.Version, SHA256: p.SHA256, URL: p.URL, Format: p.Format, Sig: p.Sig}
		tool, found := index[p.Name]
		cur := extractVersion(tool.Version)
		switch {
		case !found || !tool.Present:
			a.Kind = ActionInstall
		case cur == p.Version || cmpVersion(cur, p.Version) > 0:
			// Already at the pin, or NEWER than it — never downgrade a tool the user already has (some
			// tools, e.g. claude, self-update past the pin; apply must not roll them back).
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

// cmpVersion compares two dotted-numeric versions ("2.1.185" vs "2.1.176"), returning -1/0/1. Missing
// or non-numeric components compare as 0 — a best-effort downgrade guard, not full semver (pre-release
// tags are ignored). Used by Plan to avoid rolling a newer install back to an older pin.
func cmpVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}
