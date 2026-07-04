# 0069 — Forge account links + ephemeral repo-scoped creds: P1 implementation plan

**Status:** implemented — P1 landed on `forge-account-creds` (T1–T10; T8 TTL surfaced in `session status`, github row kind renamed ssh→github). Real-forge smoke = manual (T10, see PR). **Date:** 2026-07-03 (impl 2026-07-04)
Executes P1 of the ratified decision `specs/0068-forge-account-ephemeral-creds-flo.md`
(FLO baseline 88.0/100, 5 forced fixes folded). Prior art & laws:
`specs/0068-forge-account-ephemeral-creds-ayo.md`, specs/0047 (deploy-key staging),
specs/0067 (value-free creds UX), specs/0046 (egress allowlist union).

## Scope

**In (P1):**
- `~/.config/safeslop/accounts.cue` + loader (`internal/engine/userconfig/accounts.go`).
- `safeslop creds link|unlink|status` (manual link; probes, value-free).
- Go-native GitHub App minting (`internal/engine/creds/githubapp/`, RS256 JWT,
  `ForgeHTTP` seam) — deletes the `gh` shell-out.
- App-token git-over-HTTPS staging via per-URL credential helpers (generalizes the
  proven `pat.go` renderer); GitHub deploy keys deleted.
- Policy schema hard break: `credentials.ssh` → `credentials.github`
  (`SshCreds` → `GithubCreds`), `ForgejoCreds` loses `Mode/Token/Pat`; pointed loader
  errors on removed fields.
- Hard-deny-without-link (with `safeslop creds link` hint).
- Credential-driven egress: GitHub HTTPS + CDN hosts unioned when github creds staged.
- P1 TTL hard cap (no renewal): expiry surfaced in session status.
- Stop-path best-effort revoke of the current installation token (errors logged).

**Out:** P2 = supervisor renewal loop, `ttl` horizons, API-token staging
(`api.enabled`, Forgejo ack), `creds gc`. P3 = app-manifest onboarding. The `Api`
policy structs land in the schema NOW (schema stability), but staging with
`api.enabled: true` errors `"forge API staging lands in P2 (specs/0068 F5)"` — explicit,
not silently ignored.

**Contract clarification (forced by GitHub's token model, not a new choice):** 0068 F1
says the git token carries `contents: read|write per RepoCred.Write`, but App-token
permissions are token-wide — per-repo write granularity within one token is impossible.
Deterministic resolution honoring C4 default-deny: **partition each owner group by
`Write`** and mint one token per partition (ro set, rw set); the per-URL helpers point
each repo at its partition's token file. Worst case 2 mints/owner.

## Execution notes

- Work in a worktree: `.worktrees/forge-account-creds` (using-git-worktrees).
- TDD per task where behavior is testable (test-driven-development skill).
- Tests hermetic (AGENTS.md): forge HTTP via `ForgeHTTP` fakes / `httptest`; JWT with a
  fixed clock; **no live network, no credential APIs**.
- Every task lists Done-when; `make check` + `make build` gate the whole plan.

---

## T1 — Policy schema hard break

**Files:** `internal/engine/policy/policy.go`, `internal/engine/policy/schema/schema.cue`,
`internal/engine/policy/policy_test.go`, ripple: `internal/cli/cli.go`,
`internal/engine/creds/pat.go`, presets/testdata that mention `ssh:`.

- Replace `SshCreds` with `GithubCreds` (json `"github"` on `Credentials`):
  `Mode string` (`*"app" | "pat"`), `Write bool`, `Ttl string`, `Pat string` (ref,
  mode `"pat"` only), `Repos []RepoCred`, `Api *GithubApi{Enabled bool, Permissions []string}`.
- Slim `ForgejoCreds`: drop `Mode`, `Token`, `Pat`; keep `Write, Ttl, URL, SSHPort, Repos`;
  add `Api *ForgejoApi{Enabled bool, AckAccountWide bool}`.
- Schema: closed structs already reject unknown fields with CUE path errors; add pointed
  migration hints at load time for the three legacy shapes:
  `credentials.ssh` → `renamed to credentials.github (specs/0069)`;
  `credentials.forgejo.token` → `moved to ~/.config/safeslop/accounts.cue — run: safeslop creds link forgejo`;
  `credentials.forgejo.mode|pat` / `credentials.github.mode: "deploy-key"` → removed, with
  the replacement named. No silent aliasing (house pre-alpha posture).
- Rename the cli.go single-forge check (`Ssh != nil && Forgejo != nil` → `Github`/`Forgejo`);
  message keeps the "one forge per profile" restriction (cross-forge unification is not in
  0068 scope).
- `Api.Enabled` without ack (forgejo) = load-time policy error even in P1 (schema-level:
  `enabled` requires `ackAccountWide`); `Api.Enabled` at staging = P2 error (see Scope).

**Tests:** loader accept/reject table — new shape accepted; each legacy field rejected with
its hint text; forgejo `api.enabled` without ack rejected.
**Done when:** `go test ./internal/engine/policy/...` green; grep shows no `SshCreds`.

## T2 — Account store (`accounts.cue`)

**Files:** new `internal/engine/userconfig/accounts.go`,
`internal/engine/userconfig/schema/accounts.cue` (embedded),
`internal/engine/userconfig/accounts_test.go`.

- Mirror the existing `userconfig.Load` overlay pattern (embedded schema + virtualDir).
- Shape per 0068 F2: `accounts: [string /* "host/owner" */]: #Account` with
  `forge "github"|"forgejo"`, `host`, `owner`, `github?: {appID, installationID,
  privateKeyRef}`, `forgejo?: {tokenRef, sshPort?}`. Refs + non-secret ids ONLY (L1).
- API: `LoadAccounts(path)`, `SaveAccounts(path, *Accounts)` (atomic tmp+rename, 0600,
  parent dir 0700), `Lookup(host, owner)`, `Remove(key)`. Default path
  `~/.config/safeslop/accounts.cue`; overridable for tests.
- File is host-only: nothing here may be serialized into stage dirs, compose env, or IPC
  (L5) — enforced by review + the T5 golden tests, not runtime code.

**Tests:** schema accept/reject (bad forge kind, missing per-forge block, extra fields);
save→load roundtrip; 0600 mode asserted.
**Done when:** package tests green.

## T3 — `githubapp`: Go-native App JWT + installation-token mint

**Files:** new `internal/engine/creds/githubapp/{jwt.go,mint.go,http.go,*_test.go}`.

- `ForgeHTTP` interface (sibling of `forgejoDo`): `Do(ctx, method, url, headers, body)
  ([]byte, int, error)`; real impl = `net/http` with timeout; fakes for tests. Client
  carries `apiBase` (default `https://api.github.com`) so tests point at `httptest`.
- `AppJWT(appID int, keyPEM []byte, now time.Time)`: RS256, `iat = now-60s`,
  `exp = now+9m` (0068 G2 clock-skew guidance). `crypto/rsa` + `crypto/x509` PEM parse —
  no new deps unless stdlib JWT assembly proves error-prone; if a dep is needed,
  `golang-jwt/jwt` requires an explicit note here per AGENTS.md (no silent runtime deps).
- `InstallationInfo(ctx, h, appID, instID, keyPEM)` → `GET /app/installations/{id}`,
  returns account login + non-secret metadata (drives `link github` owner derivation and
  `status` probes; mints nothing).
- `MintToken(ctx, h, appID, instID, keyPEM, req{Repositories []string, Permissions
  map[string]string})` → `POST /app/installations/{id}/access_tokens`, returns
  `{Token, ExpiresAt}`. Enforce: empty permissions = deny (C4); >500 repos = hard error
  (G2); 422/404 mapped to `install the GitHub App on <owner>/<repo>` guidance (C4
  intersection failure).
- `Revoke(ctx, h, token)` → `DELETE /installation/token`; 401/404 = success (already
  dead); other errors returned for logging.
- Error sanitization: no token/PEM bytes in any error string (deepseek R1 gap).

**Tests (hermetic, fixed clock):** JWT header/claims golden; mint request body
(repositories + permissions exactly as requested); >500 and empty-permissions denials;
422→guidance mapping; revoke 404-is-success; error strings contain no secret bytes.
**Done when:** package tests green.

## T4 — GitHub staging: App tokens over HTTPS; deploy keys + `gh` deleted

**Files:** new `internal/engine/creds/github.go` (+`github_test.go`); surgery on
`ssh.go`, `multirepo.go`, `pat.go`; call sites `internal/cli/cli.go:1788` (StageSSH) and
the teardown that calls `RevokeSSH`.

- `StageGithub(ctx, creds, stageDir, accounts)`:
  1. Repos empty → infer single `owner/repo` from cwd origin (preserves current UX),
     then treat as declared.
  2. `Mode "pat"` → existing `stageGitHubPAT` (retyped to `*policy.GithubCreds`).
  3. Group declared repos by owner (host = github.com in P1; Enterprise deferred).
     Each owner MUST have an accounts link → else hard deny:
     `no GitHub account link for <owner> — run: safeslop creds link github` (C8). No
     silent PAT fallback.
  4. Per owner: resolve `privateKeyRef` via `secrets.Resolve` (host memory only, never
     written); partition repos by `Write`; `MintToken` per partition with
     `{contents: read|write, metadata: read}` and the exact repo list.
  5. Stage: `<stage>/git/token-<owner>[-rw]` (0600) + gitconfig via the T4a renderer +
     `<stage>/git/github-meta.json` (value-free: owners, token paths, `expiresAt`) for
     the T8 status/stop paths. Env: `GIT_CONFIG_GLOBAL`, `GIT_TERMINAL_PROMPT=0`.
- **T4a renderer generalization:** extend `renderPATGitConfig`/`writeCredentialHelper`
  (pat.go) to take per-repo token paths (repo→tokenPath map) instead of one path;
  keeps `[credential] useHttpPath = true`, per-URL helpers (`cat <file>` at credential
  time — renewal-transparent by construction), ssh→HTTPS `insteadOf` rewrites, and the
  `.container` variant with `/safeslop/runtime/...` paths. PAT mode reuses it with a
  single-entry map (behavior unchanged).
- **Deletions:** `stageGitHubMulti`, `ghRegisterArgv`, `ghRevokeArgv`, `githubKnownHosts`,
  the GitHub branch of `StageSSH`, `RevokeSSH` (gh-based); their tests. `multirepo.go`
  keeps `stageRepoSSH`/`renderAliasSSHConfig`/`stageForgejoMulti` (Forgejo deploy keys
  unchanged, 0047). GitHub SSH staging ceases to exist.
- New `RevokeGithub(ctx, stageDir)`: read meta + token files pre-wipe, `githubapp.Revoke`
  each; log failures (visible, non-fatal — L2 courtesy); called from the same teardown
  that ran `RevokeSSH`.

**Tests:** multi-owner separation (two owners → two helpers, no cross-token); mixed
write partition (ro+rw repos of one owner → two tokens, helpers point correctly);
deny-without-link message; renderer goldens (incl. container variant); meta file has no
token bytes.
**Done when:** `go test ./internal/engine/creds/...` green; `grep -r "gh api"` in
`internal/` returns nothing.

## T5 — CLI verbs: `creds link|unlink|status`

**Files:** `internal/cli/cli.go` (cmdCreds at ~1384), new `internal/cli/creds_link.go`
if cli.go bloats; golden tests beside existing CLI tests.

- `creds link github --app-id N --installation-id N --key-ref op://... [--host github.com]`:
  resolve ref → `InstallationInfo` probe (no mint) → derive owner from installation
  account login → upsert accounts entry → print value-free confirmation.
- `creds link forgejo --host H --owner LOGIN --token-ref op://... [--ssh-port N]`:
  resolve ref → probe `GET /api/v1/user/repos?limit=1` via `forgejoDo` → upsert. NEVER
  prompts for passwords/OTP (C3/G3); `--owner` is explicit because Forgejo tokens don't
  self-describe an installation target.
- `creds unlink <host>/<owner>`: remove entry; report if absent.
- `creds status [--json]`: per link — forge, host, owner, non-secret ids, probe result
  (ok / error class only), TTL model (`1h-renewable` | `deploy-key decay` |
  `account-wide token`). Probe failures never abort the listing.
- Existing `creds list|show` untouched. No `mint` verb (0067 session-owned lifecycle).
- Testability: link/status accept an injected `ForgeHTTP`/base-URL seam so goldens run
  against `httptest` fakes.

**Tests:** golden output for link/status/unlink asserting ZERO secret bytes (feed a
known fake token through the seam, grep output); `--json` shape; probe-failure rendering.
**Done when:** CLI tests green; `safeslop creds --help` shows the new verbs.

## T6 — Forgejo: token ref moves to accounts; PAT mode deleted

**Files:** `internal/engine/creds/forgejo.go`, `multirepo.go` (`stageForgejoMulti`),
`pat.go` (delete `stageForgejoPAT`), tests.

- `StageForgejo`/`stageForgejoMulti`: token ref now comes from the accounts link matched
  by (host from `fc.URL`, owner) — same grouping rule as GitHub; hard deny without link
  (`run: safeslop creds link forgejo`). `fc.Token` is gone (T1).
- Multi-owner Forgejo repos: deploy keys are per-repo so the mechanics don't change;
  each declared owner still needs a link (the account token registering the key must
  have admin on that repo — surface the forge's 403 with that hint).
- revoke-info keeps its `<base> <owner>/<repo> <id> <token-ref>` format; the ref now
  originates from accounts. `RevokeForgejo` mechanics unchanged.
- Delete `stageForgejoPAT` + its tests (contract: ForgejoCreds has no Pat/Mode).

**Tests:** deny-without-link; ref-from-accounts plumbed into revoke-info; existing
deploy-key staging tests updated to the new source of the token ref.
**Done when:** `go test ./internal/engine/creds/...` green.

## T7 — Credential-driven egress

**Files:** `internal/engine/policy/egress.go` (or the allowlist materialization point in
`internal/engine/container/policy.go` — implementer confirms where `AgentEgress` +
profile `egress:` are unioned, specs/0046).

- New `CredsEgress(prof *policy.Profile) []string`: github creds staged →
  `github.com`, `codeload.github.com`, `objects.githubusercontent.com` (0068 FIX-b;
  clones/LFS redirect to CDN hosts). `api.github.com` is NOT added in P1 (API staging
  is P2). Forgejo: verify how deploy-key SSH egress is currently allowed (squid is
  HTTP; SSH may ride a different rule from specs/0008/0046) and leave it unchanged;
  Forgejo `host:443` also waits for P2 API staging.
- Union at the same point as `AgentEgress`, container/network:deny profiles only.

**Tests:** table test — profile with github creds gets the 3 hosts; without, none.
**Done when:** egress tests green; a github-creds profile's materialized allowlist
(existing allowlist test fixture) shows the hosts.

## T8 — P1 TTL cap + stop-path revoke wiring

**Files:** `internal/cli/supervise.go`, session status path (`internal/engine/session/wire`
+ whatever renders `session status`), teardown call site for `RevokeGithub`.

- No renewal in P1 (0068 F4 interim). Session records min `expiresAt` from
  `<stage>/git/github-meta.json` at start; `session status` compares against now and
  past deadline reports: `github token expired (1h App-token ceiling; renewal lands in
  P2 — specs/0068 F4)`. Value-free.
- Stop path: `RevokeGithub` (T4) runs where `RevokeSSH` used to, BEFORE stage wipe;
  failures logged, never fatal; wipe remains the real cleanup (L2).
- No `<stage>/creds.status` file in P1 (that's the P2 renewal loop's artifact) — status
  is computed from meta at query time.

**Tests:** status rendering pre/post deadline (fixed clock / injected now); teardown
order test if the harness allows (revoke before wipe).
**Done when:** session status shows TTL state for a github-creds profile.

## T9 — Docs + skills sync (AGENTS.md mandate)

**Files:** `README.md`; `skills/` (grep for `creds list|show`, deploy key, `gh auth`,
credential workflow references — 0068 names `agent-key-lifecycle` and
`agent-sandbox-ops`; trust the grep over the list); CLI help strings.

- README creds section rewrite: link flow (App creation pointer + manual link), new
  policy schema (`github:` example incl. `pat` fallback), egress table row, TTL
  semantics (P1 cap, P2 renewal pointer), Forgejo asymmetry paragraph (account-wide
  token honesty), accounts.cue custody note.
- Update every matching skill file to the new verbs/defaults; examples must run as
  written (Done checklist #2).

**Done when:** grep for `credentials: ssh:` / `gh api` / stale verb examples across
README+skills returns nothing.

## T10 — Verification gate

- `make check` (includes ERT + Go tests) and `make build` — both must pass.
- Repo-wide greps prove the break is total: `SshCreds`, `stageGitHubMulti`,
  `ghRegisterArgv`, `credentials.*ssh:` in docs/testdata → zero hits.
- Manual smoke (documented, not automated): `safeslop creds link github` against a real
  App on a scratch repo, `safeslop run` a github-creds profile, verify clone+push and
  `creds status`. Real-forge smoke is explicitly OUTSIDE `make check` (hermetic rule).

**Done when:** both commands pass and the smoke checklist is written into the PR/commit
description.

---

## Task order & dependencies

```
T1 (policy) ──┬─→ T4 (github staging) ─→ T7 (egress) ─→ T8 (ttl/stop)
T2 (accounts) ┤                        ↘
T3 (githubapp)┘                          T5 (CLI verbs)
T1+T2 ────────→ T6 (forgejo migration)
T4..T8 ───────→ T9 (docs) ─→ T10 (gate)
```

T1/T2/T3 are independent — parallelizable as three subagent lanes. T4 is the load-bearing
integration task; keep it single-lane.

## Owed / deferred (unchanged from 0068)

- P2: renewal loop + `ttl` horizons + API staging + `creds gc` (gc's title convention
  `safeslop-<owner>-<repo>` is already frozen in code — `multirepo.go`).
- P3: app-manifest onboarding (gated on the `secrets` write-side seam decision).
- Deferred: GitHub Enterprise, mint backoff tuning, >500-repo groups, accounts stale-link gc.
