# 0102 — Forge credential P2 implementation plan

SCOPE: Implement host-only, run-scoped forge credential leases: strict credential TTL horizons; GitHub App batch renewal and API-token staging; acknowledged Forgejo API-file staging; value-free lease status; and exact-title, confirmation-gated Forgejo deploy-key garbage collection.

OFF-LIMITS: No sandbox mint/renew/revoke authority, App private-key/account-link projection, RPC/socket credential broker, secret/ref/stage-path persistence or output, GitHub Enterprise, PAT/cloud renewal, Forgejo token provisioning/introspection/filter proxy, repository discovery, or GC outside exact requested repositories. Never revoke an active GitHub token during ordinary renewal; never delete without exact title, requested repository, and `--yes`.

WORKTREE: `.worktrees/0102-forge-credential-p2/`

Contract: `credentials.github.ttl` and `credentials.forgejo.ttl` default to `"1h"`; explicit `""` has no fixed horizon; another value must be a strictly positive Go duration. The horizon begins at staging and bars future safeslop staging/minting without retroactively invalidating an issued GitHub token. Initial staging removes abandoned state for the exact run identity before minting and fails closed on cleanup failure. The shared `runProfileCtx` lifecycle owns the manager for ordinary, coupled-session, and detached-session runs.

- [x] Validate P2 policy and authority/egress contracts
  FILE:     `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy.go`, `internal/engine/policy/policy_test.go`, `internal/engine/policy/egress.go`, `internal/engine/policy/egress_test.go`, `internal/engine/policy/evaluation.go`, `internal/engine/policy/evaluation_test.go`
  CHANGE:   Add strict duration parsing for both forge TTLs; permit `""` only as unbounded horizon; reject GitHub API unless App mode with nonempty unique grammar-checked `permission:read|write` entries; retain Forgejo acknowledgement validation; require HTTPS/default 443 for enabled Forgejo API; report GitHub API as repository/permission downscoped and Forgejo API as operator-provisioned scope unverified/may be account-wide. Add `api.github.com` only for enabled GitHub API and only the validated Forgejo API hostname for enabled Forgejo API.
  VERIFY:   `go test ./internal/engine/policy/ -run 'TTL|Api|API|Egress|Authority' -v`
  EXPECTED: Valid P2 profiles decode; invalid horizons/API declarations fail before staging; evaluation and egress are value-free and least-authority accurate.

- [x] Build a fake-clock lease state machine
  FILE:     `internal/engine/creds/lease.go`, `internal/engine/creds/lease_test.go`
  CHANGE:   Define a host-only lease manager with injectable clock, timer, jitter, mint callback, and value-free snapshot. Model healthy/renewing/degraded/expired states, a run-relative horizon, 2/3 observed-lifetime GitHub renewal, 10-minute minimum usable lifetime, 5s-to-5m exponential retry with 0–20% jitter, success reset, cancellation, and no timer/goroutine leaks. Do not serialize secret material or expose a listener.
  VERIFY:   `go test ./internal/engine/creds/ -run 'Lease|Renew' -v`
  EXPECTED: Fake-time tests prove renew/retry/horizon/current-expiry transitions and cleanup without sleeps or leaked timers.

- [ ] Renew GitHub App batches atomically and stage API credentials
  FILE:     `internal/engine/creds/github.go`, `internal/engine/creds/github_expiry.go`, `internal/engine/creds/github_test.go`, `internal/engine/creds/github_expiry_test.go`, `internal/engine/creds/githubapp/mint.go`, `internal/engine/creds/githubapp/mint_test.go`
  CHANGE:   Refactor App partition construction and minting so a complete replacement batch is minted before canonical files change. Use 0600 temp siblings plus rename for token files/manifests; retain prior tokens privately until natural expiry for teardown-only best-effort revocation; never revoke them on renewal. Stage GitHub API tokens separately from git partition files: one partition gets canonical `SAFESLOP_GITHUB_TOKEN_FILE`; multiple partitions get a canonical directory and value-free manifest; optional `GITHUB_TOKEN` is documented stale after renewal. Reject short native lifetimes and preserve token-free metadata.
  VERIFY:   `go test ./internal/engine/creds/ ./internal/engine/creds/githubapp/ -run 'Github|GitHub|Mint|Revoke|Renew|API' -v`
  EXPECTED: Tests prove full-batch atomicity, 0600 modes, replacement visibility, retained-token teardown revocation, partition isolation, API downscoping, and no token bytes in manifests/errors.

- [ ] Stage Forgejo API files and expose a hermetic HTTP seam
  FILE:     `internal/engine/creds/forgejo.go`, `internal/engine/creds/multirepo.go`, `internal/engine/creds/forgejo_test.go`, `internal/engine/creds/multirepo_test.go`
  CHANGE:   Replace direct Forgejo transport use with an injectable interface shared by staging, cleanup, and GC. When API is enabled and acknowledged, resolve only the matching host/owner link in host memory and stage a 0600 canonical token file exposed solely as `SAFESLOP_FORGEJO_TOKEN_FILE`; never export a token value. At the bounded horizon remove that file and attempt best-effort deploy-key cleanup; leave unbounded leases to normal teardown.
  VERIFY:   `go test ./internal/engine/creds/ -run 'Forgejo|forgejo' -v`
  EXPECTED: Local HTTP fakes prove exact authorization requests, file-only API delivery, horizon cleanup, no value/ref leakage, and unchanged per-repository deploy-key isolation.

- [ ] Integrate lease ownership into every run lifecycle and session status
  FILE:     `internal/cli/cli.go`, `internal/cli/supervise.go`, `internal/engine/session/session.go`, `internal/engine/session/session_test.go`, `internal/cli/cli_runprofile_test.go`, `internal/cli/cli_session_test.go`, `internal/cli/cli_detach_test.go`, `internal/cli/cli_stage_test.go`
  CHANGE:   Create and start one manager after safe initial staging in `runProfileCtx`, stopping it before credential teardown. Remove abandoned exact-run stage state first. Ensure coupled `run`, coupled `session run`, and detached supervisor use the same owner, while the supervisor exposes no credential service. Persist and render only additive, value-free `credential_lease` state (provider/aggregate state, reason/error class, timestamps, bounded horizon, GitHub minimum expiry/partition count); retain compatible `github_creds`. Mark a dead manager degraded until known expiry then expired.
  VERIFY:   `go test ./internal/cli/ ./internal/engine/session/ -run 'RunProfile|Stage|Session|Detach|Lease|Credential' -v`
  EXPECTED: Every launch lane starts/stops one host-only lease; stale-stage cleanup happens before minting; records/status contain neither values, refs, nor stage paths.

- [ ] Add narrow confirmation-gated Forgejo deploy-key GC
  FILE:     `internal/cli/creds_link.go`, `internal/cli/creds_gc.go`, `internal/cli/cli.go`, `internal/cli/cli_creds_test.go`, `internal/cli/creds_link_test.go`
  CHANGE:   Add `creds gc --host HOST --repo OWNER/REPO ... [--dry-run|--yes] [--output json]`. Require host and at least one repo; default dry-run; make `--yes` destructive and mutually exclusive with `--dry-run`. Resolve only matching Forgejo links in host memory, discover all requested repos before deleting, select only exact `safeslop-<owner>-<repo>` titles, re-fetch/recheck before each delete, treat 404 as absent, attempt all candidates, and return nonzero for remaining failure classes. Emit only host/repository/title/action/count/error class.
  VERIFY:   `go test ./internal/cli/ -run 'CredsGC|Creds.*GC|GC' -v`
  EXPECTED: httptest fakes prove no deletion without `--yes`, exact-match-only selection, discovery-before-delete, recheck behavior, 404 idempotence, and value-free output.

- [ ] Update operator documentation and execute final gates
  FILE:     `README.md`, `skills/agent-key-lifecycle/SKILL.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0102-forge-credential-p2.md`
  CHANGE:   Document P2 horizon semantics, host-only renewal custody, GitHub canonical API files and stale compatibility values, Forgejo’s unverified potentially account-wide API scope and horizon cleanup side effect, opt-in egress, and exact/confirmed GC. Mark every completed plan task only after its verification command succeeds.
  VERIFY:   `make check && make build`
  EXPECTED: Full formatting, vet, Go/Emacs tests, generated-asset checks, and binary build pass; documentation examples match command help.
