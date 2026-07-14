# Forge credential P2 — decision (FLO)

Date: 2026-07-14 · Status: proposed; approval required before implementation planning

## Verdict

Implement **one run-scoped, host-only credential-lease manager** inside the shared `runProfileCtx` lifecycle. It serves ordinary `run`, coupled `session run`, and detached-supervisor runs equally; the detached supervisor is not a credential service. It has no listener/RPC, sandbox-visible account data, generic broker API, or mint endpoint.

This implements the settled P2 scope from `specs/0068`: GitHub App renewal, enforced horizons, opt-in forge API staging, and bounded Forgejo deploy-key GC. It does not add live repository discovery, cloud/PAT renewal, GitHub Enterprise, or Forgejo token provisioning/introspection.

## Contract

### Lease horizon and state

- Existing `credentials.github.ttl` and `credentials.forgejo.ttl` keep the CUE default `"1h"`. An explicit `""` means renew/stage until normal run teardown; any other value must be a strictly positive `time.ParseDuration`. The horizon begins when staging begins.
- The horizon limits safeslop's **future minting/staging**, not the forge-native validity of an already issued credential. GitHub may retain its last ≤1h token after the horizon; Forgejo/API canonical files are removed and deploy-key delete is attempted at the horizon, as already settled by 0068. Remote deletion remains best effort.
- Add a value-free durable session snapshot, `credential_lease`, with aggregate/provider `healthy|renewing|degraded|expired`, reason, timestamps, bounded-horizon time, GitHub minimum expiry/partition count, and a coarse error class. It contains no token/key bytes, refs, key IDs, stage path, or raw provider response.
- A dead supervisor is `degraded/manager_unavailable` until the last known GitHub expiry, then `expired`; it cannot remain healthy from a stale snapshot. Existing additive `github_creds` fields remain compatible.
- Before every initial stage, remove an abandoned stage directory for that exact run identity (including retired-token artifacts); failure to clean it fails before any mint. This avoids reusing a crashed run's credential material.

### GitHub renewal and API

- Keep the current repo/read-write GitHub App partitioning. Git and API credentials are separate. `github.api.enabled` is valid only in App mode with nonempty, grammar-checked, nonduplicated `<permission>:read|write` declarations; requests remain repository- and permission-downscoped.
- Git and single API-partition consumers reread canonical 0600 stage files. The latter gets `SAFESLOP_GITHUB_TOKEN_FILE`; multiple partitions get an explicit directory plus value-free manifest and no ambiguous default. `GITHUB_TOKEN` is optional single-partition launch compatibility only and documented stale after renewal.
- Renew at two-thirds of the observed native lifetime. Reject a provider response that leaves under 10 minutes of usable lifetime. Retry failures after 5s, doubling to 5m with 0–20% injected jitter; reset after success and never schedule another attempt after the horizon/current-token expiry. Tests use zero jitter and a fake clock.
- Mint a whole replacement batch before writing any canonical file; write 0600 temp siblings and atomically rename. Do not revoke the old active token at renewal. Retain old token material only in stage-private 0600 retirement files until its natural expiry so teardown can best-effort revoke it; stage wipe is still primary.
- Add deny-tier `api.github.com:443` only for enabled GitHub API staging. Mint/renew/revoke stays on the host.

### Forgejo API

- Preserve deploy keys for git. Stage the linked opaque account token only when both `api.enabled` and existing `api.ackAccountWide` are true, at a 0600 canonical file exposed as `SAFESLOP_FORGEJO_TOKEN_FILE` — never as a conventional value environment variable.
- The acknowledgement means **operator-provisioned scope unverified; it may be account-wide**. Current Forgejo supports selected-repository tokens, but safeslop cannot prove a linked opaque token's provisioning; it must never claim repository restriction or insert a filtering proxy.
- API staging rejects non-HTTPS/non-443 instance URLs and adds only that instance hostname on 443 to deny-tier egress. It adds no egress when off.

### `creds gc`

Add `safeslop creds gc --host HOST --repo OWNER/REPO ... [--dry-run|--yes] [--output json]`. Host and at least one repository are mandatory; default is dry-run; `--yes` is the mutually exclusive destructive confirmation.

For each requested repository, resolve only the matching host/owner Forgejo account link in host memory, list that repository's deploy keys, and select titles **exactly** equal to `safeslop-<owner>-<repo>`. All discovery must succeed before deletion; the delete pass re-fetches and rechecks title. `404` is already absent; other failures are safe classes and make the command nonzero after remaining candidates are attempted. No repository discovery, prefix/substring match, automatic retry, user-key/token/link/PAT cleanup, GitHub token cleanup, or deletion outside requested repositories.

GC output is value-free (host/repository/title/action/count/error class). It is a crash backstop, not a security guarantee.

## Deterministic laws

Reject implementation that (1) gives sandbox code a mint/renew/revoke capability or account material, (2) emits secret values/refs/stage paths in durable/UI/GC output, (3) claims unverified Forgejo repo scope, (4) revokes active GitHub tokens during ordinary renewal, or (5) deletes a GC candidate without exact title/repository match and `--yes`.

## Evaluation

A FLO worker drafted the decision against a locked rubric; DeepSeek independently scored it: custody 10/10, lifecycle 9/10, contract 10/10, implementation fit 10/10, operator safety 9/10. Host weighted score: **96.5/100**. No LAW override fired.

The evaluator's retirement-file, backoff, and automatic Forgejo horizon-delete concerns were applied as deterministic clarifications above. Horizon deletion remains a settled 0068 behavior and must be stated in policy/docs/evaluation rather than silently treated as local cleanup.

## Method

Expansion: 0068/0047/0087, P1 creds/session/CLI/schema/egress code. AYO: `2026-07-14-forge-credential-p2-ayo.md`, three blind lanes (Kimi/DeepSeek/Gemini; Opus unavailable due provider session limit), plus GitHub, Vault, and Forgejo primary docs. FLO worker: `flo-worker`; evaluator: `flo-evaluator-deepseek`; evaluator did not compute the weighted total. Run artifacts were local ignored files only.
