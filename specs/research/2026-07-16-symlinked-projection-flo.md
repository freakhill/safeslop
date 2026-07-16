# 2026-07-16 — Symlinked builtin projection decision

Status: decision landed
Score: **91.5 / 100** (C1 9.0×35%, C2 10.0×30%, C3 7.5×20%, C4 8.5×15%; all deterministic laws pass)

Superseding refinement: `specs/research/2026-07-16-optional-projection-globs-flo.md` changes only optional glob terminal membership. Such globs select physical regular files and aggregate-omit matching non-regular candidates without readlink/follow/open; direct sources, required globs, recursive directory descendants, and selected-file proofs remain fail-closed under this decision.

## Verdict

Permit **only engine-owned builtin projection sources** to traverse symlink components when the resolver can prove containment with pinned descriptor-relative operations. Resolve each allowed source from a pinned approved root; validate both lexical and resolved locations against allowed roots and exclusions; snapshot bytes into a private per-session engine directory; and bind-mount only that snapshot. Never bind-mount, reopen, or copy from the original or resolved host pathname after validation.

This supports ordinary in-home dotfile layouts such as `~/.config → ~/dotfiles/files/.config`. It does not allow a link to a credential directory, another home, `/tmp`, a mount crossing, special files, or an internal directory symlink. Any safety failure remains fatal even for optional items. Required sources additionally fail for absent/unreadable sources.

## Laws

1. No broad home, excluded credential/cache root, project-authored projection, or network/container-hardening relaxation.
2. No direct live mount of a symlink-resolved host source. The container sees only an engine-owned snapshot.
3. The resolver opens and retains descriptors relative to a pinned root, uses no-follow component operations except for explicitly parsed in-root links, and never reuses a checked path string.
4. Relative link targets must remain under the pinned root. Absolute targets are rejected; this removes case-folded/canonical-root spelling ambiguity and keeps target handling platform-neutral.
5. Directory trees reject internal symlinks, mount/device crossings, special files, excluded descendants, link loops, and source identity changes during copy.
6. An OS/filesystem without the descriptor guarantees fails closed with `projection_safety_unsupported`; it must not fall back to `EvalSymlinks` plus pathname reopen.

## Snapshot and test contract

A resolver returns a pinned source descriptor plus type and identity. The container provisioner copies from those descriptors into a `0700` per-session snapshot directory, builds tree snapshots under an unpublished temporary name, verifies identity before/after copy, atomically publishes the complete snapshot, and mounts that snapshot read-only. Session teardown and failed launch remove it; stale owned snapshots are cleaned at startup. Test-only resolver hooks pause immediately after source open and before copy to prove link/path replacement cannot alter staged bytes.

## Failure contract and Emacs visibility

Session records gain a value-free `last_failure` object (version, phase, stable code, builtin projection label, `~`-spelled source, required, code-owned summary/action). `last_error` remains compatibility text capped at 240 characters. Codes and templates are exactly:

| code | summary | action |
|---|---|---|
| `projection_target_outside_root` | Config projection leaves its approved home root. | Keep its symlink target inside home. |
| `projection_target_excluded` | Config projection points to an excluded credential or cache path. | Remove that link from the projected config path. |
| `projection_symlink_loop` | Config projection contains a symlink loop. | Repair the symlink chain and retry. |
| `projection_unsafe_descendant` | Config projection contains an unsafe nested entry. | Remove nested links, special files, or mount crossings. |
| `projection_source_type` | Config projection is not a regular file or safe directory. | Use a regular config file or directory. |
| `projection_snapshot_changed` | Config changed during safe projection. | Stop concurrent changes and retry. |
| `projection_safety_unsupported` | This platform cannot safely project this symlink layout. | Use a non-symlinked config path or a project profile without projection. |
| `projection_required_absent` | A required builtin config source is unavailable. | Restore the required source and retry. |

No serialized or displayed error contains target paths, raw OS errors, file contents, environment values, command output, or credentials. The Emacs terminal sentinel performs one `session status` refresh after an early exit; on `last_failure`, it shows the structured summary/action in a persistent error buffer, emits a deduplicated minibuffer message keyed by session ID+failure version+code, and refreshes the portal. Stopped/failed portal rows visibly include a bounded reason rather than only a tooltip.

## Method

Expansion: `specs/0096-contained-hybrid-default-profiles.md`, the 2026-07-12 projection FLO note, `projection.go`, session/portal rendering, and a reproduced Fish builtin failure from `~/.config` symlink.

AYO: GPT, DeepSeek, and Gemini lanes mined `openat2(2)`, runc procfd handling, secure extractors, and OpenSSH StrictModes. Consensus: path-string validation followed by reuse is unsafe; descriptor-pinned private snapshots and explicit diagnostics are required.

FLO: baseline candidate `agent/tmp/flo-runs/0107-projection-symlink/candidates/baseline.md`; DeepSeek evaluator report at `agent/tmp/flo-runs/0107-projection-symlink/evaluations/baseline-deepseek.md`. The verdict applies forced evaluator fixes: reject absolute links, enumerate every error template, cap compatibility error text, specify terminal-sentinel refresh, and require injected resolver barriers/platform adapters.
