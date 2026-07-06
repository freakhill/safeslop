# 0026 — GCP token scope downscoping (scope-first creds, review S5)

**Goal:** First slice of the security review's S5 / H5 ("scope-first, decay-second",
`specs/research/2026-06-19-design-security-review.md`): let a profile downscope the minted
GCP ADC access token to least-privilege OAuth scopes, so even a full-TTL reuse of a leaked
token is bounded to what the task actually needed — not the ADC default's broad `cloud-platform`.

**What shipped:** `#GcpAdc` gains an optional `scopes?: [...string]`; `StageGCP` passes them as
`gcloud auth application-default print-access-token --scopes=<comma-joined>`. Empty `scopes` =
ADC's default scopes, so existing profiles are unchanged.

```cue
credentials: gcp: {scopes: ["https://www.googleapis.com/auth/devstorage.read_only"]}
```

**Already handled (verified during this slice, not re-done):**
- **M1 — refresh-token reachability.** GCP delivers ONLY the short-lived access token via
  `CLOUDSDK_AUTH_ACCESS_TOKEN` (the long-lived refresh token is never read; specs/0078 removed the
  unused on-disk token copy); AWS `export-credentials` returns short-lived STS creds (the SSO
  refresh token stays in `~/.aws/sso`, never staged or mounted). The boundary never sees a
  long-lived credential.
- **S2 — ambient-authority leak.** Closed by specs/0024 (`childEnv` strict allowlist).

**Deferred (follow-ons, with rationale):**
- **AWS session-policy downscoping.** `export-credentials` can't take a session policy; the
  analogous downscope needs `assume-role` + a role ARN + an inline policy (more moving parts +
  user config). Its own slice.
- **`credential_process` delivery.** Staging creds via a `credential_process` file the SDK
  calls (instead of env vars) would keep them out of the same-uid process table — but the env
  channel is a deliberate choice (uniform across host/sandbox/container/vm with no path
  remapping). Changing it is a design fork, not a clean slice.

**Tests:** `internal/engine/creds/gcp_test.go` — `gcpTokenArgv(nil)` (default argv),
`gcpTokenArgv([...])` (comma-joined `--scopes`), env-only `StageGCP` delivery, and nil-creds noop.
`make check` + a `validate` smoke (scoped and unscoped profiles) pass.
