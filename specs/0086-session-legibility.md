# 0086 — Live session legibility (0071 first ergonomic slice)

Status: planned
Date: 2026-07-06
Follows: `specs/0071-ergonomics-review.md` recommendation #3.

SCOPE: make running/attach buffers and the portal identify the session's profile/project/tier/net and value-free credential scope at a glance.
OFF-LIMITS: no policy-schema changes; no repo-set templates/generators; no credential authoring UI; no filter/search/grouping; no secret values or secret refs in session records, portal rows, headers, logs, or tests.
WORKTREE: `.worktrees/0086-session-legibility/`

## Problem

`specs/0071` found that safeslop's cockpit is responsive and honest about tier/net, but a live agent buffer is still named by opaque session id and neither the buffer nor the portal row shows which repo/credential scope is staged. Operators with several live sessions must open details one by one to answer "which buffer, which project, which credentials?".

## Contract

- `session create/list/status` include a non-secret, value-free `credential_scopes` array for profile-backed sessions.
- Each credential scope row names only kind + non-secret target + access/scope (for example `github acme/web app rw`, `pnpm npm.pkg.github.com @org`, `aws dev us-east-1 arn:...`). It never includes token values, secret refs (`op://`, `env:`), staged file paths, or account private-key refs.
- Ad-hoc sessions and profiles without credentials omit `credential_scopes` or return an empty array.
- The portal gets a compact `Creds` column with a full value-free tooltip/help text.
- Coupled run and detached attach buffers use a self-describing name and header line, for example `*safeslop:be-dev payments [container/deny]*` plus `creds: github acme/web app rw, pnpm npm.pkg.github.com @org`.
- Portal "open live buffer" must still find buffers after their names become descriptive; it must key on a buffer-local session id, not the displayed buffer name.
- Existing JSON fields and old session records remain compatible.

## Tasks

- [x] T1 — Add value-free credential scopes to session records and JSON envelopes
  FILE:     `internal/engine/session/session.go`, `internal/cli/cli.go`, `internal/cli/cli_session_test.go`, `internal/jsoncontract/testdata/ok-session-create.golden.json`, `internal/jsoncontract/testdata/ok-session-detached.golden.json`
  CHANGE:   Add `CredentialScope {kind,name,scope}` and `Session.CredentialScopes []CredentialScope` (`json:"credential_scopes,omitempty"`). Compute it in `createSessionFromProfile` from the trusted loaded `policy.Profile` before saving. Include it from `sessionData`. Use the same access semantics as staging: declared repo entries use `RepoCred.Write`; origin-inferred GitHub/Forgejo rows use provider-level `Write`; PAT/App/deploy-key/TTL text is scope only. Include pnpm/aws/gcp/kube targets, but never refs or values. Leave ad-hoc sessions empty.
  VERIFY:   `go test ./internal/cli ./internal/engine/session -run 'Test(SessionCreate.*CredentialScope|SessionData.*CredentialScope|SessionCreateGolden|DetachedGolden)' -count=1 -v`
  EXPECTED: command exits 0; create/list/status JSON carries value-free credential scopes for profile sessions, golden envelopes remain valid, and tests assert no `op://`, `env:`, token, or private-key ref text appears.

- [x] T2 — Show credential scope in the portal row
  FILE:     `emacs/safeslop-portal.el`, `emacs/test/safeslop-test.el`
  CHANGE:   Add pure helpers to format `credential_scopes` into a compact cell and full help text. Add a `Creds` column (truncated, value-free) before `Workspace`; render `—` when empty. Update portal row/header tests for the new column and tooltip.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-portal-.*credential")'`
  EXPECTED: command exits 0; portal rows display compact credential scope without showing refs/values and empty-scope sessions display `—`.

- [x] T3 — Make live terminal buffers self-describing and findable by session id
  FILE:     `emacs/safeslop-session.el`, `emacs/safeslop-portal.el`, `emacs/test/safeslop-test.el`, `emacs/test/safeslop-contract-test.el`
  CHANGE:   Add buffer-local `safeslop-session-id`. Add pure builders for a safe buffer label (`profile-or-name`, workspace basename, `[env/net]`, fallback old id) and a header-line summary including credential scope. Have `safeslop-session--launch-term` fetch current session data best-effort before naming/headering the buffer; set the buffer-local id after terminal creation. Replace portal's live-buffer lookup from `(get-buffer "*safeslop-<id>*")` to a helper that scans buffers for `safeslop-session-id`, falling back to the old name for legacy buffers, and update exact-argv contract tests for the added status prefetch.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-session-.*\\(buffer\\|header\\|live\\)")'`
  EXPECTED: command exits 0; generated labels match the 0071 example shape, header text includes only value-free scope, and portal live-open finds renamed buffers by session id.

- [x] T4 — Docs and skill sync
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0071-ergonomics-review.md`, `specs/0086-session-legibility.md`
  CHANGE:   Document that session list/status and the Emacs portal surface value-free credential scope, and that live buffers are named/annotated with profile/project/tier/net. Mark 0071 recommendation #3 as planned/covered by this spec; do not mark the broader 100-repo template/filter recommendations done.
  VERIFY:   `rg -n 'credential scope|credential_scopes|live buffer|profile/project|0086|recommendation #3' README.md skills/agent-sandbox-ops/SKILL.md specs/0071-ergonomics-review.md specs/0086-session-legibility.md`
  EXPECTED: output shows the new value-free legibility docs and the 0071 cross-reference.

- [x] T5 — Full verification
  FILE:     whole repo
  CHANGE:   Format Go touched by T1 and run the repo gates.
  VERIFY:   `gofmt -w internal/engine/session/session.go internal/cli/cli.go internal/cli/cli_session_test.go && make check && make build`
  EXPECTED: command exits 0; Go tests, ERT suite, shell denylist gates, and build pass.

## Execution notes

Use TDD for T1–T3: write the JSON/ERT expectations first, watch them fail, then implement. The implementation is additive and backward-compatible; old session JSON without `credential_scopes` must still render as `—` in Emacs.
