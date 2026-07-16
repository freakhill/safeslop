# 2026-07-17 — Shared host-path proof decision

Status: **locked for spec 0114**
Score: **100 / 100** (C1 10×35%, C2 10×25%, C3 10×20%, C4 10×15%, C5 10×5%; every deterministic law passes)

## Verdict

Extract one descriptor-root proof engine into `internal/engine/hostpath`, initially
behind two typed uses only: builtin projection and fixed Pi OAuth. The core follows
stable source-path links as bounded lexical rewrites inside one retained approved
root. It returns descriptor-backed capabilities, never an operational resolved
pathname.

Pi may follow relative or exact-spelling absolute source-path links only when the
complete route remains inside retained HOME and on its mount. Every
resolver-reached containing directory must be owned by the effective user and
satisfy `mode & 0022 == 0`; owner-controlled `0755` is valid. The ultimate auth
leaf and existing lock/read/fresh-proof protocol remain strict.

Projection gets no behavior, inventory, authority, or public-contract change.
There is no generic host-file feature or user path-policy surface.

## Responsibility split

`internal/engine/hostpath` owns only non-weakenable mechanics:

- retained root acquisition and identity;
- bounded component queues and same-root link rewrites;
- exact lexical admission of absolute same-root targets;
- descriptor-relative lookup/open/stat/readlink and complete edge revalidation;
- mount-instance checks and supported-platform adapters;
- descriptor-backed file/directory capabilities and closed internal reasons.

Typed facades prevent a generic weakening surface:

```text
internal/engine/hostpath/
  core.go
  identity_linux.go
  identity_darwin.go
  identity_unsupported.go
  projection.go
  pi.go
```

Mandatory API properties:

- the generic walker, root capability, identities, link state, and absolute-target
  parser are unexported;
- no generic `OpenRoot(path)`, option bag, allow-link flag, mount override,
  arbitrary credential path, resolved-path/identity accessor, or public OS error;
- projection accepts only its existing engine-supplied HOME/XDG authorities and
  can only yield descriptor-derived snapshot inputs;
- Pi takes no root/source/filename/policy input and can only attempt fixed
  `.pi/agent/auth.json` reads;
- capabilities fail after close and outlive all reads/revalidation they prove;
- only `container` and `creds` may import `hostpath`, enforced by a deterministic
  repository gate using exact import paths (aliases do not bypass it).

Caller semantics remain separate. Container keeps item inventory, optionality,
targets, tree/glob selection, snapshot publication, manifests, and public errors.
Creds keeps the retry loop, strict JSON/provider/access/expiry parser, synthetic
staging, and public Pi failures.

## Core proof epoch

1. Open and retain one approved root; record descriptor and mount-instance
   identity. Unsupported mount/no-follow proof fails closed.
2. Parse one root-relative component queue. Reject NUL or lexical escape.
3. For a physical component, lstat from retained parent, open descriptor-relative,
   stat the descriptor, and re-observe the directory entry. Type, identity, and
   root mount must agree; retain the parent/name edge for final revalidation.
4. For a link, read no-follow identity+target twice. They must agree. Rebase a
   relative target at its containing directory or accept an absolute target only
   when raw bytes are an exact proper descendant of the same root. Convert to
   components and restart from retained root; never open the target pathname.
5. Reapply core and typed-profile checks to the entire rewritten route. Separate
   approved roots never combine authority.
6. Reject a repeated link edge or a 41st dereference.
7. Before use, revalidate root, every physical edge, every link identity+target,
   and mount identity through retained parents. Any change invalidates the epoch.
8. Return only a typed descriptor-backed capability. Lexical proof state remains
   private and is used only for law checks/revalidation.

Linux uses descriptor operations plus `statx` mount ID. Darwin uses descriptor
operations plus `fsid`. `!linux && !darwin` returns a closed `unsupported` reason;
there is no pathname, `st_dev`-only, or blanket symlink fallback.

## Pi profile

A complete Pi attempt starts again from the retained HOME root:

1. Prove fixed `.pi/agent` through any accepted same-HOME source links. HOME and
   every reached containing directory must be a current-user directory with
   `mode & 0022 == 0`. Read/execute bits for group/other are allowed; symlink mode
   is not an ancestry substitute.
2. Observe fixed sibling `auth.json.lock` no-follow from the proved source parent.
   Any object there is busy; never follow, remove, or repair it.
3. Prove fixed `auth.json`. A final same-HOME link is allowed, but its ultimate
   descriptor must be current-user regular, exact `0600` (no special bits),
   `nlink=1`, at most 1 MiB, and on HOME's mount.
4. Stat/read once through the pinned descriptor with a 1 MiB+1 limit, then stat
   again. Perform a fresh complete proof of the fixed lexical source from retained
   HOME; ultimate identity and stable metadata must match.
5. Reobserve the lock through original/fresh parent capabilities and revalidate
   both proof epochs. Any changed edge, leaf, parent, root, mount, or lock discards
   and zeroes bytes.

The lock remains beside the fixed lexical source name. A parent link resolves its
parent capability; a final-file link does not relocate or bypass the lock.
Busy/changed attempts consume the existing ten-attempt budget with nine 50 ms
sleeps. Permanent unsafe/missing failures retain existing value-free mappings.

All other Pi laws remain exact: trusted project opt-in, one provider/model,
printable ASCII access ≤64 KiB, duplicate/trailing JSON rejection, strictly >15
minutes twice, access-only synthetic file, no refresh/account/other provider,
private modes, atomic staging, source non-modification, and teardown wipe.

## Projection profile: no semantic delta

Keep unchanged:

- HOME/XDG roots and lazy external-XDG opening;
- source inventory, file/dir/glob meanings, labels, targets, optional statuses;
- every credential/cache exclusion, reapplied after source-link rewrites;
- source-path relative/exact absolute same-root links and 40-link limit;
- recursive-tree and direct-glob terminal link rejection;
- regular/special handling, same-mount checks, sorted traversal;
- retained file/directory proofs, digest verification, private atomic snapshots;
- manifests, mount custody, JSON, failure codes/text, and teardown.

Characterization tests lock these before extraction. The shared package replaces
the source walker/platform proof, not projection's policy or runtime surfaces.

## Binding laws

1. **Descriptor:** after approved-root acquisition, no host-source operation may
   canonicalize/resolve and reopen by pathname; operational results are retained
   capabilities only.
2. **Single root/mount:** rewritten components and descriptors remain under the
   same retained root and mount instance; approved roots do not combine.
3. **Exact links:** relative targets cannot escape; absolute targets require exact
   proper-descendant spelling; every law is reapplied; identity+target are reread;
   maximum 40 links.
4. **Stable epoch:** any root/component/link/leaf/lock/mount change invalidates the
   whole proof. Projection fails; Pi retries only within its fixed budget.
5. **Pi ancestry:** every reached containing directory is current-user-owned and
   not group/other writable. `0755` is valid; `0775` and `0757` are not.
6. **Pi leaf/protocol:** ultimate regular `0600`, current-user, nlink-one,
   same-mount, bounded leaf plus fixed lock, stable read/fresh proof, retries,
   JSON/expiry/access-only staging, and teardown are not configurable.
7. **Projection:** roots, exclusions, inventory, tree-link rejection, snapshots,
   manifests, authority, and public behavior remain unchanged.
8. **Sealing:** no CUE/CLI rule DSL, configurable root, arbitrary credential path,
   outside-HOME Pi target, builtin OAuth, or unrelated reader migration.
9. **Value-free:** no secret/ref/private or resolved path/link target/inode/mount
   ID/raw OS error in public errors, JSON, logs, metrics, or examples.
10. **Unsupported:** missing descriptor/no-follow/mount proof fails closed, with no
    pathname or device-only fallback.

Any law violation rejects the implementation.

## Required matrix

| Case | Projection | Pi OAuth |
|---|---|---|
| Physical valid route | unchanged | accept if ancestry+leaf pass |
| Relative same-root source link | unchanged accept | accept |
| Exact absolute same-root source link | unchanged accept | accept |
| Outside/prefix/ambiguous/dot target | reject | unsafe |
| Link identity/target changes | snapshot changed | discard/retry |
| >40 links or loop | symlink loop | unsafe |
| Different mount ID | unsafe descendant | unsafe |
| Link inside recursive tree/glob result | unchanged reject | N/A |
| Current-user `0755` ancestry | no new mode rule | accept |
| Wrong owner or group/other-writable ancestry | no new mode rule | unsafe |
| Nonregular/non-0600/multilink/oversize leaf | existing type law | unsafe |
| Lock before/after | N/A | retry/busy |
| Fresh proof differs | snapshot proof | discard/retry |
| Unsupported platform | safety unsupported | unsafe |

## Supersession and migration

Spec 0113 is superseded only for Pi source-path interpretation and ancestry mode:
proven same-HOME source links become valid, and the implementation is corrected
from owner-only ancestry to current-user ownership plus `mode & 0022 == 0`.
Fixed source, strict leaf, lock, read/fresh identity, retries, JSON, expiry,
access-only staging, teardown, policy, and non-ambient laws remain binding.
Projection decisions 0107/0110 are not superseded.

No CUE, JSON, persisted data, builtin profile hash, CLI, or feature-flag migration
exists. Existing practical safe layouts begin working.

## Method

Expansion used specs 0096/0107/0110/0113 and current projection/Pi code. Four
blind AYO lanes plus a host source check produced
`2026-07-17-hostpath-policy-ayo.md`. One isolated FLO worker drafted the decision.
Blind Kimi (original order) and DeepSeek (reversed) evaluators each scored all five
criteria 10/10 and found every deterministic law passing. Weighted host total is
100/100. Host clarifications make the import gate an exact deterministic CI check,
spell the supported build-tag matrix, and require only synthetic/value-free docs
examples; they do not change a scored decision.
