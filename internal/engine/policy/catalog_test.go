package policy

import (
	"sort"
	"strings"
	"testing"
)

func TestDefaultCatalogIsWellFormed(t *testing.T) {
	if err := DefaultCatalog().Validate(); err != nil {
		t.Fatalf("the in-tree catalog must validate: %v", err)
	}
}

// The v1 catalog ships binary digests as the IW2 sentinel; this both documents that
// IW2 owes the real digests and guards that we never silently mark them resolved.
func TestDefaultCatalogBinaryDigestsPendingIW2(t *testing.T) {
	pending := DefaultCatalog().BuildReady()
	if len(pending) == 0 {
		t.Fatal("expected unresolved binary digests pending IW2; got none (were real digests added without lifting the sentinel guard?)")
	}
	for _, name := range pending {
		p, ok := DefaultCatalog().pkgIdx[name]
		if !ok || p.Kind != KindBinary {
			t.Errorf("BuildReady returned %q which is not a binary", name)
			continue
		}
		// at least one build arch must still carry the sentinel
		sentinel := false
		for _, a := range buildArches {
			if p.SHA256[a] == sha256Unresolved {
				sentinel = true
			}
		}
		if !sentinel {
			t.Errorf("BuildReady returned %q which has no sentinel digest", name)
		}
		if name == "node" {
			t.Errorf("node has real digests in IW2; it must not be BuildReady-pending")
		}
	}
}

func TestPersonalBundleBuildReady(t *testing.T) {
	resolved, err := Resolve(Profile{Agent: "fish", Environment: "container", Bundles: []string{"personal"}})
	if err != nil {
		t.Fatalf("resolve personal bundle: %v", err)
	}
	catalog := DefaultCatalog()
	if pending := catalog.BuildReadyFor(resolved.Packages); len(pending) > 0 {
		t.Fatalf("personal bundle has unresolved binary digests: %v", pending)
	}
	python, ok := catalog.Lookup("python3")
	if !ok || python.Kind != KindApt || python.Version != "3.11.2-1+b1" {
		t.Fatalf("python3 must pin the Debian-snapshot apt leaf, got %+v", python)
	}
}

// node's per-arch digests are the real, verified values (IW2): present, 64-hex, and
// not the sentinel. Guards against a silent regression to placeholder digests.
func TestNodeDigestsResolved(t *testing.T) {
	p, ok := DefaultCatalog().Lookup("node")
	if !ok || p.Kind != KindBinary {
		t.Fatal("node must be a binary catalog package")
	}
	if p.Version != "22.23.1" {
		t.Errorf("node version = %q, want 22.23.1", p.Version)
	}
	for _, a := range buildArches {
		d := p.SHA256[a]
		if !isHex64(d) || d == sha256Unresolved {
			t.Errorf("node %s digest = %q, want a real 64-hex sha256", a, d)
		}
	}
	if DefaultCatalog().PackagePending("node") {
		t.Error("node must not be PackagePending (digests are resolved)")
	}
}

func TestCatalogAccessorsSorted(t *testing.T) {
	pkgs := DefaultCatalog().Packages()
	if !sort.SliceIsSorted(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name }) {
		t.Error("Packages() not sorted by name")
	}
	bnd := DefaultCatalog().Bundles()
	if !sort.SliceIsSorted(bnd, func(i, j int) bool { return bnd[i].Name < bnd[j].Name }) {
		t.Error("Bundles() not sorted by name")
	}
}

func TestPiPinIncludesLunaMetadataRelease(t *testing.T) {
	p, ok := DefaultCatalog().Lookup("pi")
	if !ok || p.Kind != KindNpm {
		t.Fatalf("pi must be an npm catalog package, got %+v", p)
	}
	if p.Version != "0.80.7" {
		t.Fatalf("pi version = %q, want 0.80.7 (first reviewed Luna activation pin)", p.Version)
	}
}

func TestDefaultBundle(t *testing.T) {
	c := DefaultCatalog()
	cases := map[string]string{
		"claude":      "claude",
		"claude-code": "claude", // normalized alias
		"pi":          "pi",
		"fish":        "",
		"zsh":         "",
		"shell":       "",
	}
	for agent, want := range cases {
		if got := c.DefaultBundle(agent); got != want {
			t.Errorf("DefaultBundle(%q) = %q, want %q", agent, got, want)
		}
	}
}

func TestValidateCatchesMalformedCatalogs(t *testing.T) {
	good := unresolvedSHA() // a structurally valid per-arch 64-hex map
	cases := []struct {
		name     string
		pkgs     []Package
		bundles  []Bundle
		defaults map[string]string
		want     string
	}{
		{"dup-package", []Package{{Name: "a", Kind: KindApt, Version: "1"}, {Name: "a", Kind: KindApt, Version: "1"}}, nil, nil, "duplicate package"},
		{"empty-name", []Package{{Name: "", Kind: KindApt, Version: "1"}}, nil, nil, "empty name"},
		{"bad-kind", []Package{{Name: "a", Kind: "script", Version: "1"}}, nil, nil, "invalid kind"},
		{"no-version", []Package{{Name: "a", Kind: KindApt}}, nil, nil, "no pinned version"},
		{"binary-no-sha", []Package{{Name: "a", Kind: KindBinary, Version: "1"}}, nil, nil, "missing a"},
		{"binary-bad-hex", []Package{{Name: "a", Kind: KindBinary, Version: "1", SHA256: map[string]string{"amd64": "zz", "arm64": "zz"}}}, nil, nil, "needs a 64-hex"},
		{"binary-one-arch-only", []Package{{Name: "a", Kind: KindBinary, Version: "1", SHA256: map[string]string{"amd64": sha256Unresolved}}}, nil, nil, "missing a"},
		{"requires-unknown", []Package{{Name: "a", Kind: KindApt, Version: "1", Requires: []string{"ghost"}}}, nil, nil, "requires unknown package"},
		{"conflict-unknown", []Package{{Name: "a", Kind: KindApt, Version: "1", Conflicts: []string{"ghost"}}}, nil, nil, "conflicts with unknown package"},
		{"over-wide-egress", []Package{{Name: "a", Kind: KindApt, Version: "1", RuntimeEgress: []string{"*"}}}, nil, nil, "over-wide egress"},
		{"single-label-egress", []Package{{Name: "a", Kind: KindApt, Version: "1", RuntimeEgress: []string{"localhost"}}}, nil, nil, "over-wide egress"},
		{"bundle-unknown-pkg", []Package{{Name: "a", Kind: KindApt, Version: "1"}}, []Bundle{{Name: "b", Packages: []string{"ghost"}}}, nil, "references unknown package"},
		{"dup-bundle", []Package{{Name: "a", Kind: KindApt, Version: "1"}}, []Bundle{{Name: "b", Packages: []string{"a"}}, {Name: "b", Packages: []string{"a"}}}, nil, "duplicate bundle"},
		{"default-missing", []Package{{Name: "a", Kind: KindApt, Version: "1"}}, nil, map[string]string{"claude": "ghost"}, "default bundle"},
		{"requires-cycle", []Package{{Name: "a", Kind: KindApt, Version: "1", Requires: []string{"b"}}, {Name: "b", Kind: KindApt, Version: "1", Requires: []string{"a"}}}, nil, nil, "cycle"},
		{"good-binary-ok", []Package{{Name: "a", Kind: KindBinary, Version: "1", SHA256: good}}, nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newCatalog(tc.pkgs, tc.bundles, tc.defaults).Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestEgressTooWide(t *testing.T) {
	wide := []string{"", "*", "localhost", "anthropic"}
	ok := []string{".anthropic.com", "anthropic.com", "api.x.io", ".a.b.c"}
	for _, d := range wide {
		if !egressTooWide(d) {
			t.Errorf("egressTooWide(%q) = false, want true", d)
		}
	}
	for _, d := range ok {
		if egressTooWide(d) {
			t.Errorf("egressTooWide(%q) = true, want false", d)
		}
	}
}

// The catalog expansion (web/Rust/Go/personal) added packages across all four kinds
// with requires-edges and scoped runtime egress. This pins the load-bearing shape so a
// silent edit (dropping a Requires, widening an egress) fails the gate. Versions/digests
// are intentionally NOT asserted here — those move on bumps; shape does not.
func TestCatalogExpansionPackageShape(t *testing.T) {
	c := DefaultCatalog()
	cases := []struct {
		name   string
		kind   PackageKind
		req    []string
		egress []string
	}{
		{"cargo-nextest", KindBinary, []string{"rust"}, nil},
		{"flip-link", KindBinary, []string{"rust"}, nil},
		{"rust", KindBinary, nil, []string{".crates.io", "static.rust-lang.org"}},
		{"go", KindBinary, nil, []string{"proxy.golang.org", "sum.golang.org"}},
		{"eslint", KindNpm, []string{"node"}, nil},
		{"web-ext", KindNpm, []string{"node"}, nil},
		{"prettier", KindNpm, []string{"node"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := c.Lookup(tc.name)
			if !ok {
				t.Fatalf("missing catalog package %q", tc.name)
			}
			if p.Kind != tc.kind {
				t.Errorf("%q kind = %q, want %q", tc.name, p.Kind, tc.kind)
			}
			if !reflectEq(p.Requires, tc.req) {
				t.Errorf("%q requires = %v, want %v", tc.name, p.Requires, tc.req)
			}
			if !reflectEq(p.RuntimeEgress, tc.egress) {
				t.Errorf("%q runtimeEgress = %v, want %v", tc.name, p.RuntimeEgress, tc.egress)
			}
		})
	}
}

// Canon guard (specs/research/2026-06-30-version-policy-flo.md): every catalog egress
// entry must be a scoped FQDN/subdomain — never `*` or a single label. A regression
// here means a package silently opened default-deny wider than reviewed.
func TestCatalogEgressIsScoped(t *testing.T) {
	for _, p := range DefaultCatalog().Packages() {
		for _, d := range append(append([]string(nil), p.BuildFetch...), p.RuntimeEgress...) {
			if egressTooWide(d) {
				t.Errorf("package %q has over-wide egress domain %q (catalog must carry scoped FQDNs only)", p.Name, d)
			}
		}
	}
}

// reflectEq compares two string slices for order-sensitive equality without pulling
// reflect into this file (the resolve tests use reflect.DeepEqual; here a tiny helper
// keeps the catalog test self-contained).
func reflectEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
