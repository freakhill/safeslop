# 2026-07-12 — Safe host projection prior-art lessons (AYO)

Status: AYO compiled for `specs/0096` T1
Run inputs: `agent/tmp/ayo-runs/0096-safe-home-projection/inputs/brief.md`, `agent/tmp/ayo-runs/0096-safe-home-projection/inputs/source-packet.md`
Lanes: Gemini, DeepSeek, GLM succeeded; GPT unavailable due usage quota.

## Target

Mine mature-system scars for projecting a small allowlist of host home/config files into safeslop's contained agent sessions without exposing credentials, silently broadening file authority, or undermining container/network/trust guarantees.

## HIGH lessons for safeslop

### 1. Keep projection positive-only; broad-home-minus-deny is unsound

Consensus across lanes. Flatpak filesystem permissions and sandbox docs are the scar: denylisting `~/.*` or "all home except secrets" is false confidence because narrower allows can bypass broader denies and files created after startup cannot be reliably masked. Kubernetes projected volume `items[]` also favors named positive items over broad object exposure.

**Apply to:** `schema.cue`, `policy.go`, `risk.go`, `compose.yml.tmpl`.

**Implication:** `specs/0096`'s allowlist-only model is correct. Broad `$HOME` and known credential paths must be hard engine rejects, not profile-tunable warnings.

### 2. Treat shell/dotfile projection as code authority, not harmless config

DeepSeek and GLM both flagged this strongly. `.zshrc`, `.zprofile`, `.zshenv`, fish `config.fish`/`conf.d`, and Starship `[custom.*]` can execute commands at shell startup/prompt render. Dotfiles are also a common accidental secret location. VS Code Dev Containers/Codespaces/Gitpod dotfile bootstrap mechanisms are powerful but historically risky because they execute user-provided shell.

**Apply to:** `entrypoint.sh`, `risk.go`, docs, `sessionData`/profile show risk output.

**Implication:** safeslop must never `source`, `eval`, or execute projected host config in its entrypoint. If agent/shell later reads it, risk surfaces must say the agent can read/use projected config and that shell config may execute inside the container.

### 3. Prefer copy into ephemeral `/home/agent` over symlink back to a host mount

Gemini and GLM converged: direct symlinks from `/home/agent` to a live host bind mount re-create path-following risk and expose live host changes during a session. Copying into `/home/agent` tmpfs preserves safeslop's `specs/0064` ephemeral-home posture: the agent can mutate its local copy without mutating the host, and state disappears on exit.

**Apply to:** `entrypoint.sh`, `compose.yml.tmpl`, `compose.go`.

**Implication:** source mounts should be read-only staging inputs only. Runtime home should contain copied material, not symlinks. This intentionally trades host live-sync for safety and determinism.

### 4. Stage sources outside `$HOME` using opaque/non-host-mirroring container paths

Kubernetes ConfigMap/Secret projections and Flatpak document portal both avoid handing sandboxed code broad or meaningful host paths. DeepSeek specifically suggested opaque paths (`/safeslop/projected/0`, manifest driven) rather than `/safeslop/host-home/.zshrc`, because path shape itself can leak topology and invite traversal assumptions.

**Apply to:** `compose.go`, `compose.yml.tmpl`, `entrypoint.sh`.

**Implication:** prefer per-item staging paths like `/safeslop/projected/<id>` with a manifest mapping to destination under `/home/agent`, not a mirrored host-home tree.

### 5. Reject symlink components / path escapes before mounting

DeepSeek and GLM cited Kubernetes `subPath` CVEs (CVE-2017-1002101, CVE-2021-25741) and runc path re-canonicalization scars. The lesson is that validating a host path then later mounting by string is TOCTOU-prone if writable components can be swapped to symlinks. Full `openat(O_NOFOLLOW)` + fd-bind is the strongest pattern; at minimum MVP must reject symlink components and resolved paths outside the host user's home/config roots.

**Apply to:** host-side projection resolver in `compose.go`/policy helper tests.

**Implication:** do not follow arbitrary symlinks in allowlisted dirs. If exact fd-bind is too large for MVP, explicitly choose a conservative fail-closed subset: require source path and each parent component to be real path elements under `$HOME`/`$XDG_CONFIG_HOME`, reject symlinks, reject non-regular files unless a directory source has been specifically approved.

### 6. Exclude `.gitconfig` and `.config/git` from MVP

All lanes independently flagged git config as credential/transitive-execution authority: `[credential] helper`, `!` shell helpers, `[include]`/`includeIf`, `url.insteadOf`, signing keys, proxy settings, and host-specific rewrites. Existing safeslop already stages controlled git config through `/safeslop/runtime/.gitconfig` and `GIT_CONFIG_GLOBAL`; mixing host git config into that undermines the controlled credential path.

**Apply to:** `schema.cue`, `policy.go`, `compose.yml.tmpl`, docs.

**Implication:** keep `.gitconfig`/`.config/git` out of the MVP allowlist. Future git config projection must be synthesized from a curated key allowlist, not a raw file projection.

### 7. Model per-item status, target, mode, and collisions

Kubernetes projected volumes allow per-item paths/modes and reject duplicate targets. macOS security-scoped bookmarks and Kubernetes optional sources teach that absent sources should be legible and non-fatal when they are convenience config.

**Apply to:** profile schema/model, risk/status JSON, entrypoint tests.

**Implication:** projection items need explicit source kind, target, file/dir mode semantics, optional status, and duplicate-target rejection. Missing optional sources should be reported as skipped, not silently ignored; unsafe or malformed sources should fail closed.

### 8. Whole-directory projection must be rare and justified

GLM and Gemini pushed against `~/.config/fish/` and `~/.pi/agent/skills/` as broad directory entries: directories can contain auto-loaded files, symlinks, accidental secrets, or content that becomes cross-agent instructions. Kubernetes `items[]` and Flatpak's "limit as much as possible" point toward file-level projection when possible.

**Apply to:** 0096 builtins, T1 decision.

**Implication:** narrow shell projection if feasible: `~/.config/fish/config.fish` and maybe `~/.config/fish/conf.d/*.fish` only after path/symlink validation. `~/.pi/agent/skills/` needs an explicit decision: it is useful, but it is a host-authored instruction/code corpus shared across agents, so risk/provenance must surface it.

### 9. Rootless/backend UID behavior can make read-only bind sources unreadable

DeepSeek flagged Podman/rootless nerdctl user namespace scars. Host UID/GID and container UID 1000 may not align, especially on macOS or rootless backends. Copy-into-tmpfs helps only if the agent can read the staged source. Changing compose to run root just to copy would weaken current user posture.

**Apply to:** compose/backend support matrix, risk/status.

**Implication:** MVP should not promise all host config files are readable across every backend/mode. It should test actual readability or fail/skip legibly. If a backend cannot make read-only projection readable by uid 1000 without weakening posture, block projection on that backend until a safe userns/keep-id story exists.

### 10. Projection provenance is different from builtin profile integrity

GLM separated two trust models: embedded builtin profile bytes can be hashed and fail-closed if the binary's builtin registry changes; projected host config is live host filesystem state and should not pretend to be immutable or integrity-pinned across sessions.

**Apply to:** `cli.go`, `session.go`, JSON envelopes.

**Implication:** session/profile JSON should list projected source names/status as live host projection, not as policy-hash-equivalent content. Builtin profile hash proves which default profile contract launched; it does not prove what host config contents were read later.

## MEDIUM / deferred lessons

- Recursive read-only bind semantics vary by kernel/runtime (`bind-recursive=readonly` on modern Docker/Linux). Good to document/test later, but MVP avoids broad recursive home mounts and should mount exact sources only.
- Per-item POSIX modes are useful, but compose bind mount modes are runtime-limited; copying into tmpfs can set modes on copies where allowed.
- Future git config support should synthesize a safeslop-owned safe gitconfig subset; do not raw-project host git config.
- Future custom profile-authored projections need a larger UI/trust model. MVP builtins can start with engine-owned selections only.

## Host synthesis

The lanes reinforce the 0096 starting direction but tighten it materially:

1. Keep allowlist projection.
2. Make projection copy-based, not symlink-based.
3. Use opaque staging paths with a generated manifest.
4. Reject symlinks/path escapes and credential dirs as engine law.
5. Exclude raw git config.
6. Treat shell configs and pi skills as readable instruction/code authority in risk output.
7. Report optional skipped sources.
8. Be honest about backend readability limits.

These become the constraints the FLO worker must score and either freeze or revise.

## Method

- Read project state from `README.md`, `CONTRIBUTING.md`, specs `0064`, `0089`, `0091`, `0094`, `0096`, and code anchors in `policy.go`, `schema.cue`, `compose.go`, `compose.yml.tmpl`, and `cli.go`.
- Blind lanes dispatched with shared brief/source packet:
  - `ayo-research-gemini` — completed, 10 lessons.
  - `ayo-research-deepseek` — completed, 10 lessons.
  - `ayo-research-glm` — completed, 10 lessons.
  - `ayo-research-gpt` — unavailable: usage limit reached.
