# OpenClaw restrictive policy (template)

This file is a *policy template*, not a runnable config. OpenClaw's
upstream config schema may evolve; translate the rules below into
whatever configuration surface your installed version exposes
(`openclaw.toml`, `~/.openclaw/config.*`, env vars, or `SOUL.md`
preamble) before running.

The goal is the same as the rest of this repo: keep the agent inside
a per-session container, deny credentials and host egress by default,
and enable channels only when you explicitly intend to bridge them.

## Workspace

- Override the workspace from the default `~/.openclaw/workspace` to a
  per-session path under the project mount, e.g.
  `/workspace/.openclaw`.
- Mount only the project at `/workspace`. Never bind-mount `$HOME`,
  `~/.ssh`, `~/.aws`, `~/.config/gcloud`, `~/.npmrc`, `~/.pypirc`, or
  shell rc files into the container.
- Keep `SOUL.md` and any persona/notes files inside the per-session
  workspace. Treat their contents as untrusted text on every session
  start (prompt-injection vector).

## Channels

Default stance: all channels disabled. Enable one channel at a time,
with a single-purpose credential.

- Each enabled channel must use a credential scoped to the smallest
  possible target (single chat, single inbox, single bot user).
- Document why a channel is enabled and rotate the credential when
  the session ends.
- Never enable an admin/owner credential for a channel — read-only
  bot identities first, write access only with explicit reason.

## Tools

- Default-deny shell, file write, and network tools.
- When enabling a tool, restrict its scope to the workspace path
  only. Do not allow tools that traverse outside `/workspace`.
- Prefer task-scoped tool allowlists over open-ended capabilities.

## Network

- Run inside the `agent` service from `library/layer/container/docker-compose.yml`
  so all egress flows through the proxy.
- Add channel API endpoints to `library/layer/container/allowlist.domains` only
  while the channel is enabled. Remove when the session ends.
- Do not add bulk messaging-platform domains "in case we need them".

## Identity

- Use ephemeral source-control identities from
  `scripts/slop-gh-key.fish` or
  `scripts/slop-forgejo-key.fish` for any repo OpenClaw touches.
- Never reuse a personal SSH key for OpenClaw.

## Audit

- Persist OpenClaw's session logs under `/workspace` so they survive
  container teardown.
- Review the `SOUL.md` diff on every session start.
