# 0112 — Progressive proxy runtime readiness and Pi activation pin

Status: in progress

SCOPE: make container deny/progressive sessions fail closed before agent launch unless the Squid proxy is actually ready; fix the Ubuntu Squid log target that currently crashes the proxy; persist a value-free structured runtime failure; and advance the reviewed Pi npm pin to the smallest patch that contains GPT-5.6 Luna metadata.

OFF-LIMITS: do not relax deny topology or ACL ordering; do not auto-grant any model/provider host; do not add `network:"progressive"`; do not expose proxy logs/URIs/headers/bodies in status; do not add Pi credentials or host auth projection in this spec; do not bypass catalog LAW/soak rules without a documented normal-lane activation waiver; do not make podman/lima deny verified.

WORKTREE: `.worktrees/0112-progressive-runtime-readiness/`

- [x] Capture RED proxy startup and readiness regressions
  FILE:     `internal/engine/container/egress_observation_test.go`, `internal/engine/container/container_test.go`, `internal/cli/cli_session_test.go`, `specs/research/2026-07-16-progressive-runtime-readiness.md`
  CHANGE:   Pin the real failure (`ubuntu/squid` exits because cache_effective_user cannot open `stdio:/dev/stdout`), require the generated config to use the image's tailed `/var/log/squid/access.log`, require `Up` to run a bounded `squid -k check` after compose-up and tear the stack down on exhaustion, and require session finish to persist a value-free `network_proxy_unavailable` failure rather than raw runtime output.
  VERIFY:   `! go test ./internal/engine/container ./internal/cli -run 'Proxy|SquidEgressObservationLog|StructuredRuntimeFailure' -count=1 -v`
  EXPECTED: New assertions fail on the stdout log target, absent readiness probe/teardown, and legacy raw-error persistence—not on test plumbing.

- [ ] Fix proxy logging and enforce launch-time readiness
  FILE:     `internal/engine/container/assets/squid.conf.tmpl`, `internal/engine/container/container.go`, `internal/engine/container/runtime_failure.go`, `internal/engine/container/egress_observation_test.go`, `internal/engine/container/container_test.go`, `internal/cli/cli.go`, `internal/cli/cli_session_test.go`
  CHANGE:   Log the existing value-free observation format to `/var/log/squid/access.log` so the image entrypoint tails it to compose logs. After `compose up -d proxy`, retry `compose exec -T proxy squid -k check` with a bounded context; on failure run compose down/remove-orphans and return an engine-owned `network_proxy_unavailable` structured failure. Generalize session finish to persist any value-free engine failure implementing the existing Failure contract.
  VERIFY:   `go test ./internal/engine/container ./internal/cli -run 'Proxy|SquidEgressObservationLog|StructuredRuntimeFailure' -count=1 -v`
  EXPECTED: Tests prove successful readiness gates agent argv construction; persistent probe failure tears down and yields only fixed summary/action/code; no raw command output or path is serialized.

- [ ] Advance the pinned Pi patch under catalog policy
  FILE:     `internal/engine/policy/catalog.cue`, `internal/engine/policy/catalog.json`, `internal/engine/policy/catalog_test.go`, `specs/research/2026-07-16-progressive-runtime-readiness.md`
  CHANGE:   Use catalog tooling to review `pi` `0.80.2 → 0.80.7`, the smallest same-minor patch containing GPT-5.6 Luna metadata. Record candidate provenance, publication/soak result, transitive npm integrity caveat, and reverse closure. If the normal-lane soak blocks, stop unless the plan note carries the explicitly approved activation-blocker waiver allowed by the locked version policy; never label it security.
  VERIFY:   `go test ./internal/engine/policy -run 'Catalog|Pi' -count=1 -v && make check-catalog-sync`
  EXPECTED: Authored/rendered catalogs agree, Pi is pinned exactly once at the reviewed patch, and every catalog law remains green.

- [ ] Synchronize operator docs and runtime smoke path
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `ci/progressive-egress-smoke.sh`, `Makefile`, `specs/0112-progressive-runtime-readiness.md`
  CHANGE:   Document that deny/progressive launch requires a ready proxy and now fails before agent start with structured remediation. Add an opt-in real-runtime smoke that creates an isolated Fish container/deny session, proves deny→observation→exact grant→success→revoke→deny, then stop/remove cleanup; it must never touch existing sessions.
  VERIFY:   `bash -n ci/progressive-egress-smoke.sh && git diff --check && rg -n 'network_proxy_unavailable|proxy.*ready|progressive-egress-smoke' README.md skills/agent-sandbox-ops/SKILL.md Makefile ci/progressive-egress-smoke.sh`
  EXPECTED: Docs match the engine contract and the smoke script is isolated, self-cleaning, value-free, and opt-in.

- [ ] Run full gates and real runtime acceptance
  FILE:     whole repo, `specs/0112-progressive-runtime-readiness.md`
  CHANGE:   Run focused tests, the real progressive smoke, UI/check/build gates, then mark this spec complete. Install only after all checks pass; merge/push both remotes and remove the worktree/branch after the dependent 0113 live Pi test confirms this runtime foundation.
  VERIFY:   `git diff --check && make test-progressive-egress-smoke && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: The real proxy stays healthy; deny/observe/grant/revoke works without runtime edits; all Go/Emacs/catalog/denylist/build gates pass; no test session/container/trust/workspace remains.
