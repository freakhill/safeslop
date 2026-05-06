---
name: agent-sandbox-ops
description: >
  Operate the local sandbox toolchain safely: Docker sandbox, Tart brew VM,
  network-limiting checks, and explicit host file sharing.
---

# Agent Sandbox Ops Skill

Use this skill whenever tasks involve runtime isolation, network limiting, or file transfer between host and sandboxed runtimes.

## Required pre-read

Before executing this skill, read:

1. `CONTRIBUTING.md`
2. `agents.md`
3. `scripts/CONVENTIONS.md`
4. `README.md`

## Command map

- Hub: `scripts/slop-sandboxctl.fish`
- Shim installer: `scripts/slop-install.fish`
- Docker runtime: `scripts/slop-agent-sandbox.fish`, `scripts/slop-agent-sandbox-tools.fish`
- Optional local runtime: `scripts/slop-macos-sandbox.fish` (`slop-sandboxctl local ...`)
- VM runtime: `scripts/slop-brew-vm.fish`

## Default policy

1. Prefer `strict-egress` network policy.
2. Keep domain access allowlisted via `library/layer/container/allowlist.domains`.
3. Use explicit file transfer for VM (`copy-in`, `copy-out`) and avoid secret transfer.

## Per-framework restrictive policies

When operating one of the supported agent runtimes, apply the matching policy template before launch:

- Claude Code → `library/layer/policy/claude-code.settings.json`
- OpenCode → `library/layer/policy/opencode.restrictive.json`
- OpenClaw → `library/task/restrictive-flows/openclaw.md` (channels disabled by default; workspace overridden away from `~/.openclaw`; `SOUL.md` treated as untrusted input)
- ZeroClaw → `library/task/restrictive-flows/zeroclaw.md` (workspace boundary, supervised autonomy, signed tool receipts kept on; OS sandbox layer is defense-in-depth — still run inside the `agent` container)

## Workflows

### Docker sandbox workflow

1. `scripts/slop-sandboxctl.fish docker up`
2. `scripts/slop-sandboxctl.fish docker shell`
3. Verify non-allowlisted egress is blocked from agent runtime.
4. `scripts/slop-sandboxctl.fish docker down`

### Optional local macOS sandbox workflow

1. `source scripts/slop-macos-sandbox.fish`
2. `slop-macos-sandbox run -- /bin/pwd`
3. For repo-wide access, use `--repo-root-access` (alias of `--path-scope repo-root`).
4. Prefer Docker/VM for untrusted code paths; use local sandbox as defense-in-depth only.

### Brew VM workflow

1. `source scripts/slop-brew-vm.fish`
2. `set -x BREW_VM_PROXY_URL http://<proxy-host>:3128`
3. `slop-brew-vm create-base`
4. `slop-brew-vm install --network-policy strict-egress <formula>`
5. `slop-brew-vm verify-network`

### File sharing workflow

- Docker: use `/workspace` mount.
- VM: use `slop-brew-vm copy-in <host-path> <guest-path>` and `slop-brew-vm copy-out <guest-path> <host-path>`.
- Recommended guest temp path: `/tmp/llm-share`.

## Safety checklist

- Do not mount host credential directories into containers/VMs.
- Do not disable strict egress for untrusted installs.
- Document every allowlist expansion with reason.

## Sync requirements after changes

If you change sandbox scripts or defaults, update in the same task:

- `README.md`
- this skill file
- any other affected skill under `skills/*/SKILL.md`
- `skills/README.md` when installation/usage guidance changes
