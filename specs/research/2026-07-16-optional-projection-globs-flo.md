# 2026-07-16 — Optional projection glob decision

Status: decision landed
Score: **92.5 / 100** (C1 9.0×40%, C2 9.5×25%, C3 9.5×25%, C4 9.0×10%; every deterministic law passes)

## Verdict

An optional projection glob is a **physical regular-file selector**. After opening its source directory through the existing pinned-root resolver, safeslop classifies each matching terminal entry with a descriptor-relative no-follow metadata lookup. Physical regular files continue through the existing no-follow open, same-file, same-mount, stable metadata, digest, retained-directory, and atomic snapshot proofs. Symlinks, directories, sockets, FIFOs, devices, and other non-regular matches are omitted without target inspection.

If any candidate is omitted, the manifest gets exactly one glob-level `skipped-nonregular` item. It contains only the approved source pattern, builtin label, optional bit, and constant status—never candidate names/counts/types, target paths/content, raw OS errors, logs, runtime arguments, environment/secret values, or other derived values. No omitted candidate is readlinked, followed, opened for data, hashed, copied, staged, or mounted.

The omission rule applies only to optional glob candidates. Required glob non-regular matches remain fatal; required globs with no eligible match remain required-absent. Direct items, recursive directory items, exclusions, absolute/outside link handling, mount crossings, and unsupported-platform behavior remain fail-closed. A platform unable to perform the no-follow descriptor-relative classification fails with `projection_safety_unsupported`; there is no pathname fallback.

## Laws

1. A non-regular glob candidate receives enumeration, basename matching, and one no-follow descriptor-relative classification only—no target or data operation.
2. Optional omission is candidate-local and value-free; eligible siblings can succeed, but omitted entries contribute zero bytes and no authority.
3. Classification/open replacement, same-file/mount failure, mutation, digest mismatch, or retained-directory proof failure is fatal and rolls back the entire unpublished snapshot.
4. Required globs and every non-glob projection surface retain the 0107 fail-closed contract.
5. Zero eligible files is a successful optional glob with `skipped-nonregular` when matching non-regular candidates existed, otherwise the existing `skipped-absent` status.

## Rejected alternatives

- **Follow relative in-home terminal symlinks:** unnecessary added read authority and race/target semantics.
- **Keep glob-wide failure and hardcode completion exclusions:** leaves ordinary defaults unusable and encodes host-specific filenames.

## Verification trace

| Law | Regression evidence |
|---|---|
| 1–2 | mixed regular/outside-link fixture; all-nonregular fixture; manifest sentinel assertions; readlink hook remains unused |
| 3 | regular-to-regular replacement barrier; existing digest, mount, and directory-proof tests |
| 4 | required mixed-glob test plus existing direct/directory/unsupported tests |
| 5 | all-nonregular optional and no-match optional status tests |

## Method

Expansion used main 0e5347f, the reproduced Fish failure, `projection.go`/tests, and the 0107 decision. AYO mined rsync, POSIX find, Go `os.Root`, and OpenSSH. An isolated FLO worker compared three contracts; a Kimi evaluator scored the selected artifact and found no fatal flaw. This note incorporates its forced clarifications on derived secret-value non-disclosure, unsupported-platform failure, and law-to-test traceability.
