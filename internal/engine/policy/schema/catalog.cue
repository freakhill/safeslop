package safeslop

// Embedded catalog schema (specs/0058 N0, 0059 W2). The curated, safeslop-owned
// package catalog. The source of truth is catalog.cue; it is rendered to catalog.json
// and embedded into the binary via go:embed. Extending the catalog is a code edit +
// review — that review IS the supply-chain boundary, distinct from squid (the runtime
// network boundary). This schema validates catalog.cue at render time (cue-vet quality),
// catching a malformed pin before it ships.

// PackageKind classifies how a catalog package is installed at image-build time. There
// is deliberately no arbitrary-"script" kind (specs/0058 N2 F1) — every recipe is a
// structured fetch from a known source, which is what keeps the review boundary real.
#PackageKind: "apt" | "npm" | "binary" | "pip"

// Upstream is a package's machine-readable discovery source for `catalog propose-version`
// and `catalog audit` (specs/0059 W5). Tooling metadata only — never affects the
// resolver or the build path.
#Upstream: {
	kind?: "github-releases" | "npm-registry" | "pypi" | "debian-snapshot" | "node-dist" | "url-regex"
	url?:         string                 // discovery endpoint
	asset?:       {[string]: string}     // arch -> artifact URL template ({version})
	manifestURL?: string                 // upstream SIGNED aggregate checksum (two-source verify)
}

// #Package: one curated, version+digest-pinned install unit.
#Package: {
	name:           string                       // catalog key, e.g. "node", "claude-code"
	kind:           #PackageKind
	version:        string                       // PINNED — never a floating tag
	sha256?:        {[string]: string}           // arch -> 64-hex; REQUIRED for kind "binary"
	requires?:      [...string]                  // other #Package names pulled into the closure
	conflicts?:     [...string]                  // packages that must not be co-enabled
	buildFetch?:    [...string]                  // domains the BUILD fetches from (provenance only)
	runtimeEgress?: [...string]                  // domains the RUNNING package needs; unioned into squid
	note?:          string                       // provenance / review rationale; rendered in plan sheets
	revision?:      int | *0                     // same-version byte-rotation counter (0059, v1.1 use)
	upstream?:      #Upstream
	publishedAt?:   string                       // upstream release date (canon FLAGGED)
}

// #Bundle: a named set of catalog packages — the premade simplification.
#Bundle: {
	name:        string
	description: string
	packages:    [...string]
}

// #Catalog: the full in-tree data — packages, bundles, and the agent -> default-bundle map.
#Catalog: {
	packages: [...#Package]
	bundles:  [...#Bundle]
	defaults: {[string]: string}   // agent -> default bundle name (e.g. claude -> "claude")
}

catalog: #Catalog
