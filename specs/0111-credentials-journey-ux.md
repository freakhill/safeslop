# 0111 — Trace-driven Credentials journey repair

Status: complete

SCOPE: make first-run and existing-operator credential workflows completable and truthful in raw Emacs and Doom/Evil by replaying actual key dispatch with fake/value-free CLI envelopes, fixing profile discovery, mixed repo access display/edit defaults, profile-scope clearing, confirmations, context continuity, and recovery.

OFF-LIMITS: no secret values, live forge/1Password/network calls, account mutation in tests, live repo discovery, sandbox mint/revoke UI, new credential provider, public JSON/schema change, account-link projection, automatic policy trust, or weakening of one-forge/network/session-lifecycle boundaries.

WORKTREE: `.worktrees/0111-credentials-journey-ux/`

- [x] Capture and review baseline user journeys
  FILE:     `specs/research/2026-07-16-credentials-journey-baseline.md`, `specs/0111-credentials-journey-ux.md`
  CHANGE:   Run raw and Evil key resolution/interactive dispatch with fake inputs for first-run link/scope, existing scope/removal, and recovery journeys; collect traces; obtain isolated raw/Evil/operator reviews; severity-rank defects and choose the minimal vertical repair.
  VERIFY:   `git diff --check && rg -n 'empty-profile-candidates|evil-link-account|Blocker|Chosen repair|0/5' specs/research/2026-07-16-credentials-journey-baseline.md`
  EXPECTED: The persistent report contains reproducible key traces, task scores, functional/ergonomic defects, safety limits, chosen repair, and rejected alternatives.

- [x] Add RED journey and mixed-access tests
  FILE:     `internal/engine/creds/inspect_test.go`, `emacs/test/safeslop-credentials-test.el`, `emacs/test/safeslop-ui-probe.el`
  CHANGE:   Add hermetic tests for mixed per-repo read/write inspection; actual displayed-key dispatch in raw/Evil slots; empty-profile first-run using fake `profile list` and `creds show`; truthful empty/refresh legends; account confirmation/context; existing-scope prefill/replacement warning/draft retention; and confirmed profile forge clear. Tests must use refs only and never call a live CLI/provider.
  VERIFY:   `go test ./internal/engine/creds -run 'Inspect.*MixedRepoAccess' -count=1 -v; emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-credentials-journey")'`
  EXPECTED: New tests fail on incorrect mixed access, missing universal keys/profile source/clear path, false guidance, lost defaults/draft, and success-buffer displacement—not on test plumbing.

- [x] Correct mixed repository access inspection
  FILE:     `internal/engine/creds/inspect.go`, `internal/engine/creds/inspect_test.go`
  CHANGE:   Render each explicit GitHub/Forgejo row from its own `RepoCred.Write`; retain the profile-level write flag only for origin inference. Keep sorting and value-free fields unchanged.
  VERIFY:   `go test ./internal/engine/creds -run 'Inspect.*(Github|Forgejo|MixedRepoAccess)' -count=1 -v`
  EXPECTED: Mixed read/write repos report distinct truthful scopes; origin and existing readiness behavior remain unchanged.

- [x] Implement truthful cross-mode actions and first-run guidance
  FILE:     `emacs/safeslop-credentials.el`, `emacs/safeslop-doom.el`, `emacs/test/safeslop-credentials-test.el`, `emacs/test/safeslop-ui-probe.el`
  CHANGE:   Add universal visible `A/U/R/X` actions with raw lowercase compatibility; bind them in Evil normal state; render active `g`/`gr`; make empty/account guidance describe link→scope and project-profile prerequisites; confirm value-free account identity before link; on success stay in Credentials with concise next-step feedback, reserving result buffers for failures.
  VERIFY:   `make test-emacs-ui-matrix && emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-credentials-journey-.*\\(key\\|guidance\\|link\\)")'`
  EXPECTED: Every displayed key dispatches its task in raw/Evil/Doom-Evil; first-run instructions are executable; cancel writes nothing; success preserves surface context; failures remain inspectable and value-free.

- [x] Implement profile-backed scope edit, clear, and retry flows
  FILE:     `emacs/safeslop-credentials.el`, `emacs/test/safeslop-credentials-test.el`
  CHANGE:   Use async existing `profile list` to select every project profile independently of credential rows; fetch existing `creds show` rows; prefill current provider/origin/read/write state; confirm a value-free before/after replacement summary including other-forge clearing; retain failed draft defaults; add `X` calling confirmed `profile credentials clear`; refresh Credentials/Profiles in place on success and state that policy trust must be reviewed.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-credentials-journey-.*\\(first-run\\|existing\\|clear\\|retry\\)")'`
  EXPECTED: Empty-profile setup completes; unchanged repos survive focused edits; removal is distinct from unlink; failure retry preserves value-free inputs; no mutation occurs on cancel or failed fetch.

- [x] Synchronize docs and replay the journeys
  FILE:     `README.md`, `emacs/README.md`, `skills/agent-key-lifecycle/SKILL.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0090-credential-connection-repo-picker.md`, `specs/research/2026-07-16-credentials-journey-baseline.md`, `specs/0111-credentials-journey-ux.md`
  CHANGE:   Document universal keys, first-run sequence, project-vs-account removal, prefilled replacement semantics, retry, policy re-trust, and no live discovery; append post-fix key traces and completion scores; mark 0090's original empty-state/key wording superseded only by 0111.
  VERIFY:   `git diff --check && rg -n 'A.*link|R.*repo|X.*clear|re-trust|post-fix|5/5|0111' README.md emacs/README.md skills/agent-key-lifecycle/SKILL.md skills/agent-sandbox-ops/SKILL.md specs/0090-credential-connection-repo-picker.md specs/research/2026-07-16-credentials-journey-baseline.md`
  EXPECTED: Docs match tested keys and distinguish account links, profile scopes, values/refs, and session-owned credentials.

- [x] Run full gates, install, and clean up
  FILE:     whole repo, `specs/0111-credentials-journey-ux.md`
  CHANGE:   Replay focused journeys, run UI matrix/check/build, install final binary/Emacs package, verify installed files, mark this spec complete, then merge/push both remotes and remove the worktree/branch.
  VERIFY:   `git diff --check && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: Journey tests and all Go/Emacs/denylist/build gates pass; installed version and both main remotes match; no live account/session state or worktree remains.
