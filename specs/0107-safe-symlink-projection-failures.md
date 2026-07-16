# 0107 — Safe symlinked builtin projection and visible session failures

Status: complete

SCOPE: permit safely-contained symlink components in engine-owned builtin projection sources through fd-pinned private snapshots, and prominently show structured, value-free startup failures in Emacs.

OFF-LIMITS: no project-authored projection; no broad-home/credential/cache projection; no live host-source mount after resolution; no absolute symlink targets; no fallback to unsafe pathname resolution; no network/container-hardening change; no secret/raw target-path rendering.

WORKTREE: `.worktrees/0107-builtin-projection-symlinks/`

POST-COMPLETION REFINEMENTS:

- Spec 0108 supersedes only terminal membership for optional builtin globs: they select physical regular files and aggregate-omit matching links/directories/special files without following or opening them. Direct sources, required globs, recursive directory descendants, and every selected-file proof retain this spec's fail-closed contract.
- Spec 0110 supersedes only this spec's blanket absolute-target rejection. Engine-owned source-path links may use an exact-spelling absolute target that is a proper descendant of the same approved root; the raw suffix is converted to components and traversal restarts from the retained descriptor. Outside-root, alternate-spelling, ambiguous-component, excluded, internal-tree, race, mount, identity, snapshot, unsupported-platform, and value-free failure laws remain unchanged. Builtin CUE bytes and policy hashes do not change.

- [x] Surface existing startup failures immediately in Emacs
  FILE: `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`
  CHANGE: on terminal process exit, fetch its session record once; when its existing non-empty `last_error` is present, display the faced session detail and a minibuffer reason instead of leaving the terminal's fast exit unexplained. Preserve the PTY fallback path and never render stdout/stderr as a diagnostic.
  VERIFY: `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT proves a quick exit with stored `last_error` opens a durable details view; no-error and PTY fallback cases remain quiet.

- [x] Define structured value-free projection failures
  FILE: `internal/engine/session/session.go`, `internal/engine/container/projection.go`, `internal/engine/session/session_test.go`, `internal/engine/container/projection_test.go`
  CHANGE: add versioned `LastFailure` fields and code-owned summaries/actions from `specs/research/2026-07-16-symlinked-projection-flo.md`; classify projection failures, cap legacy `LastError` at 240 bytes, and prove JSON never contains raw target paths/seeded secrets.
  VERIFY: `go test ./internal/engine/session ./internal/engine/container -run 'Failure|Projection' -count=1 -v`
  EXPECTED: projection errors have stable value-free codes/templates and existing session JSON remains compatible.

- [x] Build descriptor-pinned private snapshots
  FILE: `internal/engine/container/projection.go`, `internal/engine/container/launch.go`, `internal/engine/container/compose.go`, `internal/engine/container/*_test.go`
  CHANGE: add OS-specific descriptor-walk adapters; accept relative in-root source symlinks only; validate exclusions/type/identity per descriptor; snapshot under owned `0700` stage storage; mount only snapshots; add injected post-open test barriers and cleanup paths.
  VERIFY: `go test ./internal/engine/container -run 'Projection|Snapshot|Symlink' -count=1 -v`
  EXPECTED: in-home `.config` symlink snapshots correctly; escapes/exclusions/loops/races fail closed; compose never mounts a live resolved source.

- [x] Persist run-preparation failures atomically
  FILE: `internal/cli/cli.go`, `internal/cli/cli_session_test.go`, `internal/cli/supervise_test.go`
  CHANGE: record `last_failure`/bounded compatibility `last_error` before terminal launch failure exits; expose it in list/status contracts without leaking raw resolver internals.
  VERIFY: `go test ./internal/cli -run 'Session.*Failure|Projection' -count=1 -v`
  EXPECTED: a failed builtin Fish projection is visibly stopped/failed with a structured reason in every session response.

- [x] Promote failure reasons in Emacs
  FILE: `emacs/safeslop-session.el`, `emacs/safeslop-portal.el`, `emacs/test/safeslop-test.el`, `emacs/README.md`
  CHANGE: terminal sentinel fetches session status on early process exit, renders a persistent value-free failure buffer/banner, refreshes portal data, deduplicates notification, and adds bounded visible failure reason to stopped/failed rows; detail renders structured summary/action first with legacy fallback.
  VERIFY: `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT proves a fast startup exit surfaces the reason without clicking a tooltip and values remain redacted.

- [x] Document, integrate, and verify
  FILE: `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, `specs/0107-safe-symlink-projection-failures.md`
  CHANGE: document accepted in-home relative symlink snapshots, rejected targets, fail-closed fallback, and failure UI; complete this checklist only after all gates.
  VERIFY: `git diff --check && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: all guards pass and docs match the final security/UI contract.
