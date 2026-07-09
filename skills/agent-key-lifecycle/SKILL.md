---
name: agent-key-lifecycle
description: >
  Use safeslop's Go credential providers to stage short-lived GitHub/Forgejo
  credentials for agent sessions and verify cleanup behavior.
---

# Agent Key Lifecycle Skill

Use this skill when a task touches GitHub or Forgejo credentials for agent
sessions.

## Required pre-read

1. `CONTRIBUTING.md`
2. `AGENTS.md`
3. `README.md`
4. Relevant specs under `specs/`

## Current command surface

Credential staging is driven by `safeslop run <profile>` from the profile's
`credentials:` block. There is still no standalone credential mint UI: live
GitHub/Forgejo credentials are created only for a run/session and are revoked or
wiped by teardown.

Forge account links are managed out of band with `safeslop creds
link|unlink|status`: they live in `~/.config/safeslop/accounts.cue` (0600,
host-only) and hold non-secret ids + secret *refs* only. `link` probes the forge
(no token minted) and never prompts for a password/OTP; `creds status --output
json` is the UI contract and returns `data.links` rows with forge, host, owner,
non-secret ids, value-free probe class, SSH port, and TTL model only. The legacy
human `status` and raw `--json` forms remain for compatibility.

Profile forge scopes can be authored without hand-editing CUE through
`safeslop profile credentials set|clear ... --output json`. `set` writes either
`credentials.github` (GitHub App mode only; PAT fallback remains manual) or
`credentials.forgejo`, preserves other credential providers (`pnpm`/`aws`/`gcp`/
`kube`) and `secrets`, and clears only the opposite forge because staging
supports one forge per profile. `--use-origin` keeps runtime origin inference;
`--repo` and `--write-repo` declare explicit read/write `owner/repo` rows.

Read-only posture inspection exists (specs/0067): `safeslop creds list
[safeslop.cue] --output json` and `safeslop creds show <profile> --output json`
enumerate every declared secret/credential across profiles with a value-free
**readiness status** (does its `op://`/`env:` ref resolve now? is the key
`ephemeral` or the cloud auth `ambient`?). The probe resolves each ref only to
keep the pass/fail result and discards the value — no secret is read into the
output. This is surfaced in Emacs as the Credentials surface (`C-c s K`), which
also has `a` account link, `u` unlink, and `p` repo picker actions. It never
reveals values and never mints/revokes; staging stays at run time and revocation
at `session stop`.

For Emacs-driven sessions, `safeslop session stop --session-id <id>
--revoke-credentials` revokes ephemeral credentials before forcing process
termination and is idempotent (a second stop neither revokes nor kills again).
Stop reconciles the recorded PID/process identity before signalling, so a reused
detached supervisor PGID is not targeted. Revocation stays best-effort; the
decay-first guarantee remains the local wipe of staged private keys: stop,
status/list reconcile, remove, and prune all wipe the reconstructed host stage
dir.

## GitHub

First link the GitHub App installation (once per owner; stores ids + a key ref,
never the key value):

```
safeslop creds link github --app-id N --installation-id N --key-ref op://Vault/gh-app/private-key
```

Then declare `credentials.github` manually or via the repo picker / CLI:

```cue
credentials: github: {
	repos: [{repo: "owner/web"}, {repo: "owner/api", write: true}]
}
```

Omit `repos` to infer a single repository from the current `origin` remote, or
use `safeslop profile credentials set <profile> --provider github --repo owner/web
--write-repo owner/api --output json`. The inferred owner/repo components — and
any declared `repos` entries — must match `[A-Za-z0-9._-]+` before safeslop stages
git config. In the default `app` mode safeslop mints an ephemeral, repo-scoped App
installation token per owner (partitioned by `write`) and stages it over HTTPS —
no deploy keys, no `gh` CLI. An owner with no account link is a hard error. The
P1 token lifetime is ~1h with no renewal (renewal is P2). Live GitHub repository
discovery is deferred because listing installation repositories would require a
minted installation token outside the session-owned lifecycle.

PAT fallback (an existing fine-grained token, staged in a wipe-on-exit file, not
embedded in git config or the environment):

```cue
credentials: github: {
	mode: "pat"
	pat:  "env:GITHUB_FINE_GRAINED_PAT"
	repos: [{repo: "owner/web"}, {repo: "owner/api"}]
}
```

## Forgejo/Gitea

First link the Forgejo account token (stores the token ref, never the value):

```
safeslop creds link forgejo --host forgejo.example.com --owner owner --token-ref op://Vault/forgejo/token
```

Then declare `credentials.forgejo` manually or via the repo picker / CLI (deploy
keys; the registration token comes from the account link, not from
`safeslop.cue`):

```cue
credentials: forgejo: {
	url:        "https://forgejo.example.com"
	"ssh-port": 2222
	repos:      [{repo: "owner/web"}]
}
```

`safeslop profile credentials set <profile> --provider forgejo --url
https://forgejo.example.com --repo owner/web --write-repo owner/api --output json`
performs the same CUE mutation and requires `--url` for explicit repos. safeslop
mints one deploy key per repo and stages per-repo SSH aliases plus git URL
rewrites. Origin-inferred and declared owner/repo components must match
`[A-Za-z0-9._-]+`; malformed remotes fail closed before `.gitconfig` or
`.ssh/config` is rendered. Each declared owner needs a forgejo account link;
Forgejo tokens are account-wide, so prefer a dedicated bot account. Live Forgejo
repository discovery is deferred because it would use the account-wide token.

## Safety checklist

- Prefer minted App tokens (GitHub) and dedicated bot accounts (Forgejo) over personal PATs.
- Keep write access rare and profile-specific.
- Keep credentialed profiles on `network: "deny"` or a constrained container/VM path.
- Never commit token values; use `env:` or `op://` secret refs.
- Verify cleanup by checking staged runtime directories are wiped on stop/reconcile/rm/prune and deploy-key revocation ran best-effort when requested.

## Verification

Run focused tests after credential changes:

```bash
go test ./internal/engine/creds/ -v
go test ./internal/cli/ -run 'Creds(Status|Link|Unlink)|ProfileCredentials|StageProfile' -v
make check
```
