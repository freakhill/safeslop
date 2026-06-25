# Release notes

`safeslop` is distributed as a Go binary.

## Current release focus

- Single Go CLI entrypoint: `safeslop`.
- Policy-driven launch with host, sandbox, container, and VM tiers.
- Fail-closed policy trust gate.
- Staged, wipe-on-exit credentials and secrets.
- Receipt-driven install/uninstall flows.
- Go-only CI gate through `make check` and `make build`.

## Build

```bash
make dist
```

Signing/notarization packaging was removed with the Swift UI surface in specs/0049 PR2.
