# CLAUDE.md

Per-repo guidance for Claude Code and other coding agents working in `safeslop`.

## Read before editing

- `AGENTS.md` — repository operating contract and done checklist.
- `CONTRIBUTING.md` — contributor expectations.
- `README.md` — user-facing reference.
- Relevant `specs/` files — design records and implementation plans.

## Stack at a glance

`safeslop` is a single Go CLI and engine:

- CLI entrypoint: `cmd/safeslop`
- Engine packages: `internal/engine/*`
- CLI command tree: `internal/cli`
- Embedded policy schema: `internal/engine/policy/schema/schema.cue`
- Container/VM assets: `library/layer/container` plus embedded copies under
  `internal/engine/container/assets`

The deleted legacy toolkit is no longer a runtime dependency. Do not reintroduce
shell/Python wrappers for command behavior that belongs in the Go engine.

## Verify your changes

Always run before declaring work complete:

```bash
make check
make build
```

`make check` runs asset sync checks, `go vet`, `gofmt` verification, and
`go test ./...`. It is the local mirror of the Go CI gate.

For targeted changes, also run focused tests, for example:

```bash
go test ./internal/engine/creds/ -run PAT -v
go test ./internal/engine/policy/ -run 'Pinned|Latest' -v
go test ./internal/cli/ -run SeedAgentDefaults -v
```

## Development happens on Forgejo

Active development is on the `forgejo` remote:

```text
ssh://git@forgejojo.lucyjojo.me:2222/jojo/safeslop.git
```

GitHub is a release mirror. Do not push to `origin` during normal development.
Use branches and Forgejo PRs.

Create PRs with:

```bash
tea pulls create --remote forgejo --head <branch> --base main --title "..." --description "$(cat body.md)"
```

Do not use `--repo` for this repo's Forgejo PR creation flow.

## Useful code locations

- `internal/cli/cli.go` — command tree and launch orchestration.
- `internal/engine/creds/` — staged credentials and revocation.
- `internal/engine/policy/` — CUE loading, schema, lint/risk/pinning checks.
- `internal/engine/container/` — container session materialization and compose assets.
- `internal/engine/vm/` — disposable VM launch and remote command assembly.
- `internal/engine/install/` and `internal/engine/uninstall/` — receipt-driven tool install lifecycle.

## Safety notes

- Never write secrets to disk except in explicitly staged runtime directories that
  are wiped on exit. Prefer short-lived tokens and deploy keys.
- Keep host ambient credentials out of child environments; profile-declared
  secrets/credentials are the only authority that should cross boundaries.
- Keep tests hermetic. Fake external tools and APIs.
- Preserve honest isolation labels: host, container, VM each have
  different security properties. (The macOS Seatbelt `sandbox` tier was removed in
  specs/0053 — `environment` is required, with no default.)
