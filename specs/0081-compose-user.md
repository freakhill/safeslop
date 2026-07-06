# 0081 — Hard-set non-root compose user (L1)

**Status:** implemented  
**Date:** 2026-07-06

SCOPE: Close `specs/0070` L1 by making the container agent launch contract explicitly run as uid/gid 1000 in Compose, matching the image `USER 1000` and the tmpfs home ownership already assumed by the runtime template.  
OFF-LIMITS: No Dockerfile user changes, no uid/gid configurability, no privilege/capability changes beyond the Compose `user` directive, no changes to host or VM tiers, and no runtime dependency changes.  
WORKTREE: `.worktrees/0081-compose-user/`

## Design

Problem: the generated Compose file relies on the image's baked `USER 1000`; if the image user regresses or is replaced, Compose does not hard-set the non-root launch user even though `/home/agent` tmpfs ownership already assumes uid/gid 1000.

Chosen approach: render `user: "1000:1000"` on the generated `agent` service as a belt-and-suspenders launch invariant, and mirror it in the legacy container-layer Compose services that execute agent shells. Keep the uid/gid literal aligned with Dockerfile.agent and the existing tmpfs home mount; do not add policy/schema configurability for this hardening-only fix.

Rejected alternatives:

- Change Dockerfile.agent only: already true today and does not address the launch-contract gap called out by L1.
- Make uid/gid policy-configurable: unnecessary surface area for a fixed image/runtime invariant and a risk of mismatching the tmpfs home owner.
- Add runtime user probes: slower, engine-dependent, and less direct than rendering the Compose contract with a regression test.

## Tasks

- [x] Add Compose non-root user regression tests
  FILE:     `internal/engine/container/compose_test.go`
  CHANGE:   Add tests proving rendered generated Compose includes exactly one `user: "1000:1000"` for the `agent` service and that the legacy container Compose hard-sets the same user on both shell services.
  VERIFY:   `go test ./internal/engine/container/ -run TestComposeHardSetsAgentUser -count=1 -v`
  EXPECTED: Initially fails before template/legacy changes; passes after implementation.

- [x] Render generated Compose agent user
  FILE:     `internal/engine/container/assets/compose.yml.tmpl`
  CHANGE:   Add `user: "1000:1000"` to the `agent` service near `working_dir`, with a concise why-focused comment tying it to the image user and tmpfs home owner.
  VERIFY:   `go test ./internal/engine/container/ -run 'TestComposeHardSetsAgentUser|TestComposeGivesAgentWritableEphemeralHome' -count=1 -v`
  EXPECTED: Generated Compose pins the agent to uid/gid 1000 and keeps the uid/gid-owned tmpfs home.

- [x] Keep legacy/example container layer aligned
  FILE:     `library/layer/container/docker-compose.yml`
  CHANGE:   Add `user: "1000:1000"` to the legacy `agent` and `agent-tools` services, matching the generated runtime contract.
  VERIFY:   `go test ./internal/engine/container/ -run TestComposeHardSetsAgentUser -count=1 -v`
  EXPECTED: The test passes against both generated and legacy Compose files.

- [x] Update docs/spec status
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0005-sp3-container-environment.md`, `specs/0070-security-review.md`, `specs/0081-compose-user.md`
  CHANGE:   Document that container launches hard-set uid/gid 1000, mark L1 implemented in the security review, and tick completed plan tasks/status.
  VERIFY:   `rg -n 'user: "1000:1000"|uid/gid 1000|L1' README.md skills/agent-sandbox-ops/SKILL.md specs/0005-sp3-container-environment.md specs/0070-security-review.md specs/0081-compose-user.md`
  EXPECTED: User docs and security-review spec state the non-root Compose launch invariant.

- [x] Final verification
  FILE:     repository
  CHANGE:   Run the required full checks after implementation.
  VERIFY:   `make check && make build`
  EXPECTED: Both commands exit 0.
