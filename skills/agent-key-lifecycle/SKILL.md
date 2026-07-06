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
`credentials:` block. Mutating key management commands were intentionally dropped;
a future `safeslop creds gc` sweep is the only planned *mutating* credential
command.

Forge account links are managed out of band with `safeslop creds
link|unlink|status`: they live in `~/.config/safeslop/accounts.cue` (0600,
host-only) and hold non-secret ids + secret *refs* only. `link` probes the forge
(no token minted) and never prompts for a password/OTP; `status [--json]` shows a
value-free probe result + TTL model per link.

Read-only posture inspection exists (specs/0067): `safeslop creds list
[safeslop.cue] --output json` and `safeslop creds show <profile> --output json`
enumerate every declared secret/credential across profiles with a value-free
**readiness status** (does its `op://`/`env:` ref resolve now? is the key
`ephemeral` or the cloud auth `ambient`?). The probe resolves each ref only to
keep the pass/fail result and discards the value — no secret is read into the
output. This is surfaced in Emacs as the Credentials surface (`C-c s K`). It
never reveals values and never mints/revokes; staging stays at run time and
revocation at `session stop`.

For Emacs-driven sessions, `safeslop session stop --session-id <id>
--revoke-credentials` revokes ephemeral credentials before forcing process
termination and is idempotent (a second stop neither revokes nor kills again).
Revocation stays best-effort; the decay-first guarantee remains the wipe of
staged private keys.

## GitHub

First link the GitHub App installation (once per owner; stores ids + a key ref,
never the key value):

```
safeslop creds link github --app-id N --installation-id N --key-ref op://Vault/gh-app/private-key
```

Then declare `credentials.github`:

```cue
credentials: github: {
	repos: [{repo: "owner/web"}, {repo: "owner/api", write: true}]
}
```

Omit `repos` to infer a single repository from the current `origin` remote. The
inferred owner/repo components — and any declared `repos` entries — must match
`[A-Za-z0-9._-]+` before safeslop stages git config. In the default `app` mode
safeslop mints an ephemeral, repo-scoped App installation token per owner
(partitioned by `write`) and stages it over HTTPS — no deploy keys, no `gh` CLI.
An owner with no account link is a hard error. The P1 token lifetime is ~1h with
no renewal (renewal is P2).

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

Then declare `credentials.forgejo` (deploy keys; the registration token comes
from the account link, not from `safeslop.cue`):

```cue
credentials: forgejo: {
	url:        "https://forgejo.example.com"
	"ssh-port": 2222
	repos:      [{repo: "owner/web"}]
}
```

safeslop mints one deploy key per repo and stages per-repo SSH aliases plus git
URL rewrites. Origin-inferred and declared owner/repo components must match
`[A-Za-z0-9._-]+`; malformed remotes fail closed before `.gitconfig` or
`.ssh/config` is rendered. Each declared owner needs a forgejo account link;
Forgejo tokens are account-wide, so prefer a dedicated bot account.

## Safety checklist

- Prefer minted App tokens (GitHub) and dedicated bot accounts (Forgejo) over personal PATs.
- Keep write access rare and profile-specific.
- Keep credentialed profiles on `network: "deny"` or a constrained container/VM path.
- Never commit token values; use `env:` or `op://` secret refs.
- Verify cleanup by checking staged runtime directories are wiped and deploy-key revocation ran best-effort.

## Verification

Run focused tests after credential changes:

```bash
go test ./internal/engine/creds/ -v
go test ./internal/cli/ -run StageProfile -v
make check
```
