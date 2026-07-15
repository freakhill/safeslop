# 0103 — Progressive egress review and typed persistent rules

Status: complete
Date: 2026-07-15

SCOPE: make existing `environment:"container"` + `network:"deny"` progressive egress legible in Emacs and add an explicitly reviewed, policy-trusted durable exact-FQDN:port authority path. In the profile composer, label deny as **Deny (progressive review)**; in session detail, operator-opened review surfaces value-free denied destinations with **Allow now**, **Always allow**, and **Keep denied**.

OFF-LIMITS: do not add `network:"progressive"` or `network:"ask"`; do not show an agent-triggered modal/prompt, auto-open traffic, or attach a denied agent to the egress network; do not make host or `network:"allow"` grantable; do not reuse, translate, or mutate legacy `profile.egress`; do not permit IPs, private/link-local/metadata/broker/mint endpoints, wildcards, suffixes, URLs, or ports outside 80/443; do not expose request paths, query strings, headers, bodies, credentials, refs, or staged paths; do not add a daemon or runtime dependency.

WORKTREE: `.worktrees/0103-progressive-egress-review/`

## Pinned contract

`network:"deny"` remains the only composer value for progressive review; it remains an ordinary container-deny profile and does not itself grant traffic. A passive, operator-opened review is the only escalation surface. Repeated denied observations coalesce by exact FQDN:port. The agent gets the original denial and must retry after any later grant.

`Allow now` reuses the existing session overlay. `Keep denied` stores only a session-scoped, value-free acknowledgement so the currently observed destination does not demand attention again until a later deny; it never grants authority. `Always allow` is an explicit profile-policy change for future sessions only. It has a separate typed field:

```cue
persistentEgress?: [{fqdn: "api.example.com", port: 443}]
```

The field is accepted only for `environment:"container"` + `network:"deny"`; every entry uses the one shared exact-destination validator and represents exactly one normalized FQDN and port 80 or 443. It is never converted to or from the legacy string `egress` field. A `profile egress preview|add|remove` CLI owns mutation and requires the caller's expected current policy hash. Preview produces only a value-free logical CUE delta (`+/- {fqdn, port}`) plus current/candidate hashes. Add/remove fail closed when the expected hash is stale, re-render and validate the complete config before atomically writing it, and leave the changed policy untrusted; normal policy-byte trust is required before a new session can use it.

At container launch, persistent exact rules and session grants are rendered through the existing exact host+port Squid include. The session snapshot preserves persistent rules separately from dynamic grants, so grant/revoke overlay updates cannot erase a persisted rule. A persistent rule is visible as `profile-persistent / future sessions`; a dynamic grant is `session-grant / this session`; an observation/acknowledgement is not authority.

- [x] T1 — Establish the shared typed exact-egress policy contract
  FILE:     `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy.go`, `internal/engine/policy/policy_test.go`, `internal/engine/session/egress_grant.go`, `internal/engine/session/session_test.go`
  CHANGE:   Add `PersistentEgressRule {FQDN, Port}` and `Profile.PersistentEgress`; centralize exact FQDN:80/443 canonicalization and hard non-grantable rejection in `policy`, then make session grants consume that validator. Reject persistent rules on host/open profiles, duplicates after canonicalization, and all legacy/broader forms; retain existing legacy `Egress` semantics unchanged.
  VERIFY:   `go test ./internal/engine/policy ./internal/engine/session -run 'PersistentEgress|EgressGrant|ExactEgress|IPLiteral|Metadata|NetworkAllow|Host' -v`
  EXPECTED: Valid exact lowercase FQDN:443 rules decode; suffix/wildcard/IP/URL/metadata/other-port, duplicate, host, and open-network rules fail with no profile/session mutation; existing `profile.egress` fixtures remain compatible.

- [x] T2 — Materialize persistent rules without weakening the runtime overlay
  FILE:     `internal/engine/container/policy.go`, `internal/engine/container/policy_test.go`, `internal/engine/container/launch.go`, `internal/engine/container/launch_test.go`, `internal/engine/session/session.go`, `internal/engine/session/session_test.go`, `internal/cli/cli.go`, `internal/cli/egress_grant_apply_test.go`, `internal/cli/cli_session_test.go`
  CHANGE:   Snapshot typed persistent rules on profile-backed session creation; render their exact FQDN:port entries together with session grants into the existing Squid exact include on launch. Change grant/revoke overlay reconstruction to preserve the snapshot, while exposing source/lifetime separately in session data. Do not change a running session when its profile later changes; only a newly created, trusted session consumes a durable rule.
  VERIFY:   `go test ./internal/engine/container ./internal/engine/session ./internal/cli -run 'PersistentEgress|SessionGrant|ProxyReload|FailClosed|FutureSession|NoProfileMutation' -v && make check-assets`
  EXPECTED: Persistent exact entries permit only their host+port, survive session-grant add/revoke, respect hard denies, and are present only in a new profile-backed session; overlay failure preserves the prior restrictive effective set.

- [x] T3 — Add hash-checked profile persistent-egress transactions
  FILE:     `internal/cli/profile_egress.go`, `internal/cli/profile_egress_test.go`, `internal/cli/cli.go`, `internal/cli/cli_help_iw3_test.go`, `internal/jsoncontract/testdata/*.golden.json`
  CHANGE:   Add `profile egress preview|add|remove <profile> [safeslop.cue] --host H --port P --expected-policy-hash HASH --output json`. Reuse the structured profile-mutation renderer, but compare the loaded source policy hash before mutation, validate/canonicalize the rule, and emit only profile/path, rule, source/lifetime, current/candidate hash, and a logical `+/- persistentEgress: {fqdn,port}` diff. Preview does not write; add/remove atomically write only validated rendered policy and never touch legacy `egress`; output/help remains value-free.
  VERIFY:   `go test ./internal/cli -run 'ProfileEgress|PersistentEgress|PolicyHash|Help|ValueFree' -v`
  EXPECTED: Preview is non-mutating; add/remove require a matching hash, reject stale/invalid/non-enforceable requests, preserve unrelated profile fields and legacy egress, and produce a changed policy that normal launch trust rejects until re-trusted.

- [x] T4 — Add session-scoped denial acknowledgements and review contracts
  FILE:     `internal/engine/session/session.go`, `internal/engine/session/egress_grant.go`, `internal/engine/session/session_test.go`, `internal/cli/cli.go`, `internal/cli/cli_session_test.go`, `internal/jsoncontract/testdata/*.golden.json`
  CHANGE:   Add a value-free session acknowledgement record keyed by normalized observed FQDN:port and timestamp, plus `session egress dismiss --session-id ID --host H --port P --output json`. Filter only observations at/before the acknowledgement from the review result; later denied traffic becomes visible again. The action is container-deny-only, explicit, and has no proxy write/reload or profile mutation. Return pending observations and acknowledgement metadata sufficient for a UI count without request material.
  VERIFY:   `go test ./internal/engine/session ./internal/cli -run 'EgressDismiss|EgressObservation|Acknowledg|KeepDenied|NoAuthority|ValueFree' -v`
  EXPECTED: A dismissal does not add grants or alter proxy policy, suppresses only the acknowledged existing observation, and later denial reappears; invalid/non-enforceable calls return contract errors with no stored mutation.

- [x] T5 — Build the non-modal Emacs progressive-review flow
  FILE:     `emacs/safeslop-session.el`, `emacs/safeslop-profiles.el`, `emacs/safeslop-portal.el`, `emacs/test/safeslop-test.el`, `emacs/README.md`
  CHANGE:   Label container deny in compose as `Deny (progressive review)` with concise non-authorizing help. Add asynchronous session-detail/portal discovery of a passive pending-denial count and an operator-opened review buffer. Render only sanitized host:port, count, time, grantability, and source/lifetime; bind explicit actions for Allow now (existing grant argv), Keep denied (dismiss argv), and Always allow (hash-bearing preview, review buffer, then explicit add argv). Keep every network request asynchronous; never focus, pop, or invoke a confirmation from proxy traffic. Persistent review identifies the profile/policy hashes and logical rule delta; it cannot alter a running session.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT proves exact argv for preview/add/remove/dismiss, composer progressive labeling, value-free review rendering, passive/non-modal discovery, explicit operator-only actions, stale-hash handling, and no accidental CUE edit or egress action from an observation.

- [x] T6 — Synchronize operator documentation and verify the complete change
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0089-network-authority-ayo-flo.md`, `specs/0097-progressive-network-session-grants.md`, `specs/0103-progressive-egress-review.md`
  CHANGE:   Document the new exact typed `persistentEgress` field and CLI commands, progressive composer/review behavior, session-vs-future lifetime, hash/CUE-delta review and re-trust, Keep-denied semantics, non-grantable classes, and the absolute ban on agent-triggered modals/automatic authority. Mark this plan complete only after every verification command succeeds.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Formatting, Go vet/tests, Emacs tests, asset checks, and binary build pass; docs and skill examples match command help and the pinned safety contract.
