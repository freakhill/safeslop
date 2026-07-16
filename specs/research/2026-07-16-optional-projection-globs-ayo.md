# 2026-07-16 — Optional projection glob prior art

Status: lessons triaged

## Reproduced problem

The builtin Fish completion item is an optional `~/.config/fish/completions/*.fish` glob. A real host had eight physical regular matches plus two absolute symlinks resolving outside home. Safeslop 0e5347f rejected the first symlink as `projection_unsafe_descendant`, so none of the safe siblings were projected and the default Fish session did not start.

## High-pertinence lessons

1. **Separate selector membership from traversal authority.** POSIX `find` without `-H`/`-L` evaluates discovered symlinks as links; `-type f` simply excludes them. Reaching an approved directory does not authorize following every discovered link.
2. **Omission can be the safe result for optional bulk selection.** rsync `--safe-links` ignores absolute/outside symlinks while transferring eligible siblings. It neither dereferences the unsafe target nor aborts solely because that candidate exists.
3. **Optionality relaxes membership, not proof.** Once a candidate is classified as a regular file, failed no-follow open, same-file/mount checks, digest checks, or retained-directory proofs must remain fatal. OpenSSH StrictModes is the useful contrast: explicit authority-bearing inputs remain fail-loud.
4. **Classify descriptor-relatively.** Go `os.Root` supplies confinement but not mount/special-file safety. Candidate type selection must use a no-follow lookup relative to the retained directory descriptor; mount and identity checks remain caller-owned.
5. **Audit omission without enumerating host state.** One glob-level constant status can report that non-regular candidates were omitted. Candidate names, counts, targets, types, raw errors, and values are unnecessary and must remain private.

## Triage

- **HIGH:** physical-regular selection for optional builtin globs; aggregate value-free omission; fatal post-selection proof failures.
- **HIGH:** keep direct sources, recursive directory trees, and required globs fail-closed.
- **DEFERRED:** following relative in-root symlinks discovered by globs. It adds read authority and race semantics without a current requirement.
- **REJECTED:** filename-specific exclusions for host-generated completions; they are brittle and host-specific.

## Sources

- rsync `--safe-links`: https://download.samba.org/pub/rsync/rsync.1.html
- POSIX `find`: https://pubs.opengroup.org/onlinepubs/9699919799/utilities/find.html
- Go traversal-resistant APIs: https://go.dev/blog/osroot and https://pkg.go.dev/os@go1.26.4#Root
- OpenSSH StrictModes: https://man.openbsd.org/sshd_config.5

## Method

Three blind AYO lanes (GPT, DeepSeek, Gemini) converged on physical no-follow type selection, candidate-local omission for optional globs, and fatal proof failures. The host reproduced and classified the real topology using metadata only; no projected file content or link target was read by safeslop.
