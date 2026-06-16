# ZeroClaw restrictive policy (template)

This file is a *policy template*, not a runnable config. ZeroClaw's
upstream config schema may evolve; translate the rules below into
whatever configuration surface your installed version exposes
(`zeroclaw.toml`, env vars, CLI flags) before running.

ZeroClaw already ships several security-relevant defaults (workspace
boundary, supervised autonomy, OS sandbox layer, cryptographic tool
receipts). This policy keeps them on and adds the container-level
controls used elsewhere in this repo.

## Distribution

- Pin the binary by SHA-256, not by the `latest` tag. Verify the
  digest before running.
- Run inside the `agent` service from
  `library/layer/container/docker-compose.yml`. Do not run on the host even though
  the binary has its own sandbox layer — on macOS that layer reduces
  to Seatbelt and is defense-in-depth only.

## Workspace

- Set the workspace root to `/workspace`. Do not allow ZeroClaw to
  read or write outside that path.
- Mount only the project at `/workspace`. Never bind-mount `$HOME`,
  credential directories, or shell rc files into the container.

## Autonomy

- Keep supervised autonomy at the default threshold:
  - low-risk: auto-approve
  - medium-risk: requires approval
  - high-risk: blocked
- Do not raise the auto-approve threshold for the agent identity.
- Do not disable approval prompts for "trusted" tools — trust is
  per-session, not per-tool.

## Tools

- Default-deny shell, browser, and arbitrary-HTTP tools. Enable on
  demand, scoped to the workspace.
- When enabling the shell tool, confirm the container's `HTTP_PROXY`
  and `HTTPS_PROXY` are set so any subprocess inherits the proxy
  allowlist.
- Pin every MCP / custom tool by its source revision. Treat tool
  upgrades as a code change.

## Receipts and audit

- Persist tool receipts to a path under `/workspace` so the audit
  log survives container teardown.
- Verify receipt signatures when reviewing past sessions.

## Network

- Egress flows through the proxy in `library/layer/container/docker-compose.yml`.
- Add LLM provider hosts and any required tool endpoints to
  `library/layer/container/allowlist.domains`. Remove them when the session ends.
- Do not add channel/messaging hosts unless the corresponding
  channel is currently enabled.

## Identity

- Use ephemeral source-control identities from
  `scripts/slop-gh-key.fish` or
  `scripts/slop-forgejo-key.fish` for any repo ZeroClaw touches.
- Never reuse a personal SSH key for ZeroClaw.
