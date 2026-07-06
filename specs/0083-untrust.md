# 0083 — Add `safeslop untrust` (0070 L3)

**Status:** implemented

SCOPE: Close `specs/0070` L3 by adding a Go CLI caller for the existing `trust.Store.Revoke`: `safeslop untrust [safeslop.cue]` removes the host-side approval for the same canonical policy path used by `safeslop trust`/launch gates.

OFF-LIMITS: Do not change trust hash semantics, launch gating, session policy-hash checks, host consent, or container behavior. Do not revive the removed cockpit/control-plane UI/RPC from `specs/0033`. Do not persist anything except the updated trust store.

WORKTREE: `.worktrees/0083-untrust/`

Design: mirror `safeslop trust` in the root command map and human/JSON output, but make revoke idempotent because removing privilege should not be blocked by whether the entry currently exists. `untrust` resolves `findConfig(arg0(args))`, canonicalizes exactly like the launch trust key, calls `Store.Revoke`, and prints/returns the canonical path removed. A subsequent `run` or profile-backed `session create/run` observes `Untrusted` and fails closed until `safeslop trust` is run again.

- [x] Add red CLI tests for `untrust`
  FILE:     `internal/cli/cli_trust_test.go`, `internal/cli/cli_help_iw3_test.go`
  CHANGE:   Add tests proving `safeslop untrust [path]` removes a prior approval, is idempotent when no entry exists, supports JSON output, and is registered in root help/command map.
  VERIFY:   `go test ./internal/cli/ -run 'Untrust|Help|CommandsRegistered' -v`
  EXPECTED: Fails because the `untrust` command/helper does not exist yet.

- [x] Implement `safeslop untrust`
  FILE:     `internal/cli/cli.go`
  CHANGE:   Add `cmdUntrust`, register it in `newRoot`, and add a small `revokePolicyTrust(policyPath)` helper that uses `canonicalPolicyPath`, `loadTrustStore`, and `Store.Revoke`.
  VERIFY:   `go test ./internal/cli/ -run 'Untrust|Help|CommandsRegistered' -v`
  EXPECTED: Targeted CLI tests pass.

- [x] Update docs/spec status
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0070-security-review.md`, `specs/0083-untrust.md`
  CHANGE:   Document `safeslop untrust [safeslop.cue]`, explain revoked trust blocks future launches until re-trusted, mark L3 implemented, and mark this spec implemented.
  VERIFY:   `rg -n 'untrust|L3|0083' README.md skills/agent-sandbox-ops/SKILL.md specs/0070-security-review.md specs/0083-untrust.md`
  EXPECTED: Docs and specs mention the new command and L3 implementation.

- [x] Run final verification
  FILE:     repository root
  CHANGE:   No code changes; run the required gates from the worktree.
  VERIFY:   `make check && make build`
  EXPECTED: Both commands exit 0.
