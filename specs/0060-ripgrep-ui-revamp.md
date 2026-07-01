# 0060 — Ripgrep buildability + Emacs operator UI revamp

SCOPE:
- Finish the JSON-contract bug fix for `profile create/show --output json` when a resolved package cannot build.
- Make `ripgrep` actually buildable in container profiles: real all-arch sha256 pins + Dockerfile wiring + build args.
- Revamp the Emacs operator UI first slice from three parallel FLO reviews: Profiles, Portal/Session, and global navigation/error state.

OFF-LIMITS:
- Do not weaken network defaults or host/container boundaries.
- Do not add runtime dependencies outside the Go binary / existing Emacs Lisp package.
- Do not bind global `C-c s D`; existing tests intentionally keep it unbound.
- Do not change host-tier hue semantics in this pass.
- Do not make detached sessions the default; detached run remains explicit and warned.

WORKTREE: `.worktrees/fix-profile-create-json-buildready/`
BRANCH: `fix/profile-create-json-buildready`

## FLO synthesis

Parallel FLO lanes reviewed:
- Profiles surface: launch dead-end, inspect cul-de-sac, missing safety/buildability status, sort wiping header chrome.
- Portal/session: state-aware open, detached flow, stop race, credential warnings, session detail, completion.
- Global operator UI/nav: universal keys, persistent errors/empty state, output buffer nav, auto-refresh visibility.

Evaluator scores for the combined implementation direction:
- C1 usability clarity: 8.0/10
- C2 implementation fit: 7.5/10
- C3 safety/defaults: 8.0/10
- C4 navigation coherence: 8.5/10
- C5 testability: 8.0/10

## Tasks

- [ ] Task 1 — JSON contract errors for profile create/show image-recipe failures
  FILE: `internal/cli/cli.go`, `internal/cli/cli_profile_iw3_test.go`
  CHANGE: For `profile create --output json` and `profile show --output json`, return contract envelopes for load/resolve/show failures; preflight `profileResolvedData` before writing in `create` so unbuildable selectors do not mutate `safeslop.cue`.
  VERIFY: `go test ./internal/cli -run 'TestProfile(ShowUnbuildablePackageReturnsEnvelope|CreateUnbuildablePackageReturnsEnvelopeAndDoesNotWrite)' -count=1`
  EXPECTED: Tests pass; the original ripgrep command returns `ok:false` JSON and writes no config.

- [ ] Task 2 — Resolve ripgrep pins in catalog lockstep
  FILE: `internal/engine/policy/catalog.cue`, `internal/engine/policy/catalog.json`
  CHANGE: Replace ripgrep IW2 sentinel sha256 values for v14.1.1 amd64/arm64 with real upstream SHASUMS256 digests; render catalog JSON from CUE.
  VERIFY: `go test ./internal/engine/policy -run 'TestDefaultCatalog|TestCatalog' -count=1 && make render-catalog && git diff --exit-code -- internal/engine/policy/catalog.json`
  EXPECTED: Catalog validates; rendered JSON is in sync.

- [ ] Task 3 — Wire ripgrep into container image recipe/build
  FILE: `internal/engine/container/identity.go`, `internal/engine/container/assets/Dockerfile.agent.tools`, `internal/engine/container/identity_test.go`
  CHANGE: Mark `ripgrep` IW2-buildable; add guarded Dockerfile install using arch-specific artifact URL + sha256 verification and install the `rg` binary under `/usr/local/bin`; assert build args include `ENABLE_RIPGREP`, version, and per-arch sha args.
  VERIFY: `go test ./internal/engine/container -run 'TestResolveRecipe|TestAgentImage|TestToolsBuildArgs|TestRecipe' -count=1`
  EXPECTED: Container identity tests pass; resolving `pi+ripgrep` no longer fails on sentinel/buildability.

- [ ] Task 4 — End-to-end profile create for pi+ripgrep returns ok JSON
  FILE: `internal/cli/cli_profile_iw3_test.go`
  CHANGE: Add/adjust CLI test proving `profile create --name test --agent pi --environment container --bundle pi --package ripgrep --network allow --output json` succeeds after ripgrep buildability and includes ripgrep in the resolved identity set.
  VERIFY: `go test ./internal/cli -run 'TestProfileCreate.*Ripgrep|TestProfileCreateWritesNewCue' -count=1`
  EXPECTED: Tests pass; exact user command shape is now accepted.

- [ ] Task 5 — Global operator UI navigation/error substrate
  FILE: `emacs/safeslop-surface.el`, `emacs/safeslop.el`, `emacs/safeslop-doom.el`, `emacs/test/safeslop-test.el`
  CHANGE: Add universal surface keys (`d` doctor, `E` last error, `L`, `?`, `q`) to shared surface map; make output buffers inherit surface nav; store output args and implement safe `g` refresh; add persistent error/empty banner helpers and net faces; update Doom bindings/tests.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit`
  EXPECTED: Existing and new core UI tests pass.

- [ ] Task 6 — Profiles revamp slice
  FILE: `emacs/safeslop-profiles.el`, `emacs/safeslop-doom.el`, `emacs/test/safeslop-profiles-test.el`, `emacs/README.md`
  CHANGE: Add `x` launch from profile with isolation/network confirmation; inspect buffer action keys (`x/e/c/g`); move delete from `d` to `D`; render tier/nav hints, net faces, split empty/error state, non-sortable columns preserving header; update docs/tests.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -f ert-run-tests-batch-and-exit`
  EXPECTED: Profile UI tests pass and no header-wipe sort path remains.

- [ ] Task 7 — Portal/session revamp slice
  FILE: `emacs/safeslop-portal.el`, `emacs/safeslop-session.el`, `emacs/safeslop-doom.el`, `emacs/test/safeslop-test.el`, `emacs/test/safeslop-contract-test.el`, `emacs/README.md`, `README.md`, `skills/agent-sandbox-ops/SKILL.md`
  CHANGE: Cache sessions by id; state-aware `RET`; portal-local `D` detached run with credential warning; guarded `R`; stop refresh after callback; completing-read ids; session detail buffer; net/status help; profile jump; ad-hoc container progress; portal auto-refresh visible/pausable; docs.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -f ert-run-tests-batch-and-exit`
  EXPECTED: Session/portal tests pass; `C-c s D` remains unbound.

- [ ] Task 8 — Final verification/install
  FILE: repo
  CHANGE: Run full checks and install the new CLI/Emacs package.
  VERIFY: `make check && make build && make install && make install-emacs`
  EXPECTED: All tests/builds pass; `~/.local/bin/safeslop` and Emacs package are updated.
