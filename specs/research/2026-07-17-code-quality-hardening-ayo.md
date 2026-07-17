# Code-quality hardening prior-art review

Date: 2026-07-17
Status: triaged input to decision-FLO and spec 0115

SCOPE: mine mature path-serialization, durable-state, policy-transaction, lifecycle-identity, supply-chain, and decomposition mechanisms for the verified defects on `main@94a5203`.

OFF-LIMITS: no VM/sandbox return, daemon, SQLite/runtime database, public v1 contract break, weaker container deny, arbitrary package scripts, live-network unit tests, or path canonicalization substituted for descriptor proofs.

## Current defects consumed

The shared packet pinned eight source-reproduced defects: direct `workspace:"."` aliases the cache stage read-write through Compose; raw workspace text permits YAML/mount injection; session JSON is torn/lost-update prone and corrupt entries disappear; running egress can exceed durable state; direct runs share a deterministic FNV32 stage; Squid and npm inputs are not fully content-pinned; active CI/docs reference removed surfaces; and large Go/Elisp fronts rely on mutable globals.

## HIGH lessons adopted

### 1. Canonicalize authority once and serialize it as data

Resolve project workspaces relative to the policy file, then `Abs` → require an existing directory → `EvalSymlinks` on the absolute path. Carry only that canonical absolute value into launch, evaluation, session creation, stage identity, and Compose. Docker documents that all relative paths are resolved from the first Compose file, not the caller's cwd; `--project-directory` can override this, but safeslop should remove the ambiguity before invocation. [Compose merge paths](https://docs.docker.com/compose/how-tos/multiple-compose-files/merge/) [Compose CLI](https://docs.docker.com/reference/cli/docker/compose/)

Replace short bind strings with long-form bind objects and an encoded YAML scalar. Disable source creation where the backend supports `bind.create_host_path: false`; fail if the canonical source disappears. Escape Compose's `$` interpolation (`$$`) rather than rejecting otherwise valid Unix paths. Newline/control characters must never become YAML structure. [Bind mounts](https://docs.docker.com/engine/storage/bind-mounts/) [Compose interpolation](https://docs.docker.com/reference/compose-file/interpolation/)

**Project refinement:** do not reject every colon—valid host paths can contain one, and structured encoding removes its delimiter meaning. Do not pass the workspace through a new environment variable; that creates another interpolation surface. Keep the existing template but make every host path a deterministic encoded scalar in long form.

### 2. Use one crash-safe per-session mutation primitive

Git's lockfile API combines `O_CREAT|O_EXCL`, write-to-lock, and atomic rename so readers see old or new bytes, never a partial target. SQLite's commit analysis adds the load-bearing durability detail: sync the file before rename and sync the parent directory after namespace change. [Git lockfile API](https://git-scm.com/docs/api-lockfile) [SQLite atomic commit](https://sqlite.org/atomiccommit.html)

For safeslop, use the project's existing advisory-`flock` pattern for crash-released interprocess exclusion, plus a same-directory unique temp, mode enforcement, file sync, rename, and directory sync. Add an internal monotonic record revision and expose only a locked `Update`/transition API for read-modify-write. Keep a guarded low-level create/save path only where no stale object can overwrite a record. `Store.List` must return a value-free corruption error; an unreadable record is never authoritative absence.

**Project refinement:** prefer kernel advisory locks over PID-stamped stale lockfiles; the repo already uses `flock`, which vanishes on process exit. Revision/CAS remains useful as a programming guard against callers carrying stale values beyond the lock.

### 3. Maintain `effective authority ⊆ durable authority` during every transition

Envoy xDS pairs each response with a nonce and retains the last successfully applied version on NACK; version and applied state are distinct. Transactional firewalls similarly expose complete generations, not unchecked partial edits. [Envoy xDS ACK/NACK](https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol)

A widening grant must persist the durable upper bound before live activation. A narrowing revoke must activate the narrower set before replacing the durable upper bound. Hold the per-session lock across each transition. Candidate/restore writes must be atomic; every reload must be positively acknowledged. If the previous known-good policy cannot be restored and re-acknowledged, stop/reap the boundary and persist only a value-free failure—uncertainty is not a warning.

**Project refinement:** the prior Docker Desktop smoke proved that replacing an individually bind-mounted file can retain the old inode. Mount a dedicated, secret-free overlay directory into the proxy, then atomically rename `session-grants.conf` inside that directory. Keep immutable Squid/static allowlist mounts separate.

### 4. Separate human names from invocation identity

Kubernetes object UIDs and systemd invocation IDs exist because a reusable name is not an activation identity. Give every direct run a cryptographically random invocation key and use it for stage/resource labels. Session IDs remain the deterministic cleanup identity; preserve the existing hash-suffixed reconstruction for legacy/current session records so a deployed running session is still stoppable. Never use FNV32 as ownership identity.

### 5. Pin the enforcement plane and transitive npm graph

Docker and Kubernetes both distinguish mutable tags from immutable image digests; a digest is the only stable reference for the Squid enforcement image. [Docker image validation](https://docs.docker.com/build/policies/validate-images/) [Kubernetes images](https://kubernetes.io/docs/concepts/containers/images/)

Pin Squid by reviewed multi-platform OCI digest and add the strongest live-tested service restrictions: all capabilities dropped, no-new-privileges, read-only root, and explicit writable tmpfs paths; use non-root only if the image startup contract passes the real smoke.

npm's lock describes the exact transitive tree and stores SRI integrity for registry artifacts; `npm ci` refuses manifest/lock disagreement and does not rewrite the lock. [package-lock](https://docs.npmjs.com/cli/v10/configuring-npm/package-lock-json/) [npm ci](https://docs.npmjs.com/cli/v8/commands/npm-ci/)

Use one reviewed lock project per buildable npm package so profile package selection stays minimal. Materialize those lock projects into the build context, install with `npm ci`, expose only the expected binary, and add a catalog↔lock/integrity drift gate. Preserve Pi's ignore-scripts rule; any package needing lifecycle scripts gets an explicit per-package reviewed exception rather than a global relaxation.

### 6. Decompose behind unchanged fronts and make drift executable

Move cohesive Go command groups and Elisp sub-surfaces behind existing exported/interactively named fronts. Use an instance-owned dependency bundle for new/changed workflows, then migrate old package globals in behavior-preserving slices. Do not combine a public behavior change with a wholesale rewrite.

CI must execute or deterministically validate every documented target/path. Removed VM targets and obsolete image recipes should be impossible to mention in active docs/workflows without a gate failing. Strict Elisp byte compilation should be green before it becomes mandatory.

## MEDIUM / staged lessons

- Record applied egress generation separately if recovery needs to distinguish durable intent from acknowledged runtime state; do not expose raw runtime output.
- Add a real Compose-config regression and real progressive smoke after hermetic YAML/model tests.
- Convert safe tests to `t.Parallel` only after their dependency bundles and environment/state roots are isolated; a count target is not a correctness goal.
- Split the largest Go/Elisp files after security behavior is green; file length alone is not an architecture.

## Rejected / deferred

- **SQLite/Bolt state dependency:** unnecessary for one-record transactions and conflicts with the signed-binary/no-runtime-dependency constraint.
- **Save-first for revoke:** a crash can leave effective authority broader than durable state; transition order must be direction-aware.
- **Best-effort rollback:** rejected by the deny-on-uncertainty law.
- **Atomic rename over the currently individual file bind:** contradicted by the project's observed Docker Desktop inode-visibility race; use a directory bind.
- **Reject all `$`/colon/space paths:** structured serialization should preserve valid paths; reject only invalid/non-directory/control/NUL cases.
- **One lock project installing every npm tool:** violates profile package minimality; use per-package lock projects.

## Method

Blind lanes: Gemini, DeepSeek, GLM, and GPT (10 lessons each), all grounded in one exact source packet. Host lane corroborated with canonical Docker, Git, SQLite, Envoy, npm, and Kubernetes documentation. Consensus was strong on six mechanisms above. The main contradictions were lock-file versus `flock`, save ordering, colon rejection, and file-bind rename; project constraints resolved them as recorded.
