# 0078 — GCP token hardening (M5)

**Status:** implemented  
**Date:** 2026-07-06

## Source

Implements `specs/0070-security-review.md` M5: `StageGCP` wrote a
`gcp-access-token` file that nothing consumed; GCP delivery is already via
`CLOUDSDK_AUTH_ACCESS_TOKEN`.

## Decision

Remove the dead on-disk GCP token artifact instead of wiring/documenting it.
`StageGCP` continues to mint only a short-lived ADC access token and returns it
through the secret environment channel. It must not create a stage directory or
write `gcp-access-token` when GCP is the only credential being staged.

## Tasks

- [x] Add a regression test that preserves env delivery and rejects the dead file.
- [x] Remove the `gcp-access-token` write and GCP-only stage-dir creation.
- [x] Update the security-review finding status.
- [x] Verify `make check` and `make build`.
