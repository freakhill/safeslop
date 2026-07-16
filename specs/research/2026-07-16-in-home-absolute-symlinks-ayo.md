# 2026-07-16 — In-home absolute projection symlink prior art

Status: lessons triaged

## Frame

The reproduced builtin Pi session failed because `~/.pi/agent` is an absolute link to an exact lexical descendant of the configured home. Spec 0107 intentionally accepted only relative in-root links. The question is whether absolute syntax can be admitted without adding read authority or returning to pathname validation followed by reopen.

## HIGH lessons

1. **Model an approved root, not the process root.** Linux `openat2(2)` with `RESOLVE_IN_ROOT` interprets absolute paths relative to a directory descriptor; the kernel path-lookup documentation treats that descriptor as an effective root. Safeslop can emulate only the needed subset: recognize an absolute link whose raw spelling is an exact descendant of the same approved root, derive its relative components without cleaning, and restart from the retained `os.Root`.
2. **Never make the absolute target operational.** `EvalSymlinks`, `realpath`, `Stat`, or `Open` on the target string would recreate the validate/reopen race seen in secure extraction and container mount escapes. The target may be inspected only lexically; all filesystem access stays descriptor-relative and all copied bytes come from retained descriptors.
3. **Keep exact root spelling as an admission gate.** Default macOS filesystems can treat case- or Unicode-distinct strings as the same object. Permission and archive systems have repeatedly been bypassed when string normalization differed from filesystem equivalence. Safeslop should not case-fold, Unicode-normalize, resolve aliases, or accept alternate root spellings. A spelling it cannot map unambiguously to the configured root remains rejected even if the host filesystem would resolve it in-root.
4. **Re-apply every law after conversion.** The converted complete path—link target plus remaining source components—must pass the same escape and excluded-root checks before traversal restarts. Existing component identity, symlink re-read, mount identity, 40-link bound, recursive-tree no-link rule, snapshot digest/proof, and value-free error laws remain unchanged.
5. **Test syntax and adversarial equivalence separately.** Acceptance needs exact-spelling home and external-XDG root cases. Rejection needs outside and excluded targets, alternate case/Unicode/root aliases, syntactic escape forms, root-only/type failures, and readlink replacement. Tests must prove no raw target pathname is opened or serialized.

## MEDIUM lessons

- Inode-based loop tracking could supplement path keys, but the existing canonical in-root path set plus unchanged 40-link limit is sufficient for this narrow refinement. Do not add new loop machinery without a failing case.
- Kernel-native `openat2` could eventually replace parts of the userspace walk on Linux, but it would not solve the macOS implementation and is outside this change.

## DEFERRED / rejected

- **Canonicalize then compare:** rejected; it touches attacker-changeable pathnames and creates platform-specific alias behavior.
- **Case-fold or Unicode-normalize targets:** rejected; matching filesystem equivalence portably is not tractable and could bypass exact excluded-root spellings.
- **Allow arbitrary absolute targets because snapshots are read-only:** rejected; snapshots prevent live mutation but do not remove unauthorized host read authority.
- **Add user-configurable projection roots:** deferred; this task only refines interpretation beneath roots already approved by the engine.

## Project implication

Refine `projectionResolver.openPinned` only. For an absolute target, select the same already-open root used for the current source, require the raw target to be an exact-spelling proper descendant, derive a non-escaping relative path without cleaning, append remaining components, run existing laws, and restart. Never reopen the target pathname. Existing relative behavior and public failure codes remain stable; the `projection_target_outside_root` summary/action should describe approved-root spelling rather than falsely claiming every rejected absolute link is physically outside home.

## Sources

- Linux `openat2(2)`: https://man7.org/linux/man-pages/man2/openat2.2.html
- Linux pathname lookup: https://docs.kernel.org/filesystems/path-lookup.html
- Go `os.Root`: https://pkg.go.dev/os#Root
- macOS normalization-insensitive archive traversal example: https://github.com/google/safearchive/pull/1
- runc mount-path hardening context: https://github.com/opencontainers/runc/security/advisories/GHSA-c3xm-pvg7-gh7r

## Method

Three blind AYO lanes (Kimi, DeepSeek, Gemini) plus a host canonical-source lane. All lanes converged on exact-spelling lexical admission followed by descriptor-relative restart, with no absolute pathname operation. The macOS lane strengthened rejection of case/Unicode/alias spellings; optional inode loop tracking was triaged as unnecessary.
