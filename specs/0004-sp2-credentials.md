# SP2 — credential providers: 1Password secrets + pnpm token helper

**Goal:** Add the two new credential capabilities from the design (specs/0001 §7) to the Go
engine — 1Password-sourced secret injection and a pnpm/npm registry-token helper — wired into
`slop run` with stage-then-wipe-on-exit. Builds on the SP1 engine.

## What this delivers

### Schema (`internal/engine/policy/schema/schema.cue`)
A profile may now declare:

```cue
secrets?: {[string]: #SecretRef}     // env var name -> secret ref
credentials?: { pnpm?: [...#PnpmRegistry] }
#SecretRef:    string & =~"^(op://|env:).+"   // 1Password URI or env:NAME
#PnpmRegistry: { host: *"registry.npmjs.org" | string, token: #SecretRef, scope?: string }
```

Invalid refs (anything not `op://…` / `env:…`) are rejected at validate time.

### Secrets resolver (`internal/engine/secrets`)
`Resolve` / `ResolveMap` turn refs into values: `op://…` via `op read --no-newline` (the 1Password
CLI), `env:NAME` from the launching environment. `OpAvailable` / `OpSignedIn` back `slop doctor`.
Errors are kept generic so `op`'s stderr can't echo a value; **resolved values are never logged.**

### pnpm staging (`internal/engine/creds`)
`StagePnpm` resolves each registry's token, renders a scoped `.npmrc`
(`//host/:_authToken=…`, plus `@scope:registry=…` when set), writes it `0600` into the stage,
and returns `NPM_CONFIG_USERCONFIG=<stage>/.npmrc` so npm/pnpm pick it up — the user's real
`~/.npmrc` is untouched.

### Run lifecycle (`internal/cli`)
`slop run` now: resolves `secrets` into the child env, stages the pnpm `.npmrc` under
`<workspace>/.slop/runtime/<profile>/`, launches under the profile's environment, and **wipes
the stage on exit** (deferred before the process exits). `--dry-run` prints the secret/token
**refs** (never values) and the seatbelt profile. `slop doctor` gains a `1password-signedin`
line.

## Verification (macOS arm64, Go 1.26)

- `make check` (vet + gofmt + `go test ./...`) green; **24 tests across 7 packages.**
- New tests: secret-ref validation; `env:` resolution; pnpm `.npmrc` rendering + `0600`
  staging; and an end-to-end `runProfile` test that confirms the secret is injected into the
  child env, `NPM_CONFIG_USERCONFIG` points at a staged `.npmrc`, and the stage is wiped on
  exit. All hermetic — `env:` refs only, **no live `op` or registry calls** (repo rule).
- Smoke: `slop run work --dry-run` shows `op://…` refs (not values); `slop doctor` reports
  `op` present + signed-in state.

## Deliberately deferred

- **gh / forgejo ephemeral-key providers** — a faithful port of the existing fish
  `slop-gh-key`/`slop-forgejo-key` logic (ssh-keygen + GitHub/Forgejo APIs). Lower novelty,
  needs live APIs; next within the credentials track.
- **1Password SSH agent** path (`SSH_AUTH_SOCK` → op agent socket, bind-mounted into the
  container) — lands with SP3 (container), where the bind-mount applies.
- **npm granular-token minting via API** (`npm token create` + revoke-on-exit) — the
  ephemeral-token variant; the SP2 path sources an existing token from 1Password/env, which is
  the simpler, secure default.

## Usage

```cue
work: {
  agent: "shell", network: "allow"
  secrets: {ANTHROPIC_API_KEY: "op://Private/Anthropic/credential"}
  credentials: pnpm: [{host: "npm.pkg.github.com", token: "op://Private/GH Packages/token", scope: "@myorg"}]
}
```

```
slop run work --dry-run   # show what would be staged (refs only)
slop run work             # resolve, stage .npmrc + inject env, launch, wipe on exit
```
