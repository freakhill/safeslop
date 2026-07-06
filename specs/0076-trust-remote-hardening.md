# 0076 — Trust TOCTOU + git remote injection hardening (0070 M1/M2)

**Status:** plan, executing  **Date:** 2026-07-06

SCOPE: Fix `specs/0070-security-review.md` M1, then M2, in that order.
OFF-LIMITS: No new dependencies, no live network/credential APIs in tests, no trust-store schema change, no broad credential-provider redesign, no changes to egress/container defaults.
WORKTREE: `.worktrees/0076-trust-remote-hardening/`

## Problem

M1: `safeslop run` can parse one read of `safeslop.cue` but approve/check trust against another read, so validated bytes and executed bytes are not provably identical. The profile session create lane has the same parse-then-check shape.

M2: Git origin inference accepts owner/repo text from agent-writable `.git/config` and later writes it into generated git/ssh config, allowing quoted-newline or directive injection unless repo path components are constrained.

## Contract

- Policy-backed launch/create paths read the policy bytes once for their parse + trust decision, and carry the hash of those same bytes forward.
- `safeslop trust` may remain a pure approval command for current file bytes; launch-time `--trust` must approve the bytes that were parsed for launch.
- GitHub/Forgejo owner and repo components accepted from origin inference or declared repo lists must match `[A-Za-z0-9._-]+`; rejected values must fail before staging config files or API URLs are rendered.
- Existing valid repo examples keep working.

## Tasks

- [x] T1 — M1: bind policy-backed launch parsing to the trusted byte slice
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_trust_test.go`, `internal/cli/cli_trust_session_test.go`
  CHANGE:   Add tests for a loaded policy artifact whose config and trust hash come from one byte slice; refactor `cmdRun` and `createSessionFromProfile` to use that artifact instead of `policy.Load(path)` followed by a separate `checkTrust(path)` read. Keep `checkTrust` for trust CLI/session re-verify callers that intentionally inspect current bytes.
  VERIFY:   `go test ./internal/cli -run 'Test(LoadPolicyForLaunch|SessionCreateFromProfileRecordsLoadedPolicyHash|EnforceTrustGate|VerifySessionTrustDetectsDrift)' -count=1 -v`
  EXPECTED: command exits 0; targeted trust/session tests pass.

- [x] T2 — M2: reject injected owner/repo components before rendering staged git/ssh config
  FILE:     `internal/engine/creds/multirepo.go`, `internal/engine/creds/ssh.go`, `internal/engine/creds/forgejo.go`, tests in `internal/engine/creds/*_test.go`
  CHANGE:   Centralize repo component validation in the shared repo parsing path; apply it to declared repo specs plus GitHub and Forgejo origin inference. Add regression tests with embedded newline/quote config payloads and keep current valid forms green.
  VERIFY:   `go test ./internal/engine/creds -run 'Test(ParseOwnerRepo|ParseForgejoRemote|SplitOwnerRepo|RenderAliasSSHConfigRejects)' -count=1 -v`
  EXPECTED: command exits 0; injected remote/repo strings are rejected and valid examples still pass.

- [x] T3 — Docs/spec sync
  FILE:     `README.md`, `skills/agent-key-lifecycle/SKILL.md`, `specs/0070-security-review.md`, `specs/0076-trust-remote-hardening.md`
  CHANGE:   Document that origin-inferred repo names are constrained before credentials are staged; mark M1/M2 as implemented in 0070; update this checklist.
  VERIFY:   `rg -n 'M1|M2|origin|owner/repo|A-Za-z0-9' README.md skills/agent-key-lifecycle/SKILL.md specs/0070-security-review.md specs/0076-trust-remote-hardening.md`
  EXPECTED: output shows the new/revised documentation and 0070 status notes.

- [x] T4 — Full verification
  FILE:     whole repo
  CHANGE:   Format and run required gates.
  VERIFY:   `gofmt -w internal/cli/cli.go internal/cli/cli_trust_test.go internal/cli/cli_trust_session_test.go internal/engine/creds/multirepo.go internal/engine/creds/ssh.go internal/engine/creds/forgejo.go internal/engine/creds/multirepo_test.go internal/engine/creds/ssh_test.go internal/engine/creds/forgejo_test.go && make check && make build`
  EXPECTED: command exits 0; Go tests, ERT suite, denylist gates, and build pass.
