# 0115 — Transactional boundary and code-quality hardening

Status: approved; implementation in progress

SCOPE: fix every verified code-quality finding on `main@94a5203`: canonical policy-relative workspaces and safe Compose serialization; unique direct-run ownership; crash-durable concurrent session records; direction-aware acknowledged live egress changes; digest/integrity-pinned proxy and npm build inputs; current CI/docs; and behavior-preserving Go/Elisp decomposition.

OFF-LIMITS: no public v1 JSON/JSONL/CUE break; no VM/sandbox/daemon/database; no weaker container-deny, trust-byte, descriptor-hostpath, projection, credential, value-free, or teardown law; no configurable extra host path; no arbitrary npm package/binary/script; no claim of hermetic or bit-reproducible networked builds; no automatic rewrite or abandonment of an existing running session.

WORKTREE: `.worktrees/0115-code-quality-hardening/`

Decision notes: `specs/research/2026-07-17-code-quality-hardening-ayo.md`, `specs/research/2026-07-17-code-quality-hardening-flo.md`.

Frozen acceptance laws:

- exactly one canonical existing workspace is the RW host bind, never the stage;
- effective runtime egress is always a subset of durable reviewed authority;
- record corruption, stale mutation, and commit uncertainty fail loudly and value-free;
- direct invocation identity is random and single-owner; deployed session cleanup remains reconstructable;
- enforcement and build package inputs are content-pinned without widening package/script authority;
- behavior fixes precede refactors; unchanged fronts and contracts remain covered by real tests.

## Wave 5 — plan and decisions

- [x] Land prior-art, adversarial decision, and executable plan
  FILE:     `specs/research/2026-07-17-code-quality-hardening-ayo.md`, `specs/research/2026-07-17-code-quality-hardening-flo.md`, `specs/0115-code-quality-hardening.md`
  CHANGE:   Record verified reproductions, frozen laws, ranked alternatives, selected workspace/state/egress/identity/supply-chain contracts, migration behavior, and dependency-ordered RED→GREEN tasks. The host-adjusted FLO score is 90/100 with no deterministic-law violation.
  VERIFY:   `git diff --check && rg -n '90.0 / 100|LAW-PATH|direction-aware|policy-relative|per-package|behavior-preserving' specs/research/2026-07-17-code-quality-hardening-*.md specs/0115-code-quality-hardening.md`
  EXPECTED: The approved design is complete, clean, and precise enough that implementation does not reopen a security-boundary decision.

## Wave 6 — independent foundations (serialized where files overlap)

- [ ] Add crash-durable, interprocess-safe session transactions
  FILE:     `internal/engine/session/session.go`, new `internal/engine/session/store.go`, new `internal/engine/session/atomic.go`, `internal/engine/session/session_test.go`, new focused session transaction tests; affected CLI session tests/calls only as required to compile
  CHANGE:   First reproduce malformed-list suppression, torn-write fault points, two-process lost update, stale-object save, directory-sync uncertainty, and concurrent rename/dismiss. Add per-record advisory `flock`, fresh-read transaction/update API, internal revision/CAS, same-directory 0600 `O_EXCL` temp → full write → file sync/close → rename → parent-directory sync, no-replace create, and locked removal. Retire unrestricted last-writer-wins `Save`; migrate every Store lifecycle method to one locked mutation. `Get`/`List` return typed value-free corruption and no partial list. Legacy records without revision remain readable and gain revision only on a successful mutation. Keep public `sessionData` and wire fixtures unchanged.
  VERIFY:   `go test ./internal/engine/session ./internal/cli -run 'Session|Store|Atomic|Concurrent|Corrupt|Revision|Rename|Dismiss' -count=1 -v && go test -race ./internal/engine/session ./internal/cli -run 'Session|Store|Concurrent|Rename|Dismiss' -count=1`
  EXPECTED: Both concurrent successful mutations survive or one receives a typed stale/retry result; readers see old/new complete records only; corruption never becomes absence; no public payload gains internal revision state.

- [ ] Canonicalize workspace authority and render typed Compose mounts
  FILE:     new `internal/engine/workspace/*.go`, its tests, `internal/engine/policy/schema/schema.cue`, `internal/cli/cli.go` and focused profile/session/run tests, `internal/engine/container/compose.go`, `launch.go`, new `mount_plan.go`, `internal/engine/container/assets/compose.yml.tmpl`, container tests
  CHANGE:   First reproduce `workspace:"."` mounting the stage, policy-directory/CWD disagreement, missing/non-directory workspaces, workspace↔stage overlap, newline structure injection, `${...}` interpolation, and hostile valid spaces/colon/quotes/Unicode paths. Resolve empty against invocation CWD and project-relative against the canonical policy directory, then absolute/existing-directory/symlink canonicalize once and carry that authority through evaluation, session persistence, staging, and launch. Keep this package separate from sealed descriptor `hostpath`. Validate controls at the engine boundary. Build a typed mount plan with exactly one RW bind (`workspace:/workspace`); render every bind in long form with `create_host_path:false`, every dynamic scalar as escaped JSON/YAML, literal `$` as `$$`, and explicit project/file/project-directory Compose arguments. Unsupported backends fail closed.
  VERIFY:   `go test ./internal/engine/workspace ./internal/engine/container ./internal/cli -run 'Workspace|Compose|Mount|Profile.*Path|Session.*Workspace|Injection|Interpolation' -count=1 -v && make check-hostpath-imports`
  EXPECTED: Direct, dry-run/show, and session paths agree on one canonical workspace; decoded Compose has exactly one RW host bind and cannot gain structure; descriptor hostpath APIs/imports remain sealed.

- [ ] Pin and harden proxy and npm build inputs
  FILE:     new `library/layer/container/proxy-image.lock.json`, new `library/layer/container/npm-locks/*/{package.json,package-lock.json}`, mirrored embedded assets, `internal/engine/container/assets.go`, `identity.go`, new npm/proxy contract code and tests, `Dockerfile.agent.tools`, `compose.yml.tmpl`, `Makefile`, sync/check scripts
  CHANGE:   First make tag/placeholder proxy refs, catalog-lock drift, missing transitive SRI, foreign source types, extra locks, wrong binary, and unreviewed script policy fail hermetically. Resolve and review the `ubuntu/squid` multi-platform OCI-index digest and amd64/arm64 manifests. Use only that lock in Compose. Add `cap_drop:ALL`, no-new-privileges, read-only root, PID bound, and only live-required nosuid/nodev tmpfs paths; service-level non-root remains conditional on the exact live smoke. Create one exact npm lock project for each buildable npm catalog entry (`claude-code`, `pi`, `pnpm`), a closed package→binary→script-policy registry, selected build-context materialization, `npm ci`, and recipe hashing of selected lock bytes. Preserve Pi ignore-scripts and require an explicit reviewed exception for any lifecycle action. Never stage credentials into the build context and never describe registry-backed builds as hermetic.
  VERIFY:   `make check-assets check-npm-locks check-proxy-image-lock && go test ./internal/engine/container -run 'Proxy|NPM|Tool|Recipe|Identity|BuildContext' -count=1 -v`
  EXPECTED: A moving proxy tag or unlocked/unselected npm artifact cannot enter a recipe; package selection stays minimal; proxy restrictions are explicit and unit gates are network-free.

- [ ] Repair executable CI surfaces and add drift gates
  FILE:     delete `.woodpecker/integration.yml`; replace/rename `.github/workflows/sandbox-images-check.yml` and `.woodpecker/sandbox-images.yml`; `.github/workflows/go.yml`, `.woodpecker/go.yml`, `ci/*drift*.sh`, `Makefile`
  CHANGE:   Remove the nonexistent Tart integration target and stale sandbox-exec comments. Replace obsolete agent-sandbox/CREWAI/Pydantic/AG2 image commands with one current container-image target that supplies the actual base/recipe/lock context and builds representative shell, Claude, Pi, and pnpm selections. Add a deterministic active-doc/workflow denylist for removed VM commands/paths and obsolete image/build arguments; exclude historical `specs/` evidence.
  VERIFY:   `bash -n ci/*.sh && make check-active-surface-drift && rg -n 'test-container-images|check-npm-locks|check-proxy-image-lock' .github/workflows .woodpecker Makefile && ! rg -n 'test-integration|internal/engine/vm|agent-sandbox-tools|ENABLE_(CREWAI|PYDANTIC|AG2)' .github/workflows .woodpecker CONTRIBUTING.md skills README.md`
  EXPECTED: Every active CI command exists and matches current host/container recipes; removed surfaces cannot silently re-enter active docs/workflows.

## Wave 7 — dependent runtime identity and authority

- [ ] Give every direct run a random single-owner runtime identity
  FILE:     `internal/cli/cli.go`, focused run/stage/session tests, `internal/engine/container/reap.go` and tests, additive internal session stage-layout fields where needed
  CHANGE:   First reproduce same-profile concurrent stage destruction and targeted deterministic ownership collision. Generate `run-<32 lowercase hex>` from 128 bits of `crypto/rand` after approval and before staging; use it consistently for stage, Compose project, exact ownership label, marker, cleanup, and dead-invocation reap. New sessions may use layout 2 keyed directly by random session ID. Legacy records without layout/runtime fields keep exact existing `stageDirFor("session-"+id, workspace)` and label reconstruction for status/stop/reconcile/rm/prune/revoke; do not rewrite on read. Markers remain value-free (ID/PID/process-start token only), and no cleanup signals an unverified PID.
  VERIFY:   `go test ./internal/cli ./internal/engine/container ./internal/engine/session -run 'Invocation|Concurrent.*Run|Stage|Reap|Legacy|Layout|Session.*Cleanup' -count=1 -v && go test -race ./internal/cli ./internal/engine/container -run 'Invocation|Concurrent.*Run|Stage|Reap' -count=1`
  EXPECTED: Concurrent direct runs cannot share or remove resources; exact dead-run cleanup works; the existing deployed session remains reconstructably stoppable and untouched.

- [ ] Make live egress updates direction-aware and acknowledged
  FILE:     `internal/engine/session/egress_grant.go`, session records/tests, replace `internal/engine/container/egress_grant_apply.go` with focused overlay/transaction code and tests, `compose.go`, `compose.yml.tmpl`, `internal/cli/cli.go`, grant/revoke/dismiss/reconcile tests, `ci/progressive-egress-smoke.sh`
  CHANGE:   First fault-inject every widen/narrow step: durable write, overlay temp/sync/rename, replacement, readiness/hash ACK, final commit, restore, and crash recovery. Bind a dedicated secret-free proxy-overlay directory, install generation-labelled/hash-checked grants atomically inside it, and replace the proxy service so revocation closes existing tunnels. Hold the session transaction lock throughout. Widen persists its upper bound before activation; narrow activates+ACKs before shrinking durable authority; dismiss is record-only. Internal applied-generation/hash and pending direction fields remain absent from public output. Ambiguous candidate/restore/runtime state tears down the full boundary and emits only fixed `network_authority_uncertain`. A legacy running session bootstraps the unchanged current set before its first live mutation; normal cleanup needs no bootstrap.
  VERIFY:   `go test ./internal/engine/session ./internal/engine/container ./internal/cli -run 'Egress|Grant|Revoke|Dismiss|Transition|Generation|Uncertain|Legacy' -count=1 -v && go test -race ./internal/engine/session ./internal/engine/container ./internal/cli -run 'Egress|Grant|Revoke|Dismiss|Transition' -count=1`
  EXPECTED: At every injected crash/failure, runtime authority is no broader than durable reviewed authority; success means matching durable and inspected generations; uncertainty removes the boundary instead of warning through it.

## Wave 8 — behavior-preserving maintainability and synchronization

- [ ] Decompose the Go CLI and replace mutable CLI seams with instance dependencies
  FILE:     split `internal/cli/cli.go` into focused root/run/profile/session/runtime/output files; new per-root dependency bundle; affected CLI tests
  CHANGE:   Only after safety behavior is green, move cohesive declarations without renaming package/API/commands/flags/envelopes. Give each root execution an owned dependency bundle for clock, store, runtime detector, launcher, overlay transaction, process/liveness, socket, host-exec, and policy mutation seams; migrate mutable test globals in small slices. Immutable registries/compiled regexes may remain package globals. Add parallelism only to tests with isolated environment/files/dependencies. Keep `cli.go` as a readable root/front rather than a 3,480-line implementation.
  VERIFY:   `go test ./internal/cli -shuffle=on -count=2 && go test -race ./internal/cli -count=1 && test "$(wc -l < internal/cli/cli.go)" -lt 1200 && gofmt -w internal/cli && git diff --check`
  EXPECTED: Existing CLI behavior/goldens remain byte-compatible, mutable seam sharing is materially reduced, shuffled/race runs pass, and the front is navigable.

- [ ] Decompose Emacs profile/session features and enforce strict compilation
  FILE:     `emacs/safeslop-profiles.el`, `emacs/safeslop-session.el`, new focused evaluation/profile-compose/session-terminal/egress feature files, `emacs/safeslop.el`, Doom autoloads if needed, ERT files, `Makefile`
  CHANGE:   Move cohesive implementations behind unchanged interactive/internal symbols and `provide`/`require` fronts; preserve mode maps, buffer-local state, callbacks, output envelopes, and autoload behavior. Fix all remaining docstring-width warnings for Emacs 29/30/32, then make strict byte compilation part of `make check`. Do not mix UX changes into the move.
  VERIFY:   `SAFESLOP_ELISP_WERROR=1 make test-emacs && make test-emacs-ui-matrix && test "$(wc -l < emacs/safeslop-profiles.el)" -lt 1200 && test "$(wc -l < emacs/safeslop-session.el)" -lt 1000 && git diff --check`
  EXPECTED: All existing ERT/UI fixtures and symbols remain compatible, strict compilation is green, and both oversized fronts are reduced.

- [ ] Synchronize operator contracts and spec evidence
  FILE:     `README.md`, `CONTRIBUTING.md`, `emacs/README.md`, `skills/README.md`, `skills/agent-sandbox-ops/SKILL.md`, `skills/agent-key-lifecycle/SKILL.md`, relevant catalog/container skills, `specs/0115-code-quality-hardening.md`
  CHANGE:   Document policy-relative canonical workspaces, hostile-path rejection versus valid quoting, unique invocation ownership, corruption/stale errors, proxy-replacement grant/revoke semantics, uncertainty teardown, legacy cleanup, digest-pinned Squid, per-package npm integrity locks, honest build network, current CI commands, and unchanged value-free/public boundaries. Remove active VM test paths and obsolete image instructions while retaining historical context where explicitly labelled.
  VERIFY:   `git diff --check && make check-active-surface-drift && rg -n 'policy-relative|canonical workspace|generation|digest|package-lock|legacy|value-free' README.md CONTRIBUTING.md emacs/README.md skills specs/0115-code-quality-hardening.md`
  EXPECTED: CLI help, README, skills, CI, and spec describe the same tested behavior and defaults without reviving removed surfaces or exposing values.

## Wave 9 — authoritative verification

- [ ] Run hermetic, race, strict, image, and real-runtime gates
  FILE:     whole repo; `specs/0115-code-quality-hardening.md` acceptance note
  CHANGE:   Run format/diff, focused suites, shuffled tests, full internal race tests, asset/catalog/path/drift/npm/proxy gates, strict Emacs and UI matrix, build, representative locked npm images, hostile-path Compose config, simultaneous direct invocations, disposable legacy-layout cleanup/bootstrap, proxy grant→revoke with old-tunnel termination, and every injected uncertainty teardown. Use only disposable state; do not mutate or stop the pre-existing user session. Record exact supported-runtime evidence without raw credentials/paths/output.
  VERIFY:   `git diff --check && go test -shuffle=on ./... && go test -race ./internal/... && SAFESLOP_ELISP_WERROR=1 make test-emacs && make test-emacs-ui-matrix && make check && make build && make test-container-images && make test-progressive-egress-smoke`
  EXPECTED: All authoritative commands exit 0; real runs prove deny→grant→revoke and cleanup; host credential bytes remain unchanged; no disposable session/stage/container/trust/temp state remains.

## Wave 10 — independent final review and deployment

- [ ] Pass independent security/spec/code-quality review, merge, and deploy
  FILE:     whole branch and completion note in this spec
  CHANGE:   Have isolated reviewers inspect the complete `94a5203..HEAD` diff against every frozen law, verified finding, public contract, migration path, supply-chain lock, failure interleaving, and maintainability claim. Resolve every blocking/high finding with RED→GREEN proof and rerun affected plus authoritative gates. Mark complete, merge to clean `main`, push `origin` and `github`, install matching CLI/Emacs artifacts, verify installed revision, and remove the worktree/branch only after both remotes match. Leave the user's existing session untouched.
  VERIFY:   `git diff --check && make check && make build && git status --short --branch`
  EXPECTED: Independent review has no unresolved blocker/high; main, both remotes, and installed version match; the repository/worktree is clean and no user session was disturbed.
