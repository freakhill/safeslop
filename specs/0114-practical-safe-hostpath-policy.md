# 0114 — Practical-safe shared host path proofs

Status: in progress

SCOPE: replace the duplicated projection/Pi source walkers with one descriptor-root host-path proof engine; preserve projection behavior exactly; allow Pi's fixed OAuth source through proven same-HOME relative or exact absolute source-path links; and correct Pi ancestry from owner-only to current-user-owned, no-group/other-write semantics so ordinary `0755` layouts work.

OFF-LIMITS: no CUE/CLI path-rule DSL; no configurable/extra root; no arbitrary credential path; no outside-HOME or cross-root Pi link; no builtin ambient OAuth; no raw auth projection; no projection inventory/exclusion/tree-link/snapshot/manifest/public-contract change; no pathname canonicalize/reopen fallback; no mount-proof weakening; no migration of GitHub/cloud/kube/account-link or another host reader; no secret/ref/private/resolved path/link target/inode/mount/raw OS error in public output.

WORKTREE: `.worktrees/0114-hostpath-policy/`

Decision notes: `specs/research/2026-07-17-hostpath-policy-ayo.md`, `specs/research/2026-07-17-hostpath-policy-flo.md`.

- [x] Land prior-art and adversarial host-path decision
  FILE:     `specs/research/2026-07-17-hostpath-policy-ayo.md`, `specs/research/2026-07-17-hostpath-policy-flo.md`, `specs/0114-practical-safe-hostpath-policy.md`
  CHANGE:   Record the duplicated-reader defect, OpenSSH-compatible ancestry threshold, retained-root capability design, typed projection/Pi uses, exact same-root link laws, platform/mount requirements, value-free failure law, 0113 supersession, and executable TDD sequence.
  VERIFY:   `git diff --check && rg -n '100 / 100|mode & 0022|same-HOME|no semantic delta|Descriptor' specs/research/2026-07-17-hostpath-policy-*.md`
  EXPECTED: Notes are clean and pin safe practical links/0755 without adding path authority or weakening any projection/Pi leaf/lifecycle law.

- [x] Add RED Pi practicality and shared-proof characterization tests
  FILE:     `internal/engine/creds/pi_test.go`, `internal/engine/container/projection_test.go`, `internal/engine/hostpath/hostpath_test.go`
  CHANGE:   Reproduce current failure for owner-controlled `0755` ancestry and relative/exact-absolute in-HOME links at `.pi`, `agent`, and final auth leaf. Add outside/prefix/ambiguous/loop/dangling, writable/wrong-owner ancestry, strict leaf, lock, replacement, value-free, and same-device/different-mount cases. Lock projection's current links/exclusions/XDG/tree/glob/snapshot/manifest/failure behavior before extraction; add a test-only shared-proof API seam so RED fails on behavior assertions rather than missing symbols.
  VERIFY:   `! go test ./internal/engine/creds ./internal/engine/container ./internal/engine/hostpath -run 'HostPath|PiOAuth.*(Symlink|Ancestry)|Projection.*Characterization' -count=1 -v`
  EXPECTED: Only new safe Pi link/0755 acceptance assertions fail against the old blanket rejection; unsafe cases and projection characterization remain closed/green.

- [x] Extract the shared proof core and migrate projection without semantic delta
  FILE:     `internal/engine/hostpath/*.go`, `internal/engine/container/projection.go`, `internal/engine/container/projection_identity_*.go`, `internal/engine/container/projection_test.go`
  CHANGE:   Move retained-root component/link proof, exact absolute-target conversion, mount-instance adapters, descriptor-backed pinned nodes, and revalidation into the unexported hostpath core with a typed projection facade. Preserve lazy HOME/XDG roots, exclusions after rewrites, 40-link limit, recursive/glob link rejection, digest/tree proofs, private atomic snapshots, manifests, and existing failure mappings byte-for-byte. Support linux/darwin only; other build tags return unsupported with no fallback.
  VERIFY:   `go test ./internal/engine/hostpath ./internal/engine/container -run 'HostPath|Projection|Snapshot|Symlink|AbsoluteTarget|PinnedRoot' -count=1 -v`
  EXPECTED: One core owns source-path resolution and every projection characterization remains unchanged, including race/mount/non-disclosure failures.

- [x] Migrate Pi to the typed fixed-source facade
  FILE:     `internal/engine/hostpath/pi.go`, `internal/engine/hostpath/*_test.go`, `internal/engine/creds/pi.go`, `internal/engine/creds/pi_test.go`, `internal/cli/cli_stage_test.go`
  CHANGE:   Replace Pi's raw no-link walker with a complete fixed `.pi/agent/auth.json` proof from retained HOME. Accept only proven same-root/same-mount links; require every reached directory to be current-user-owned with `mode & 0022 == 0`; retain exact regular/0600/nlink1/size leaf, lexical sibling lock before/after, descriptor read, fresh full proof, byte zeroing, ten attempts/nine 50 ms sleeps, JSON/access/expiry/stage/teardown, and existing value-free failure mappings.
  VERIFY:   `go test ./internal/engine/hostpath ./internal/engine/creds ./internal/cli -run 'HostPath|PiOAuth|StagePi' -count=1 -v`
  EXPECTED: Realistic same-HOME links and 0755 ancestry pass; outside/unstable/writable/cross-mount/unsafe leaf cases fail before launch; access-only bytes and public contracts are unchanged.

- [x] Seal architecture and synchronize operator contracts
  FILE:     `ci/hostpath-import-denylist.sh`, `Makefile`, `internal/engine/hostpath/api_test.go`, `README.md`, `emacs/README.md`, `skills/agent-key-lifecycle/SKILL.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0113-pi-oauth-staging.md`, `specs/0114-practical-safe-hostpath-policy.md`
  CHANGE:   Add a deterministic import/API gate allowing only container/creds typed hostpath uses and rejecting generic root/path/options/accessors. Document exact same-root links, current-user non-writable `0755` ancestry, unchanged strict leaf/lock/fresh proof, and rejected outside/mount/race cases. Mark 0113 superseded only for link interpretation and ancestry threshold; use synthetic/value-free examples only.
  VERIFY:   `bash -n ci/hostpath-import-denylist.sh && make check-hostpath-imports && git diff --check && rg -n 'same-HOME|0755|0022|0600|same mount|supersed' README.md emacs/README.md skills/agent-key-lifecycle/SKILL.md skills/agent-sandbox-ops/SKILL.md specs/0113-pi-oauth-staging.md`
  EXPECTED: No generic path capability is exposed; docs make practical acceptance and strict residual boundaries explicit without leaking private topology.

- [ ] Run full gates and real-home Pi acceptance, deploy, and clean up
  FILE:     whole repo, `specs/0114-practical-safe-hostpath-policy.md`
  CHANGE:   Run focused suites, progressive runtime smoke, UI/check/build. If the live smoke exposes Docker Desktop's bind-file visibility race, make proxy reload retry boundedly while Squid retains its prior fail-closed config. Using the normal real HOME (no copied auth fixture), run host Luna then a disposable trusted Pi OAuth session through deny→observe `chatgpt.com:443`→grant→real Luna marker→revoke→deny; compare host auth bytes without output, stop/remove, and prove stage/container/session/temp/trust cleanup. Mark complete, merge/push both remotes, install matching binary/Emacs files, and remove worktree/branch while leaving the existing user session untouched.
  VERIFY:   `git diff --check && make test-progressive-egress-smoke && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: Hermetic and real-home gates pass; the user's safe linked `~/.pi/agent` works directly; host auth is unchanged; no test state remains; installed version and both remotes match.
