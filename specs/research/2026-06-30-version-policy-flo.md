# Catalog package-version selection & bump policy

Date: 2026-06-30 · Status: **canon (locked)** — Expansion → ayo → FLO.
Baseline verdict 6.50/10; **4 forced fixes applied → 7.28/10; re-evaluation: no fatal
flaws.** This is the policy the catalog bump/add tooling (specs/0059, to be written)
encodes, and the rule a reviewer applies to a hand-edited bump. It *completes* the
pinning story: `pinning.go` bans floats; this policy says WHICH pin and HOW to bump.

## Verdict

Pick the **smallest sufficient step**; verify it with a **real all-arch digest**
before it lands; **soak** every non-security bump and **fast-track** only security
bumps (MVP: explicit human confirm; future: a signed `fast_track` feed). The catalog
is safeslop-owned, one-pin-per-name, one golden base (0058 D2), mutation-gated by code
review — the **supply-chain** boundary, distinct from squid the **runtime net**
boundary. This policy makes that reviewed edit mechanical, honest about repro gaps,
and unable to ship an unpinned or rolled-back artifact. **LAW-A** (atomic all-arch
real digest) and the **monotonic floor** are hard overrides; **soak** and
**reverse-closure blast-radius** are soft gates against xz/liblzma-class burns.

Egress policy is **unchanged** by bumps: `egressTooWide` still governs, and a package's
`RuntimeEgress` is only ever unioned into the squid allowlist (never relaxed).

## Selection defaults (per-Kind)

"Smallest sufficient step" is **Kind-specific** because the catalog mixes semver
(`22.23.1`), Debian-epoch (`3.11`), and calver (`2026.6.11`):

- **semver kinds** (`binary`, `npm`, `pip`): latest **patch** of the currently-pinned
  **minor**. A minor/major step is an explicit review event, never the default.
  ("minor"/"patch" are defined ONLY for semver.)
- **apt** (Debian epoch + upstream-revision): the latest Debian **revision** within
  the **same upstream version** on the maintained suite (bookworm / bookworm-security
  backport); never cross the upstream version without explicit review. The snapshot
  timestamp moves with it (LAW-C).
- **calver** (e.g. `mise 2026.6.11`): the latest date within the **same calendar-year
  major**; monotonic date floor.
- **Until the per-Kind version parser exists (OWED — gates v1), non-semver kinds are
  NOT mechanically bumpable**: `propose-version` may list candidates but a **human**
  selects + confirms; the tool never auto-selects for non-semver kinds. (This kills the
  false promise of universal mechanical bumpability flagged in F1.)
- **X2 (MVS-lowest vs SemVer-maximize) → smallest sufficient step.** In a
  one-version-per-name, review-gated catalog there is no satisfier range to maximize or
  minimize over, so "newest-compatible" has no mechanical meaning; "smallest
  sufficient" is the only coherent default.
- **Security bump = lowest patched version on the currently-pinned lineage**
  (Debian/Ubuntu `fixed-in` backport semantics). Jump to a newer major only when the
  advisory has no backport on the still-maintained line; never chase newest-upstream
  for a CVE.
- **Channel ban (LAW-B, extends `pinning.go`):** bump tool and `Validate` refuse
  `rc`/`beta`/`alpha`/`nightly`/`head`/`dev`/`pre`/`stable-preview` — stable releases
  only.
- **Monotonic floor:** a bump may never lower below the currently-pinned value
  (rollback ban). Comparison is per-Kind (OWED parser).

## The digest gate (LAW-A) — scoped to `binary`

- **LAW-A — atomic all-arch real digest (for `binary`).** For a `binary` package,
  `Version` and `SHA256` commit together or not at all, and every arch in
  `buildArches={amd64,arm64}` must carry a real 64-hex sha256 (`isHex64`);
  **all-arch-or-none** — never ship one arch pinned and the other pending.
- **No build against `sha256Unresolved`:** `BuildReady()`/`PackagePending()`/
  `BuildReadyFor()` already refuse; the bump tool inherits this — a bump that cannot
  obtain a real 64-hex sha256 for every arch is REFUSED.
- **Never leave the sentinel after a version change; never auto-fill a stale digest;**
  re-fetch and refuse otherwise.
- **Two-source > single-source (WEAK):** prefer a digest cross-checked against an
  upstream SIGNED aggregate manifest (node `SHASUMS256.txt`, Go checksum DB, GitHub
  release attestation), labelled `signed-manifest`; a self-computed single download is
  fallback only, labelled `self-computed-WEAK`.
- **npm/pip** have no per-arch sha256 (the code scopes sha256 to `KindBinary`); their
  integrity is the separate, named-weaker transitive mechanism below — do not conflate.

## Kind rules

- **`binary` is the preferred kind** (sha256-verifiable, multi-arch, hermetic → real
  bit-repro for the recipe). Reserve `npm`/`pip`/`apt` for what only they provide.
- **`apt` = two-part transaction (LAW-C):** a leaf `Version` bump and the
  Debian-snapshot timestamp baked into the base recipe must move **together**; refuse
  one without the other. Prefer `binary`; reserve `apt` for apt-only needs (e.g.
  `python3`).
- **`npm`/`pip` — transitive gap NAMED, not hidden** (mirrors 0058
  build-confidentiality-honesty): a top-level version pin leaves transitives floating
  (npm permits re-publish without a new version). The recipe SHOULD install via
  `npm ci` / `pip install --require-hashes` against a committed lockfile/integrity map.
  MVP MAY defer the lockfile but MUST carry a per-entry comment documenting the gap.

## Lanes & soak

- **Two lanes:**
  - **`--security` (fast-track):** MVP = **explicit human-confirmed `--security` flag +
    documented CVE id**; it **WAIVES the soak clock** (a soft gate) but **NEVER a hard
    gate** (LAW-A/B/C/D, monotonic floor all still enforced). **The SIGNED `fast_track`
    feed (machine-verifiable waiver: signing identity, key, validity window) is NAMED
    FUTURE HARDENING**, consistent with 0058 ("advisory feed = named, not built") and
    the prior cross-family FLO; it is NOT pretended to exist. When the feed lands it
    replaces the human confirm; until then the human confirm is the honest, auditable
    waiver recorded on the plan sheet.
  - **normal:** quarantined for a SemVer-aware soak window; full review.
- **X1 (soak mechanism) → SemVer-aware clock floor scaled by bump magnitude:**
  patch < minor < major (illustrative tunable: patch ≥3d, minor ≥14d, major ≥30d). For
  apt use Debian-revision magnitude; for calver use date delta. This dissolves the
  soak-vs-fastfix contradiction; waived ONLY by the security waiver.
- **X3 (`--urgent` third tier) → DEFER (YAGNI):** model a non-security must-fix as a
  normal bump with a documented waiver.
- **X6 (`ProposedVersion` soak-staging field) → DEFER:** git branch + PR + CI is the
  staging surface.

## Dependency-aware bumping

- **Reverse-closure re-validation:** bumping a foundational package (`node`, `rust`,
  `go`, `python3`) re-runs `Validate` over its entire REVERSE `requires`-closure and
  reports the blast radius on the plan sheet; it proceeds only if the closure still
  resolves (no new conflict/cycle).
- **HONEST LIMIT (X5):** because `Requires` is **NAME-ONLY** today, the blast-radius
  list carries **no compatibility signal** — it is a name list, not a "safe to bump"
  proof. Auto minor/major of foundations is therefore GATED behind manual review until
  versioned `Requires` lands.
- **X5 (versioned `Requires: [{name, minVersion}]`) → FLAGGED:** name-only `Requires`
  is the blocker for safe automated minor/major; MVP keeps name-only + manual review +
  reverse-closure build gate; versioned `Requires` is the documented near-term enabler.
  Promote before any non-interactive minor/major bump is attempted.

## Advisory / revocation

- **Yank/revocation:** HARD-FAIL on NEW pins against a yanked version; WARN on a
  currently-pinned version that becomes yanked; **NEVER auto-delete** (preserves
  bit-repro for profiles that already built against it). Distinct from CVE.
- **`fixed_in` / min-version:** the advisory/revocation feed carries a `fixed_in` per
  CVE; a security bump selects from versions ≥ `fixed_in` on the current lineage. MVP:
  `fixed_in` is operator-supplied (from the CVE advisory) since the signed feed is
  future work.
- **Unmaintained audit tier:** `audit` reports an `unmaintained` flag (advisory feed +
  manual annotation for MVP).

## Command surfaces (mutation-separated)

- `safeslop catalog propose-version <name>` — **read-only:** discovers candidates per
  upstream source (livecheck), prints a plan sheet, mutates nothing. (GATED on OWED
  `Upstream`/`Livecheck` + the per-Kind parser for non-semver.)
- `safeslop catalog bump <name> [--security]` — **mutates:** enforces LAW-A/B/C/D,
  monotonic floor, soak window (or waiver), reverse-closure re-validation; writes the
  plan sheet.
- `safeslop catalog add <name>` — **mutates:** new `Name` + kind + pinned version +
  real digests; channel ban + full `Validate`.
- `safeslop catalog audit` — **read-only:** staleness report (versions-behind-upstream,
  unmaintained, yanked, lane assignment); proposes, never commits.
- `safeslop bundle {add,remove,list}` — membership mutation/listing; each
  `add`/`remove` re-validates that bundles still reference real packages.
- **Plan sheet on every bump:** version diff, per-arch sha256 diff, origin URL,
  verification method (`signed-manifest` vs `self-computed-WEAK`), changelog link, CVE
  id (if security), blast-radius list, lane + soak/waiver state.
- **X4 (`Revision`) → OWED, deadlock removed (F2):** integer `Revision` (default 0).
  The bump **TOOL AUTO-INCREMENTS `Revision`** when it detects a same-`Version` byte
  rotation (it fetches, compares sha, and writes `Version`-unchanged + `Revision`+1 +
  new-sha as ONE atomic unit) — there is no refusal of the tool's own write. The
  REFUSE is only a `Validate`-time **LINT against a HAND-edit** that changes sha256 at
  an unchanged `Version`+`Revision` (catches a human who forgot); that lint reports,
  it does not deadlock, because the fix path is "run `catalog bump`, which
  auto-increments".

## Schema deltas (with v1-executability gate)

| field | status | v1-gate | why |
|---|---|---|---|
| per-Kind version parser (semver / Debian epoch+revision / calver) | OWED | **GATES v1** | monotonic floor + ordering + "smallest sufficient step" for non-semver are impossible without it; bump/propose/audit non-functional |
| `Upstream`/`Livecheck` (per-package discovery source) | OWED | **GATES v1** | `propose-version` + `audit` are no-ops without it |
| `Revision` (same-`Version` byte-rotation counter) | OWED | v1.1 | same-`Version` re-tags are MANUAL in v1; ship v1 without, add when the first re-tag occurs |
| `PublishedAt` (upstream release date) | FLAGGED | no | unmaintained audit wants upstream age; MVP derives `unmaintained` from the advisory feed + manual annotation |
| versioned `Requires: [{name, minVersion}]` | FLAGGED | no | blocker for safe automated minor/major of foundations; promote before non-interactive minor/major |
| `ProposedVersion` (soak-staging field) | DEFERRED | no | git branch + PR + CI is the staging surface |

## Rejections / Deferred / Owed

- **Rejected:** soak-by-flat-clock (blocks urgent patches / fails major-burn);
  always-newest selection (inflates blast radius); SemVer-maximize (no range to
  maximize over); a standing `--urgent` lane (YAGNI); `ProposedVersion` field
  (duplicates git staging); pretending the signed `fast_track` feed exists (contradicts
  0058 honesty).
- **Deferred (named future hardening):** the signed `fast_track` advisory/revocation
  feed; the npm/pip committed lockfile/integrity map; `PublishedAt`-driven unmaintained
  detection.
- **Owed (gates v1 tooling):** per-Kind version parser; `Upstream`/`Livecheck`
  metadata. **Flagged (near-term):** versioned `Requires` (unlocks safe auto
  minor/major); `Revision` (v1.1, on first same-`Version` re-tag).

## Method

**Expansion sources:** `internal/engine/policy/catalog.go` (`Package`, `Validate`,
`BuildReady`/`PackagePending`, `sha256Unresolved`, `buildArches`, `egressTooWide`),
`resolve.go` (topo + identity + egress union), `pinning.go` (float-ban syntax lint),
spec `0058` (D1/D2, N0/N1/N2, recipeID-as-cache-key, apt-snapshot pin,
build-confidentiality-unenforced-honestly), and the frozen prior cross-family FLO
verdicts (TIME defense vs sha+sig; monotonic epoch floor; sha256+provenance anchor;
advisory min-version + `fast_track`).
**ayo prior-art lanes (xhigh):** DeepSeek-V4-Pro · Gemini · GLM-5.x — corpus Nixpkgs
(stable/unstable soak, r-ryantm bot, ofBorg/Hydra), Debian/Ubuntu security
(bookworm-security fixed-in backports, stable-updates), Homebrew (devel/stable,
livecheck, bump-formula-pr, revision/rebuild), Rust (stable/beta/nightly train,
RustSec, yank, Cargo.lock), Go (MVS, module proxy + checksum DB), npm (SemVer, audit,
package-lock integrity), distro security teams (USN, fast-track vs normal-security).
**FLO:** 1 worker (`flo-worker`, xhigh) drafted; 1 adversarial cross-family evaluator
(`flo-evaluator-deepseek`, xhigh) scored + named fatal flaws; host computed total +
applied forced fixes + re-evaluated.
**Rubric (weights sum 100):** Security-correctness 30 · Reproducibility-honesty 25 ·
Maintenance/automatability 20 · Upgrade-safety/blast-radius 15 · Frozen-contract-fit 10.
**Deterministic LAWs (override → criterion 0):** LAW-A atomic all-arch real digest / no
`sha256Unresolved` build · LAW-B no float or non-stable-channel version · LAW-C apt
snapshot-timestamp coordination · LAW-D one version per catalog name.
**Scores:** baseline 6.50 (Sec 6.5 / Repro 7.5 / Maint 4.5 / Blast 6.5 / Fit 8.0) →
**after 4 forced fixes 7.28** (Sec 7.0 / Repro 9.0 / Maint 4.5 / Blast 7.5 / Fit 9.0);
re-evaluation: **no fatal flaws**, F1–F4 confirmed landed. Remaining weaknesses are
honest load-bearing gaps (OWED/FLAGGED), not contract breaches; the tool implementation
is the job of specs/0059, not this canon.
