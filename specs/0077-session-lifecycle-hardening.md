# 0077 — Session lifecycle hardening (0070 M4/M3)

**Status:** plan, executing  **Date:** 2026-07-06

SCOPE: Fix `specs/0070-security-review.md` M4 first, then M3. M4 closes the local staged-credential orphan left by SIGKILL/crash paths; M3 prevents detached `session stop` from signalling a reused PID/PGID.
OFF-LIMITS: No new runtime dependencies, no live network/credential APIs in tests, no trust-store or policy schema change, no broad session protocol redesign, no weakening of container/egress defaults.
WORKTREE: `.worktrees/0077-session-lifecycle-hardening/`

## Problem

M4: A session whose run wrapper/supervisor dies without defers can leave the deterministic host stage dir behind under the user cache dir. B2 relocated it out of the workspace, but `status`/`list` reconcile, `stop`, `rm`, and `prune` still do not consistently delete the orphaned local bearer files.

M3: Detached `session stop` targets `-sess.PID` as a process group. If the supervisor died and the PID was reused before stop, safeslop can signal an unrelated process group. `stop` must reconcile immediately and only signal a PID whose kernel identity still matches the session record.

## Contract

- `session status`/`list`, `session stop`, `session rm`, and `session prune` wipe a reconstructed session stage dir with `os.RemoveAll`, idempotently, without requiring live network or credentials.
- `session stop --revoke-credentials` still revokes before termination; stage-dir wiping is local cleanup and also runs when `--revoke-credentials` is absent.
- Newly running sessions record a non-secret kernel process identity token alongside PID when the platform exposes one. Existing records without a token fall back to the old signal-0 liveness behavior.
- Liveness reconcile treats a PID with a mismatched token as dead/stale. `session stop` performs this reconcile before signalling so a reused detached supervisor PID is marked stopped and cleaned, not killed.
- Existing valid session flows, detach/attach socket surfacing, and stale-socket sweep behavior stay intact.

## Tasks

- [x] T1 — M4: wipe orphaned stage dirs on stop/remove/prune/reconcile
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_session_test.go`, `internal/engine/session/session_test.go`
  CHANGE:   Add a local `sessionWipeStageDir` cleanup callback using `sessionStageDir` + `os.RemoveAll`; thread it through status/list reconcile, stop, rm, and prune. Keep credential revocation separate so reconcile does not call live forge APIs. Add regression tests for crashed-session reconcile and stop without `--revoke-credentials` removing a seeded stage dir.
  VERIFY:   `go test ./internal/cli ./internal/engine/session -run 'Test(Session(Status|List|Stop|Remove|Prune).*Stage|TestStoreStop|TestReconcile)' -count=1 -v`
  EXPECTED: command exits 0; orphaned stage dirs are removed on the local cleanup paths while existing stop/reconcile behavior remains green.

- [x] T2 — M3: bind PID/PGID stop decisions to a process identity token
  FILE:     `internal/engine/session/session.go`, `internal/engine/session/process_token_*.go`, `internal/engine/session/session_test.go`, `internal/cli/cli.go`, `internal/cli/cli_session_test.go`
  CHANGE:   Add a `process_token` field to session records; record it in `MarkRunning`/`MarkRunningDetached` from an OS process-start token (`/proc/<pid>/stat` starttime + boot id on Linux; `kern.proc.pid` `p_starttime` on Darwin; empty fallback elsewhere). Change liveness reconcile to accept the full session, verify token match when present, and update `session stop` to run reconcile+cleanup before calling `Store.Stop`. Add tests for token mismatch, legacy-empty fallback, and stop not signalling a stale/reused detached PID.
  VERIFY:   `go test ./internal/engine/session ./internal/cli -run 'Test(Process|Reconcile|SessionStop).*' -count=1 -v`
  EXPECTED: command exits 0; token mismatch reconciles to stopped and `session stop` does not call the killer for a stale detached record.

- [ ] T3 — Docs/spec sync
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `skills/agent-key-lifecycle/SKILL.md`, `specs/0070-security-review.md`, `specs/0077-session-lifecycle-hardening.md`
  CHANGE:   Document that stale-session reconcile/stop/rm/prune wipe host stage dirs and that detached stop verifies the recorded supervisor identity before group signalling; mark M4/M3 implemented in 0070 and update this checklist.
  VERIFY:   `rg -n 'M3|M4|stage dir|stage-dir|process identity|reconcile|PID|PGID|wipe' README.md skills/agent-sandbox-ops/SKILL.md skills/agent-key-lifecycle/SKILL.md specs/0070-security-review.md specs/0077-session-lifecycle-hardening.md`
  EXPECTED: output shows the new/revised lifecycle cleanup and PID/PGID guard documentation.

- [ ] T4 — Full verification
  FILE:     whole repo
  CHANGE:   Format and run required gates.
  VERIFY:   `gofmt -w internal/cli/cli.go internal/cli/cli_session_test.go internal/engine/session/session.go internal/engine/session/session_test.go internal/engine/session/process_token_*.go && make check && make build`
  EXPECTED: command exits 0; Go tests, ERT suite, denylist gates, and build pass.
