# Agent Operating Contract

This file defines repository-level behavior for human and LLM agents.

## Mandatory Read Order

Before making changes, agents must read:

1. `CONTRIBUTING.md`
2. `README.md`
3. Relevant skill files in `skills/`
4. Relevant specs under `specs/`

## Required Behaviors

- Keep command UX and safety defaults consistent across the `safeslop` Go CLI.
- Treat network limiting as a first-class control; do not weaken defaults silently.
- Preserve explicit host file-sharing boundaries for container workflows.
- Use comment best practices: why-focused, concise, linked to official references where needed.
- New engine work goes in Go under `cmd/safeslop` and `internal/engine/*`.
- Keep engine tests hermetic: no live network or credential APIs in unit tests.
- Do not introduce runtime dependencies outside the signed Go binary unless a spec explicitly approves them.

## Docs and Tests Must Stay In Sync

Any behavior/interface change requires matching updates in:

- `README.md`
- Affected skill files under `skills/`
- Relevant tests under `internal/**` or `cmd/**`
- Relevant specs when a plan/checklist is being executed

## Done Checklist

1. CLI help paths are updated when command surfaces change.
2. Documentation examples still match real commands.
3. Skill workflows reflect current command behavior/defaults.
4. Test cases reflect new/changed argv handling, flags, or error paths.
5. `make check` passes.
6. `make build` passes.
