# Forge credential P2 — AYO prior-art triage

Date: 2026-07-14 · Status: input to decision-FLO

## Question

How should safeslop renew forge credentials, enforce their maximum lifetime, stage API access, and clean orphaned deploy keys without letting secret values, mint authority, or unbounded access cross the host/sandbox boundary?

## High-confidence lessons applied

1. **Separate an issued credential's expiry from the policy maximum lifetime.** Vault leases make a requested renewal advisory and enforce a max TTL independently; GitHub installation access tokens expire after one hour. Treat `credentials.*.ttl` as a run-relative **maximum renewal horizon**, never as a request that changes the forge's own native token lifetime. Persist only the horizon and expiry metadata; at the horizon stop minting, expose a value-free degraded/expired status, and rely on stage wipe / best-effort cleanup. [Vault leases](https://developer.hashicorp.com/vault/docs/concepts/lease), [GitHub installation tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)

2. **Renew by overlap and atomic replacement, never mid-flight revocation.** GitHub lets an App mint another installation token; a token is valid for one hour and `DELETE /installation/token` invalidates the particular token used for the request. A Git credential helper can reread a canonical token file for each operation. The renewal path must write a 0600 temporary sibling then atomically replace the canonical file, retain the old token until natural expiry, and revoke all still-readable current/retained tokens only at teardown. [GitHub token lifetime](https://docs.github.com/en/organizations/managing-programmatic-access-to-your-organization/github-credential-types), [GitHub revoke endpoint](https://docs.github.com/en/rest/apps/installations)

3. **Make the host lifecycle, not detached mode, own renewal.** A detached supervisor already owns a `runProfileCtx` lifetime, but coupled `run` and coupled `session run` can also exceed an hour. A run-scoped host lease manager started/stopped by the shared staging lifecycle lets all session modes obey the same contract; the detached supervisor merely keeps that run alive. This corrects the P1 implementation gap without introducing a sandbox broker.

4. **API staging must disclose the forge's actual server-side scope.** GitHub App API tokens can be downscoped by repository and permission at mint time (up to 500 repositories). Forgejo access tokens now support selected-repository mode for a restricted subset of repository/issue scopes, but safeslop's existing account links contain only an opaque ref and cannot prove its provisioning scope. Therefore P2 must not represent a linked Forgejo token as repo-scoped: its API status must say `operator-provisioned scope unverified`, and API staging needs an explicit acknowledgement of that residual authority. Do not add a filtering proxy. [GitHub downscoping](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app), [Forgejo token scope](https://forgejo.org/docs/latest/user/token-scope/)

5. **Garbage collection needs a narrow, observable namespace.** Best-effort teardown can miss deploy keys after a crash. `creds gc` should enumerate only deploy keys whose title exactly matches the existing frozen `safeslop-<owner>-<repo>` convention, print a value-free plan first, and delete only after explicit `--yes`; it is not general key administration. This is a backstop, not a security primitive.

## Deferred

- Changing account-link creation to mint or introspect Forgejo tokens.
- Live repository discovery, GitHub Enterprise, or a sandbox-side credential broker.
- A generic renewable interface for AWS/GCP/Kubernetes/PAT credentials.
- Automatically deleting unscoped Forgejo tokens or user-managed deploy keys.

## Method

Expansion used `specs/0068`, `specs/0047`, P1 credential/staging/session code, schema/egress rules, and account-link CLI. Blind lanes completed: Kimi, DeepSeek, Gemini; Opus was unavailable due its provider session limit. Host research used the primary documentation links above. Lanes agreed on overlap renewal, max-horizon enforcement, value-free metadata, and narrow GC; Forgejo's current selected-repository token support contradicted the older blanket “account-wide” wording, so the decision must preserve honest unverified-scope disclosure rather than manufacture a repo guarantee.
