# 0073 — Session-lane profile fidelity (credentials silently stripped)

Status: implemented

## Problem

`sessionProfile` (internal/cli/cli.go) rebuilds a profile-backed session's
profile from the session record's scalar fields only: `{Agent, Environment,
Network, Workspace}` (+ the pinned package identity set). Everything else in
the approved `safeslop.cue` profile — `credentials:` (github/forgejo/pnpm/
aws/gcp/kube), `secrets:`, `egress:`, `toolchain:` — is silently dropped on
the session lane.

Found live during the 0069 T10 residual container smoke (2026-07-04): a
trusted `credentials: github:` profile launched via `session create` +
`session run --detach` came up with no staged git credentials at all — no
`git/` stage subtree, no `github-meta.json`, no 1Password mint, and therefore
no `session status` `github_creds` TTL block (0069 T8 can never fire on the
lane it was built for). The T7 `CredsEgress` union never fires either; the
allowlist's github entries came from agent/package egress defaults.

This inverts the boundary's contract in both directions:

- **Degraded silently** (availability): the agent gets none of the
  credentials the approved policy promised. Nothing fails; the agent just
  can't clone/push.
- **Trust-gate blind spot** (integrity): 0072 F1 re-verifies at run time that
  the *bytes* of `safeslop.cue` are still host-approved, then launches a
  synthetic profile that ignores most of what those bytes say. Approval is
  checked; the approved policy is not what runs.

`safeslop run` (the coupled, non-session verb) is unaffected: it parses the
cue directly.

## Fix (F1)

For profile-backed sessions (`sess.Profile != "" && sess.PolicyPath != ""`),
`sessionProfile` re-reads the pinned policy and launches the profile the
approval covers, fail-closed:

1. Read `sess.PolicyPath` bytes; require `trust.Hash(bytes) ==
   sess.PolicyHash` (exactly the create-time-approved bytes — same hash the
   0072 F1 gate verifies against the trust store; parsing from the same read
   eliminates the verify→parse TOCTOU).
2. `policy.LoadBytes` those bytes; require `Profiles[sess.Profile]` to still
   exist.
3. Launch that profile, with the record staying canonical for the fields it
   owns: `Workspace` from the record; the pinned `Resolved.IdentitySet` +
   `BareAgent` override still applies.
4. Any failure (unreadable file, hash mismatch, vanished profile, parse
   error) is an error — never a silent fallback to the synthetic profile.

Ad-hoc sessions (`--agent`/`--environment`; `PolicyPath` empty) keep the
synthetic reconstruction — there is no policy file to be faithful to, and
create gates them with `--trust-host`.

`sessionProfile` grows an error return; callers (`recordSessionBackend`,
`cmdSessionRun`, `Supervise`) propagate it.

## Non-goals

- No session-record schema change (the cue stays the single source of truth;
  the record keeps pinning path+hash, not policy content).
- No change to `safeslop run`, revoke, or stage-dir derivation.

## Tests

- Profile-backed session: reconstructed profile carries `credentials:` and
  `secrets:` from the cue (the exact fields the bug dropped).
- Hash mismatch (file edited after create) → error mentioning re-trust.
- Profile removed from the cue after create → error.
- Ad-hoc session (empty `PolicyPath`) → synthetic profile, no file reads.
- `Resolved.IdentitySet`/`BareAgent` override still applied on both paths.

## Verification

- `make check` (hermetic).
- Live re-run of the 0069 T10 residual smoke: session create → run --detach →
  `session status` shows `github_creds` TTL → in-container clone + mutating
  push → `stop --revoke-credentials` revokes before wipe.
