---
name: agent-sandbox-ops
description: >
  Operate safeslop isolation profiles safely: host, container, and VM.
---

# Agent Sandbox Ops Skill

Use this skill whenever tasks involve runtime isolation, network limiting, or
file transfer between host and sandboxed runtimes.

## Required pre-read

1. `CONTRIBUTING.md`
2. `AGENTS.md`
3. `README.md`
4. Relevant specs under `specs/`

## Command map

- `safeslop validate` — validate a policy against the embedded schema.
- `safeslop list` — list available profiles.
- `safeslop catalog list [--bundles] --output json` — list curated package catalog entries/bundles for profile creation UIs.
- `safeslop profile create --name N --agent A --environment E [--bundle B] [--package P] --output json` — create or update a `safeslop.cue` profile.
- `safeslop profile show <name> --output json` — inspect a profile with resolved package set and dry-run image recipe.
- `safeslop lock [profile] --output json` — write repo-root `safeslop.lock.json` for the selected profile's recipe identity.
- `safeslop trust` — approve a policy's exact bytes for launch.
- `safeslop run <profile>` — launch a trusted profile.
- `safeslop session create --profile <name> --output json` — create an Emacs-visible session from an existing profile; the record includes resolved recipe/image metadata for the portal, and the Emacs client streams slow first-use image-build output into `*safeslop session progress*` with the final exit status.
- `safeslop session create --agent <claude|pi|fish|zsh> --environment <host|container> --workspace <dir> --output json` — create an ad-hoc Emacs-visible session record (`--environment` is required). `claude-code` remains accepted as a compatibility alias for `claude` but is not advertised in new UI/docs.
- `safeslop session run --session-id <id> [--detach]` — run the session agent under safeslop isolation. Coupled (default) needs a controlling terminal (Emacs supplies one via `make-term`); with no usable TTY it emits the `PTY_UNAVAILABLE` contract error and the caller switches to the `--output jsonl` status monitor. `--detach` instead launches a per-session supervisor that owns the agent + its PTY, serves it over a per-session unix socket, and returns immediately (the buffer is freed).
- `safeslop session attach --session-id <id>` — rejoin a detached session's agent over its socket under a controlling terminal, exiting with the agent's code; one active attach at a time. No usable TTY emits `PTY_UNAVAILABLE`.
- `safeslop session status --session-id <id> --output <json|jsonl>` — inspect or monitor session state; a running detached session also reports its `socket`.
- `safeslop session stop --session-id <id> --revoke-credentials --output json` — stop idempotently, revoking ephemeral credentials before terminating the process (a detached supervisor's whole process group), and removing the socket.
- `safeslop doctor` — report available tools and isolation tiers.
- `safeslop down` — tear down safeslop-managed host-container stacks by label (the Lima VM backend is torn down through its own runtime path).
- `safeslop gc [--until <age>] [--keep <N>]` — remove only unreferenced safeslop-managed images; current resolving profiles, the repo lockfile, and live sessions anchor images.

## Default policy

- `environment` is required (`host` | `container` | `vm`) — there is no default
  tier (specs/0053 removed the macOS Seatbelt `sandbox` tier).
- Prefer `environment: "container"` with `network: "deny"` for everyday agent work:
  network-bound agents (claude, pi) need their runtime + egress inside the boundary.
- Use `environment: "vm"` for untrusted code or maximum isolation.
- Use `environment: "host"` only when you accept no isolation.
- Do not mount or expose host credential directories to agents.

## Common workflows

### Create and inspect a profile

```bash
safeslop catalog list --bundles --output json
safeslop profile create --name review --agent claude --environment container --network deny --output json
safeslop profile show review --output json
safeslop lock review --output json
safeslop session create --profile review --output json
safeslop validate
safeslop list
safeslop run review --dry-run
```

In Emacs, `C-c s F` opens the Profiles surface. Use `RET`/`i` to inspect a
profile's resolved packages/egress/recipe, `e` to edit the CUE at that profile's
block, `n` to create, `c` to clone, `d` for guided manual deletion, `S` to sort,
and `g` to refresh.

### Trust and launch

```bash
safeslop trust
safeslop run review
```

### Container profile

```cue
profiles: container_review: {
	agent:       "claude"
	environment: "container"
	network:     "deny"
	egress:      [".internal.example.com"]
}
```

The container tier enforces egress by topology: the agent sits on an internal
network and reaches HTTP(S) through the proxy allowlist.

### VM profile

```cue
profiles: vm_review: {
	agent:       "pi"
	environment: "vm"
	network:     "deny"
}
```

The VM tier is disposable. Use explicit staged state and copy boundaries; do not
rely on broad host mounts.

## Safety checklist

- Keep network allowlists narrow and documented.
- Prefer read-only credentials; use write credentials only for explicit workflows.
- Verify `safeslop doctor` output before depending on a tier.
- Run `safeslop down` to clean up safeslop-managed host-container stacks after interrupted work.
- Run `safeslop gc --keep 2` only when you want to reclaim unreferenced managed images; it preserves profile/lock/live-session anchors.

## Verification

```bash
go test ./internal/engine/container/ ./internal/engine/vm/ -v
make check
```
