# 0110 — Exact in-home absolute projection symlinks

Status: in progress

SCOPE: allow engine-owned builtin projection source paths to follow exact-spelling absolute symlinks that are proper descendants of the same pinned approved root, by converting only their lexical suffix and restarting descriptor-relative traversal.

OFF-LIMITS: no pathname canonicalization/reopen; no outside/alternate-spelling/cross-root target; no configurable projection roots; no project-authored projection; no recursive-tree symlinks; no excluded-root, mount, identity, snapshot, non-disclosure, network, container, schema, CLI, dependency, or builtin-CUE relaxation.

WORKTREE: `.worktrees/0110-in-home-absolute-symlinks/`

- [x] Land prior-art and adversarial security decisions
  FILE:     `specs/research/2026-07-16-in-home-absolute-symlinks-ayo.md`, `specs/research/2026-07-16-in-home-absolute-symlinks-flo.md`, `specs/0110-in-home-absolute-projection-symlinks.md`
  CHANGE:   Record the reproduced Pi/Claude topology, mature-system lessons, exact-root lexical admission verdict, deterministic laws, failure text, rejected alternatives, and executable plan.
  VERIFY:   `git diff --check && rg -n '100 / 100|same approved root|Never resolve, open, stat, copy|exact in-root relative or absolute' specs/research/2026-07-16-in-home-absolute-symlinks-*.md`
  EXPECTED: Notes are whitespace-clean and pin syntax-only authority, descriptor restart, ambiguous-spelling rejection, tests, and docs.

- [ ] Reproduce exact in-home absolute links as RED
  FILE:     `internal/engine/container/projection_test.go`
  CHANGE:   Add strict lexical table coverage and production Pi/Claude builtin snapshot fixtures using `~/.pi/agent` as an exact-spelling absolute in-home link; retain outside/excluded/ambiguous/malformed rejection and add absolute-link replacement and pinned-root pathname-replacement proofs. Assert snapshot bytes and failures never expose or read attacker sentinels. Add the private helper signature in the GREEN task so RED fails on current behavior, not test plumbing.
  VERIFY:   `go test ./internal/engine/container -run 'Absolute.*Symlink|AbsoluteTarget|PinnedRoot' -count=1 -v`
  EXPECTED: Acceptance tests fail specifically with `projection_target_outside_root`; rejection/race/non-disclosure cases remain closed.

- [ ] Implement exact-root lexical conversion and truthful failure text
  FILE:     `internal/engine/container/projection.go`, `internal/engine/container/projection_test.go`
  CHANGE:   Add a private raw POSIX strict-descendant helper; in `openPinned`, after the existing stable readlink proof, accept only exact proper descendants of `root.Name()`, append remaining components, re-run existing laws, and restart from the borrowed root descriptor. Never operate on the absolute target pathname. Update only `projection_target_outside_root` summary/action to the decision text and test it.
  VERIFY:   `go test ./internal/engine/container -run 'Projection|Snapshot|Symlink|AbsoluteTarget|PinnedRoot' -count=1 -v`
  EXPECTED: Exact in-root absolute Pi/Claude links snapshot correctly; malformed/outside/excluded/replaced targets and every existing safety proof remain fail-closed and value-free.

- [ ] Synchronize projection documentation
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, `specs/0107-safe-symlink-projection-failures.md`, `specs/0110-in-home-absolute-projection-symlinks.md`
  CHANGE:   Replace relative-only wording with exact-spelling in-approved-root relative-or-absolute source-link behavior; retain rejection of alternate spellings, outside/excluded targets, internal links, races, and unsupported platforms; note that builtin CUE hashes do not change.
  VERIFY:   `git diff --check && rg -n 'exact-spelling|relative or absolute|alternate.*spelling|0110' README.md skills/agent-sandbox-ops/SKILL.md specs/0096-contained-hybrid-default-profiles.md specs/0107-safe-symlink-projection-failures.md`
  EXPECTED: Operator and historical-contract docs match the narrow implemented resolver refinement without implying broader host reach.

- [ ] Run repository gates and real builtin Pi smoke
  FILE:     whole repo, `specs/0110-in-home-absolute-projection-symlinks.md`
  CHANGE:   Run targeted tests, Emacs UI matrix, full check/build, install the built binary, create and run a fresh default Pi session against the real absolute in-home `~/.pi/agent` link, prove it reaches running/interactive startup with the unchanged builtin hash, then stop/remove test and prior failed records. Mark this spec complete only after all checks and cleanup succeed.
  VERIFY:   `git diff --check && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: All gates pass; installed Pi starts from descriptor-pinned snapshots; no test session/container/stage remains; repository docs/spec are complete.
