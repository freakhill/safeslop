# Release notes

`safeslop` is distributed as a Go binary plus the optional SwiftUI cockpit app.

## Current release focus

- Single Go CLI entrypoint: `safeslop`.
- Policy-driven launch with host, sandbox, container, and VM tiers.
- Fail-closed policy trust gate.
- Staged, wipe-on-exit credentials and secrets.
- Receipt-driven install/uninstall flows.
- Go-only CI gate through `make check` and `make build`.

## Build and sign

```bash
make dist
make sign
```

`make sign` runs `app/packaging/sign-notarize.sh` and requires the Apple
Developer signing/notary environment described in that helper.
