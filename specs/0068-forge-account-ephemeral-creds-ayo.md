# 0068 — Forge account linking + ephemeral repo-scoped creds: prior-art lessons (ayo)

Date: 2026-07-02 · Status: ayo compiled/triaged; feeds the 0068 decision-FLO.

## Headline

1. **GitHub App installation tokens are the only API-mintable, repo-subset + permission-subset
   scoped, auto-expiring (1h) credential on GitHub** — fine-grained PATs cannot be created via
   API (web-UI only, ≤50/user; GitHub's own guidance: "use a GitHub App"). One installation
   token serves both git-over-HTTPS and the REST API.
2. **Forgejo/Gitea has no per-repo API credential and no password-free token minting** —
   token creation requires BasicAuth(+OTP); token scopes are account-wide; OAuth2 tokens are
   unscoped. Deploy keys stay Forgejo's only per-repo primitive. Any design promising
   symmetric least-privilege across forges is a lie; the asymmetry must be surfaced.
3. **Identity is a property of the human, not the workspace**: the account link (broker
   credential) belongs in user-level state (userconfig + keychain/1Password refs), while
   project policy CUE declares only scope (which repos, which permissions).

## Triaged lessons

### HIGH (consensus; carried into the decision-FLO)

- **C1 GitHub broker = dedicated GitHub App** (app id + installation id as non-secret link
  state; private key as `op://` ref). Mint installation tokens per session, downscoped at
  request time. Demote ambient `gh` and BYO-PAT to fallback. *(4/4 lanes; G1/G2)*
- **C2 Account-link home = user-level state, never project CUE.** gh/aws/gcloud precedent;
  committed-config leaks are the recurring scar. Policy CUE keeps declaring scope; userconfig
  (or a sibling user-level file) holds links keyed by (forge-kind, host, owner) with refs +
  non-secret ids only. *(4/4; L1)*
- **C3 Forgejo: broker = pre-provisioned minimally-scoped token (`read/write:repository`)
  as ref; NEVER capture the account password; OAuth2 unusable (unscoped). API access inside
  a session = explicit opt-in acknowledging account-wide blast radius; deploy keys remain the
  per-repo git path.* (4/4; G3/G4/G5)*
- **C4 Downscope every mint on two independent axes** — `repositories` AND `permissions` —
  default-deny, omission is a bug; intersect the request with the profile's declared
  `RepoCred` set (confused-deputy defense). Scars: GitLab CI_JOB_TOKEN cross-project attacks;
  GITHUB_TOKEN's forced flip to read-only defaults. *(4/4; G2)*
- **C5 TTL is the load-bearing control; revoke is courtesy.** Validates existing L2. Enforce
  the currently-decorative `ttl` fields: bind session lifetime to the shortest staged-cred
  TTL, or run a HOST-side renewal loop (Vault Agent / octokit auth-app pattern) that
  re-mints and re-stages into the stage dir before expiry. Sandbox never self-renews. *(4/4)*
- **C6 Replace the `gh` shell-out with Go-native HTTP + RS256 JWT** (backdate `iat` ~60s,
  `exp` ≤10m — documented GitHub clock-skew footgun). Interface-seam all forge HTTP so unit
  tests run against fakes (resolves the L3 ambient-`gh` tension; satisfies L4). *(4/4)*
- **C7 Split git-transport cred from forge-API cred** as separately staged artifacts with
  independent scopes/lifetimes; git via credential-helper/SSH-alias seams (specs/0047
  machinery), API token via its own 0600 stage file. Never bake tokens into remote URLs or
  `.git/config`. *(4/4)*
- **C8 Link UX: `safeslop creds link/unlink/status` verbs**, value-free output (`gh auth
  status` model: identity, scopes, expiry — never the token). GitHub App onboarding via the
  app-manifest flow (returns the PEM in one redirect; avoids hand-copied keys). No-link =
  hard deny; never silently fall back to ambient `gh`. *(3/4 + host)*
- **C9 Egress: broker/mint endpoints stay unreachable from the sandbox** (IMDS/Capital One
  scar); auto-add the forge API host to the session egress allowlist only when an API-capable
  cred is staged. *(3/4; L5)*

### MEDIUM (design work needed; not decision-blocking)

- Ref-only per-mint audit record (link id, repo-set, permission-set, TTL, timestamp) — glm.
- `safeslop creds gc` orphan deploy-key sweep (best-effort revoke guarantees orphans) — deepseek.
- Single fixed container path for staged creds to kill the dual `.gitconfig`/`.container`
  render — deepseek.
- BYO-token env override for headless/CI runs (keyring-less environments) — gemini/opus.

### DEFERRED / CONTESTED (named for the FLO)

- Host-side filtering API proxy for Forgejo (repo-scopes an unscopeable token; heavy new
  machinery inside the trust boundary) — gemini only; FLO should accept/reject explicitly.
- OIDC/keyless federation to eliminate stored broker keys — no OIDC issuer in this context.
- GitHub git transport: deploy keys (structural 1-repo guarantee, no TTL) vs App-token HTTPS
  (1h TTL, covers API too) — deepseek prefers keys, glm/opus prefer one App token; FLO fork.
- Standalone `creds mint` diagnostic verb — gemini pro; tension with 0067's "lifecycle owned
  by run/session"; FLO fork.

## Actionables

1. S4: create the account-link layer (user-level, ref-only, keyed by forge/host/owner).
2. S1: add a GitHub App provider (Go-native JWT + HTTP); relocate Forgejo broker token into
   the link layer; keep deploy-key minting seams.
3. S2: extend policy CUE from `RepoCred{repo,write}` toward declared API permission sets
   (default none).
4. S3: make TTL real (session cap or supervisor-driven re-mint; supervisor from specs/0051
   is the natural renewal home).
5. S5: `creds link|unlink|status` + effective-scope display (Forgejo API = "account-wide"
   danger channel).
6. S6: egress auto-allowlist for forge API hosts only when an API cred is staged.

## Net

Everything mintable already exists upstream: GitHub Apps vend exactly the repo-set-scoped
ephemeral tokens safeslop needs, and Forgejo structurally cannot — so the design centers on
a per-forge broker abstraction (App key vs scoped token), user-level link custody, mint-time
downscoping against the profile's declared repo-set, and TTL enforcement. safeslop's frozen
laws (refs-not-values, stage-dir-only values, best-effort revoke) all survived contact with
prior art; the `gh` shell-out and the decorative `ttl` fields did not.

## Method

Blind lanes: ayo-research-gemini, ayo-research-deepseek, ayo-research-glm, ayo-research-opus
(all xhigh) + host lane; kimi lanes unavailable (broken max-token config). Shared grounding
packet host-verified 2026-07-02 from GitHub/Forgejo/Gitea docs (G1–G5 in the lane brief).
Expansion sources: internal/engine/creds/*.go, internal/engine/policy/policy.go, specs/0047,
specs/0067, internal/cli/cli.go, internal/engine/userconfig.
