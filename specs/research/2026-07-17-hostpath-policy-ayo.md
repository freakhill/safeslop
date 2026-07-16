# 2026-07-17 — Practical-safe host path prior art

Status: lessons triaged for spec 0114

## Frame

Safeslop already has two incompatible host-path readers. Builtin projection safely
follows exact in-root source-path links through a retained `os.Root`; Pi OAuth has
a second walker that rejects every link. Pi also implemented parent mode
`&0077 == 0`, stricter than its locked decision's “no group/other write” rule.
The real owner-controlled `~/.pi/agent` link and ordinary `0755` ancestry therefore
fail despite remaining inside HOME.

Question: what shared rules admit practical same-HOME links and `0755` ancestry
without restoring pathname reopen, escape, mount, alias, or replacement races?

Named surfaces: `internal/engine/container/projection.go`,
`internal/engine/creds/pi.go`, a shared `internal/engine/hostpath` proof core, and
existing value-free projection/Pi failures.

## HIGH lessons

1. **Keep authority in retained root descriptors.** Linux `openat2` and
   capability-oriented APIs (`os.Root`, cap-std) show that the root handle—not a
   canonical string—is the authority. After root acquisition, lookup, readlink,
   stat, open, revalidation, lock observation, and reads must stay
   descriptor-relative. Never return a “safe path” for reopening.

2. **Treat an accepted link as syntax, not authority.** Relative targets may
   rewrite the remaining component queue only while it stays under the same root.
   A raw absolute target is admitted only as an exact lexical proper descendant
   of that same root, converted to components, and restarted from the retained
   descriptor. This is the narrow, cross-platform analogue of
   `RESOLVE_IN_ROOT`; no target pathname becomes operational.

3. **Reapply every law after every rewrite.** Containment, exclusions, node type,
   mount identity, ancestry constraints, link budget, and identity checks apply to
   the rewritten route. A second approved root does not become a link target;
   HOME and external XDG remain separate capabilities.

4. **Use stable proof epochs.** Kubernetes subPath and runc mount-path escapes are
   scars from validate-then-reopen. Read link identity+target twice, compare an
   opened descriptor with its directory entry, retain the parent/name edge, and
   revalidate the complete route before use. Any changed edge invalidates the
   epoch; projection fails, while Pi retries its whole fixed proof.

5. **Use mount-instance identity.** `RESOLVE_NO_XDEV` blocks both ordinary and
   bind-mount crossings. `st_dev` alone cannot identify a bind mount. Preserve
   Linux `statx` mount IDs and Darwin descriptor `fsid`; unsupported platforms
   fail closed.

6. **Separate ancestry integrity from leaf secrecy.** OpenSSH StrictModes rejects
   a containing directory when ownership is wrong or `mode & 0022 != 0`; it does
   not require owner-only ancestry. Group/other read or execute cannot replace an
   entry. Pi should therefore accept current-user-owned `0755` directories while
   retaining an exact `0600`, regular, current-user, single-link, bounded leaf.

7. **Do not infer safety from symlink mode bits.** Integrity comes from the
   descriptor-reached containing directory and final target predicates. The link
   itself cannot substitute for either check.

8. **Return capabilities plus closed reasons.** Projection needs descriptor-backed
   file/directory operations for snapshots; Pi needs a fixed auth read with
   sibling-lock and fresh-name proof. Neither needs a resolved path. Internal
   reasons must be closed and value-free, then mapped to existing caller-owned
   failure codes.

9. **Seal uses, not knobs.** A generic CUE path DSL, configurable root, arbitrary
   credential filename, or “allow symlinks” option would make safety
   caller-selectable. Share one non-weakenable proof core behind typed projection
   and fixed-Pi facades. Initially migrate no other reader.

## Scoped application

- Extract the reusable source walker and platform mount/identity proof from
  projection into `internal/engine/hostpath`.
- Projection keeps its roots, inventory, exclusions, optionality, recursive-tree
  no-link law, snapshots, manifests, and public failures unchanged.
- Pi keeps fixed `.pi/agent/auth.json`, strict leaf, sibling lock, ten attempts,
  50 ms delay, stable read/fresh proof, JSON/expiry/access-only staging, and
  teardown. It gains only proven same-HOME source-path links and the corrected
  ancestry threshold `mode & 0022 == 0`.

## Contested or rejected

- Do not add credential ancestry mode/owner checks to projection; that is an
  unrelated compatibility and authority decision.
- Do not require owner-only ancestry: it rejects `0755` without preventing an
  additional replacement capability.
- Do not canonicalize then compare/reopen, accept another approved root, allow a
  mount crossing, or add a pathname fallback.
- Do not force a generic second content read. Pi's writer lock, descriptor
  pre/post stat, fresh full proof, and bounded retry remain the scoped protocol.
- Do not migrate GitHub, cloud, kube, or account-link readers in this change.

## Sources

- Linux `openat2(2)` and path resolution:
  <https://man7.org/linux/man-pages/man2/openat2.2.html>,
  <https://docs.kernel.org/filesystems/path-lookup.html>
- Go `os.Root`: <https://pkg.go.dev/os#Root>
- OpenSSH portable `safe_path` / StrictModes (`auth.c`, `misc.c`): current-user or
  root ownership plus rejection of mode `022` on ancestry.
- Kubernetes subPath CVE-2017-1002101 and CVE-2021-25741.
- runc mount-path hardening: GHSA-c3xm-pvg7-gh7r.
- Rust cap-std capability-oriented filesystem design.

## Method

Four blind AYO lanes (Kimi, DeepSeek, Gemini, GPT) received one brief and source
packet. GPT returned inline; the other lanes wrote artifacts. The host checked
official `openat2`, path-resolution, Go, and OpenSSH evidence. All lanes converged
on descriptor capabilities, same-root link interpretation, non-writable `0755`
ancestry, strict credential leaves, same-mount proofs, and sealed engine uses.
