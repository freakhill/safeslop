# 0099 — Buildable personal bundle

Status: planned
Date: 2026-07-14

SCOPE: make the existing `personal` catalog closure safely image-buildable so the 0096 contained-hybrid builtin profiles remain launchable with `bundles:["personal"]`.

OFF-LIMITS: no all-zero digest bypass, unpinned installer/download, runtime tool download, arbitrary script package kind, silent package omission, or change to container-deny/progressive-egress authority.

WORKTREE: `.worktrees/0099-buildable-personal-bundle/`

Design: retain the catalog as the reviewed source of package version, artifact URL, architecture, and digest. Add explicit package-specific Dockerfile handlers and identity allowlist coverage only after each binary has verified amd64+arm64 digests; use the pinned Debian snapshot only for catalog entries deliberately modeled as `apt`. Add a closure contract test so `profile show` discovers drift before an operator creates a builtin session.

- [x] T1 — Pin and validate the personal package artifacts
  FILE: `internal/engine/policy/catalog.cue`, `internal/engine/policy/catalog.json`, `internal/engine/policy/catalog_test.go`
  CHANGE: Replace every personal binary sentinel with reviewed per-architecture SHA256 values and preserve structured upstream provenance; classify any Debian package intentionally installed from the pinned snapshot as `apt` with its version policy.
  VERIFY: `make check-assets && go test ./internal/engine/policy -run 'Catalog|BuildReady' -v`
  EXPECTED: The personal closure has no unresolved binary digest and catalog source/rendered JSON stay identical.

- [x] T2 — Add explicit image-build handlers for the personal closure
  FILE: `internal/engine/container/assets/Dockerfile.agent.tools`, `internal/engine/container/identity.go`, `internal/engine/container/identity_test.go`
  CHANGE: Add one guarded, checksum-verifying handler per non-apt package and explicit pinned-snapshot installation for apt packages; add only supported packages to the identity allowlist.
  VERIFY: `go test ./internal/engine/container -run 'Recipe|BuildArgs|Personal' -v`
  EXPECTED: Recipe resolution emits deterministic build args for the complete personal closure and unsupported packages still fail closed.

- [ ] T3 — Prove builtin profiles are launchable with personal
  FILE: `internal/engine/policy/builtins_test.go`, `internal/cli/cli_profile_test.go`, `internal/cli/cli_session_test.go`
  CHANGE: Assert each builtin resolves a buildable recipe and profile show/session create expose the contained-hybrid provenance and personal closure.
  VERIFY: `go test ./internal/engine/policy ./internal/cli -run 'Builtin|ProfileDefaults|SessionCreate.*Builtin' -v`
  EXPECTED: All four defaults are inspectable and session-creatable without a local policy.

- [ ] T4 — Complete 0096 docs and final verification
  FILE: `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, `specs/0099-buildable-personal-bundle.md`
  CHANGE: Document that builtin personal tools are pinned image-build inputs; mark 0096 only after the builtin contract is truly launchable.
  VERIFY: `git diff --check && make check && make build`
  EXPECTED: All checks pass and the operator documentation matches the shipped defaults.
