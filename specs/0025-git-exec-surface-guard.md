# 0025 — git exec-surface guard (detect repo-write → host code-exec)

**Goal:** Close the first slice of the security review's S3 finding
(`specs/research/2026-06-19-design-security-review.md`): the agent runs against a
writable repo, so a prompt-injected or malicious agent can plant a `.git/hooks`
script — or a `.git/config` `hooksPath` / `fsmonitor` / filter / `!alias` — that
the **host** executes on its next `git` command in that repo (the classic
devcontainer escape). Reachable in every tier (the repo is writable in all of them).

**Decision (detect-first, prevent-later).** A full *prevention* (e.g. making
`.git` read-only, or running the agent in a worktree whose gitdir lives outside
the writable mount) breaks the agent's own legitimate git use and has real
tradeoffs — it is a harder follow-on. This slice ships the clean, non-breaking,
**all-tiers** mitigation: a host-side fingerprint of git's executable surface
taken before launch and compared on exit, with a prominent warning if it changed.
It surfaces tampering right when the session ends — before the user's next `git`
command — without constraining the agent.

**Architecture:**
- `internal/engine/gitguard` — `Snapshot(repoRoot) State` fingerprints the
  exec-surface: sha256 of `.git/config` plus per-file sha256 of every
  **executable, non-`.sample`** hook under `.git/hooks` (git only runs executable
  non-sample hooks). `State.Diff(after)` lists exec-surface changes (config
  changed, or a hook added/modified); removals aren't an exec risk and aren't
  reported. A missing/non-dir `.git` yields an empty, stable snapshot (no error),
  so callers snapshot unconditionally.
- `internal/cli` `runProfile` — `gitBefore := gitguard.Snapshot(ws)` before the
  launch switch; `defer warnGitExecSurface(ws, gitBefore)` prints a stderr warning
  naming each changed path on exit. Best-effort: snapshot errors are ignored, the
  agent is never blocked.

**Scope:** the CLI `run` path (the shipped power-user surface, mirroring how
specs/0022's trust gate landed CLI-first). The cockpit `OpenSession` path gets
the same before/after hooks around its session lifecycle as a fast-follow.

**Tech stack:** Go stdlib only (`crypto/sha256`, `os`, `path/filepath`). TDD on
the pure `Snapshot`/`Diff` plus a stderr-capture test of the run-path wiring.

**Tests:**
- `internal/engine/gitguard/gitguard_test.go` — planted hook + config change are
  flagged; `.sample` and non-executable files are ignored; non-git dir is stable.
- `internal/cli/gitguard_warn_test.go` — the warning names a planted hook; silent
  when nothing changed.

## Deferred (follow-on slices)
- **Prevention**, not just detection: e.g. agent works in a git worktree whose
  real gitdir is outside the writable mount; or a sandbox-tier `(deny file-write*
  .git/hooks)` plus neutralizing `core.hooksPath`. Each has tradeoffs vs the
  agent's git workflow — score with a FLO before committing.
- Wire the same before/after guard into the `OpenSession` cockpit path.
- Broaden beyond `.git`: other host-auto-executed repo files (`.envrc` for
  direnv, editor task configs) are the same class; warn on those too.
