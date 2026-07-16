# 0108 — Safe optional projection glob selection

Status: complete

SCOPE: make builtin Fish optional `*.fish` projection globs select physical regular files while safely omitting terminal symlink/special-file candidates, preserving descriptor-pinned snapshots and value-free reporting.

OFF-LIMITS: no following terminal glob symlinks; no absolute/outside/excluded target access; no change to direct-source or recursive-directory fail-closed behavior; no project-authored projection; no pathname fallback; no candidate names/counts/targets/raw errors/values in manifests or UI; no network/container-hardening change.

WORKTREE: `.worktrees/0108-safe-optional-projection-globs/`

- [x] Land the approved selector decision
  FILE:     `specs/research/2026-07-16-optional-projection-globs-ayo.md`, `specs/research/2026-07-16-optional-projection-globs-flo.md`, `specs/0108-safe-optional-projection-globs.md`
  CHANGE:   Record the reproduced topology, prior-art lessons, physical-regular selector verdict, deterministic laws, rejected alternatives, and executable plan.
  VERIFY:   `git diff --check && rg -n 'physical regular-file selector|skipped-nonregular|92.5 / 100' specs/research/2026-07-16-optional-projection-globs-*.md`
  EXPECTED: Notes are whitespace-clean and pin selection, omission, proof, compatibility, and non-disclosure contracts.

- [x] Reproduce optional-glob rejection as RED tests
  FILE:     `internal/engine/container/projection_test.go`
  CHANGE:   Add hermetic mixed/all-nonregular/required fixtures proving optional glob symlink and directory matches should be omitted as one aggregate status while regular siblings snapshot; assert outside target/name/content sentinels and readlink activity never appear. Add the new-hook replacement test with its production hook in the GREEN task so RED fails on behavior rather than test plumbing.
  VERIFY:   `go test ./internal/engine/container -run 'OptionalGlob|RequiredGlob' -count=1 -v`
  EXPECTED: New optional tests fail specifically because current code returns `projection_unsafe_descendant`; unchanged required behavior passes.

- [x] Implement no-follow physical-regular glob selection
  FILE:     `internal/engine/container/projection.go`, `internal/engine/container/projection_test.go`
  CHANGE:   Add `skipped-nonregular`; classify basename matches with `os.Root.Lstat`; omit non-regular candidates only when the glob item is optional; add a test-only post-classification barrier and test, then compare classification identity to the no-follow opened file; retain every existing selected-file/mount/digest/directory/atomic-publication proof; keep classification/race failures fatal.
  VERIFY:   `go test ./internal/engine/container -run 'Projection|Snapshot|Symlink|OptionalGlob|RequiredGlob' -count=1 -v`
  EXPECTED: Mixed/all-nonregular optional globs pass without reading unsafe links; replacement and required/direct/directory cases fail closed with stable codes.

- [x] Document the narrowed optional-glob contract
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, `specs/0107-safe-symlink-projection-failures.md`, `specs/research/2026-07-16-symlinked-projection-flo.md`, `specs/0108-safe-optional-projection-globs.md`
  CHANGE:   Explain that optional builtin globs copy physical regular matches and aggregate-omit terminal links/non-regular candidates, while direct/required/tree/proof failures remain fatal; mark 0107's internal-link law as superseded only for optional terminal glob membership.
  VERIFY:   `git diff --check && rg -n 'optional.*glob|skipped-nonregular|physical regular' README.md skills/agent-sandbox-ops/SKILL.md specs/0096-contained-hybrid-default-profiles.md specs/0107-safe-symlink-projection-failures.md specs/research/2026-07-16-symlinked-projection-flo.md`
  EXPECTED: Operator docs match the safe implemented distinction without suggesting outside links are followed.

- [x] Smoke-test the real Fish topology and run full gates
  FILE:     whole repo, `specs/0108-safe-optional-projection-globs.md`
  CHANGE:   Build the worktree binary, rerun the reproduced stopped Fish session against the real host metadata, verify it reaches running state without exposing or mounting the two outside links, stop it cleanly, then complete the checklist only after all repo gates pass.
  VERIFY:   `git diff --check && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: Real default Fish starts from private snapshots, cleanup succeeds, all UI/Go/Emacs/denylist/build gates pass, and the spec is complete.
