# Contributing

Thanks for contributing.

## First Read

Before editing code or docs, read:

1. `AGENTS.md`
2. `README.md`
3. Relevant `specs/` files for the area you are changing
4. Relevant skill files in `skills/`

## Go Engine

`safeslop` is a single signed Go binary. Engine and CLI code live in
`cmd/safeslop` and `internal/engine/*`; the policy schema is embedded through
Go. There is no external policy compiler required at runtime.

- Format with `gofmt`.
- Keep `go vet ./...` clean.
- Put tests next to the code and keep them hermetic.
- Do not call live forges, credential providers, registries, or cloud APIs from
  unit tests. Use fakes and local HTTP test servers.
- Preserve safe defaults: `network: "deny"` unless a policy opts into more
  authority. `environment` is required (host/container/vm) — there is no default
  tier (specs/0053).

## Docs, Skills, and Tests Sync Policy

When command behavior, policy schema, defaults, or safety guarantees change,
update all relevant docs and tests in the same change:

- `README.md`
- Related skill files under `skills/`
- Go tests for changed behavior or error paths
- Specs/checklists when executing a written plan

## Verification

Run at least:

```bash
make check
make build
```

For targeted work, also run the narrower package tests that prove the changed
behavior.

## Network and File-Sharing Guardrails

- Keep deny-by-default egress and explicit allowlists.
- Do not broaden allowlist domains without rationale.
- For VM paths, prefer explicit copy-in/copy-out behavior over broad host mounts.
- Never expose host credential directories to containers or VMs.
- Keep staged credentials short-lived, scoped, and wiped on exit.
