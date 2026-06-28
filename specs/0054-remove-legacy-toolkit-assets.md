# 0054 — Remove the legacy toolkit assets under `library/`

Status: accepted (2026-06-28)

## Why

The mid-2026 rewrite moved all runtime behavior into the `safeslop` Go binary
(specs/0001). The `library/` tree is a remnant of the former fish/Python toolkit.
Only `library/layer/container/` is still live — `make sync-container-assets` copies
it into `internal/engine/container/assets` and `make check` verifies the sync. The
rest (`layer/policy/**` `.sb` Seatbelt profiles, a duplicate CUE schema, presets,
docker-compose/squid fixtures; `layer/host/`, `layer/vm/`; and `task/**` recipes)
is reference-only material "kept for compatibility with the design history" — dead
weight that now also contradicts current behavior (e.g. `.sb` Seatbelt profiles
after specs/0053 removed the sandbox tier).

Verified before removal: nothing outside `library/` references these paths except
historical `specs/*` (design records). The Go engine embeds its own schema and
presets from `internal/engine/policy/`; no `go:embed` or Makefile path points at
the removed files. The repo `:latest`-pinning gate (`TestRepositoryHasNoLatestPins`)
only asserts zero findings, so fewer scanned files cannot break it.

## What changes

- Delete `library/task/`, `library/layer/policy/`, `library/layer/host/`,
  `library/layer/vm/`, and `library/layer/README.md`.
- Keep `library/layer/container/` (the live, Makefile-synced container assets).
- Rewrite `library/README.md` to describe only the surviving container assets.

## Out of scope
- Historical `specs/*` — design records; their references to the removed paths are
  left intact (they describe the state at the time they were written).

## Verification
`make check` and `make build` both green.
