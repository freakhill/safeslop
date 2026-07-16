# 0109 — Demand-loaded builtin Fish projection

Status: in progress

SCOPE: make fresh builtin Fish containers start with normal container-owned Fish configuration by projecting only demand-loaded physical regular functions/completions, never eager host `config.fish` or `conf.d` scripts.

OFF-LIMITS: no `--no-config`/init bridge; no shell parsing/rewriting/conditional sourcing; no command shim/package/egress expansion; no host path or writable mount; no project/ad-hoc/host Fish behavior change; no relaxation of specs 0107/0108 snapshots.

WORKTREE: `.worktrees/0109-fish-demand-loaded-projection/`

- [x] Land the approved Fish startup decision
  FILE:     `specs/research/2026-07-16-fish-container-startup-ayo.md`, `specs/research/2026-07-16-fish-container-startup-flo.md`, `specs/0109-fish-demand-loaded-projection.md`
  CHANGE:   Record the live root cause, prior-art lessons, exact two-glob verdict, exact-byte hash transition, rejected alternatives, laws, and executable tasks.
  VERIFY:   `git diff --check && rg -n '94.25 / 100|functions-completions-only|does \*\*not\*\* project' specs/research/2026-07-16-fish-container-startup-*.md`
  EXPECTED: Notes pin a least-authority deterministic startup contract and migration.

- [x] Reproduce eager builtin projection as RED
  FILE:     `internal/engine/policy/builtins_test.go`, `internal/engine/container/projection_test.go`
  CHANGE:   Add policy deep-equality expectations for exactly two ordered optional Fish globs and a fake-home snapshot test with eager sentinels plus regular function/completion fixtures; assert eager sources/bytes/manifest rows are absent and only demand-loaded assets publish.
  VERIFY:   `go test ./internal/engine/policy ./internal/engine/container -run 'BuiltinFish|FishBuiltin' -count=1 -v`
  EXPECTED: Tests fail because current builtin still includes and snapshots `config.fish`/`conf.d`.

- [ ] Implement Fish projection v2 and hash migration
  FILE:     `internal/engine/policy/builtins.go`, `internal/engine/policy/builtins/fish.cue`, `internal/engine/policy/builtins_test.go`, `internal/cli/cli_agentargv_test.go`, `internal/cli/cli_session_profile_test.go`
  CHANGE:   Remove eager items; add the exact CUE contract marker; pin new hash `sha256:92da9d4ef90abd8f84031d9578650c319f22e3a7a7776ae34d33ed1e26e9a85e` and old-hash rejection; prove host/container argv stays exactly `fish` and fresh reconstruction gets the two-item projection.
  VERIFY:   `go test ./internal/engine/policy ./internal/engine/container ./internal/cli -run 'BuiltinFish|FishBuiltin|AgentArgvAcceptsFish|SessionProfile.*Builtin' -count=1 -v`
  EXPECTED: Two-glob projection, exact hash migration, normal argv, snapshot minimization, and fail-closed old records pass.

- [ ] Synchronize operator documentation
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, `specs/0108-safe-optional-projection-globs.md`, `specs/0109-fish-demand-loaded-projection.md`
  CHANGE:   Replace broad Fish-config wording/four-item allowlist with exact demand-loaded functions/completions; explain eager host startup exclusion and fresh-session migration while preserving snapshot laws.
  VERIFY:   `git diff --check && rg -n 'demand-loaded|config\.fish|conf\.d|fresh Fish session' README.md skills/agent-sandbox-ops/SKILL.md specs/0096-contained-hybrid-default-profiles.md specs/0108-safe-optional-projection-globs.md`
  EXPECTED: Docs never imply host startup scripts execute in builtin containers.

- [ ] Run isolated real Fish smoke and full gates
  FILE:     whole repo, `specs/0109-fish-demand-loaded-projection.md`
  CHANGE:   With a temporary HOME, seed eager output/error sentinels, a demand-loaded function/completion, and non-regular candidates; start a fresh builtin Fish Docker session, prove no eager log output, function/completion lookup works, only private snapshots mount, source files stay unchanged, then stop/remove and complete this checklist only after all gates.
  VERIFY:   `git diff --check && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: Isolated live Fish startup is clean/useful, cleanup leaves no session/container/stage, and every UI/Go/Emacs/denylist/build gate passes.
