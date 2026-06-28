# 0056 — Dev Containers (containers.dev) integration assessment

> **Substrate note:** the VM tier was removed in **specs/0057**; safeslop is
> container-only, so the container/VM substrate discussion below is moot (the
> lockfile is simply container-scoped).

Status: research note (ayoflo: expansion → cross-model research → source-grounded FLO).
Evaluated against specs/0055 (image-matrix redesign).

> **Status caveat (verified against the tree):** of 0055 only **W0** is built today.
> `recipeID` (W1, planned in a not-yet-existing `internal/engine/container/identity.go`),
> the `safeslop.managed`/`safeslop.session` **labels** (W3), and BuildKit
> `--mount=type=cache` (W4) **do not exist yet** — the current `Dockerfile.agent`
> still installs `uv` via the unpinned `curl … | sh` this note criticises. Every
> hook below therefore targets a *planned* 0055 wave; none is a change to existing
> code. Where the draft said "already", read "will, in W<n>".

## Verdict

**Adopt-selectively, narrow — the valuable integration is conceptual, not
wholesale.** Borrow the **lockfile idea** (it reinforces 0055's planned content-hash
recipe identity). **Reject** consuming/publishing Features, the devcontainer CLI,
lifecycle hooks, dotfiles, and IDE/Codespaces interop — each fights safeslop's
hermetic identity, hardened boundary, or the nerdctl/no-buildx engine seam. The
`devcontainer.metadata` label is **marginal — probably skip** (see axis 5). The
optional `devcontainer.json` *ingest* path is a real-but-later convenience, gated
by a strict allowlist.

There is no version of "ride the devcontainer stack" that keeps safeslop honest:
the standard optimises a mutable, root-capable, open-network developer box;
safeslop optimises a read-only, cap-dropped, default-deny boundary.

## Current-spec facts this rests on (Exa-grounded; judge-corrected)

- Features are OCI artifacts, pinnable by version **or digest** via
  `devcontainer-lock.json` (records the Feature's OCI manifest SHA-256). **But the
  lockfile pins the Feature, not the binaries its `install.sh` fetches at build.**
- A Feature's `install.sh` runs as root at build. Non-hermetic installers are an
  *ecosystem norm, not a spec invariant* — the spec does not forbid a hermetic
  Feature (this nuance matters for the "considered-and-rejected" section).
- `privileged`/`capAdd`/`securityOpt` and resolved Feature entries live in the
  `devcontainer.metadata` image label — which is a **JSON array of
  `{id, version, …}` Feature records**, not a free-form blob.
- Lifecycle hooks: `onCreateCommand`/`updateContentCommand`/`postCreateCommand`/
  `postStartCommand`/`postAttachCommand`; Features can contribute commands.
- The `@devcontainers/cli` is a **Node.js** tool, Docker-centric (no first-class
  nerdctl target). It drives the daemon's BuildKit; the **buildx CLI plugin** is
  not strictly required (only the `--mount=type=cache` Feature-caching optimisation
  benefits from it). The disqualifier is the Node + Docker-daemon coupling vs the
  `runtime.Engine` (docker-host / nerdctl-lima) seam, not buildx per se.

## The six integration axes, triaged

| # | Axis | Verdict | Mechanism / reason |
|---|------|---------|--------------------|
| 1 | Consume Features as tool-layers | **REJECT** | `install.sh` typically fetches **unpinned** binaries at build (`devcontainer-lock.json` pins the Feature, not what it `curl`s) → breaks content-hash identity; needs network (fights WARP + forces OCI registries into the squid default-deny allowlist); untested on nerdctl/lima. A bespoke version/digest-pinned `RUN` + BuildKit cache (0055 W4) is hermetic and strictly better. (A hermetic-subset middle ground exists — see below — and is also rejected, for now.) |
| 2 | Ingest a repo's `devcontainer.json` (declarative fields only) | **OPTIONAL / LATER, gated** | Parse an **allowlisted** subset (`image`, filtered `containerEnv`) as recipe hints; **discard** `mounts`/`runArgs`/`*Command`/`dockerComposeFile`/dotfiles. safeslop's Dockerfile + compose + squid + security flags always win. |
| 3 | devcontainer CLI as build/up engine | **REJECT** | Node.js + Docker-daemon coupling breaks the `runtime.Engine` seam; opaque Feature composition conflicts with the planned `recipeID` + `withBuildLock`. All three research lanes rejected it. |
| 4 | Publish agents (claude/pi) as Features | **REJECT** | The Feature's `install.sh` would fetch the agent from a release URL → still needs egress, no offline-reproducibility win for the VPN audience. Distribution misfit. |
| 5 | Emit `devcontainer.metadata` for interop | **MARGINAL — probably SKIP** | IDE attach is cargo-cult for a headless runner **and** breaks under the boundary (VS Code server wants `~/.vscode-server` writable vs `read_only`; egress to `update.code.visualstudio.com` vs default-deny). The label's schema is a *Feature-entry array* safeslop can't honestly populate — emitting it is either inert (nothing consumes it) or **misleads** a reader into thinking it's a normal dev box, in tension with `EnvTier` honesty (`policy.go`). Skip, or emit only an explicitly non-authoritative marker. |
| 6 | Lifecycle hooks / dotfiles | **REJECT** | `postCreateCommand` etc. and dotfile cloning run arbitrary commands with egress at create-time → violate `read_only` + default-deny. `updateRemoteUserUID` mutates `/etc/passwd` at start (only `/tmp`,`/var/tmp` are tmpfs) → violates `read_only`. |

## The one genuinely valuable borrow: a content-hash lockfile

The stock `devcontainer-lock.json` pins only Feature artifact digests — for a
project whose identity is the *whole* image that is a **misleading "locked"
impression** (it ignores the base image and everything Features fetch). safeslop
should implement its **own** lockfile (devcontainer-lock-*inspired*, not the same
schema) keyed on the 0055 `recipeID`:

```
./safeslop.lock.json          # repo root — NOT under .safeslop/ (see below)
{
  "recipeID": "<12-hex>",     # authoritative: sha256(dockerfile-bytes + sorted build-args)
  "agent": "claude", "substrate": "container",
  "base": "node:22-bookworm-slim@sha256:…",     # decoded provenance (already inside recipeID)
  "toolLayers": ["uv", "mise"],                  # the ENABLE_* toggles that were on
  "miseVersion": "2026.6.11"                     # versions only where 0055 actually pins
}
```

`recipeID` is the **authority**; the other fields are its *decoded inputs*,
recorded for human audit / portability — not independent hash sources (an earlier
draft listed a redundant `buildArgHash` + `baseDigest`; both are already subsumed
by `recipeID = sha256(dockerfile + sorted build-args)`). This gives honest
"locked" semantics, needs **zero network and no buildx**, and fits the engine seam.

**Path correction (the draft's worst bug):** the lockfile must live at the **repo
root** (`safeslop.lock.json`), *not* under `.safeslop/`. `.safeslop/` is git-ignored
(`.gitignore:17`), is the reconcile-wiped ephemeral runtime root
(`container.go` `runtimeRoot`), **and** is skipped by the policy repo-walker
(`pinning.go:53` `case ".git",".worktrees","node_modules",".safeslop": SkipDir`) —
so a lockfile there is uncommittable and invisible, the exact opposite of the
"inspectable + portable" identity this note exists to add. (Root `safeslop.lock.json`
is safe: `.gitignore:24`'s `/safeslop` matches only the built binary.)

**Substrate scope (corrected):** this lockfile is **container-substrate today**.
`recipeID(dockerfile, buildArgs)` is Dockerfile-shaped; the VM path has no analog —
`provisionToolchain(ctx, ip, kind)` (`vm/vm.go:181`) keys on toolchain *kind*
(mise/nix), carrying no comparable hash. 0055 D6/W5 only *gesture* at a future VM
recipe marker. So VM serialisation is an **open question**, not a solved
"substrate-neutral" property.

**Ownership cost (judge-flagged):** pinning tool/base digests ourselves means owning
digest resolution — registry auth, multi-arch manifest walking, and the fact that a
base-image re-tag rotates `recipeID` (correct invalidation, a feature, but real
work). Worth naming before committing to it.

## Considered and rejected: a hermetic-subset Feature path

A reviewer argued Features-rejection conflates "Features *commonly* `curl` unpinned
binaries" with "Features are *inherently* non-hermetic," and that safeslop could
**filter** to a hermetic subset — e.g. run `install.sh` under a seccomp trace that
fails closed on any non-OCI-registry `connect()`, or statically reject
`curl|wget|git clone|pip install|apt-get update`, recovering tool-layer
composability while keeping a content-addressable `recipeID`.

This is real, and noted as the **single reconsideration trigger**. It is rejected
*now* because: (a) sandboxing/auditing arbitrary third-party `install.sh` is *more*
security-review surface than writing the ~one-line bespoke `RUN` it would replace;
(b) it does not fix the WARP-MITM-CA or nerdctl/lima-untested problems; (c) the
composability win is marginal for a four-tool set (uv/pnpm/bunx/mise). Revisit only
if the tool-layer set grows large and a vetted hermetic-Feature registry exists.

## If ingest is ever built (`safeslop import-devcontainer`, axis 2)

A `devcontainer.json` is **untrusted repo input**. Ingest must **allowlist** safe
declarative fields and **discard** every constraint-violation vector:

- KEEP (filtered): `image` (as a base hint, re-pinned by digest), `containerEnv`
  (name-allowlisted, never secrets/PATH-overrides).
- DISCARD: `mounts` (vs `read_only`), `runArgs` (can re-add caps / `--privileged` /
  `--network=host`), all `*Command` lifecycle hooks (egress + mutation),
  `dockerComposeFile` (multi-substrate + Compose-API coupling), dotfiles.

safeslop's boundary flags and squid egress are **never** sourced from the repo file.

## Concrete hooks into 0055 (all target planned waves)

- **W1 (recipe identity).** When `identity.go`/`recipeID` land, add a `safeslop lock`
  path writing `./safeslop.lock.json` (schema above). ~small.
- **W3/W4 (labels).** If axis 5 is pursued at all, the `devcontainer.metadata`
  **image `LABEL`** belongs in the Dockerfile beside the planned `safeslop.managed`
  image label — *not* on the compose service (`safeslop.session` is the runtime
  compose label). Default: skip.
- **Ingest (#2).** A separate, later `safeslop import-devcontainer` with the
  allowlist above; never the build engine.

> **0055 defect found en route:** 0055 W2/W3 reference
> `library/layer/container/compose.yml.tmpl`, which **does not exist** — the only
> template is the embedded `internal/engine/container/assets/compose.yml.tmpl`
> (library has `docker-compose.yml`, and the template is **not** in the Makefile
> `SYNCED` set). 0055's compose hooks must target the embedded path directly. Fix in
> 0055 before building W2/W3.

## What this is explicitly NOT

Not adopting the devcontainer build/run model, Features, the CLI, lifecycle hooks,
or dotfiles. On "no primitive expresses the boundary" (a draft overstatement):
`devcontainer.json` *can* express the **coarse** flags via `capDrop`/`securityOpt`/
`runArgs` (`--read-only`, `--cap-drop=ALL`, `--network=none`) — but it **cannot**
express the squid **per-domain egress allowlist** or the honest `EnvTier` tiers,
and safeslop would not trust a repo-supplied config to define its boundary
regardless. The boundary stays a safeslop-owned overlay.

## Method footer

- **Expansion:** 6 integration axes × 4 hard constraints (security posture, engine
  seam, substrate symmetry, reproducibility).
- **Research (ZDR/no-training routes):** Gemini 3.1 Pro (security/mechanics), GLM 5.2
  (adoption/ecosystem), DeepSeek V4 Pro (reproducibility/identity), + Exa grounding.
  Kimi (no key) and Sakana Fugu (weekly cap) unavailable this session.
- **FLO:** host proposal → 1 adversarial repo-read lane (read the actual source) + 2
  blind cross-family judges. Gen-1 scores: **source-fidelity 72 / Gemini 92 / GLM
  75**. The split was direction-strong / execution-flawed; this revision fixed the
  flagged execution: planned-vs-implemented tense, the `.safeslop/` lockfile-path bug
  (verified gitignored + walker-skipped), the over-specified schema, the
  substrate-neutral overstatement, the buildx claim, the metadata-label tension, the
  ingest policy, the lifecycle/dotfiles triage, and the hermetic-subset middle ground.
- **Load-bearing decision:** integration is conceptual — **borrow the lockfile**, at
  the **repo root**, keyed on `recipeID`. Do **not** re-litigate Features-as-tool-
  layers (rejected on hermetic-identity + egress + nerdctl grounds; one reconsideration
  trigger documented).
