package policy

import "testing"

// specs/0059 W1 — the per-Kind version parser. These tests are the definition of done
// for Wave 1: parsing the three grammars, ordering within each (monotonic floor),
// magnitude classification (soak scaling), and the LAW-B stable-channel ban. Pure and
// hermetic.

func TestInferScheme(t *testing.T) {
	cases := []struct {
		kind PackageKind
		ver  string
		want Scheme
	}{
		{KindBinary, "22.23.1", SchemeSemver},   // node
		{KindBinary, "14.1.1", SchemeSemver},    // ripgrep
		{KindBinary, "2026.6.11", SchemeCalver}, // mise — 4-digit year
		{KindBinary, "0.9.98", SchemeSemver},    // zoxide (leading 0, not a year)
		{KindNpm, "2.1.121", SchemeSemver},      // claude-code
		{KindApt, "3.11", SchemeDebian},         // python3
		{KindApt, "1:1.2.3-4", SchemeDebian},    // epoch+revision
		{KindPip, "1.2.3", SchemeSemver},
	}
	for _, tc := range cases {
		if got := InferScheme(tc.kind, tc.ver); got != tc.want {
			t.Errorf("InferScheme(%q,%q) = %q, want %q", tc.kind, tc.ver, got, tc.want)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "   ", "abc"} {
		if _, err := Parse(bad, SchemeSemver); err == nil {
			t.Errorf("Parse(%q, semver) should error", bad)
		}
	}
	if _, err := Parse("nosuch", SchemeDebian); err == nil {
		t.Error("Parse(non-numeric, debian) should error")
	}
}

func TestCompareSemver(t *testing.T) {
	must := func(s string) Version {
		v, err := Parse(s, SchemeSemver)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	less := []struct{ a, b string }{
		{"22.23.0", "22.23.1"},
		{"22.23.1", "22.24.0"},
		{"22.24.9", "23.0.0"},
		{"1.2.3", "1.2.3.1"}, // missing trailing component is lower
		{"0.9.8", "0.10.0"},  // numeric, not lexical: 9 < 10
	}
	for _, c := range less {
		if got := Compare(must(c.a), must(c.b)); got >= 0 {
			t.Errorf("Compare(%q,%q) = %d, want < 0", c.a, c.b, got)
		}
	}
	if got := Compare(must("1.2.3"), must("1.2.3")); got != 0 {
		t.Errorf("Compare equal = %d, want 0", got)
	}
}

func TestCompareCalver(t *testing.T) {
	must := func(s string) Version {
		v, err := Parse(s, SchemeCalver)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := Compare(must("2026.6.11"), must("2026.7.1")); got >= 0 {
		t.Errorf("calver 2026.6.11 < 2026.7.1, got %d", got)
	}
	if got := Compare(must("2026.12.31"), must("2027.1.1")); got >= 0 {
		t.Errorf("calver cross-year 2026.12.31 < 2027.1.1, got %d", got)
	}
}

func TestCompareDebian(t *testing.T) {
	must := func(s string) Version {
		v, err := Parse(s, SchemeDebian)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	less := []struct{ a, b string }{
		{"3.11", "3.12"},
		{"1.2.3-1", "1.2.3-2"}, // Debian revision bump
		{"1:0.0.0", "2:9.9.9"}, // epoch dominates: 1: < 2:
		{"0.9", "1:0.0.0"},     // any epoch > no epoch
	}
	for _, c := range less {
		if got := Compare(must(c.a), must(c.b)); got >= 0 {
			t.Errorf("debian Compare(%q,%q) = %d, want < 0", c.a, c.b, got)
		}
	}
}

func TestCompareRejectsCrossScheme(t *testing.T) {
	a, _ := Parse("1.2.3", SchemeSemver)
	b, _ := Parse("1.2.3", SchemeCalver)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Compare across schemes should panic")
		}
	}()
	Compare(a, b)
}

func TestMagnitudeSemver(t *testing.T) {
	must := func(s string) Version {
		v, err := Parse(s, SchemeSemver)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		from, to string
		want     MagnitudeKind
	}{
		{"22.23.1", "22.23.5", MagPatch},
		{"22.23.1", "22.24.0", MagMinor},
		{"22.23.1", "23.0.0", MagMajor},
		{"22.23.1", "22.23.1", MagNone},
	}
	for _, c := range cases {
		if got := Magnitude(must(c.from), must(c.to)); got != c.want {
			t.Errorf("Magnitude(%q->%q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestMagnitudeDebianAndCalver(t *testing.T) {
	debian := func(s string) Version {
		v, err := Parse(s, SchemeDebian)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	calver := func(s string) Version {
		v, err := Parse(s, SchemeCalver)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := Magnitude(debian("1.2.3-1"), debian("1.2.3-2")); got != MagRevision {
		t.Errorf("debian revision magnitude = %q, want %q", got, MagRevision)
	}
	if got := Magnitude(debian("1.2.3-1"), debian("1.3.0-1")); got != MagMinor {
		t.Errorf("debian upstream magnitude = %q, want %q", got, MagMinor)
	}
	if got := Magnitude(calver("2026.6.11"), calver("2026.7.1")); got != MagMinor {
		t.Errorf("calver same-year magnitude = %q, want %q", got, MagMinor)
	}
	if got := Magnitude(calver("2026.6.11"), calver("2027.1.1")); got != MagMajor {
		t.Errorf("calver cross-year magnitude = %q, want %q", got, MagMajor)
	}
}

func TestIsStableChannel(t *testing.T) {
	stable := []string{"22.23.1", "0.9.98", "1.2.3", "2026.6.11", "3.11", "1:1.2.3-4"}
	for _, s := range stable {
		if !IsStableChannel(s) {
			t.Errorf("IsStableChannel(%q) = false, want true", s)
		}
	}
	unstable := []string{
		"1.0.0-rc1", "2.0.0-beta.2", "0.0.1-alpha", "1.2.3-nightly",
		"3.0.0-head", "1.0.0-dev", "1.0.0-pre", "2.0.0-preview", "1.27rc1",
	}
	for _, s := range unstable {
		if IsStableChannel(s) {
			t.Errorf("IsStableChannel(%q) = true, want false", s)
		}
	}
}
