# Same-realpath shadow dedup: Expansion → ayo → FLO decision

Date: 2026-07-10
Status: decision accepted — narrows `specs/0075-host-helper-exec-hardening.md` shadow rule
without weakening it; does NOT reopen 0088 "no weakening host-helper shadow refusal".
Baseline score: 96.5/100; 0 deterministic fixes applied; 1 forced fixture-audit fix folded in.

## Verdict

ADOPT same-`(dev,ino)` deduplication in `hostexec.Resolve`: PATH entries that are the same
file object (by `os.SameFile`, dev+ino, following symlinks) count as ONE helper; two or more
DISTINCT identity groups remain a hard `ErrShadowed`. Execute the first PATH-order entry (the
symlink), never the realpath. This preserves the no-binary-substitution guarantee (same inode ⇒
same bytes ⇒ not the shadow threat) while dissolving the OrbStack-class false positive (two
symlinks to one `OrbStack.app` binary + Homebrew's own binary → OrbStack aliases collapse,
Homebrew still shadows). It is a narrowing by the actual threat, not a weakening.

## Decision body (exact contract)

- **Identity seam.** Add `SameFile(pathA, pathB string) (bool, error)` to the injectable
  `LookupEnv` (`internal/engine/hostexec/hostexec.go:49-54`). Production = `os.Stat` (follows
  symlinks) on both + `os.SameFile` (dev+ino). Fakes use explicit `{dev,ino}`; an unknown fake
  identity returns an error, never accidentally equal. No inode/dev numbers leak into errors/JSON.
- **Shared classifier** (used by both `Resolve` and `Inspect`): groups `LookAll` candidates in
  PATH order by `SameFile` (first occurrence = group representative). Aborts on stat/compare
  error with a typed `ErrIdentity` (hard — execute nothing; neither `ErrNotFound` nor evidence
  of distinct inodes). `LookAll` stays raw (`hostenv/reconstruct.go:71-96`).
- **`Resolve`** (`hostexec.go:139-149`): 0 candidates ⇒ `ErrNotFound`; 1 identity group ⇒
  success (`Resolved.Path = all[0]`, `Resolved.All = raw all`); ≥2 groups ⇒ `ErrShadowed`
  (error lists one representative per group, first-seen order). Aliases don't vote: 2 OrbStack
  aliases + 1 Homebrew binary = 2 groups ⇒ still `ErrShadowed`. Applied UNIFORMLY to
  credential/runtime/other (shared `Resolve`); the no-warn-and-execute rule (0075) is preserved.
- **`Inspect`** (`hostexec.go:74-81`): `All` = raw (unchanged); `Path = All[0]`;
  `Present` = true iff classification ok with ≥1 candidate; new `AliasPaths` = non-representative
  raw paths; new `ShadowedPaths` = representatives after the first; `Shadowed = len(ShadowedPaths)
  > 0` (NOT `len(All) > 1`). On `ErrIdentity`: keep `Path`/`All`, `Present=false`.
- **`CommandResolved`** UNCHANGED (`hostexec.go:199-206`): `cmd.Path`/`Args[0] = Resolved.Path`
  (the symlink). Never `EvalSymlinks` or swap to canonical — preserves PATH determinism,
  argv0 behavior, app-bundle launch context, and code-signing.
- **Runtime detect** (`internal/engine/container/runtime`): uses `res.Path` for the D4 probe and
  execution; caches the pathname, not an inode. `SAFESLOP_CONTAINER_RUNTIME` stays
  `docker|podman|lima` name-only (`runtime.go:85-107`); `ErrShadowed`/`ErrIdentity` propagate as
  hard (only `ErrNotFound` = absent candidate).
- **Doctor JSON** (`internal/cli/cli.go:151-165`): `present = Present && !Shadowed && Err==nil`;
  emit `all_paths` when >1 raw candidate; `alias_paths` when non-empty; `shadowed_paths` only
  when non-empty (distinct representatives); on `ErrIdentity` emit `present:false` +
  `identity_unverified:true` (never mislabel unknown identity as a shadow). Emacs preflight
  (`emacs/safeslop-session.el`) aborts only on non-empty `shadowed_paths` — alias-only Docker is
  permitted; mixed/distinct Docker stays blocked.

### H8 — opt-out `SAFESLOP_NO_SHADOW_DEDUP`: REJECTED

Strict raw-entry multiplicity does not reduce binary-substitution risk when only the first path
executes and all aliases name the same file object. It would add an ambient config branch,
inconsistent-support risk across helper classes, and another security-sensitive env contract
without addressing TOCTOU. A defective implementation should be reverted/fixed centrally, not
selected per-process. No new shadow-related env var is introduced.

## Threat-model note

`os.SameFile` establishes point-in-time file-object identity, not content identity or
immutability. Distinct inodes remain shadows even when their bytes/versions/signatures appear
equal; a same-inode file can be modified in place. The validate→probe→exec TOCTOU already
exists in the current path-resolution + cached-path design; inode dedup neither creates a new
race class nor solves it (closing it needs `execveat(fd)`/`openat2(RESOLVE_NO_SYMLINKS)`,
unavailable to userspace Go on macOS). Keep resolution, probe, and execution adjacent; introduce
no diagnostic/network round-trip between them; do not describe the cached absolute pathname as an
inode pin.

## Goldens / test plan (hermetic — LAW-D)

New: `TestResolveSameInodeAliasesSelectsFirstPATHEntry`,
`TestResolveDistinctInodesReturnsErrShadowed`,
`TestResolveSameInodeAliasesPlusDistinctBinaryReturnsErrShadowed`,
`TestResolveHardlinkAliasesPass`, `TestInspectSameInodeAliasesAreVisibleNotShadowed`,
`TestCommandResolvedExecutesFirstAliasPath`, `TestResolveIdentityFailureFailsClosed`,
plus doctor (`TestDoctorReportsPresentWithSameInodeAliases`,
`TestDoctorReportsAliasesPlusDistinctShadow`) and detect
(`TestDetectSameInodeRuntimeAliasesProbeFirstPath`) cases. Hardlink case uses `t.TempDir` +
`os.Link` + production `os.SameFile`.

Existing: `TestResolveMissingShadowedAbsoluteAndRelative` and
`TestInspectReportsShadowWithoutFailing` get explicit distinct IDs; `TestDoctorReportsShadowedHelperWithoutMarkingPresent`
supplies distinct IDs and checks `all_paths`. STAY unchanged:
`TestDetectShadowedRuntimeIsHardError` (distinct-binary hard-fail), `TestDetectCachesResolvedRuntimePath`
(pathname stability), `TestEnvLookAllFindsShadows` (raw enumeration).

**Forced fix (from evaluator C4):** the implementation must audit EVERY existing multi-path
`fakeEnv` fixture in `hostexec_test.go`/`detect_test.go`/`cli_test.go` and assign shared IDs for
aliases / distinct IDs for genuine shadows — not only the two named above.

## Rejections / deferred

REJECTED now: realpath-string dedup or canonical-path execution; content/version equality;
majority selection among aliases+distinct; warn-and-execute; runtime-only exceptions or
path-valued runtime overrides; `SAFESLOP_NO_SHADOW_DEDUP`.
DEFERRED (separate specs): content-hash / signing-key helper pinning; `SAFESLOP_SECURE_PATH`;
`StrictModes`-style parent-directory ownership checks beyond the settled sanitized-PATH boundary;
descriptor-pinned execution via `execveat`/`openat2`.

## Method

Expansion read: `specs/0075`, `specs/0088`, `specs/0066`, `specs/0035`,
`specs/research/2026-07-05-host-helper-exec-hardening-ayo-flo.md`,
`specs/research/2026-06-21-host-launch-friction-flo.md`, `internal/engine/hostexec`,
`internal/engine/hostenv`, `internal/engine/container/runtime`, goldens.
AYO lanes: Gemini 3.1 Pro + DeepSeek + Kimi K2.7 (all returned; note at
`specs/research/2026-07-10-same-realpath-shadow-dedup-ayo.md`).
FLO worker: flo-worker (Sakana). FLO evaluator: flo-evaluator-kimi-thinking (Kimi, cross-family),
single judge. Rubric: C1 30 / C2 20 / C3 20 / C4 15 / C5 15. Scores: C1=10, C2=10, C3=9, C4=9,
C5=10 → weighted **96.5/100**. LAWs A/B/C/D all PASS. Fatal flaws: none. 0 deterministic fixes;
1 forced fixture-audit fix folded into the test plan.
