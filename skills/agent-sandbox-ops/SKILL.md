---
name: agent-sandbox-ops
description: >
  Operate safeslop isolation profiles safely: host, sandbox, container, and VM.
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
- `safeslop trust` — approve a policy's exact bytes for launch.
- `safeslop run <profile>` — launch a trusted profile.
- `safeslop session create --agent <pi|claude> --workspace <dir> --output json` — create an Emacs-visible safe-default session record.
- `safeslop session run --session-id <id>` — run the session agent under safeslop isolation.
- `safeslop session status --session-id <id> --output <json|jsonl>` — inspect or monitor session state.
- `safeslop session stop --session-id <id> --revoke-credentials --output json` — stop idempotently, revoking ephemeral credentials before process termination.
- `safeslop doctor` — report available tools and isolation tiers.
- `safeslop down` — tear down container/VM sessions.

## Default policy

- Prefer `environment: "sandbox"` and `network: "deny"` for everyday agent work.
- Use `environment: "container"` when per-domain egress control matters.
- Use `environment: "vm"` for untrusted code or maximum isolation.
- Do not mount or expose host credential directories to agents.

## Common workflows

### Inspect a profile

```bash
safeslop validate
safeslop list
safeslop run review --dry-run
```

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
	agent:       "shell"
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
- Run `safeslop down` to clean up container/VM sessions after interrupted work.

## Verification

```bash
go test ./internal/engine/container/ ./internal/engine/vm/ ./internal/engine/sandbox/ -v
make check
```
