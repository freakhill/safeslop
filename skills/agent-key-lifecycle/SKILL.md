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
`credentials:` block. Manual key management commands were intentionally dropped;
a future `safeslop creds gc` sweep is the only planned manual credential command.

For Emacs-driven sessions, `safeslop session stop --session-id <id>
--revoke-credentials` revokes ephemeral credentials before forcing process
termination and is idempotent (a second stop neither revokes nor kills again).
Revocation stays best-effort; the decay-first guarantee remains the wipe of
staged private keys.

## GitHub

Use `credentials.ssh` for GitHub:

```cue
credentials: ssh: {
	mode: "deploy-key"
	repos: [{repo: "owner/web"}, {repo: "owner/api", write: true}]
}
```

Omit `repos` to infer a single repository from the current `origin` remote.
When `repos` is present, safeslop mints one deploy key per repo and stages
per-repo SSH aliases plus git URL rewrites.

PAT opt-in:

```cue
credentials: ssh: {
	mode: "pat"
	pat:  "env:GITHUB_FINE_GRAINED_PAT"
	repos: [{repo: "owner/web"}, {repo: "owner/api"}]
}
```

PAT values are staged in a wipe-on-exit file, not embedded in git config or
process environment. safeslop does not mint or revoke account PATs.

## Forgejo/Gitea

Use `credentials.forgejo`:

```cue
credentials: forgejo: {
	mode:       "deploy-key"
	url:        "https://forgejo.example.com"
	token:      "env:FORGEJO_ADMIN_TOKEN"
	"ssh-port": 2222
	repos:      [{repo: "owner/web"}]
}
```

PAT opt-in uses `mode: "pat"`, `pat: <secret-ref>`, `url`, and explicit `repos`.

## Safety checklist

- Prefer deploy keys over account PATs.
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
