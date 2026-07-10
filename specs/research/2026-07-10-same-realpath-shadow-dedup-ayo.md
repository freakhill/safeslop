# Same-realpath shadow dedup: cross-model prior-art lessons

Date: 2026-07-10
Resolves: the narrowing question for `specs/0075-host-helper-exec-hardening.md` /
`internal/engine/hostexec` — whether PATH entries that resolve to the SAME underlying
binary (symlink/hardlink) may be treated as a single non-shadowed helper, while keeping
hard-shadow failure for entries that resolve to DIFFERENT binaries. Concrete trigger:
OrbStack installs `docker` as two symlinks to one file in `OrbStack.app`, plus Homebrew
ships its own separate `docker` binary; safeslop currently refuses all three → no
container runtime detected → no container profile launches, though OrbStack works.

## Headline

1. The shadow threat is "executing UNEXPECTED bytes" (binary substitution). Identity must
   therefore be measured by **`(dev, ino)` via `os.SameFile`**, not by realpath string and
   never by content/version. Same inode ⇒ same bytes ⇒ not the shadow threat.
2. Execution-layer dedup (`Resolve`) must be separated from diagnostic reporting (`Inspect`):
   collapse same-inode entries for execution, but keep surfacing every raw PATH entry so the
   operator still sees the OrbStack duplicates and `doctor` can report `present=true`-with-aliases.

## Triaged lessons

### HIGH (carry into decision-FLO)

- **H1 — Dedup by `(dev, ino)`, not realpath.**
  LESSON: Compare candidates with `os.SameFile` (stat follows symlinks → target inode;
  hardlinks share an inode), including the device id since hardlinks cannot span filesystems.
  EVIDENCE: Go stdlib `os.SameFile`; Nix `optimiseStore` uses content-hash only because its
  store is read-only/content-addressed; `filepath.EvalSymlinks` is "an appealing but incorrect
  solution" (LWN, "The Trouble with Symbolic Links") because intermediate components can be
  swapped (its own TOCTOU) and it misses hardlinks.
  RELEVANCE: `hostexec.Resolve` — after `all := LookAll(name)`, stat each, dedup by
  `SameFile`, then apply `len(unique) > 1 → ErrShadowed`. Keep `LookAll` raw.

- **H2 — Execute the first PATH-order entry (the symlink), NOT the realpath.**
  LESSON: macOS app-bundle launchers (OrbStack `xbin/docker`, Docker Desktop) locate adjacent
  Frameworks/dylibs via argv[0]/invocation path and rely on the symlink path for code-signing
  context; executing the resolved `Contents/MacOS/...` path strips that context and can break
  the app. Honor POSIX PATH order — return the first-encountered entry among the identical set.
  EVIDENCE: OrbStack issues #1006/#1528/#904; Docker Desktop same pattern; Nix-darwin `.app`
  wrapper discussions; Go `exec.LookPath` returns the PATH entry, kernel re-resolves at exec.
  RELEVANCE: `hostexec.CommandResolved` — `cmd.Path`/`Args[0]` stay the chosen PATH entry.

- **H3 — Keep diagnostics raw; only execution dedups.**
  LESSON: `Inspect.All` must list every raw PATH entry; `doctor` should report `present=true`
  (with an aliases/duplicates signal) when all entries collapse to one inode, while still
  listing them — otherwise doctor says "docker unavailable" while `run` works (confusing UX),
  and the operator loses visibility into the duplicate.
  EVIDENCE: Homebrew `brew doctor` shadowed-command warnings are diagnostic-only (PR #22130);
  safeslop's `Inspect` already reports `Shadowed` without failing.
  RELEVANCE: `hostexec.Inspect`, `internal/cli` doctor JSON, Emacs preflight (`specs/0088`).

- **H4 — Genuine shadow stays hard-fail (LAW).**
  LESSON: ≥2 candidates with DISTINCT inodes (e.g. OrbStack symlink + Homebrew's own binary,
  or two same-version but differently-built/signed copies) must remain `ErrShadowed` for both
  credential and runtime helpers. Content/version similarity is NOT identity.
  EVIDENCE: K8s exec-plugin allowlist matches by path and refuses ambiguity; OpenSSH
  `AuthorizedKeysCommand` requires root-owned, not group/other-writable; Nix refuses to trust
  "looks the same".
  RELEVANCE: goldens — `TestDetectShadowedRuntimeIsHardError`,
  `TestResolveMissingShadowedAbsoluteAndRelative`; add same-inode-pass and distinct-inode-fail cases.

- **H5 — TOCTOU is inherited, not introduced; document it.**
  LESSON: Inode dedup narrows the same-binary false-positive; it does NOT close the
  validate-vs-exec TOCTOU (closing it needs `execveat(fd)`/`openat2(RESOLVE_NO_SYMLINKS)`,
  unavailable to userspace Go on macOS). Keep the resolve→probe→exec gap minimal; do not cache
  the resolved path across a diagnostic/network round-trip.
  EVIDENCE: Linux CVE-2024-43882 (execve TOCTOU); LWN symlink article; Apple Secure Coding
  Guide on test-then-use races.
  RELEVANCE: `hostexec.Resolve`/`CommandResolved`, `runtime.go` D4 probe ordering.

- **H6 — Sanitized-PATH directory trust is the precondition.**
  LESSON: Same-inode dedup is safe because the PATH is already a sanitized allowlist
  (path_helper + brew/user/shim dirs, world-writable/relative/`..` dropped). Optionally add
  an OpenSSH-`StrictModes`-style guard (chosen symlink's directory not group/other-writable)
  as belt-and-suspenders, but do not rely on it as the primary boundary.
  EVIDENCE: sudo `secure_path`/`env_reset`; OpenSSH `StrictModes`; current `hostenv.sanitize`.
  RELEVANCE: `hostenv.Reconstruct`/`sanitize` is the trust boundary; `Resolve` dedups within it.

- **H7 — Name override stays name-only; dedup applies uniformly.**
  LESSON: `SAFESLOP_CONTAINER_RUNTIME=docker|podman|lima` selects WHICH runtime name; it does
  not bypass shadow detection, and if the named runtime has ≥2 distinct binaries it still fails
  closed. Apply dedup uniformly across helper classes (the shared `hostexec.Resolve`) so the
  security property is consistent.
  EVIDENCE: K8s allowlist resolves basenames then enforces; sudo matches paths but still
  applies secure_path to children.
  RELEVANCE: `internal/engine/container/runtime` detect; honors 0075/0088 "no weakening".

### MEDIUM (contested / weigh in FLO)

- **H8 — Opt-out `SAFESLOP_NO_SHADOW_DEDUP=1` to revert to strict fail-closed.**
  LESSON: Match Go `GODEBUG=execerrdot=0` and Homebrew `HOMEBREW_NO_PATH_SHADOW_CHECK=1`:
  secure-by-default, documented security-relevant opt-out for edge cases / regressions.
  EVIDENCE: Go 1.19 `ErrDot` + `GODEBUG=execerrdot=0`; Homebrew PR #22130.
  RELEVANCE: `hostexec.Resolve`. (FLO must weigh added surface vs reversibility value.)

### DEFERRED

- Content-hash / signing-key helper verification (K8s future direction) — a future optional
  `Spec.Checksum`/`SAFESLOP_DOCKER_SHA256`; inode dedup is a cheap stepping stone, leave room.
- `SAFESLOP_SECURE_PATH` sudo-style nuclear PATH replacement — too heavy for now.

## Actionables

1. `hostexec.Resolve`: after `LookAll`, dedup by `os.SameFile` (dev+ino); `len(unique)==1` ⇒
   resolve the first PATH-order entry; `len(unique)>1` ⇒ `ErrShadowed` (error lists unique inodes).
2. `hostexec.Inspect`: keep `All` raw; add a same-inode-alias signal so `Shadowed` reflects
   genuinely distinct binaries only; `doctor` reports `present=true`-with-aliases in that case.
3. Goldens: add same-inode-pass, distinct-inode-fail, hardlink-pass, cross-trust-dir cases;
   keep `TestDetectShadowedRuntimeIsHardError` asserting distinct-binary hard-fail.
4. Keep `SAFESLOP_CONTAINER_RUNTIME` name-only; apply dedup uniformly (credential + runtime).
5. (FLO-decided) whether to add `SAFESLOP_NO_SHADOW_DEDUP=1` opt-out.

## Net

Cross-family consensus (Gemini + DeepSeek + Kimi): same-inode dedup at the execution layer is
security-safe because it preserves the no-binary-substitution guarantee (same inode ⇒ same
bytes), keeps genuine distinct-binary shadows fail-closed, and leaves diagnostics raw. The
load-bearing caveats are: compare by `(dev,ino)` not realpath; execute the symlink PATH entry
(not the realpath) to preserve app-bundle context; and treat the validate-vs-exec TOCTOU as
inherited/honest rather than solved. The opt-out (H8) and a StrictModes-style parent-dir guard
are the contested refinements for the decision-FLO.

## Method

Families/routes used: host (orchestrator) + Gemini 3.1 Pro + DeepSeek + Kimi (K2.7 for-coding).
All three lanes returned. No lanes unavailable.
