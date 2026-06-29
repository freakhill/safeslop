package policy

import (
	"fmt"
	"sort"
	"strings"
)

// The package catalog (specs/0058 N0). A profile declares which build-time packages
// go into its container image by referencing catalog entries by name — individually
// (`packages`) or via named sets (`bundles`). The catalog itself is safeslop-owned,
// in-tree engine data (not user-authored): extending it is a code edit + review, and
// that review is the SUPPLY-CHAIN boundary — distinct from squid, the runtime network
// boundary (specs/0058 N2). Every entry is version-pinned; `binary` kinds also pin a
// content digest. This is the curated generalization of the old hardcoded
// ENABLE_CLAUDE_CODE/PI build args (identity.go).

// PackageKind classifies how a catalog package is installed at image-build time.
// There is deliberately no arbitrary-"script" kind: every recipe is a structured
// fetch from a known source, which is what keeps the review boundary meaningful
// (specs/0058 N2 F1).
type PackageKind string

const (
	KindApt    PackageKind = "apt"    // pinned apt package (base apt source is Debian-snapshot-pinned in IW2)
	KindNpm    PackageKind = "npm"    // pinned global npm package (requires node)
	KindBinary PackageKind = "binary" // pinned single binary/tarball, sha256-verified
	KindPip    PackageKind = "pip"    // pinned pip package (requires python3)
)

// sha256Unresolved is the placeholder digest for binary entries whose real content
// hash is resolved in IW2 (the golden-base + Dockerfile wave). It is structurally a
// valid 64-hex string so Validate passes, but BuildReady rejects it so no image is
// ever built against an unpinned binary. All-zero is an obvious "unset" sentinel — we
// never ship a fake-real digest (specs/0058 N2 honesty).
const sha256Unresolved = "0000000000000000000000000000000000000000000000000000000000000000"

// buildArches are the dpkg --print-architecture names safeslop builds agent images
// for. A `binary` package must pin a sha256 per arch (its release tarball/binary is
// multi-arch): the parametrized Dockerfile selects by `dpkg --print-architecture` and
// verifies against the matching digest, so one image recipe builds correctly on both.
var buildArches = []string{"amd64", "arm64"}

// unresolvedSHA is the per-arch sentinel map for binary entries whose real digests are
// still owed. BuildReady reports any package still carrying it (the build path refuses).
func unresolvedSHA() map[string]string {
	m := make(map[string]string, len(buildArches))
	for _, a := range buildArches {
		m[a] = sha256Unresolved
	}
	return m
}

// Package is one curated, safeslop-owned, pinned build-time install unit.
type Package struct {
	Name    string      `json:"name"`    // catalog key, e.g. "node", "claude-code"
	Kind    PackageKind `json:"kind"`    //
	Version string      `json:"version"` // PINNED — never a floating tag
	// SHA256 is the per-arch content digest, keyed by dpkg --print-architecture name
	// ("amd64", "arm64"). REQUIRED for kind "binary" — one 64-hex digest per build arch,
	// because release binaries are multi-arch (specs/0058 N1/N2).
	SHA256    map[string]string `json:"sha256,omitempty"`
	Requires  []string          `json:"requires,omitempty"`  // other package names pulled into the closure
	Conflicts []string          `json:"conflicts,omitempty"` // packages that must not be co-enabled
	// BuildFetch lists domains the BUILD fetches from. Provenance/audit only — NOT
	// enforced (the build does not traverse squid); a seed for a future build-proxy
	// (specs/0058 N2).
	BuildFetch []string `json:"buildFetch,omitempty"`
	// RuntimeEgress lists domains the RUNNING package needs; the resolver unions these
	// into the profile's squid allowlist (specs/0058 N2). A leading dot is a subdomain
	// suffix match; a bare host is exact — same convention as #Profile.egress.
	RuntimeEgress []string `json:"runtimeEgress,omitempty"`
}

// Bundle is a named set of catalog packages — the "premade" simplification that makes
// profile creation simpler (specs/0058 N0).
type Bundle struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Packages    []string `json:"packages"`
}

// catalogPackages is the v1 curated catalog. Versions are pinned; binary digests are
// the IW2-resolved sentinel for now (see sha256Unresolved). Keep entries sorted by
// name for readability.
var catalogPackages = []Package{
	{Name: "bun", Kind: KindBinary, Version: "1.1.38", SHA256: unresolvedSHA(),
		BuildFetch: []string{"github.com"}}, // provides bunx
	{Name: "claude-code", Kind: KindNpm, Version: "2.1.121", Requires: []string{"node"},
		BuildFetch: []string{"registry.npmjs.org"}, RuntimeEgress: []string{".anthropic.com"}},
	{Name: "fd", Kind: KindBinary, Version: "10.2.0", SHA256: unresolvedSHA(),
		BuildFetch: []string{"github.com"}},
	{Name: "mise", Kind: KindBinary, Version: "2026.6.11", SHA256: unresolvedSHA(),
		BuildFetch: []string{"github.com"}},
	// node — official multi-arch tarball, sha256-verified per arch (digests from
	// nodejs.org/dist/v22.23.1/SHASUMS256.txt; amd64 == the x64 tarball). Provides npm,
	// which claude-code/pi/pnpm require.
	{Name: "node", Kind: KindBinary, Version: "22.23.1", SHA256: map[string]string{
		"amd64": "9749e988f437343b7fa832c69ded82a312e41a03116d766797ac14f6f9eee578",
		"arm64": "0294e8b915ab75f92c7513d2fcb830ae06e10684e6c603e99a87dbf8835389c1",
	}, BuildFetch: []string{"nodejs.org"}},
	{Name: "pi", Kind: KindNpm, Version: "0.80.2", Requires: []string{"node"},
		BuildFetch: []string{"registry.npmjs.org"}},
	{Name: "pnpm", Kind: KindNpm, Version: "9.15.0", Requires: []string{"node"},
		BuildFetch: []string{"registry.npmjs.org"}},
	{Name: "python3", Kind: KindApt, Version: "3.11",
		BuildFetch: []string{"deb.debian.org", "snapshot.debian.org"}},
	{Name: "ripgrep", Kind: KindBinary, Version: "14.1.1", SHA256: unresolvedSHA(),
		BuildFetch: []string{"github.com"}},
	{Name: "uv", Kind: KindBinary, Version: "0.5.11", SHA256: unresolvedSHA(),
		BuildFetch: []string{"astral.sh", "github.com"}},
}

// catalogBundles is the v1 set of premade bundles. `jq` is omitted from base-tools
// because it ships in the golden base floor (specs/0058 N1).
var catalogBundles = []Bundle{
	{Name: "base-tools", Description: "ripgrep + fd search tools", Packages: []string{"ripgrep", "fd"}},
	{Name: "claude", Description: "Claude Code (Anthropic) + Node runtime", Packages: []string{"node", "claude-code"}},
	{Name: "node", Description: "Node.js + pnpm + bun for JS/TS work", Packages: []string{"node", "pnpm", "bun"}},
	{Name: "pi", Description: "pi coding agent + Node runtime", Packages: []string{"node", "pi"}},
	{Name: "python", Description: "Python 3 + uv", Packages: []string{"python3", "uv"}},
}

// agentDefaultBundle maps an agent to the bundle implied by selecting it, so that
// `--agent claude` installs claude-code without the user restating it. Agents absent
// here (fish, zsh, shell) imply no packages — a tiny shell-only image. The default is
// additive (always included so the agent can launch); an opt-out (--no-default-bundle)
// lands with the CLI wave (specs/0058 N0/N4).
var agentDefaultBundle = map[string]string{
	"claude": "claude",
	"pi":     "pi",
}

// Catalog is an indexed view over a set of packages + bundles + agent defaults. The
// default catalog is the in-tree data above; tests build synthetic catalogs to
// exercise the resolver's error paths.
type Catalog struct {
	pkgs     []Package
	bundles  []Bundle
	pkgIdx   map[string]Package
	bndIdx   map[string]Bundle
	defaults map[string]string
}

// newCatalog indexes the given data. It does not validate — call Validate for that.
func newCatalog(pkgs []Package, bundles []Bundle, defaults map[string]string) *Catalog {
	c := &Catalog{
		pkgs:     pkgs,
		bundles:  bundles,
		pkgIdx:   make(map[string]Package, len(pkgs)),
		bndIdx:   make(map[string]Bundle, len(bundles)),
		defaults: defaults,
	}
	for _, p := range pkgs {
		c.pkgIdx[p.Name] = p
	}
	for _, b := range bundles {
		c.bndIdx[b.Name] = b
	}
	return c
}

// DefaultCatalog returns the in-tree curated catalog (specs/0058).
func DefaultCatalog() *Catalog {
	return newCatalog(catalogPackages, catalogBundles, agentDefaultBundle)
}

// Packages returns the catalog's packages, sorted by name (drives `catalog list`).
func (c *Catalog) Packages() []Package {
	out := append([]Package(nil), c.pkgs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Bundles returns the catalog's bundles, sorted by name.
func (c *Catalog) Bundles() []Bundle {
	out := append([]Bundle(nil), c.bundles...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DefaultBundle returns the bundle name implied by selecting agent, or "" if none.
func (c *Catalog) DefaultBundle(agent string) string { return c.defaults[NormalizeAgent(agent)] }

// Lookup returns the catalog package named name (the build path reads its pinned
// version + per-arch digests to emit deterministic build args).
func (c *Catalog) Lookup(name string) (Package, bool) { p, ok := c.pkgIdx[name]; return p, ok }

// Validate checks that the catalog is internally consistent: unique names, valid
// kinds, pinned versions, binary digests present, requires/conflicts/bundle/default
// targets resolvable, no requires-cycle, and non-degenerate egress globs. It is the
// guard that the in-tree catalog is well-formed (a test calls it on DefaultCatalog).
func (c *Catalog) Validate() error {
	seen := make(map[string]bool, len(c.pkgs))
	for _, p := range c.pkgs {
		if p.Name == "" {
			return fmt.Errorf("catalog: package with empty name")
		}
		if seen[p.Name] {
			return fmt.Errorf("catalog: duplicate package %q", p.Name)
		}
		seen[p.Name] = true
		switch p.Kind {
		case KindApt, KindNpm, KindBinary, KindPip:
		default:
			return fmt.Errorf("catalog: package %q has invalid kind %q", p.Name, p.Kind)
		}
		if p.Version == "" {
			return fmt.Errorf("catalog: package %q has no pinned version", p.Name)
		}
		if p.Kind == KindBinary {
			for _, a := range buildArches {
				digest, ok := p.SHA256[a]
				if !ok {
					return fmt.Errorf("catalog: binary package %q is missing a %s sha256", p.Name, a)
				}
				if !isHex64(digest) {
					return fmt.Errorf("catalog: binary package %q needs a 64-hex %s sha256 (got %q)", p.Name, a, digest)
				}
			}
		}
		for _, r := range p.Requires {
			if _, ok := c.pkgIdx[r]; !ok {
				return fmt.Errorf("catalog: package %q requires unknown package %q", p.Name, r)
			}
		}
		for _, x := range p.Conflicts {
			if _, ok := c.pkgIdx[x]; !ok {
				return fmt.Errorf("catalog: package %q conflicts with unknown package %q", p.Name, x)
			}
		}
		for _, d := range append(append([]string(nil), p.BuildFetch...), p.RuntimeEgress...) {
			if egressTooWide(d) {
				return fmt.Errorf("catalog: package %q has an over-wide egress domain %q", p.Name, d)
			}
		}
	}
	bseen := make(map[string]bool, len(c.bundles))
	for _, b := range c.bundles {
		if b.Name == "" {
			return fmt.Errorf("catalog: bundle with empty name")
		}
		if bseen[b.Name] {
			return fmt.Errorf("catalog: duplicate bundle %q", b.Name)
		}
		bseen[b.Name] = true
		for _, pn := range b.Packages {
			if _, ok := c.pkgIdx[pn]; !ok {
				return fmt.Errorf("catalog: bundle %q references unknown package %q", b.Name, pn)
			}
		}
	}
	for agent, bn := range c.defaults {
		if _, ok := c.bndIdx[bn]; !ok {
			return fmt.Errorf("catalog: agent %q default bundle %q is not a bundle", agent, bn)
		}
	}
	// A requires-cycle anywhere makes topological install order impossible.
	if _, err := c.topoAll(); err != nil {
		return err
	}
	return nil
}

// BuildReady reports the binary packages whose digest is still the IW2 sentinel for
// any build arch. The build path must refuse to build a package while it is pending —
// callers gate on it (specs/0058 N2 honesty: never build against an unpinned binary).
func (c *Catalog) BuildReady() []string {
	var pending []string
	for _, p := range c.pkgs {
		if p.Kind != KindBinary {
			continue
		}
		for _, a := range buildArches {
			if p.SHA256[a] == sha256Unresolved {
				pending = append(pending, p.Name)
				break
			}
		}
	}
	sort.Strings(pending)
	return pending
}

// PackagePending reports whether package name is a binary still carrying the sentinel
// digest for some build arch (so its image must not be built). Unknown names are not
// pending. Used by the build path to gate the resolved set (specs/0058 N2).
func (c *Catalog) PackagePending(name string) bool {
	p, ok := c.pkgIdx[name]
	if !ok || p.Kind != KindBinary {
		return false
	}
	for _, a := range buildArches {
		if p.SHA256[a] == sha256Unresolved {
			return true
		}
	}
	return false
}

// BuildReadyFor reports the binary packages within names (a resolved set) that are
// still pending real digests — the build path's gate for a specific profile.
func (c *Catalog) BuildReadyFor(names []string) []string {
	var pending []string
	for _, n := range names {
		if c.PackagePending(n) {
			pending = append(pending, n)
		}
	}
	sort.Strings(pending)
	return pending
}

// topoAll topologically sorts every package by its requires edges, returning an error
// on a cycle. Used by Validate; the per-profile order comes from Resolve.
func (c *Catalog) topoAll() ([]string, error) {
	all := make([]string, 0, len(c.pkgs))
	for _, p := range c.pkgs {
		all = append(all, p.Name)
	}
	return c.topo(all)
}

// isHex64 reports whether s is exactly 64 lowercase/uppercase hex digits.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// egressTooWide flags a degenerate egress glob the catalog must never carry: empty,
// "*", or a single DNS label with no dot (specs/0058 N2 S6). ".example.com" and
// "example.com" are fine.
func egressTooWide(d string) bool {
	if d == "" || d == "*" {
		return true
	}
	return !strings.Contains(strings.TrimPrefix(d, "."), ".")
}
