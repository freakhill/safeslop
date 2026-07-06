# 0082 — Wire host-launch consent gate (0070 L2)

**Status:** implemented

SCOPE: Close `specs/0070` L2 by making `policy.HostConsentStatements`, `HostHeadlineBody`, and `HostScopeLine` live in the Go CLI launch path for host-tier launches.

OFF-LIMITS: Do not reintroduce the removed cockpit/control-plane server. Do not persist consent. Do not add external runtime dependencies. Do not weaken policy-byte trust, session trust re-verification, network defaults, or container launch behavior.

WORKTREE: `.worktrees/0082-consent-gate/`

Design: Keep the existing policy helpers as the single source of truth and add a small CLI comprehension gate around them. `safeslop run` and `safeslop session run` will require matching yes/no answers before launching a `host` profile/session; `container` launches remain unchanged. Detached host sessions collect consent in the issuing `session run --detach` process before the supervisor is spawned, so the background PTY is not born already blocked on a prompt.

- [x] Add failing CLI consent tests
  FILE:     `internal/cli/cli_consent_test.go`, `internal/cli/cli.go`
  CHANGE:   Add tests for the prompt accepting matched answers, rejecting wrong answers, and for `cmdRun`/`cmdSessionRun` invoking the host consent gate before host launch. Add only the smallest test seams needed (`sessionHasInteractivePTY` may become a var) while red.
  VERIFY:   `go test ./internal/cli/ -run 'HostLaunchConsent|CmdRunHost|SessionRunHost' -v`
  EXPECTED: Fails because the consent helper and launch wiring do not exist yet.

- [x] Implement the host consent gate and launch wiring
  FILE:     `internal/cli/cli.go`
  CHANGE:   Add `confirmHostLaunchConsent`/`requireHostLaunchConsent` helpers that print `HostHeadlineBody`, `HostScopeLine`, and three shuffled `HostConsentStatements`, parse yes/no answers, and fail closed on any mismatch or interrupted input. Wire it into `cmdRun` after policy trust and into `cmdSessionRun` before host coupled/detached launch. Add a best-effort `mountedVolumes` helper for the scope line.
  VERIFY:   `go test ./internal/cli/ -run 'HostLaunchConsent|CmdRunHost|SessionRunHost' -v`
  EXPECTED: Targeted CLI consent tests pass.

- [x] Update docs/spec status for the behavior change
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0070-security-review.md`, `specs/0082-consent-gate.md`
  CHANGE:   Document that host launches require a per-launch comprehension gate, mark L2 implemented in `specs/0070`, and mark this spec implemented.
  VERIFY:   `rg -n 'host.*consent|comprehension|L2' README.md skills/agent-sandbox-ops/SKILL.md specs/0070-security-review.md specs/0082-consent-gate.md`
  EXPECTED: Docs and specs mention the new host consent gate and L2 implementation.

- [x] Run final verification
  FILE:     repository root
  CHANGE:   No code changes; run the required gates from the worktree.
  VERIFY:   `make check && make build`
  EXPECTED: Both commands exit 0.
