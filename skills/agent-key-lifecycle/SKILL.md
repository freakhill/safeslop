---
name: agent-key-lifecycle
description: >
  Use safeslop's Go credential providers to stage short-lived GitHub/Forgejo or
  access-only Pi OAuth credentials for agent sessions and verify cleanup behavior.
---

# Agent Key Lifecycle Skill

Use this skill when a task touches GitHub, Forgejo, or Pi OAuth credentials for
agent sessions.

## Required pre-read

1. `CONTRIBUTING.md`
2. `AGENTS.md`
3. `README.md`
4. Relevant specs under `specs/`

## Current command surface

Credential staging is driven by `safeslop run <profile>` from the profile's
`credentials:` block. There is still no standalone credential mint UI: live
GitHub/Forgejo credentials are created only for a run/session and are revoked or
wiped by teardown. Pi OAuth is different: safeslop snapshots an existing host
access bearer without refresh authority; teardown wipes local copies but cannot
revoke it at the issuer.

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
`kube`/`pi`) and `secrets`, and clears only the opposite forge because staging
supports one forge per profile. `--use-origin` keeps runtime origin inference;
`--repo` and `--write-repo` declare explicit read/write `owner/repo` rows.

Read-only posture inspection exists (specs/0067): `safeslop creds list
[safeslop.cue] --output json` and `safeslop creds show <profile> --output json`
enumerate every declared secret/credential across profiles with a value-free
**readiness status** (does its `op://`/`env:` ref resolve now? is the key
`ephemeral` or the cloud/Pi host auth `ambient`?). The probe resolves each ref only to
keep the pass/fail result and discards the value — no secret is read into the
output. This is surfaced in Emacs as the Credentials surface (`C-c s K`). Its universal
raw/Evil actions are `A` link account, `U` unlink account, `R` configure profile
repository scopes, and `X` clear only profile GitHub/Forgejo scopes (`g` refreshes
in raw Emacs, `gr` in Evil). First create/clone a project profile, then use
`A → R`; builtins are immutable. `R` sources candidates from `profile list` even
when no credential rows exist, preloads current value-free read/write scopes, and
confirms the complete replacement. Failed account/scope writes retain value-free
drafts for `K → A/R` retry. Scope changes modify policy bytes and require
review/re-trust. Account unlink warns that declared profiles will fail staging
until relinked or cleared; unlink and profile clear are deliberately separate. The surface never
reveals values and never mints/revokes; staging stays at run time and revocation
at `session stop`.

For Emacs-driven sessions, `safeslop session stop --session-id <id>
--revoke-credentials` revokes ephemeral credentials before forcing process
termination and is idempotent (a second stop neither revokes nor kills again).
Stop reconciles the recorded PID/process identity before signalling, so a reused
detached supervisor PGID is not targeted. Closing a coupled terminal sends
`SIGHUP` and triggers its deferred reap/wipe. Closing an attach buffer merely
disconnects from a detached supervisor; use explicit stop for its bounded
`SIGTERM`→`SIGKILL`, label reap, socket removal, and stage wipe. Revocation stays
best-effort; the decay-first guarantee remains the local wipe of
staged private keys: stop, status/list reconcile, remove, and prune all wipe the
reconstructed host stage dir. For Pi OAuth, `--revoke-credentials` means the same
local wipe only; the access bearer remains valid until upstream expiry. Legacy
session records without layout/runtime fields keep workspace-hashed stage
reconstruction and historical-backend normalization so cleanup remains possible;
corrupt records, stale mutations, and commit uncertainty are reported as typed
value-free errors rather than absence or success.

## Pi OAuth (access-only MVP)

Pi OAuth requires an explicit, exact-byte-trusted **project** profile; builtins
never carry it. The only accepted MVP declaration is Pi + container + deny with
the literal provider/model pair:

```cue
profiles: luna: {
	agent:       "pi"
	environment: "container"
	network:     "deny"
	credentials: pi: {
		provider: "openai-codex"
		model:    "gpt-5.6-luna"
	}
}
```

Review the complete policy and run `safeslop trust`. At each new launch,
safeslop safely reads only the default host `~/.pi/agent/auth.json`. Relative
same-HOME links and exact absolute same-HOME descendant links are accepted at the
fixed components or final leaf. HOME and every reached directory must be owned by
the current user with `mode & 0022 == 0` (`0755` is valid); the ultimate leaf is
still exact regular `0600`, single-link, bounded, and on the same mount. Safeslop
checks the lexical sibling lock before/after the descriptor read and requires a
matching fresh full proof. Outside/ambiguous/dangling links, writable or
wrong-owner ancestry, mount crossings, and races remain unsafe. It rejects a
busy/unsafe/malformed source and requires **more than 15 minutes** of access
lifetime. It stages only a synthetic `type:api_key` entry in the container tmpfs;
refresh, account metadata, other providers, exact expiry, source path, and bearer
never enter public output. There is no broker, listener, startup injection,
refresh, or renewal. If the Pi lock remains busy, let host Pi finish or run:

```bash
pi --list-models gpt-5.6-luna
```

The access token still has **provider-default replay authority**. Selecting Luna
and allowing one destination are workflow controls, not cryptographic token
downscoping. `chatgpt.com` is deliberately absent from static egress; review the
denied observation, then grant/revoke only the current session:

```bash
safeslop session egress grant --session-id ID --host chatgpt.com --port 443 --output json
safeslop session egress revoke --session-id ID --grant-id G --output json
```

On a running session, a grant that adds a session-grant row or a revoke that
removes one replaces the proxy and succeeds only after an exact generation,
revision, and overlay-hash ACK;
replacement is required so revocation closes old tunnels. Created sessions persist
changes for launch without a live replacement. If network authority cannot be
proven, safeslop attempts full teardown
and records `network_authority_uncertain`. If teardown is not proven, egress
mutations remain blocked until explicit stop/reap; start a fresh session afterward.

Stop, reconcile, remove, and prune recursively delete host-stage and tmpfs copies.
That is local wipe, not issuer revocation; start a new session after expiry or host
refresh. Emacs has no Pi OAuth mutation action in this MVP: edit CUE, review, and
re-trust.

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
no deploy keys, no `gh` CLI. An owner with no account link is a hard error. A
**host-owned** lease renews complete App-token batches atomically; the container
has file access only and cannot mint, renew, or revoke. `ttl` defaults to `"1h"`;
a positive Go duration is a run-relative horizon for future staging/renewal, while
explicit `""` lasts to normal teardown without retroactively invalidating an
issued token. Live GitHub repository discovery remains deferred because listing
installation repositories would require a minted installation token outside the
session-owned lifecycle.

Opt into GitHub API staging only for App mode with unique `permission:read` or
`permission:write` declarations. One partition receives
`SAFESLOP_GITHUB_TOKEN_FILE`; multiple partitions receive
`SAFESLOP_GITHUB_TOKEN_DIR` and `SAFESLOP_GITHUB_TOKEN_MANIFEST`. Do not use a
copied `GITHUB_TOKEN`: safeslop deliberately does not inject it because it would
be stale after host renewal. `api.github.com` is added to deny-tier egress only
for this opt-in.

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
Forgejo tokens are account-wide, so prefer a dedicated bot account. `ttl` has the
same default/horizon semantics as GitHub; at a bounded horizon safeslop removes an
opted-in API token file and attempts best-effort deploy-key cleanup. To enable the
file-only API token (`SAFESLOP_FORGEJO_TOKEN_FILE`), declare
`api.enabled: true` and `api.ackAccountWide: true` with an HTTPS/default-443 URL.
The scope remains operator-provisioned, unverified, and may be account-wide; that
exact API hostname is added to deny-tier egress only for this opt-in. Live Forgejo
repository discovery is deferred because it would use the account-wide token.

### Narrow deploy-key GC

`creds gc` is not a discovery or broad cleanup interface. Supply every target and
use `--yes` only after reviewing its default dry-run output:

```bash
safeslop creds gc --host forgejo.example.com --repo owner/web --dry-run --output json
safeslop creds gc --host forgejo.example.com --repo owner/web --yes --output json
```

It resolves only host/owner-matching links in host memory; discovers all requested
repos before deletion; selects only exact `safeslop-<owner>-<repo>` titles; and
rechecks each candidate. HTTP 404 is already absent. It never deletes an
unrequested repo or a merely similar title.

## Safety checklist

- Prefer minted App tokens (GitHub) and dedicated bot accounts (Forgejo) over personal PATs.
- Keep write access rare and profile-specific.
- Keep credentialed profiles on `network: "deny"` or a constrained container path.
- Never commit token values; use `env:` or `op://` secret refs, or the literal value-free Pi OAuth opt-in.
- Treat Pi model/egress selection as workflow constraints, not bearer downscoping.
- Verify cleanup by checking staged runtime directories are wiped on stop/reconcile/rm/prune and deploy-key revocation ran best-effort when requested.

## Verification

Run focused tests after credential changes:

```bash
go test ./internal/engine/creds/ -v
go test ./internal/cli/ -run 'Creds(Status|Link|Unlink|GC)|ProfileCredentials|StageProfile|PiOAuth' -v
make check
```
