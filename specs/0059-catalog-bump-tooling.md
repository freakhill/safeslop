# 0059 — Catalog version tooling (bump / propose-version / add / audit + bundle CRUD)

Status: planned (design note + executable plan).
Branch: `0059-catalog-bump-tooling` · Worktree: `.worktrees/0059-catalog-bump-tooling/`.
Implements the policy canonized in `specs/research/2026-06-30-version-policy-flo.md`.
Builds on the expanded catalog from `feat(policy): expand catalog packages & bundles`
(`main` @ c74e4f2).

## Problem (one line)

The catalog version-selection & bump policy is canonized but unenforceable: there is
no tool to bump a package (fetch all-arch digests, enforce the digest gate + monotonic
floor + soak + reverse-closure), propose compatible versions, add a package, audit
staleness, or manage bundles — every bump is a hand-edit with no guardrails.

### Success criteria

- `safeslop catalog bump <pkg> [--to V|--security]` enforces LAW-A/B/C/D + monotonic
  floor + SemVer-aware soak + reverse-closure re-validation; writes a reviewable plan
  sheet; and applies the edit to the catalog source.
- `catalog propose-version <pkg>` lists upstream candidates (per-Kind livecheck) with
  would-be digests + blast radius; read-only. Non-semver kinds require human confirm.
- `catalog add <pkg>` adds a new pinned entry (channel ban + full Validate).
- `catalog audit` reports staleness (versions-behind-upstream, unmaintained, yanked,
  lane assignment); read-only.
- `bundle {add,remove,list}` mutates/lists bundle membership, re-validating references.
- All engine logic is hermetically unit-tested (no live network); live fetch lives
  behind a `Fetcher` seam. `make check` + `make build` green.

## OFF-LIMITS

- `recipeID` semantics, squid/egress policy (union-only, `egressTooWide` unchanged),
  the resolver's topological/identity algorithms, and the `Package`/`Catalog`/
  `Validate`/`Resolve`/`DefaultCatalog` **API** (callers in `cli` + tests depend on it).
- No new runtime dependency beyond the Go stdlib (`net/http`) — the signed binary stays
  self-contained. No live network or credential APIs in unit tests.
- `versioned Requires` (FLAGGED in the canon) is NOT built; "propose compatible
  versions" lists candidates + blast radius + human-confirm, it does not prove
  compatibility. `Revision` auto-increment + hand-edit lint ships at v1.1 (the field
  is added now, always 0). `ProposedVersion`/`PublishedAt` are NOT built (git branch +
  PR is the soak staging; unmaintained is advisory-fed for MVP).

## Pinned contracts (design decisions — do not re-litigate during execution)

**D1 — catalog storage migrates to an authored `catalog.cue` that renders to an embedded
`catalog.json` (the one new fork; user-directed refinement).** Today the catalog is a
hand-written Go literal (`var catalogPackages = []Package{…}`) in `catalog.go`. A bump
tool cannot robustly mutate a compiled-in Go literal without fragile AST surgery every
bump, and a pure-JSON source would diverge from the repo's everything-is-CUE convention
(`safeslop.cue`, `schema/schema.cue`, `presets/*.cue`). Decision: the source of truth is
`internal/engine/policy/catalog.cue` — authored CUE, validated against a
`#Catalog`/`#Package`/`#Bundle` schema (added to `schema/catalog.cue`), with provenance
as a structured `note:` field per entry. A render step (`make render-catalog`, backed by
the `cuelang.org/go` already in `go.mod`) compiles it to
`internal/engine/policy/catalog.json`, which is committed and `go:embed`-ed; a
`make check` sync check (mirroring the existing `check-assets` pattern) fails CI on
catalog.cue↔catalog.json drift. `DefaultCatalog()` loads the embedded JSON, so the
catalog stays off the hot CUE-eval path and runtime behaviour is unchanged. `bump`/`add`
(W4) decode → mutate structs → re-emit BOTH `catalog.cue` (deterministic, via the cue lib)
and `catalog.json`; both files move in lockstep, giving lockfile-clean diffs.
Provenance comments become a structured `Note` field (rendered in plan sheets — strictly
better than free-form comments). The `Package`/`Catalog`/`Validate`/`Resolve`/
`DefaultCatalog` **API and behaviour are unchanged** (all existing tests stay green), so
this is a reversible storage refactor, not a contract change; the supply-chain boundary
is unchanged (editing `catalog.cue` is still a reviewed code edit). (Considered +
rejected: AST surgery on `catalog.go`; pure-JSON source — diverges from repo CUE
convention; plan-sheet-only with manual apply — loses the "mutate" value the canon
requires.)

**D2 — live fetch behind a `Fetcher` seam; hermetic tests inject fixtures.**
```go
type Fetcher interface { Get(url string) ([]byte, error) }
```
Production: `net/http` (stdlib, no new dep). Tests: a `fixtureFetcher{map[string][]byte}`
fed fixture manifests (SHASUMS256.txt, GitHub releases JSON, npm registry JSON). No test
ever touches the network. Per AGENTS.md hermeticity.

**D3 — per-package upstream discovery via an `Upstream` field.**
```go
type Upstream struct {
    Kind        string            `json:"kind,omitempty"`        // github-releases|npm-registry|pypi|debian-snapshot|node-dist|url-regex
    URL         string            `json:"url,omitempty"`         // discovery endpoint
    Asset       map[string]string `json:"asset,omitempty"`       // arch→artifact URL template ({version})
    ManifestURL string            `json:"manifestURL,omitempty"` // upstream SIGNED aggregate checksum (two-source verify)
}
```
`propose-version`/`audit` are no-ops without it (canon v1-gate), so the migration
annotates every catalog package with its `Upstream`.

**D4 — mutation applies to `catalog.json` in-place; plan sheet always emitted.**
`bump`/`add`/`bundle add|remove` edit `catalog.json` (load → mutate → Validate → write),
then print the plan sheet. The maintainer reviews the `catalog.json` diff, runs
`make check`, commits. `propose-version`/`audit` are read-only (no write).

**D5 — the four LAWs are hard gates inside the bump path (canon).**
LAW-A atomic all-arch real digest (no `sha256Unresolved` survives a bump); LAW-B no
float/non-stable-channel version; LAW-C apt bump coordinates the Debian-snapshot
timestamp (two-part); LAW-D one version per name.

## Waves (sequential; each gates the next)

### Wave 1 — per-Kind version parser (pure, no network) · v1-gate
Unblocks every bump/propose/audit monotonic-floor + soak-classification check.
- [ ] **version.go** — `VersionKind` inference (semver for binary/npm/pip, debian for
      apt, calver for date-stamped like `mise 2026.6.11`); `Parse`; `Compare(a,b) int`
      (monotonic floor); `Magnitude(from,to) (patch|minor|major|revision|date)`; the
      channel ban `IsStableChannel(s) bool` (LAW-B: reject `rc|beta|alpha|nightly|head|
      dev|pre|stable-preview`).
  FILE: `internal/engine/policy/version.go`, `internal/engine/policy/version_test.go`
  VERIFY: `go test ./internal/engine/policy/ -run TestVersion -count=1`
  EXPECTED: parses `22.23.1`/`3.11`/`2026.6.11`; Compare orders them within-kind;
  Magnitude classifies 22.23.1→22.23.5 patch, →22.24.0 minor, →23.0.0 major;
  IsStableChannel rejects `1.0.0-rc1`,`nightly`, accepts `22.23.1`.

### Wave 2 — catalog storage migration to embedded JSON · D1
- [ ] Generate `catalog.json` from the current literal (38 packages + 10 bundles),
      comments → `Note`; add `Revision int`, `Upstream *Upstream`, `PublishedAt string`,
      `Note string` to `Package` (D3 fields; `Revision` always 0 in v1).
  FILE: `internal/engine/policy/catalog.json`, `internal/engine/policy/catalog.go`
  CHANGE: `catalog.go` drops the literal, gains `//go:embed catalog.json` + a loader in
          `DefaultCatalog()`; `Validate` accepts the new optional fields; existing
          `catalogPackages`/`catalogBundles` vars become the loaded data.
  VERIFY: `make check`
  EXPECTED: every existing catalog/resolve/cli-catalog test stays green (API unchanged);
            `TestDefaultCatalogIsWellFormed` still passes (Validate is the authority).

### Wave 3 — catalog edit primitives + reverse-closure (pure) · D4
- [ ] **catalog_edit.go** — `AddPackage`, `BumpPackage(name, version, shaByArch, lane)`
      (atomic version+all-arch sha; LAW-A all-arch-or-none; LAW-C apt snapshot pair),
      `BundleAdd`, `BundleRemove`; each returns `(new *Catalog, diff Diff, error)` and
      re-Validates. `ReverseClosure(name) []string` for blast radius. Pure (no I/O).
  FILE: `internal/engine/policy/catalog_edit.go`, `internal/engine/policy/catalog_edit_test.go`
  VERIFY: `go test ./internal/engine/policy/ -run 'TestCatalogEdit|TestReverseClosure' -count=1`
  EXPECTED: BumpPackage refuses a single-arch sha; refuses a version lower than current
            (monotonic floor via Wave 1 Compare); refuses a channel version; AddPackage
            refuses a duplicate name; BundleAdd re-validates references.

### Wave 4 — bump orchestrator + plan sheet (behind Fetcher seam) · D2, D5
- [ ] **bump.go** + **plansheet.go** — `Bump(cat, name, target, lane, fetcher)`:
      fetch per-arch digests via `fetcher` (all-arch-or-none), cross-check against
      `ManifestURL` when present (`signed-manifest`) else label `self-computed-WEAK`,
      enforce LAW-A/B/C/D + monotonic floor + soak (Waived only by `--security`) +
      reverse-closure re-validation, return `(new *Catalog, PlanSheet, error)`.
      PlanSheet carries version diff, per-arch sha diff, origin, verification method,
      changelog link, CVE id, blast radius, lane + soak/waiver state.
  FILE: `internal/engine/policy/bump.go`, `internal/engine/policy/plansheet.go`,
        `internal/engine/policy/bump_test.go`
  VERIFY: `go test ./internal/engine/policy/ -run 'TestBump|TestPlanSheet' -count=1`
  EXPECTED: with a fixtureFetcher (fixture SHASUMS + artifacts), Bump produces a
            mutated Catalog with real per-arch shas + a plan sheet labelled
            `signed-manifest`; refuses when arm64 fixture is absent (LAW-A); refuses a
            rollback target (monotonic floor); `--security` waives soak, not LAW-A.

### Wave 5 — livecheck discovery + audit (behind Fetcher seam) · D3
- [ ] **upstream.go** — per-Kind strategies list candidate versions from `Upstream`
      (`github-releases`→releases JSON, `npm-registry`→registry JSON, `node-dist`→
      `index.json`, `pypi`→json API, `debian-snapshot`→timestamped, `url-regex`→scrape).
      `ProposeVersions(cat, name, fetcher) ([]Candidate, error)`; `Audit(cat, fetcher)
      (*Report, error)` (versions-behind, unmaintained-advisory, yanked, lane). Annotate
      `catalog.json` with `Upstream` for every package (data task).
  FILE: `internal/engine/policy/upstream.go`, `internal/engine/policy/upstream_test.go`,
        `internal/engine/policy/catalog.json` (Upstream annotations)
  VERIFY: `go test ./internal/engine/policy/ -run 'TestPropose|TestAudit|TestUpstream' -count=1`
  EXPECTED: with fixture release/registry JSON, ProposeVersions lists candidates newest-
            first with would-be per-arch shas; non-semver kinds return candidates flagged
            `requires-human-confirm`; Audit reports a fixture-stale package.

### Wave 6 — CLI wiring (cobra, enveloped JSON + human plan sheet) · D4
- [ ] Extract/extend `internal/cli/cli_catalog.go`: `catalog bump <pkg> [--to V|
      --security]`, `catalog propose-version <pkg>`, `catalog add <pkg>`, `catalog
      audit`; add `bundle` parent with `add`/`remove`/`list`. Machine output via the
      enveloped JSON contract; human plan sheet to stdout otherwise. Live fetch behind
      the `Fetcher` seam (production `net/http`; tests inject fixtures via a wired seam).
  FILE: `internal/cli/cli_catalog.go`, `internal/cli/cli_catalog_test.go`,
        `internal/cli/cli_bundle_test.go`
  VERIFY: `go test ./internal/cli/ -run 'TestCatalog|TestBundle' -count=1`
  EXPECTED: `catalog bump ripgrep --to 14.2.0 --output json` (fixture fetcher) returns an
            OK envelope with the plan sheet + mutated catalog bytes; `bundle list` lists
            bundles; `bundle add` re-validates.

### Wave 7 — verify + docs
- [ ] `make check` + `make build` green; README catalog-bump section;
      `skills/agent-sandbox-ops/SKILL.md` bump-workflow note; AGENTS done-checklist.
  FILE: `README.md`, `skills/agent-sandbox-ops/SKILL.md`
  VERIFY: `make check && make build`
  EXPECTED: exit 0; 77+/77+ ERT; new Go tests green.

## Execution model

Waves are sequential (each depends on the prior wave's API). Dispatch one subagent per
wave with its task block + OFF-LIMITS + the canon ref; controller reviews the diff +
runs the wave's VERIFY before the next wave (subagent-driven-development's
review-checkpoint pattern, adapted to a sequential dependency chain). The controller
never reads implementation code — only the spec, the diff stat, and VERIFY output.

## Method

Design-planning (ordinary): the policy is already canonized (ayo-flo, 2026-06-30), so
this spec consumes it rather than re-deriving. The one new fork (D1 storage migration)
is reversible + API-preserving, so it is pinned here rather than routed through ayo-flo.
