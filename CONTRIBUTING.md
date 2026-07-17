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
  authority. `environment` is required (host/container) — there is no default
  tier (specs/0053 and later container-only cleanup removed historical tiers).

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

`make check` includes asset/catalog drift checks, npm package-lock/SRI and
proxy-image lock checks, active-surface drift, host-helper and hostpath denylist
gates, `go vet`, `gofmt` verification, `go test ./...`, and strict Emacs tests.
Container-image work must also run `make test-container-images`; progressive
egress work must run the opt-in Docker gate `make test-progressive-egress-smoke`.
For targeted work, also run the narrower package tests that prove the changed
behavior.

## Network and File-Sharing Guardrails

- Keep deny-by-default egress and explicit allowlists.
- Do not broaden allowlist domains without rationale.
- Keep the workspace boundary policy-relative, canonical, existing, and separate
  from the private runtime stage; exactly one read-write host bind is allowed.
- Accept hostile-but-valid path text through typed Compose/YAML quoting, but reject
  controls/format characters, non-directories, missing paths, and workspace-stage
  overlap.
- Never expose host credential directories to containers.
- Keep staged credentials short-lived, scoped, value-free in public output, and
  wiped on exit or session cleanup.
