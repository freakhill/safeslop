# 2026-07-16 — In-home absolute projection symlink decision

Status: decision landed
Score: **100 / 100** (C1 10×35%, C2 10×30%, C3 10×20%, C4 10×15%; all deterministic laws pass)

## Verdict

Permit an absolute symlink encountered while resolving an engine-owned builtin projection source only when its raw POSIX spelling is an exact lexical proper descendant of the **same** approved root already bound to that source. Convert only the descendant suffix to components, then restart from the retained root descriptor. Never resolve, open, stat, copy, or mount through the absolute target pathname.

This adds syntax, not authority: every accepted target was already reachable through an accepted relative link beneath the same root.

## Laws

1. The exception is limited to source-path links handled by `projectionResolver.openPinned`; recursive selected trees still reject every internal link.
2. The target must begin byte-for-byte with `root + "/"`; no other approved root is considered. Root itself, prefix collisions, alternate case/Unicode/alias spelling, empty components, `.`, `..`, duplicate separators, and trailing separators are rejected.
3. The target is never cleaned or canonicalized for admission. The accepted suffix is split lexically and traversal restarts from the already-open root descriptor; the root descriptor is borrowed and never closed by a restart.
4. Existing link re-read/identity checks happen before admission. The 40-link budget spans restarts.
5. Converted target plus remaining source components re-enter existing exclusion, no-follow, mount/type/identity, snapshot, and value-free failure paths unchanged.
6. Unsupported descriptor platforms remain fail-closed with no pathname fallback.

## Exact predicate

For eligible cleaned POSIX `root` (absolute, not `/`, no trailing slash), accept `target` only when it begins with exact bytes `root + "/"` and every `/`-split suffix component is non-empty and neither `.` nor `..`. Return the joined relative suffix. This helper performs no filesystem operation and no case/Unicode normalization.

A rejected absolute target keeps code `projection_target_outside_root`, but its code-owned text becomes truthful for ambiguous spellings:

- Summary: `Config projection target is not safely within its approved root.`
- Action: `Use an exact in-root relative or absolute symlink target.`

No target/root bytes or raw OS error enter the failure.

## Required proof

Hermetic RED/GREEN tests must cover:

- production Pi and Claude builtin tables through an exact-spelling in-home absolute `~/.pi/agent` link;
- strict parser acceptance and root/root-prefix/outside/case/Unicode/dot/dot-dot/duplicate/trailing rejection;
- absolute targets into excluded roots and outside-root marker content;
- link replacement between read and re-read;
- pinned-root pathname replacement proving traversal uses the original descriptor, never the replacement tree;
- unchanged relative, recursive-tree, mount, type, snapshot, optional-glob, unsupported-platform, and non-disclosure suites.

The real host topology must then start a fresh builtin Pi session. Builtin CUE bytes and hashes do not change.

## Rejected alternatives

- Continue rejecting every absolute link: safe but needlessly breaks the reproduced Pi/Claude topology.
- `EvalSymlinks`/`realpath` then compare/reopen: TOCTOU and platform alias hazard.
- `filepath.Clean`/`filepath.Rel` admission: erases ambiguous syntax before the security decision.
- Case-fold/Unicode-normalize or inode-equivalence matching: lets filesystem aliases influence authority selection.
- Match another approved root or add configurable roots: capability expansion outside this bug.
- Rewrite the operator's host symlink: surprising host mutation rather than an engine fix.

## Migration and docs

Implementation changes only `internal/engine/container/projection.go` and its tests. Synchronize `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, and `specs/0107-safe-symlink-projection-failures.md`. No CLI, schema, dependency, builtin CUE, policy hash, or persisted-data migration occurs.

## Method

Expansion used specs 0096/0107–0109, the live stopped Pi record, and the production resolver. Three blind AYO lanes plus a host source lane landed `2026-07-16-in-home-absolute-symlinks-ayo.md`. An isolated FLO worker produced the baseline; Kimi evaluated it against the locked rubric. All deterministic laws passed. The host applied three clarifications without re-evaluation: borrow rather than duplicate the retained root descriptor, explicitly test trailing slashes, and enumerate exact documentation files.
