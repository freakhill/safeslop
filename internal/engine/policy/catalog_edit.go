package policy

import (
	"fmt"
	"sort"
)

// Diff records one pure catalog edit for Wave 4's plan sheet renderer (specs/0059
// D4). Bumps carry the LAW-A/B/C/D decision data: package, version delta, per-arch
// digest delta when bytes are pinned, magnitude, lane, and reverse-closure blast
// radius.
type Diff struct {
	Operation       string
	PackageName     string
	BundleName      string
	OldVersion      string
	NewVersion      string
	OldSHA256       map[string]string
	NewSHA256       map[string]string
	Magnitude       MagnitudeKind
	Lane            string
	ReverseClosure  []string
	AddedPackages   []string
	RemovedPackages []string
}

// AddPackage returns a validated catalog with p appended. LAW-D is enforced before
// indexing so a second package name never silently overwrites the lookup view.
func AddPackage(c *Catalog, p Package) (*Catalog, Diff, error) {
	if c == nil {
		return nil, Diff{}, fmt.Errorf("catalog edit: nil catalog")
	}
	if _, ok := c.Lookup(p.Name); ok {
		return nil, Diff{}, fmt.Errorf("law-D: package %q already exists", p.Name)
	}
	if !validPackageKind(p.Kind) {
		return nil, Diff{}, fmt.Errorf("catalog add: package %q has invalid kind %q", p.Name, p.Kind)
	}

	pkgs, bundles, defaults := cloneCatalogData(c)
	pkgs = append(pkgs, clonePackage(p))
	next := newCatalog(pkgs, bundles, defaults)
	if err := next.Validate(); err != nil {
		return nil, Diff{}, err
	}

	affected, _ := ReverseClosureErr(next, p.Name)
	return next, Diff{
		Operation:      "add-package",
		PackageName:    p.Name,
		NewVersion:     p.Version,
		NewSHA256:      cloneStringMap(p.SHA256),
		Magnitude:      MagNone,
		ReverseClosure: affected,
	}, nil
}

// BumpPackage is the pure Wave-3 mutation primitive. It enforces the canon gates
// that do not require orchestration state: LAW-A all-arch binary digests, LAW-B stable
// channels, LAW-C apt snapshot pairing, and the monotonic floor (specs/0059 D5; canon
// specs/research/2026-06-30-version-policy-flo.md). Soak is deliberately not gated
// here; Wave 4 owns field-exposure policy.
func BumpPackage(c *Catalog, name, version string, shaByArch map[string]string, lane string) (*Catalog, Diff, error) {
	if c == nil {
		return nil, Diff{}, fmt.Errorf("catalog edit: nil catalog")
	}
	pkg, ok := c.Lookup(name)
	if !ok {
		return nil, Diff{}, fmt.Errorf("bump: unknown package %q", name)
	}
	if !IsStableChannel(version) {
		return nil, Diff{}, fmt.Errorf("law-B: package %q version %q is not a stable channel", name, version)
	}
	if pkg.Kind == KindApt {
		return nil, Diff{}, fmt.Errorf("law-C: apt bumps require the coordinated Debian-snapshot timestamp (two-part) — not yet wired")
	}

	oldScheme := InferScheme(pkg.Kind, pkg.Version)
	newScheme := InferScheme(pkg.Kind, version)
	oldVersion, err := Parse(pkg.Version, oldScheme)
	if err != nil {
		return nil, Diff{}, fmt.Errorf("bump: current version for %q: %w", name, err)
	}
	newVersion, err := Parse(version, newScheme)
	if err != nil {
		return nil, Diff{}, fmt.Errorf("bump: new version for %q: %w", name, err)
	}
	if oldVersion.Scheme != newVersion.Scheme {
		return nil, Diff{}, fmt.Errorf("bump: version scheme change for %q (%s -> %s) cannot enforce monotonic floor", name, oldVersion.Scheme, newVersion.Scheme)
	}

	magnitude := Magnitude(oldVersion, newVersion)
	affected, err := ReverseClosureErr(c, name)
	if err != nil {
		return nil, Diff{}, err
	}
	diff := Diff{
		Operation:      "bump-package",
		PackageName:    name,
		OldVersion:     pkg.Version,
		NewVersion:     version,
		OldSHA256:      cloneStringMap(pkg.SHA256),
		Magnitude:      magnitude,
		Lane:           lane,
		ReverseClosure: affected,
	}

	var newSHA map[string]string
	if pkg.Kind == KindBinary {
		newSHA, err = validateBinaryBumpSHA(name, shaByArch)
		if err != nil {
			return nil, diff, err
		}
		diff.NewSHA256 = cloneStringMap(newSHA)
	}

	cmp := Compare(newVersion, oldVersion)
	if cmp < 0 {
		return nil, diff, fmt.Errorf("bump: monotonic floor: package %q would move from %q to lower version %q", name, pkg.Version, version)
	}
	if cmp == 0 {
		if pkg.Kind == KindBinary && !sameBuildArchSHA(pkg.SHA256, newSHA) {
			return nil, diff, fmt.Errorf("bump: same version requires Revision (v1.1)")
		}
		return nil, diff, fmt.Errorf("bump: no change")
	}

	pkgs, bundles, defaults := cloneCatalogData(c)
	for i := range pkgs {
		if pkgs[i].Name != name {
			continue
		}
		pkgs[i].Version = version
		if pkg.Kind == KindBinary {
			pkgs[i].SHA256 = cloneStringMap(newSHA)
		}
		break
	}
	next := newCatalog(pkgs, bundles, defaults)
	if err := next.Validate(); err != nil {
		return nil, diff, err
	}
	return next, diff, nil
}

// BundleAdd returns a validated catalog with package names appended to an existing
// bundle. The final Validate call is the Wave-3 guard that every bundle reference still
// resolves to a real catalog package (specs/0059 D4).
func BundleAdd(c *Catalog, bundleName string, names ...string) (*Catalog, Diff, error) {
	if c == nil {
		return nil, Diff{}, fmt.Errorf("catalog edit: nil catalog")
	}
	bundle, ok := c.bndIdx[bundleName]
	if !ok {
		return nil, Diff{}, fmt.Errorf("bundle add: unknown bundle %q", bundleName)
	}
	for _, name := range names {
		if _, ok := c.Lookup(name); !ok {
			return nil, Diff{}, fmt.Errorf("bundle add: unknown package %q", name)
		}
	}

	pkgList, bundles, defaults := cloneCatalogData(c)
	var added []string
	for i := range bundles {
		if bundles[i].Name != bundleName {
			continue
		}
		present := make(map[string]bool, len(bundles[i].Packages)+len(names))
		for _, name := range bundles[i].Packages {
			present[name] = true
		}
		for _, name := range names {
			if present[name] {
				continue
			}
			bundles[i].Packages = append(bundles[i].Packages, name)
			present[name] = true
			added = append(added, name)
		}
		break
	}
	if len(added) == 0 {
		return nil, Diff{Operation: "bundle-add", BundleName: bundle.Name}, fmt.Errorf("bundle add: no change")
	}

	next := newCatalog(pkgList, bundles, defaults)
	if err := next.Validate(); err != nil {
		return nil, Diff{}, err
	}
	return next, Diff{Operation: "bundle-add", BundleName: bundleName, AddedPackages: cloneStringSlice(added)}, nil
}

// BundleRemove returns a validated catalog with package names removed from an existing
// bundle. Removing is pure repair-friendly: it matches bundle entries by name and then
// relies on Validate to reject any remaining dangling references.
func BundleRemove(c *Catalog, bundleName string, names ...string) (*Catalog, Diff, error) {
	if c == nil {
		return nil, Diff{}, fmt.Errorf("catalog edit: nil catalog")
	}
	if _, ok := c.bndIdx[bundleName]; !ok {
		return nil, Diff{}, fmt.Errorf("bundle remove: unknown bundle %q", bundleName)
	}

	pkgList, bundles, defaults := cloneCatalogData(c)
	remove := make(map[string]bool, len(names))
	for _, name := range names {
		remove[name] = true
	}
	var removed []string
	removedSeen := make(map[string]bool, len(names))
	for i := range bundles {
		if bundles[i].Name != bundleName {
			continue
		}
		kept := bundles[i].Packages[:0]
		for _, name := range bundles[i].Packages {
			if remove[name] {
				if !removedSeen[name] {
					removed = append(removed, name)
					removedSeen[name] = true
				}
				continue
			}
			kept = append(kept, name)
		}
		bundles[i].Packages = kept
		break
	}
	if len(removed) == 0 {
		return nil, Diff{Operation: "bundle-remove", BundleName: bundleName}, fmt.Errorf("bundle remove: no change")
	}

	next := newCatalog(pkgList, bundles, defaults)
	if err := next.Validate(); err != nil {
		return nil, Diff{}, err
	}
	return next, Diff{Operation: "bundle-remove", BundleName: bundleName, RemovedPackages: cloneStringSlice(removed)}, nil
}

// ReverseClosure returns the sorted blast radius for name. Unknown names cannot be
// reported through this legacy-shaped helper, so callers that need the error should use
// ReverseClosureErr.
func ReverseClosure(c *Catalog, name string) []string {
	out, _ := ReverseClosureErr(c, name)
	return out
}

// ReverseClosureErr returns the transitive set of packages whose Requires edges pull in
// name, excluding name itself. This is the Wave-3 blast radius rendered by bump plan
// sheets; an unknown root is a caller error rather than an empty impact set.
func ReverseClosureErr(c *Catalog, name string) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("reverse closure: nil catalog")
	}
	if _, ok := c.Lookup(name); !ok {
		return nil, fmt.Errorf("reverse closure: unknown package %q", name)
	}

	reverse := make(map[string][]string, len(c.pkgs))
	for _, p := range c.pkgs {
		for _, req := range p.Requires {
			reverse[req] = append(reverse[req], p.Name)
		}
	}

	seen := map[string]bool{name: true}
	queue := append([]string(nil), reverse[name]...)
	var out []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		out = append(out, current)
		queue = append(queue, reverse[current]...)
	}
	sort.Strings(out)
	return out, nil
}

func validPackageKind(kind PackageKind) bool {
	switch kind {
	case KindApt, KindNpm, KindBinary, KindPip:
		return true
	default:
		return false
	}
}

func validateBinaryBumpSHA(name string, shaByArch map[string]string) (map[string]string, error) {
	if shaByArch == nil {
		return nil, fmt.Errorf("law-A: binary package %q requires sha256 for every build arch", name)
	}
	out := make(map[string]string, len(buildArches))
	for _, arch := range buildArches {
		digest, ok := shaByArch[arch]
		if !ok {
			return nil, fmt.Errorf("law-A: binary package %q is missing %s sha256", name, arch)
		}
		if !isHex64(digest) {
			return nil, fmt.Errorf("law-A: binary package %q needs a real 64-hex %s sha256", name, arch)
		}
		if digest == sha256Unresolved {
			return nil, fmt.Errorf("law-A: binary package %q has unresolved %s sha256", name, arch)
		}
		out[arch] = digest
	}
	return out, nil
}

func sameBuildArchSHA(oldSHA, newSHA map[string]string) bool {
	for _, arch := range buildArches {
		if oldSHA[arch] != newSHA[arch] {
			return false
		}
	}
	return true
}

func cloneCatalogData(c *Catalog) ([]Package, []Bundle, map[string]string) {
	pkgs := make([]Package, len(c.pkgs))
	for i, p := range c.pkgs {
		pkgs[i] = clonePackage(p)
	}
	bundles := make([]Bundle, len(c.bundles))
	for i, b := range c.bundles {
		bundles[i] = Bundle{
			Name:        b.Name,
			Description: b.Description,
			Packages:    cloneStringSlice(b.Packages),
		}
	}
	return pkgs, bundles, cloneStringMap(c.defaults)
}

func clonePackage(p Package) Package {
	p.SHA256 = cloneStringMap(p.SHA256)
	p.Requires = cloneStringSlice(p.Requires)
	p.Conflicts = cloneStringSlice(p.Conflicts)
	p.BuildFetch = cloneStringSlice(p.BuildFetch)
	p.RuntimeEgress = cloneStringSlice(p.RuntimeEgress)
	if p.Upstream != nil {
		upstream := *p.Upstream
		upstream.Asset = cloneStringMap(p.Upstream.Asset)
		p.Upstream = &upstream
	}
	return p
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}
