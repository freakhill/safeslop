# 0095 — Same-realpath shadow dedup

Status: planned
Date: 2026-07-10

Implements the ayo-flo decision at
`specs/research/2026-07-10-same-realpath-shadow-dedup-flo.md`.

SCOPE: narrow `hostexec.Resolve`'s shadow rule by the actual threat (binary
substitution): PATH entries that are the SAME file object (by `os.SameFile`,
dev+ino, following symlinks) count as ONE helper; two or more DISTINCT identity
groups remain a hard `ErrShadowed`. Execute the first PATH-order entry (the
symlink), never the realpath. Diagnostics still surface every raw PATH entry.

OFF-LIMITS (from 0075/0088 + decision): do NOT weaken distinct-binary shadow
failure; do NOT add a path/shadow-bypass env (no `SAFESLOP_NO_SHADOW_DEDUP`);
`SAFESLOP_CONTAINER_RUNTIME` stays a name-only override; do NOT execute the
resolved realpath (preserve app-bundle context); no live Docker/OrbStack calls
in tests; no content/hash/version identity.

WORKTREE: `.worktrees/0095-same-realpath-shadow-dedup/`

## Tasks

- [ ] T1 — Add the identity seam and wire all LookupEnv implementers
  FILE: `internal/engine/hostexec/hostexec.go`, `internal/engine/hostenv/reconstruct.go`, test fakes in `internal/engine/hostexec/hostexec_test.go`, `internal/cli/cli_stage_test.go`, `internal/engine/toolchain/toolchain_test.go`, `internal/engine/creds/hostexec_test.go`, `internal/engine/secrets/secrets_test.go`
  CHANGE: Add `SameFile(pathA, pathB string) (bool, error)` to the `LookupEnv` interface. Production `Env.SameFile` = `os.Stat` (follows symlinks) on both + `os.SameFile` (dev+ino); a stat error is returned (never accidentally equal). Every test fake implements `SameFile` with the conservative default: `(a == b, nil)` (identical path string => same; distinct strings => distinct) so existing shadow goldens stay green untouched; new tests configure shared identity explicitly.
  VERIFY: `go build ./... && go vet ./...`
  EXPECTED: Compiles; all existing tests still pass (interface widened, fakes default to path-string equality).

- [ ] T2 — Classify by identity in Resolve/Inspect (TDD red first)
  FILE: `internal/engine/hostexec/hostexec.go`, `internal/engine/hostexec/hostexec_test.go`
  CHANGE: Add typed `ErrIdentity`. Add a `Resolver` classifier that groups `LookAll` candidates in PATH order by `SameFile` (first occurrence = representative); abort to `ErrIdentity` on a stat/compare error. Rewrite `Resolve`: 0 candidates -> `ErrNotFound`; 1 identity group -> success (`Path=all[0]`, `All`=raw); >=2 groups -> `ErrShadowed` (error lists one representative per group). Extend `Inspection` with `AliasPaths []string` and `ShadowedPaths []string`; `Shadowed = len(ShadowedPaths) > 0` (NOT `len(All) > 1`); `Present` true iff classification ok with >=1 candidate; on `ErrIdentity` keep `Path`/`All`, `Present=false`. `CommandResolved` unchanged (uses `Resolved.Path`, the symlink). Write the failing tests FIRST: same-inode aliases resolve to first entry; distinct inodes -> `ErrShadowed`; aliases + one distinct binary -> `ErrShadowed`; hardlink aliases pass; Inspect shows aliases-not-shadowed; CommandResolved executes first alias; identity failure fails closed.
  VERIFY: `go test ./internal/engine/hostexec/ -v`
  EXPECTED: New tests pass; `TestResolveMissingShadowedAbsoluteAndRelative` and `TestInspectReportsShadowWithoutFailing` stay green (fakes default distinct).

- [ ] T3 — Surface aliases in the doctor contract
  FILE: `internal/cli/cli.go`, `internal/cli/cli_test.go`
  CHANGE: `doctorReport` emits `all_paths` when >1 raw candidate, `alias_paths` when non-empty, `shadowed_paths` only when non-empty (distinct representatives); `present = Present && !Shadowed && Err==nil`; on `ErrIdentity` emit `present:false` + `identity_unverified:true` (never mislabel unknown as shadow). Add `TestDoctorReportsPresentWithSameInodeAliases` (present=true, alias_paths set, no shadowed_paths) and `TestDoctorReportsAliasesPlusDistinctShadow` (present=false, both alias_paths and shadowed_paths). Update `TestDoctorReportsShadowedHelperWithoutMarkingPresent` fixture to distinct paths and assert `all_paths`.
  VERIFY: `go test ./internal/cli/ -run 'Doctor' -v`
  EXPECTED: New doctor tests pass; distinct-shadow assertion unchanged in spirit.

- [ ] T4 — Emacs runtime preflight permits alias-only Docker
  FILE: `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`
  CHANGE: Confirm the preflight aborts ONLY on non-empty `shadowed_paths` (alias-only `present=true` Docker must NOT abort). Add an ERT case: doctor JSON with `present=true` + `alias_paths` + no `shadowed_paths` permits launch; mixed JSON with `shadowed_paths` still aborts. No change if it already keys only on `shadowed_paths`.
  VERIFY: `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: Alias-only Docker permits launch in the preflight; shadowed Docker still aborts.

- [ ] T5 — Docs and spec completion
  FILE: `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0095-same-realpath-shadow-dedup.md`
  CHANGE: Document that same-file PATH aliases (e.g. OrbStack's two docker symlinks) are treated as one helper and do not block launch, while genuinely distinct binaries still fail closed; note the inherited TOCTOU caveat briefly. Mark spec complete only after all verification passes.
  VERIFY: `git diff --check && make check && make build`
  EXPECTED: All gates pass; README/skill match behavior.
