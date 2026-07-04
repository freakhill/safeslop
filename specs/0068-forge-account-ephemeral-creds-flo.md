# 0068 — Forge account linking + ephemeral repo-scoped creds: decision (FLO)

Date: 2026-07-02 · Status: decision landed (baseline 88.0/100; 5 deterministic fixes applied).
Awaiting user ratification before implementation planning (spec 0069).

## Verdict

GitHub broker = a dedicated GitHub App; per-session **installation tokens** (fixed 1h,
downscoped at mint time on repos AND permissions) replace deploy keys and the ambient `gh`
shell-out for both git-over-HTTPS and API on GitHub. Forgejo keeps 0047 deploy keys for git;
in-session Forgejo API = explicit opt-in staging of the pre-provisioned account-wide token
with a mandatory blast-radius ack; the filtering proxy is rejected. Account links live in a
new user-level `~/.config/safeslop/accounts.cue` (refs + non-secret ids only), managed by
value-free `safeslop creds link|unlink|status|gc` verbs; no standalone mint. TTL enforcement
= supervisor-driven host-side renewal (P2) behind an interim P1 hard 1h ceiling.

## Decision body

### F1 — GitHub: App is the primary broker; App tokens are the git transport

- **Broker:** dedicated GitHub App. App id + installation id are non-secret link state; the
  app private key exists only as an `op://`/`env:` ref (L1), resolved via `secrets.Resolve`
  on the HOST at mint time, held in memory only, never staged, never crossing the sandbox
  boundary (L5).
- **Mint path (Go-native; resolves the standing L3 violation):** new package
  `internal/engine/creds/githubapp/` — RS256 JWT (iat backdated 60s, exp 9m per G2
  clock-skew guidance) → `POST /app/installations/{id}/access_tokens`. All forge HTTP goes
  through a `ForgeHTTP` interface seam (sibling of `forgejoDo`) for hermetic fakes (L4).
- **Two mints per (host, owner) group, separately staged (C7):**
  - *git token:* permissions `{contents: read|write per RepoCred.Write, metadata: read}`,
    `repositories` = exactly the declared RepoCred set for that owner.
  - *api token:* minted only if `github.api.enabled`; permissions = declared
    `api.permissions` (empty = deny mint, default-deny per C4); same repo downscope.
- **Git transport staging (FIX-a, replaces the draft's credential-store):** reuse the proven
  `pat.go` seam (`renderPATGitConfig` family): per-owner token file
  `<stage>/git/token-<owner>` (0600) + staged gitconfig fragment with
  `[credential] useHttpPath = true` and one per-repo-URL `credential.helper` entry
  (`!f() { echo username=x-access-token; printf 'password='; cat <token-file>; }; f`),
  plus `insteadOf` ssh→HTTPS rewrites for declared repos. Multi-owner sessions therefore
  cannot cross-send tokens (helpers match on full repo path, not host), and because the
  helper re-reads the file on every git op, **renewal is transparent to in-flight work**.
- **Multiple owners:** engine groups declared repos by (host, owner); each owner requires a
  link; one token pair per group; >500 repos in a group = hard error (G2 limit).
- **Disposition of existing code:** the GitHub branch of `internal/engine/creds/ssh.go` +
  `multirepo.go` (`stageGitHubMulti`, `gh api` shell-out) is **deleted**; Forgejo deploy-key
  mechanics (0047) unchanged. `pat.go` survives solely as the explicit `mode:"pat"` BYO
  fallback; never a silent fallback — no link and no pat = hard deny with a
  `safeslop creds link` hint (C8).
- **Why tokens beat deploy keys on GitHub:** revoke is best-effort (L2) — a deploy key that
  survives a failed revoke is immortal with repo access; an App token dies at 60 minutes
  regardless. Minting deploy keys via the App would also require `administration:write`,
  strictly broader than `contents:write`.

### F2 — Account-link state: new user-level `accounts.cue`

- **Home:** `~/.config/safeslop/accounts.cue`, separate from `config.cue` (machine-written
  by `creds link`; separation avoids clobbering hand-edited prefs). Loaded/validated by
  `internal/engine/userconfig/accounts.go` with embedded CUE schema `accounts_schema.cue`
  (reuses the existing userconfig overlay pattern). Written atomically, mode 0600.
- **Shape (keyed by `host/owner`):**

  ```cue
  #Account: {
    forge: "github" | "forgejo"
    host:  string   // "github.com", "git.example.org"
    owner: string   // account/org login this link authorizes
    github?:  { appID: int, installationID: int, privateKeyRef: string }
    forgejo?: { tokenRef: string, sshPort?: int }
  }
  accounts: [string]: #Account
  ```

- **Custody rules:** refs and non-secret ids only (L1); the file is host-only — never
  mounted, copied, or serialized into the sandbox or IPC envelopes (L5). Project policy CUE
  continues to declare *scope only* (repos/write/ttl/api); identity stays with the human
  (C2). `ForgejoCreds.Token` migrates here as `tokenRef`.

### F3 — CLI surface (value-free per 0067)

```
safeslop creds link github  --app-id N --installation-id N --key-ref op://... [--host github.com]
safeslop creds link forgejo --host H --owner LOGIN --token-ref op://... [--ssh-port N]
safeslop creds unlink <host>/<owner>
safeslop creds status [--json]
safeslop creds gc [--host H] [--yes]
```

- `link github` probes by signing a JWT and calling `GET /app/installations/{id}` — proves
  key custody and derives `owner` from the installation account login **without minting any
  credential**. `link forgejo` probes `GET /api/v1/user/repos?limit=1` (covered by
  `read:repository`); probe values discarded per 0067. Forgejo linking NEVER prompts for
  passwords/OTP (C3/G3) — the user pre-provisions a `read:repository`/`write:repository`
  token in the Forgejo UI.
- `status` lists, per link: forge, host, owner, non-secret ids, probe result (ok/error
  class), and TTL model (`"1h-renewable"` for GitHub App, `"deploy-key decay"` /
  `"account-wide token"` for Forgejo). No values, ever. (FIX-e names the fields.)
- **No standalone `mint` verb** — it would create live credentials outside the
  session-owned lifecycle settled in 0067; the JWT probe inside `link`/`status` covers
  diagnostics without minting.
- `gc` sweeps orphaned deploy keys matched by the **frozen title convention
  `safeslop-<owner>-<repo>`** (already emitted by `stageGitHubMulti`/`stageForgejoMulti`;
  pinned as a contract by this note — FIX-e resolves the draft's Owed item), listing before
  deleting; output value-free.
- Existing `creds list|show` unchanged.

### F4 — TTL enforcement: supervisor renewal, phased behind a hard cap

- **Target semantics (P2):** the detached supervisor (0051) runs a host-side renewal loop
  (`internal/engine/supervisor/credrenew.go`): re-mint at ~2/3 of token lifetime (≈minute
  40 for GitHub's 1h) and atomically write+rename the staged token file. **The old token is
  NOT revoked at renewal** (FIX-c): it self-expires ≤20 minutes later, and revoking it
  mid-flight could kill in-progress git/API operations — the renewal race the draft missed.
  **Minute 55 of a 1h token:** the stage dir already holds a token minted at ~minute 40;
  minute-60 expiry is a non-event. Renewal-mint failures retry with backoff until old-token
  expiry, then the supervisor writes value-free `<stage>/creds.status`
  (`{state:"expired", expiresAt}`) and session status surfaces it. The sandbox never
  self-renews and can never reach mint endpoints (L5/C9).
- **Consumption contract (FIX-d):** the staged FILE is canonical. Git re-reads it per
  operation via the credential helper (renewal transparent). For API tokens, sessions get
  non-secret path envs (`SAFESLOP_GITHUB_TOKEN_FILE`, `SAFESLOP_FORGEJO_TOKEN_FILE`);
  optional value materialization into a conventional env var (e.g. `GITHUB_TOKEN`) stays
  available for tool compatibility but is documented as a **stale-after-renewal snapshot** —
  fine for P1 (≤1h sessions), discouraged for long P2 sessions.
- **Stop path (FIX-c):** best-effort revoke of the CURRENT installation token — host reads
  the staged token file pre-wipe and calls `DELETE /installation/token` (mirror of
  `RevokeForgejo`'s stage-dir revoke-info pattern); 404/expired = success; errors logged
  (not swallowed silently) and non-fatal. Stage-dir wipe remains the real cleanup (L2).
- **Interim (P1):** hard ceiling — no renewal; supervisor records the min-TTL deadline at
  session start and marks creds expired at minute 60; subsequent git/API ops fail 401 and
  session status names the cause with the renewal caveat.
- **`ttl` fields become enforced:** `Ttl` = maximum renewal horizon from session start
  (`""` = renew for session lifetime); past the horizon the supervisor deliberately stops
  renewing and marks the session degraded. For Forgejo deploy keys, `Ttl` drives a
  supervisor best-effort revoke at horizon (plus at stop). Revocation everywhere stays
  courtesy (L2).

### F5 — Forgejo in-session API: explicit opt-in, ack required; proxy rejected

- Forgejo/Gitea tokens are account-wide, period (G3/G5) — no per-repo Forgejo API
  credential exists and this note does not pretend otherwise. In-session API access stages
  the linked account token to `<stage>/forge/forgejo-api-token` (0600) **only** when policy
  sets both `api.enabled: true` and `api.ackAccountWide: true`; `enabled` without `ack` is
  a hard policy error; session start prints a value-free blast-radius warning.
- **Egress (C9 + FIX-b):** Forgejo `host:sshPort` allowed whenever repos are declared
  (deploy-key git); Forgejo `host:443` added only when the API token is staged. GitHub git
  staging allows `github.com:443` **plus the object/CDN hosts standard clones require:
  `codeload.github.com:443` and `objects.githubusercontent.com:443`** (unioned via the
  existing 0046 allowlist machinery); `api.github.com:443` only when `github.api.enabled`.
  Mint/renewal traffic originates from the HOST supervisor only, never the sandbox netns.

### Policy schema deltas (`internal/engine/policy/policy.go`)

Pre-alpha hard break; loader rejects removed fields with pointed errors — no silent
aliasing (house posture: pre-alpha optimizes for correctness over churn-avoidance).

```go
type GithubCreds struct {              // replaces SshCreds
    Mode  string     // *"app" | "pat"
    Write bool
    Ttl   string     // renewal horizon
    Pat   string     // ref; mode "pat" only
    Repos []RepoCred
    Api   *GithubApi // nil = no API cred staged
}
type GithubApi struct { Enabled bool; Permissions []string }
type ForgejoCreds struct {             // Mode, Token, Pat removed
    Write bool; Ttl string; URL string; SSHPort int
    Repos []RepoCred
    Api   *ForgejoApi
}
type ForgejoApi struct { Enabled bool; AckAccountWide bool }
```

Mint-time intersection (C4): request repositories = declared RepoCred set ∩
installation-accessible repos; any declared repo outside the installation = hard fail at
session start with an "install the App on X" error.

### Phasing

1. **P1:** `accounts.cue` + loader; `creds link|unlink|status` (manual link); `githubapp`
   Go-native mint; per-URL-helper git staging; delete `gh` shell-out; policy migration;
   hard-deny-without-link; egress rules (incl. CDN hosts); P1 TTL cap; stop-path revoke.
2. **P2:** supervisor renewal + `ttl` horizons; `api.enabled` staging for both forges
   (+ Forgejo ack); `creds gc`.
3. **P3:** app-manifest onboarding flow (PEM held in memory, pushed to the secret manager
   via a `secrets` write-side seam, ref stored; aborts to manual instructions if
   unavailable).

### Doc/test sync (AGENTS.md mandate)

- **README:** creds section rewrite — link flow, new schema, egress table, TTL/renewal
  semantics, Forgejo asymmetry.
- **skills/**: `agent-key-lifecycle`, `agent-sandbox-ops`, and any skill referencing
  `creds list|show`, deploy keys, or credential workflows updated to new verbs/defaults.
- **Tests (hermetic, L4):** `githubapp` JWT + mint/renew against `ForgeHTTP` fakes with a
  fixed clock; downscope-intersection table tests incl. undeclared-repo deny and >500
  error; accounts schema accept/reject; policy loader migration errors for removed fields;
  CLI golden tests asserting zero secret bytes in `link|status|gc` output; credential-helper
  gitconfig render tests (multi-owner separation, useHttpPath).
- `make check` and `make build` gate completion.

## Rejections

- **Forgejo host-side filtering proxy:** re-implements forge authorization over an evolving
  REST surface; exposes a mint-adjacent trusted endpoint to hostile sandbox traffic
  (contradicts C9); holds the account-wide token in a long-lived host service; sells false
  least-privilege confidence. Revisit only if Forgejo ships repo-scoped tokens upstream.
- **GitHub deploy keys as primary transport:** immortal on failed revoke vs 1h self-expiry;
  minting via App needs `administration:write` (broader than `contents`); keeps the `gh`
  shell-out for no gain.
- **Fine-grained PATs as primary:** not API-mintable, ≤50/user, web-only (G1); demoted to
  explicit `mode:"pat"` fallback.
- **Forgejo OAuth2/device flow:** tokens unscoped (G4).
- **Minting Forgejo tokens via BasicAuth:** capturing passwords/OTP is out (C3/G3).
- **Link state in project policy CUE / inline in config.cue:** identity belongs to the
  human, not the workspace; machine-rewriting a hand-edited prefs file is clobber-prone.
- **Sandbox self-renewal / standalone `creds mint`:** violate L5/C9 and 0067's
  session-owned lifecycle respectively.
- **Rejected evaluator flags (with reasons):** "graceful schema deprecation" — contradicts
  the settled pre-alpha posture (hard break with pointed loader errors IS the migration);
  "refs in argv leak metadata" — refs are L1-permitted by design and carry no secret value;
  accepted as-is for pre-alpha.

## Deferred / Owed

- **Owed:** `secrets` write-side seam (`op item create`) decision — gates P3 manifest
  onboarding only.
- **Deferred:** GitHub Enterprise (same-host git/API changes the egress split); mint
  rate-limit/backoff tuning; >500-repo owner groups; Forgejo repo-scoped tokens upstream
  watch item; `accounts.cue` stale-link gc.

## Method

- Pipeline: Expansion (creds/*.go, policy.go, specs/0047+0067, cli.go, userconfig) → ayo
  (4 blind lanes gemini/deepseek/glm/opus + host, kimi broken; note
  `specs/0068-forge-account-ephemeral-creds-ayo.md`) → decision-FLO.
- FLO roles: worker = `flo-worker` (single-shot draft, no tools); scorer =
  `flo-evaluator-deepseek` (cross-family, rubric-locked); flag-only auditor =
  `flo-evaluator-gemini` (flags investigated, never averaged). Host computed totals.
- Locked rubric / scores: R1 boundary-custody 25% → 9; R2 least-privilege 25% → 10;
  R3 settled-machinery fit 20% → 9; R4 lifecycle honesty 15% → 7; R5 surface coherence
  15% → 8. **Weighted baseline 88.0/100. Fatal flaws (scorer): none.**
- Deterministic fixes applied by host (evidence-forced, no new hard sub-choice → no
  re-evaluation cycle): FIX-a per-URL credential helpers + `useHttpPath` (gemini FATAL
  flag; in-repo precedent `pat.go` proves the pattern); FIX-b GitHub CDN egress hosts
  (standard clone/LFS reality); FIX-c no mid-flight revoke at renewal + stop-path revoke
  with logged errors (deepseek W1 + gemini MINOR); FIX-d token consumption contract
  (deepseek W2); FIX-e gc title convention pinned from existing code + `status` fields
  named (deepseek W3/W4).
