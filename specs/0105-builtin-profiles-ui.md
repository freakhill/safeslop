# 0105 — Builtin profiles in the Profiles UI

Status: complete

SCOPE: expose signed-binary builtin profiles (`claude`, `fish`, `pi`, `zsh`) in `profile list` and the Emacs Profiles surface, including from directories without `safeslop.cue`.

OFF-LIMITS: do not alter builtin profile bodies, precedence, hashes, policy trust, or launch behavior; do not make builtins editable/deletable/clonable through the UI; do not silently fall back when an existing project policy is invalid.

WORKTREE: `.worktrees/0105-builtin-profiles-ui/`

- [x] Add list-contract regressions
  FILE: `internal/cli/cli_profile_test.go`
  CHANGE: specify additive `builtins` rows, project-over-builtin precedence, no-config builtin listing, and invalid-config fail-closed behavior.
  VERIFY: `go test ./internal/cli/ -run 'TestProfileList' -v`
  EXPECTED: tests fail until `profile list` exposes signed builtins.

- [x] Implement additive builtin listing
  FILE: `internal/cli/cli.go`, `internal/cli/cli_profile_test.go`
  CHANGE: return full provenance-bearing builtin rows under `data.builtins`; without a config return empty project profiles plus builtins; preserve schema errors for an invalid existing config.
  VERIFY: `go test ./internal/cli/ -run 'TestProfileList' -v`
  EXPECTED: project rows and builtins are distinguishable; local names override matching builtin rows in clients.

- [x] Render source-aware builtin rows safely
  FILE: `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE: merge project rows with non-shadowed builtins, add a Source column, preserve project precedence, and reject edit/delete/clone on immutable builtins while allowing inspect/launch.
  VERIFY: `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT shows all defaults without CUE, labels sources, hides shadowed builtin duplicates, and blocks mutation actions on builtin rows.

- [x] Document and verify
  FILE: `README.md`, `emacs/README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0105-builtin-profiles-ui.md`
  CHANGE: explain that builtin rows are launchable, trusted binary defaults; project rows override them; builtin mutation requires creating a project profile rather than editing the embedded default.
  VERIFY: `git diff --check && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: docs match UI/CLI and all checks pass.
