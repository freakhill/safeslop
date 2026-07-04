package policy

import (
	"fmt"
	"strconv"
	"strings"
)

// Version comparison for the catalog bump policy (specs/0059 W1; canon
// specs/research/2026-06-30-version-policy-flo.md). The catalog mixes three version
// grammars — semver (`22.23.1`), Debian (`3.11`, `1:1.2.3-4`), and calver
// (`2026.6.11`) — and a single semver comparator silently mis-orders them, which would
// break the monotonic floor (LAW: a bump may never roll a package back). This package
// parses + compares per grammar and classifies a bump's magnitude (drives the
// SemVer-aware soak window) and the channel ban (LAW-B: stable releases only).
//
// It is pure: no I/O, no network, no catalog mutation. The bump path (W4) composes it.

// Scheme is the version grammar. It is per-package, not strictly per-Kind: most
// `binary`/`npm`/`pip` entries are semver, but a date-stamped binary (e.g. mise
// `2026.6.11`) is calver; `apt` is Debian. InferScheme resolves the default; a future
// explicit catalog field may override it (W2).
type Scheme string

const (
	SchemeSemver Scheme = "semver"
	SchemeDebian Scheme = "debian"
	SchemeCalver Scheme = "calver"
)

// MagnitudeKind classifies how big a step a bump is, scaled so the soak window can
// require more field exposure for a bigger step (canon X1: patch < minor < major). For
// Debian the axis is the upstream revision; for calver it is the date / calendar year.
type MagnitudeKind string

const (
	MagPatch    MagnitudeKind = "patch"    // semver: same major.minor
	MagMinor    MagnitudeKind = "minor"    // semver: same major; calver: same year; debian: same upstream
	MagMajor    MagnitudeKind = "major"    // semver: new major; calver: new year
	MagRevision MagnitudeKind = "revision" // debian: same upstream version, new Debian revision
	MagNone     MagnitudeKind = "none"     // no change
)

// Version is a parsed version string tagged with its grammar. Two Versions are
// comparable only when their Scheme matches (Compare panics otherwise — a cross-grammar
// comparison is a caller bug, never a runtime branch).
type Version struct {
	Raw    string
	Scheme Scheme
	// semver / calver: numeric components split on '.', e.g. "22.23.1" -> [22,23,1].
	// debian: upstream components (after an optional epoch) and a Debian revision.
	num    []int64
	epoch  int64  // debian only
	debRev string // debian Debian-revision (after '-'), "" if none
}

// InferScheme picks the default grammar for a version string given its package Kind:
// apt -> Debian; otherwise semver, unless the first component is a 4-digit year
// (>= 2000), which marks a calver binary (mise et al.). The sniff is a Wave-1 default;
// W2 may add an explicit per-package override.
func InferScheme(kind PackageKind, s string) Scheme {
	if kind == KindApt {
		return SchemeDebian
	}
	if first, ok := firstIntComponent(s); ok && first >= 2000 && first < 10000 {
		return SchemeCalver
	}
	return SchemeSemver
}

// Parse parses s under scheme. It rejects empty input and non-numeric components
// (surfaces a caller/format bug rather than ordering garbage).
func Parse(s string, scheme Scheme) (Version, error) {
	if strings.TrimSpace(s) == "" {
		return Version{}, fmt.Errorf("version: empty string")
	}
	switch scheme {
	case SchemeDebian:
		return parseDebian(s)
	case SchemeCalver:
		return parseDotted(s, SchemeCalver)
	default:
		return parseDotted(s, SchemeSemver)
	}
}

// Compare returns -1/0/+1. Both must share a Scheme; a mismatch is a fatal caller bug.
func Compare(a, b Version) int {
	if a.Scheme != b.Scheme {
		panic(fmt.Sprintf("version: Compare across schemes %q vs %q", a.Scheme, b.Scheme))
	}
	switch a.Scheme {
	case SchemeDebian:
		if c := cmpInt64(a.epoch, b.epoch); c != 0 {
			return c
		}
		if c := cmpNumSlice(a.num, b.num); c != 0 {
			return c
		}
		return strings.Compare(a.debRev, b.debRev)
	default: // semver + calver: identical numeric component-wise comparison
		return cmpNumSlice(a.num, b.num)
	}
}

// Magnitude classifies the step from -> to (both same Scheme). to == from is MagNone.
func Magnitude(from, to Version) MagnitudeKind {
	if from.Scheme != to.Scheme {
		// A cross-scheme bump is not classifiable; treat as major (most conservative
		// soak) rather than panic, since a catalog grammar change is plausible.
		return MagMajor
	}
	if Compare(from, to) == 0 {
		return MagNone
	}
	switch from.Scheme {
	case SchemeDebian:
		if sameUpstream(from, to) {
			return MagRevision
		}
		return MagMinor
	case SchemeCalver:
		if len(from.num) > 0 && len(to.num) > 0 && from.num[0] == to.num[0] {
			return MagMinor // same calendar year
		}
		return MagMajor
	default: // semver
		fm, fmi, _ := semverMajorMinor(from)
		tm, tmi, _ := semverMajorMinor(to)
		if fm != tm {
			return MagMajor
		}
		if fmi != tmi {
			return MagMinor
		}
		return MagPatch
	}
}

// IsStableChannel reports whether a version string denotes a stable release (LAW-B).
// It rejects pre-release / unstable markers: rc, beta, alpha, nightly, head, dev, pre,
// preview — matched case-insensitively as substrings, so `1.2.3` is stable but
// `1.2.3-rc1`, `0.0.1-nightly`, `2.0.0-beta.2`, and `1.27rc1` (Go's release-tag
// style) are not. Substring matching is safe in this domain: stable catalog versions
// are numeric and never contain these letter-stems. The catalog pins stable releases
// only.
func IsStableChannel(s string) bool {
	t := strings.ToLower(s)
	for _, bad := range bannedChannelStems {
		if strings.Contains(t, bad) {
			return false
		}
	}
	return true
}

var bannedChannelStems = []string{"rc", "beta", "alpha", "nightly", "head", "dev", "pre", "preview"}

// --- parsers ---

func parseDotted(s string, scheme Scheme) (Version, error) {
	v := Version{Raw: s, Scheme: scheme}
	for _, part := range strings.Split(s, ".") {
		// Tolerate a trailing build/pre-release segment by stopping at the first
		// non-numeric component (the channel ban catches unstable suffixes separately).
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			break
		}
		v.num = append(v.num, n)
	}
	if len(v.num) == 0 {
		return Version{}, fmt.Errorf("version: %q has no numeric components", s)
	}
	return v, nil
}

func parseDebian(s string) (Version, error) {
	v := Version{Raw: s, Scheme: SchemeDebian}
	rest := s
	if i := strings.Index(rest, ":"); i >= 0 {
		epoch, err := strconv.ParseInt(rest[:i], 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("version: bad Debian epoch in %q", s)
		}
		v.epoch = epoch
		rest = rest[i+1:]
	}
	// Split upstream version from Debian revision on the LAST '-'.
	rev := ""
	if i := strings.LastIndex(rest, "-"); i >= 0 {
		rev = rest[i+1:]
		rest = rest[:i]
	}
	v.debRev = rev
	for _, part := range strings.Split(rest, ".") {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			break
		}
		v.num = append(v.num, n)
	}
	if len(v.num) == 0 {
		return Version{}, fmt.Errorf("version: Debian %q has no numeric upstream components", s)
	}
	return v, nil
}

// --- helpers ---

func firstIntComponent(s string) (int64, bool) {
	i := strings.IndexAny(s, ".-")
	first := s
	if i >= 0 {
		first = s[:i]
	}
	n, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func semverMajorMinor(v Version) (int64, int64, bool) {
	if len(v.num) < 2 {
		return 0, 0, false
	}
	return v.num[0], v.num[1], true
}

func sameUpstream(a, b Version) bool {
	if a.epoch != b.epoch {
		return false
	}
	return cmpNumSlice(a.num, b.num) == 0
}

func cmpNumSlice(a, b []int64) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var ai, bi int64
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if c := cmpInt64(ai, bi); c != 0 {
			return c
		}
	}
	return 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// splitVersionTokens splits a version string on the conventional delimiters so the
// channel ban can inspect each token. "1.2.3-rc1" -> ["1","2","3","rc1"].
func splitVersionTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '/' || r == '+'
	})
}
