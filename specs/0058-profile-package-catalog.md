# 0058 — Profile package catalog, bundles, and the single golden base image

Status: planned (design note; implementation in later waves)
Branch: `spec-0058-package-catalog`

Supersedes **0055 W4** (minimal images + the narrow `#Tool` enum) and **0055 W6**
(Emacs cockpit chrome) — both are generalized here.
Builds on `main` @ `9494fb7` (merged PR #83), which already carries **0055 W0–W2**
(agent surface, recipe identity, terminal correctness) and the **VM-tier removal**
(0057). Adopts the lockfile from **0056**.
Leaves intact **0055 W3** (reap + GC, Bug A), now implemented after IW4 with the
added GC contract (§N6).

## Why

A profile cannot influence its own container image today. The build args are
hardcoded — `internal/engine/container/identity.go:51-52` pins
`ENABLE_CLAUDE_CODE=true` + `ENABLE_PI=true` for **every** profile — so the image is
one-size-fits-all regardless of what the profile declares. `#Profile` has no notion
of "what tools do I want" (its fields: agent, environment, workspace, network,
egress, secrets, credentials, toolchain). Profiles and session launch are
disconnected: `safeslop-session-new` prompts agent/env/network ad hoc and never
consults a profile. And there is no real profile-*creation* path —
`safeslop-profiles-new` only scaffolds a whole new `safeslop.cue` from a preset, and
there is **no** CLI `profile create` at all (only `profile list` + `profile presets`).

Goal: let a profile declare **which packages** go into its container, chosen from a
**curated catalog**, simplified by premade **bundles**, baked onto **one minimal
golden base**, and created through a real **Emacs UI**.

## Confirmed decisions (user, 2026-06-28)

- **D1 — curated catalog, not arbitrary packages.** Each catalog entry is
  safeslop-owned: a version/digest-pinned, hermetic install recipe with declared
  domains. No free-form `apt`/`npm`/`pip` user input. (Consistent with 0056, which
  rejected third-party devcontainer Features and endorsed "a bespoke
  version/digest-pinned `RUN` + BuildKit cache" as strictly better.)
- **D2 — ONE golden base, all-else-is-a-package.** A single `debian-bookworm-slim`
  (digest-pinned) base for *every* agent. `node`, `uv`, `pnpm`, `bunx`, `mise`,
  `claude-code`, `pi`, … are catalog packages. `claude`/`pi` profiles get a default
  bundle (they need `node` to launch). Diverges from 0055 W4's two-base plan
  (`node-slim` + `debian-slim`); supersedes it.
- **D3 — deliverable is this design note.** Implementation lands later, a fresh
  worktree per wave.

## What this note corrects about its own first draft

A cross-family adversarial review (GLM-5.x · DeepSeek-V4-Pro, §Method) caught two
**overclaims** in the first draft. Both are load-bearing; this version fixes them and
they shape N0/N1/N2:

1. **`recipeID` is a build *cache + dedup* key — not a guarantee of bit-identical
   images.** "Same inputs → same id" is true (correct skip-rebuild + image sharing).
   "Same id → same bytes" is **false** as long as installs are non-hermetic:
   `apt-get install <pinned-leaf>` pulls *unpinned* transitive deps from a moving
   archive, so the same id built 30 days apart yields different bytes.
   Bit-reproducibility is a **separate, stronger** property, delivered only by
   hermetic recipes (sha256-pinned artifacts; apt pinned to a Debian snapshot).
2. **The catalog is the *supply-chain review* boundary — not a build-time *network*
   boundary.** squid is the runtime network boundary and the *only* enforced egress
   policy. The image **build does not traverse squid**, and nothing else enforces
   egress at build time. Build-time **integrity** rests on **checksums**, not the
   network. Build-time **confidentiality** is **not** enforced — named honestly
   below, not papered over with an "audit" label.

## N0 — the package / bundle / catalog model (root)

### `#Package` (catalog entry)

A catalog entry is a safeslop-owned, hermetic, pinned install unit. There is **no
arbitrary-`script` kind** in the MVP catalog — every entry is a structured fetch from
a known package source, which is what keeps the review boundary meaningful (§N2 F1).

```cue
#PackageKind: "apt" | "npm" | "binary" | "pip"

#Package: {
    name:    string                 // catalog key: "node", "uv", "claude-code", …
    kind:    #PackageKind
    version: string                  // PINNED — never a floating tag
    sha256?: string                  // REQUIRED for kind "binary"
    requires?:  [...string]          // other #Package names (closure), e.g. claude-code→node
    conflicts?: [...string]          // mutually-exclusive packages → hard error if co-enabled
    buildFetch?:    [...string]      // domains the BUILD fetches from — PROVENANCE only,
                                     //   NOT enforced today; seed for a future build-proxy (§N2)
    runtimeEgress?: [...string]      // domains the RUNNING package needs — UNIONed into squid (§N2)
}
```

The catalog itself is **in-tree, embedded, safeslop-owned** (a CUE/Go data table,
single source of truth). Extending it is a code edit + review — that review is the
**supply-chain boundary** (which recipes are trusted), distinct from squid (the
runtime network boundary). It is **not** user-supplied at runtime.

### `#Bundle`

```cue
#Bundle: { name: string, description: string, packages: [...string] }  // expanded+deduped+requires-closed
```

Premade bundles (the "make profile creation simpler" ask):

| bundle | packages | for |
|--------|----------|-----|
| `claude`     | node, claude-code   | the `claude` agent (its default) |
| `pi`         | node, pi            | the `pi` agent (its default) |
| `node`       | node, pnpm, bunx    | JS/TS work |
| `python`     | python3, uv         | Python work |
| `base-tools` | ripgrep, fd, jq     | everyday CLI ergonomics |

### `#Profile` additions

```cue
#Profile: {
    // …existing fields…
    bundles?:  [...string]   // #Bundle names
    packages?: [...string]   // #Package names (à la carte, on top of bundles)
}
```

`policy.Profile` gains `Bundles []string` + `Packages []string`. The narrow 0055-W4
`#Tool` enum is **replaced** by the catalog. Orthogonal to the existing `#Toolchain`
(mise/nix *runtime version-manager*): a `#Package` bakes a binary into the image at
build; `#Toolchain` provisions a version-manager at run. `mise` may appear as both
(the package bakes the binary; the toolchain field says "use it") — document the
distinction.

### Resolution (the resolver — shared by build, CLI, lint)

```
resolved(profile) =
    topological_order(                         // build order (correctness)
        requires_closure(
            default_bundle(profile.agent)      // claude→{node,claude-code}, pi→{node,pi}, fish/zsh→{}
            ∪ expand_bundles(profile.bundles)
            ∪ profile.packages
        )
    )
identity_set = sorted(set(resolved))           // identity (dedup / recipeID)
```

- **Identity vs order are separate** (review S2): `recipeID` hashes the **sorted
  set**; the generated build applies packages in **topological order** so `node`
  installs before `claude-code` for *every* enabled subset. A fixed parametrized
  Dockerfile must emit its `RUN` steps in a requires-respecting order, or the
  generator must.
- **Hard errors** (review S3), unit-tested in IW1: a `requires` **cycle**; two
  co-enabled **conflicting** packages. One version per catalog `name` (no in-catalog
  version skew).
- **`default_bundle(agent)`** is included so the agent can launch. It is **additive
  but overridable**: if a profile sets `packages`/`bundles` explicitly and omits the
  agent's launch package, lint **warns** (not errors — power users may bring their
  own); an explicit `--no-default-bundle` (CLI) / `bareAgent` opt-out is honored.
  State as a deliberate constraint, not an emergent one.
- **Migration** (review S5): a profile with empty `bundles`+`packages` resolves to
  exactly `default_bundle(agent)` — **byte-equivalent to today's per-agent
  behaviour**. (Behaviour change to name in the IW2 migration note: today's image
  bakes *both* claude-code and pi; post-IW2 a legacy `claude` profile resolves to
  `{node, claude-code}` only — leaner, functionally complete. No transitional flag.)

## N2 — egress + hermeticity (the crux: three distinct properties)

The first draft collapsed three different guarantees into one "hermetic, audited"
claim. They are separate, and only two are *enforced*:

### 1. Runtime egress — ENFORCED (by squid)

squid is the only network-policy boundary; it governs the running container,
default-deny per-domain. A package whose *running* behaviour needs a domain
(`claude-code`→`api.anthropic.com`) declares `runtimeEgress`; the resolver **unions**
those into that profile's squid allowlist (alongside `#Profile.egress`). **Union-only
— never relaxes default-deny, never changes `network: allow|deny`, never sourced from
untrusted repo input.** (0055/0056 invariant preserved.)

- **Glob semantics** (review S6): match the existing `allowlist.domains` /
  squid `dstdomain` convention — `.example.com` matches the domain + subdomains; a
  bare host matches only that host. A **lint rejects over-wide entries** (bare `*`, a
  single label) in catalog `runtimeEgress` and requires a justifying comment per
  domain. (Attestation-by-observation — diffing declared egress against a sandboxed
  runtime trace — is future hardening, named not built. Today the honest-boundary
  guarantee rests on reviewer competence + the width lint.)

### 2. Build-time integrity — ENFORCED (by checksums, not the network)

Every catalog install is **version-pinned**; this fixes the current
`Dockerfile.agent` unpinned `curl -LsSf https://astral.sh/uv/install.sh | sh`.

- `binary` kind **sha256-verifies** the fetched artifact (`uv`, `mise`, `node` are
  single binaries/tarballs: pin + checksum).
- `apt` kind pins to a **Debian snapshot** (`snapshot.debian.org` timestamp baked
  into the base recipe) so transitive deps are frozen — **without this, content
  addressing is a lie for apt installs** (review F2/DeepSeek FATAL). Prefer `binary`
  kinds; reserve `apt` for what only apt provides, always snapshot-pinned.
- `npm`/`pip` pin exact versions (+ integrity hashes where the registry provides
  them).
- sha256 verification sits **above** TLS, so a checksummed fetch **survives a
  WARP/Gateway TLS-MITM** intact (an org turning on TLS inspection does not break a
  checksummed install — note it; readers will otherwise guess).

This is what makes `recipeID` a meaningful cache/dedup key **and** gives genuine
bit-reproducibility *for hermetic recipes*.

### 3. Build-time confidentiality — NOT enforced (named, not hidden)

The image **build** (`nerdctl`/`docker build` via the `runtime.Engine` seam) runs on
the host / lima engine's network — it does **not** traverse squid, and **no proxy
intercepts it**. WARP is transport, not policy. So a build step could in principle
reach any host. The honest mitigations:

- the MVP catalog has **no arbitrary-`script` kind** — every recipe is a structured,
  pinned fetch from a known source, reviewed (so there is no "run arbitrary shell as
  root and exfiltrate the build context" vector through the catalog);
- the build context is just the repo;
- `buildFetch` domains are recorded as **provenance** and are the **seed** for an
  *optional future* build-time egress-allowlisted network (BuildKit `--network` to an
  allowlist proxy) — **explicitly out of the MVP**. We do **not** claim build-time
  network enforcement we do not have.

> **`recipeID` semantics, restated:** `recipeID = sha256(Dockerfile bytes + baseID +
> sorted ENABLE_* args)` is the build **cache + dedup identity** (same inputs → same
> id → skip-rebuild + share). Bit-reproducibility (same id → same bytes) holds **only
> for hermetic recipes** (property 2). N3 drift-detection compares `recipeID`
> (inputs), not image bytes.

**Ownership cost** (0056 judge-flag, named): pinning snapshots/digests means owning
the bumps — a base re-tag or snapshot/version bump rotates `recipeID` (correct
invalidation, real maintenance). A `safeslop catalog` audit/update path is future
work, out of scope here.

## N1 — the single golden base + image identity

Keep the existing two-image split (`safeslop-base` + `safeslop-tools`), **generalized**
— a minimal evolution of 0055 W1, not a rewrite:

- **`safeslop-base`** = the **golden base**: `debian:bookworm-slim@sha256:…`
  (digest-pinned) + only the universal floor: `ca-certificates`, `curl`, `git`,
  `jq`, `less`, `bash`, `fish`, `zsh`, and `xterm-256color` terminfo — the apt floor
  **pinned to a Debian snapshot** (§N2.2). **No node, no python, no uv.** `bash` is
  included because some agents expect `/bin/bash` (debian-slim ships only `dash` as
  `/bin/sh`). One recipe, one id, built once, shared by *every* profile; rotates only
  when the base digest, snapshot, or floor changes. (Today's base is
  `node:22-bookworm` — IW2 is where the big remaining byte-win lands: ~1.8GB → a few
  hundred MB for shell-only profiles; `node` re-added only where a bundle pulls it.)
- **`safeslop-agent`** (renamed from `safeslop-tools`) = `FROM safeslop-base:<baseID>`
  + the profile's resolved set as `ENABLE_<PKG>` build-arg toggles, each a
  `RUN --mount=type=cache,target=…` guarded by `if [ "$ENABLE_X" = true ]`, emitted in
  **topological order**. `recipeID(agent) = sha256(Dockerfile.agent bytes + baseID +
  sorted ENABLE_* args)`, so a base change propagates (the existing W1 pattern threads
  `BASE=` into the tools args).
- **`node` becomes a package:** install the official, version-pinned Node tarball
  (sha256-verified) into `/usr/local` — it `provides` `npm`, which the
  `claude-code`/`pi` packages `require`. `fish`/`zsh` stay in the base (universal), so
  shell-only profiles get a genuinely tiny image.
- **One parametrized multi-stage `Dockerfile.agent`:** retire
  `Dockerfile.agent.tools` (fold in); **delete** the dead crewai/pydantic-ai/ag2
  ARG/RUN blocks (the 0055 W0 flag-flip becomes permanent). BuildKit cache mounts per
  package-manager. Builds set `DOCKER_BUILDKIT=1` (+ the nerdctl equivalent).
- **`identity.go` stops hardcoding** `ENABLE_CLAUDE_CODE/PI` (today
  `identity.go:51-52`): `agentImageTags()` takes the resolved set and emits the toggle
  args from it. `buildImages` builds the golden base once (skip if id present) then the
  agent image (skip if id present), each under `withBuildLock(id)`.
- **`default_bundle(agent)`** lives in `policy`, shared by build + CLI + lint.

## N3 — the lockfile (adopt 0056)

At the **repo root** (`safeslop.lock.json` — *not* `.safeslop/`, which 0056 verified is
gitignored + policy-walker-skipped):

```json
{ "recipeID": "<12-hex>", "agent": "claude",
  "base": "debian:bookworm-slim@sha256:…",
  "bundles": ["claude"], "packages": ["claude-code", "node"],
  "versions": {"node": "22.x", "claude-code": "2.1.121"} }
```

`recipeID` is authoritative; the rest are its decoded inputs (audit/portability).
`safeslop lock` writes it; `safeslop run` may verify **input** drift (it does **not**
assert byte-identity — §N2). Zero network, no buildx. `.gitignore` handling explicit
in IW3 (the root file must stay committed).

## N4 — CLI

- **`safeslop profile create`** (the missing primitive):
  `--name N --agent A --environment E [--bundle B …] [--package P …] [--workspace W]
   [--network deny|allow] [--no-default-bundle] [--output json]`. Writes/updates a
  `#Profile` into `safeslop.cue` (creating the `safeslop: {version, profiles: {}}`
  envelope if absent), validating via `policy.Load` **before** write.
- **`safeslop profile show <name>`** → the profile + its **resolved** set + the
  dry-run `recipeID` (identity without building).
- **`safeslop catalog list [--bundles] --output json`** → the catalog + bundles —
  the single in-tree source that drives the Emacs picker.
- Resolver (expand → requires-closure → topological → dedup; default-bundle;
  conflict/cycle errors; runtimeEgress union) lives in `policy`, shared by
  build/CLI/lint. Keep `profile list` / `profile presets`.

## N5 — Emacs

- **`safeslop-profiles-create`** — a structured flow replacing the
  scaffold-from-preset gap: name → agent (`completing-read`) → environment →
  **bundles** (`completing-read-multiple` from `catalog list --bundles`) →
  **packages** (`completing-read-multiple` from `catalog list`) → network → workspace
  → `profile create` → refresh.
- **Bridge profile ↔ session:** let `safeslop-session-new` optionally pick an existing
  profile (today fully ad hoc), keeping ad-hoc as fallback.
- **Portal columns** (0055 W6): `Recipe` (resolved bundles/packages) + `Image`
  (`recipeID` tag), from session JSON.
- **Async build-progress buffer** (0055 W6): reuse the `make-process` async pattern
  from `safeslop-install.el` for the slow lazy first build (mind its exit-code
  handling).
- Gated by `make test-emacs` (eat optional, `term` fallback — 0055 D7/W2).

## N6 — implementation wave plan (terminal)

No `image-matrix` prerequisite — it **already landed** (PR #83, `main` @ `9494fb7`:
0055 W0–W2 + VM removal). Each wave is its own worktree off `main`, gated by
`make check` + `make build`.

- **IW1 — model + catalog + resolver (N0, N2).** `#Package`/`#Bundle`/catalog in the
  schema; `policy.Profile.{Bundles,Packages}`; the resolver. Pure Go + CUE, fully
  unit-testable. **Tests:** requires-cycle → error; conflict → error; topological
  order; empty profile → `default_bundle(agent)` (migration); `runtimeEgress` width
  lint. Foundational, largest.
- **IW2 — golden base + parametrized Dockerfile + identity (N1).** `debian-slim`
  digest-pinned base, apt floor **Debian-snapshot-pinned**; drop node/python/uv from
  base; one multi-stage `Dockerfile.agent` with per-package toggles + cache mounts in
  topological order; retire `Dockerfile.agent.tools`; `identity.go` reads the resolved
  set; generalized `recipeID`; default-bundle wiring; **a build-network assertion
  check** (confirm the docker-host *and* nerdctl-lima build egress path actually
  works — review S1); the migration note; live byte-win measurement. Absorbs 0055 W4.
  Honor the asset two-copy sync (`library/layer/container` → `make
  sync-container-assets` → `make check-assets`).
- **IW3 — lockfile + CLI (N3, N4).** `safeslop.lock.json` + `safeslop lock`;
  `profile create` / `profile show` / `catalog list`.
- **IW4 — Emacs create-UI + picker + portal columns (N5).** Absorbs 0055 W6.
- **(done) 0055 W3 — reap + GC (Bug A), with an added contract.** Independent of
  IW1–IW4. **GC is profile-anchored** (review S4/DeepSeek): it never reaps an image
  that is (a) the current `recipeID` of any successfully-resolving profile, (b)
  referenced by a committed `safeslop.lock.json`, or (c) attached to a live session;
  `keep-N`/`until` applies only to the unreferenced remainder. The per-package-set
  image cardinality this design introduces made that contract **non-optional**, not
  a minor carry-over.

## Invariants (carried from 0053 / 0055 / 0056 / 0057)

- Catalog is safeslop-owned + pinned; **no** third-party devcontainer Features (0056).
- squid is the runtime network boundary; `runtimeEgress` only **unions** declared
  domains, never opens default-deny, never sourced from untrusted repo input
  (0055/0056). The catalog review is the **supply-chain** boundary — a *different*
  thing from squid; do not conflate them.
- Honest `EnvTier`; no sandbox/VM reintroduction; `environment` required, no default
  (0053/0057).
- Asset two-copy sync for every `Dockerfile`/allowlist edit; `compose.yml.tmpl`
  embedded-only, not synced (0055/0056).
- v1 JSON contract; `claude-code` stays an accepted alias, dropped from UI surfaces;
  `AGENT_UNSUPPORTED` golden keeps firing via a still-unsupported agent (0055 D1).

## Method footer

- **dago.** Plan: a hand-built 7-node DAG (N0→N2→N1→{N3, N4→N5}→N6); **ayoflo waived**
  for planning — predecessor specs 0055/0056 already decompose the build pipeline and
  the three architecture forks were user-confirmed, so the novelty is node *content*,
  not graph *shape*. The cycle/mis-ordering risk ayoflo would catch was instead caught
  by the final cross-family adversarial FLO.
- Node resolution: all host-resolved (each well-constrained by 0055/0056 + live code);
  the egress crux (N2) resolved by separating the three properties
  (runtime-enforced / build-integrity-by-checksum / build-confidentiality-unenforced).
- **Verification — cross-family adversarial FLO** over the whole note:
  **GLM-5.x** (z.ai, subscription) + **DeepSeek-V4-Pro** (OpenRouter, ZDR-enforced),
  both prompted to refute. They converged on two FATAL overclaims — (F2) input-hash
  presented as hermetic; (F1) build-time network presented as an enforced/audited
  boundary — plus serious gaps S1–S6 (lima build-net validation, topological order,
  resolver cycles/conflicts, profile-anchored GC, legacy migration, runtimeEgress
  width). Gen-1 verdicts: GLM **RECONSIDER until F1/F2 fixed**; DeepSeek
  **SOUND-WITH-FIXES**. This revision integrates every finding (the "What this
  corrects" section + the N2 three-property rewrite + apt-snapshot pin + S1–S6).
  Sakana Fugu was available but **skipped** — two convergent skeptics gave clear
  signal; a third (per-token-paid) was redundant.
- **Load-bearing decisions** (do not re-litigate): `recipeID` is a cache/dedup key,
  not bit-reproducibility; the catalog is the supply-chain review boundary, distinct
  from squid (the runtime network boundary); apt installs require a Debian-snapshot
  pin; build-time network is unenforced (integrity via checksums); GC is
  profile-anchored.
