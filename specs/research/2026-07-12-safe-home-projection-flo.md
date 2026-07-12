# 2026-07-12 — Safe host projection mount capability decision (FLO)

Status: decision landed for `specs/0096` T1
Score: **90.0 / 100** (no deterministic LAW cap triggered)
Inputs: `specs/0096-contained-hybrid-default-profiles.md`, `specs/research/2026-07-12-safe-home-projection-ayo.md`, `agent/tmp/flo-runs/0096-safe-home-projection/inputs/{goal,rubric,packet}.md`

## Verdict

**Adopt allowlist-style safe host projection for contained-hybrid builtin profiles**, with these binding tightenings over the 0096 candidate:

1. Projection is **copy-based, not symlink-based**: read-only host sources are staged under opaque paths and copied into `/home/agent` tmpfs. No `/home/agent` entry points back to a live host bind mount.
2. Use **opaque staging paths + manifest**: mount each approved source at `/safeslop/projected/<id>:ro` and write `/safeslop/runtime/projection.json`; do not mirror host topology as `/safeslop/host-home/...`.
3. Projection is **engine-owned builtin-only in MVP**. Add/reserve schema/model fields, but reject project-authored projection until a later UI/trust model exists.
4. Keep `.gitconfig` and `.config/git` **excluded**. Future support must synthesize a safeslop-owned safe gitconfig subset from approved keys; never raw-project host git config in MVP.
5. Fish config projection is narrowed from all of `~/.config/fish/` to explicit optional globs/files: `config.fish`, `conf.d/*.fish`, `functions/*.fish`, and `completions/*.fish`; keep `fish_variables` excluded.
6. `~/.pi/agent/skills/` may be projected into both `pi` and `claude` builtins, but it is explicitly treated as a shared host-authored instruction/code corpus and must be surfaced as such in risk/provenance output.
7. Resolver law: reject symlink components, resolved paths outside `$HOME`/`$XDG_CONFIG_HOME`, known credential/excluded roots, duplicate targets, and non-regular files unless an item is explicitly typed as `dir`/`glob`.
8. Directories/globs expand into **per-file projection entries** before launch. Every expanded file gets its own manifest/session status row; policy-item summaries alone are insufficient.
9. Optional absent/unreadable files skip with legible status; required absent/unreadable files fail closed. Resolver-law violations fail closed regardless of optional flag.
10. Backend readability cannot weaken the runtime posture. Preserve `user:"1000:1000"`, `read_only:true`, `cap_drop:[ALL]`, `no-new-privileges`, `/home/agent` tmpfs, and `network:"deny"`; unreadable sources skip/fail legibly instead.

No broad `$HOME`, no credential directories, no host-home writes, no network weakening, and no trust weakening are permitted.

## MVP contract

### User-facing model

Contained-hybrid builtin profiles (`pi`, `claude`, `fish`, `zsh`) carry an engine-owned read-only projection of a small positive allowlist of host config files. The host sources are bound read-only at opaque staging paths, then copied into ephemeral `/home/agent` tmpfs during entrypoint setup. Workspace remains the only read-write host mount.

Operators see projection authority before run and in session/status JSON: source, target, kind, optional/required, label, per-expanded-file status, backend if relevant, and whether the source is live host filesystem state.

### Schema/model

Add reserved fields similar to:

```cue
#ProjectionItem: {
    source: string
    target?: string
    kind: "file" | "dir" | "glob" | *"file"
    optional?: bool | *true
    label?: string
}

#Projection: {
    enabled?: bool | *false
    items?: [...#ProjectionItem]
}
```

`#Profile` gains `projection?: #Projection`, but MVP rules reject user-authored `projection` in `safeslop.cue` with a spec-cited error. Embedded builtins may populate it.

### Builtin projection items

| builtin | projected items |
|---|---|
| `pi` | `~/.pi/agent/AGENTS.md` (file, required); `~/.pi/agent/skills/` (dir, optional, shared instruction/code corpus) |
| `claude` | `~/.pi/agent/AGENTS.md` (file, required); `~/.pi/agent/skills/` (dir, optional, shared instruction/code corpus) |
| `fish` | `~/.config/fish/config.fish` (file, optional); `~/.config/fish/conf.d/*.fish` (glob, optional); `~/.config/fish/functions/*.fish` (glob, optional); `~/.config/fish/completions/*.fish` (glob, optional) |
| `zsh` | `~/.zshrc`, `~/.zprofile`, `~/.zshenv` (files, optional); `~/.config/starship.toml` (file, optional) |

Fish `fish_variables` remains excluded because it is mutable shell state and may carry surprising environment/state values. Zsh `.zlogout` is excluded in MVP because it affects session teardown but does not improve startup ergonomics enough to widen the code surface.

### Excluded sources

Hard rejects, not warnings:

- broad `$HOME` or any whole-home mount;
- `~/.ssh/`, `~/.aws/`, `~/.kube/`, `~/.docker/`, `~/.gnupg/`, `~/.config/gcloud/`, `~/.config/safeslop/`;
- `~/.npmrc`, `~/.pypirc`, `~/.cargo/credentials*`;
- browser/cookie/keychain state;
- safeslop account-link/stage/cache dirs, including `~/Library/Caches/safeslop/` and `~/.cache/safeslop/`;
- `.gitconfig` and `.config/git` raw projection;
- symlink components or paths resolving outside `$HOME`/`$XDG_CONFIG_HOME`;
- duplicate targets;
- non-regular files unless the item is explicitly typed as `dir`/`glob` and every contained file passes validation.

### Runtime behavior

- Render each validated source as a read-only bind mount under `/safeslop/projected/<id>`.
- Generate `/safeslop/runtime/projection.json` with expanded per-file entries.
- Entrypoint reads the manifest and copies projected files into `/home/agent` tmpfs. It must never `source`, `.`, `eval`, or execute projected content.
- Entrypoint writes `/home/agent/.safeslop/projection-status` so the agent can inspect what was projected.
- Optional absent source: `skipped-absent`, launch proceeds.
- Required absent source: fail closed.
- Optional unreadable source: `skipped-unreadable`, launch proceeds.
- Required unreadable source: fail closed.
- Resolver-law violation: fail closed, even for optional sources.

### Risk / provenance

Risk and JSON surfaces must show:

- `profile_source` and `policy_hash` for builtin/project profile provenance;
- `projection` entries with per-expanded-file status;
- label that projection is live host filesystem state, not content-pinned by the builtin profile hash;
- file reach as `workspace (rw) + read-only projected host config copied into ephemeral home`;
- warning that shell config and pi skills are readable instruction/code authority and may execute/use tools inside the container if the agent/shell invokes them.

## Rejected alternatives

- Broad `$HOME` minus unsafe paths.
- Raw `.gitconfig` / `.config/git` projection.
- Symlinking projected files into `/home/agent`.
- Mirroring host topology inside the container.
- Whole `~/.config/fish/` projection.
- Project-authored projection in MVP.
- Read-write host config mounts.
- Weakening container user/read-only/capability posture to fix backend readability.
- Adding `network:"progressive"`, `network:"ask"`, or agent-triggered prompts; progressive network remains the separate 0089 session-grants track.

## Implementation anchors

- `internal/engine/policy/schema/schema.cue`: add projection schema; mark project-authored projection rejected in MVP.
- `internal/engine/policy/policy.go`: decode/validation, builtin-only enforcement, duplicate-target/excluded-root/symlink/path-escape rejection.
- `internal/engine/policy/risk.go`: risk axes and lines for projected host config.
- `internal/engine/container/compose.go`: host resolver, glob expansion, opaque mount IDs, projection manifest, backend readability status.
- `internal/engine/container/assets/compose.yml.tmpl`: read-only projection volumes, preserving existing container hardening.
- `internal/engine/container/assets/entrypoint.sh`: copy-only projection into tmpfs home; status file; never execute projected content.
- `internal/cli/cli.go`, `internal/engine/session/session.go`: projection JSON/provenance on profile/session surfaces.

## Scoring

Locked rubric weights: C1 35%, C2 20%, C3 20%, C4 15%, C5 10%.

Evaluator: `flo-evaluator-deepseek` on `agent/tmp/flo-runs/0096-safe-home-projection/candidates/baseline-decision.md`.

| Criterion | Score | Weight | Contribution |
|---|---:|---:|---:|
| C1 — Credential/file safety and frozen-law compliance | 9.5 / 10 | 35 | 33.25 |
| C2 — Usability for contained-hybrid defaults | 8.0 / 10 | 20 | 16.00 |
| C3 — Architectural fit with current safeslop surfaces | 9.0 / 10 | 20 | 18.00 |
| C4 — Legibility, auditability, and operator control | 8.5 / 10 | 15 | 12.75 |
| C5 — Phaseability and testability | 10.0 / 10 | 10 | 10.00 |
| **Total** |  | **100** | **90.00** |

Deterministic LAW override: **none triggered**.

Host applied two non-fatal clarifications from evaluator weaknesses without re-running FLO: include optional fish `functions/*.fish` and `completions/*.fish` globs while keeping `fish_variables` excluded; require per-expanded-file projection rows for dir/glob items before launch. These tighten usability/legibility without weakening safety laws.

## Method

- Expansion packet written to `agent/tmp/flo-runs/0096-safe-home-projection/inputs/packet.md`.
- AYO lanes: Gemini, DeepSeek, GLM succeeded; GPT unavailable due usage quota. Compiled note: `specs/research/2026-07-12-safe-home-projection-ayo.md`.
- Configured `flo-worker` was unavailable due provider weekly quota. A separate configured fallback worker (`lean-frontier-glm-max`) was used under the FLO worker contract; it had read-only tools and returned the candidate inline, which the host persisted to `agent/tmp/flo-runs/0096-safe-home-projection/candidates/baseline-decision.md`.
- Evaluator: `flo-evaluator-deepseek`, original criterion order. Report: `agent/tmp/flo-runs/0096-safe-home-projection/evaluations/baseline-decision-deepseek.md`.
- Host computed weighted total and applied only the two clarifications above.
