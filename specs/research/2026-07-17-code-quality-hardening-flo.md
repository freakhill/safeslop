# Code-quality hardening decision — FLO

Date: 2026-07-17
Status: selected for spec 0115
Score: host-adjusted **90.0 / 100**; no deterministic LAW violation

SCOPE: select the implementation contract for all verified workspace, Compose, session-state, egress-transaction, run-identity, supply-chain, CI/docs, and maintainability findings on `main@94a5203`.

OFF-LIMITS: no public v1 JSON/CUE break, VM/sandbox return, daemon/database, weaker deny topology, configurable extra host path, arbitrary package/script kind, value-bearing state/output, or replacement of descriptor hostpath proofs with string canonicalization.

Prior art: `specs/research/2026-07-17-code-quality-hardening-ayo.md`.

## Ranked approaches

1. **Selected — one canonical launch authority, crash-durable locked records, direction-aware egress generations, and content-pinned build inputs.** Fix behavior in dependency-ordered TDD waves, then perform behavior-preserving Go/Elisp decomposition. Highest proof strength; costs additional internal state and a brief proxy recreation on grant/revoke.
2. **Minimal patch — absolute paths, quoted YAML, atomic writes, and existing Squid reconfigure.** Smaller, but cannot prove an applied generation, does not terminate already-open revoked tunnels, and leaves difficult rollback/file-bind inode ambiguity. Rejected by LAW-AUTH.
3. **Database/daemon or big-bang rewrite.** A journal service could own transitions, but violates the no-daemon/no-runtime-dependency contract and couples safety fixes to migration. Rejected.

## Deterministic laws

- **LAW-PATH:** one canonical existing workspace is the sole RW host mount; neither stage nor another host path is reachable through it; serialized content cannot add YAML/Compose structure.
- **LAW-STATE:** every session read-modify-write is interprocess-serialized, stale-guarded, and atomically durable; corrupt/unreadable records are never absence.
- **LAW-AUTH:** at every crash/failure point, effective egress is a subset of durable reviewed authority; unprovable state tears down the boundary.
- **LAW-SUPPLY:** Squid is digest-pinned; every buildable npm package has a selected, reviewed transitive integrity lock.
- **LAW-COMPAT:** current/legacy session cleanup remains valid; host/container only; trust, hostpath, value-free, and v1 contracts do not weaken.

## Selected contracts

### 1. Workspace and Compose boundary

Create `internal/engine/workspace`, not a generic export from sealed `hostpath`. Its resolver receives configured workspace, canonical policy path (when project-backed), and captured invocation CWD:

1. empty means invocation CWD;
2. project-relative means policy-directory-relative; ad-hoc/builtin relative means invocation-CWD-relative;
3. `Abs` → require existing directory → `EvalSymlinks` on the absolute path → require directory again;
4. reject NUL, invalid UTF-8, newline, control/format/line-separator characters;
5. carry only the canonical absolute path into dry-run/show, session persistence, staging, and launch.

Immediately before Compose, revalidate existence and reject workspace/stage-root overlap in either direction. This is a point-in-time pathname check for the intentionally writable workspace; it does not replace descriptor-pinned read-only projections.

Replace every short bind scalar with long form (`type/source/target/read_only/bind.create_host_path:false`). Encode dynamic YAML values with a single helper built from JSON string encoding (JSON strings are valid YAML), after replacing literal `$` with Compose's `$$`. Spaces, colons, quotes, Unicode, and literal dollar signs remain valid. No path travels through an environment variable. All Compose calls use an explicit validated project name, absolute compose file, and `--project-directory`.

A typed mount plan must prove exactly one writable bind—the canonical workspace to `/workspace`—and all stage/proxy/projection mounts read-only. Hermetic tests decode the emitted YAML and assert topology. A real `docker compose config --format json` fixture covers hostile-but-valid scalars; unsupported `create_host_path:false` runtimes fail closed with upgrade guidance.

### 2. Run identity and migration-safe stage ownership

Profile names and FNV hashes cease to be direct-run identities. Every direct invocation gets 128 random bits (`run-<32hex>`) used for stage, Compose project, ownership label, and reap. This removes same-profile concurrency and targeted FNV collision.

New sessions use their random session ID as runtime identity and may record additive internal `runtime_id`/`stage_layout` fields omitted from `sessionData`. Existing records without those fields keep the exact current hash-suffixed stage/label reconstruction for stop, revoke, reconcile, rm, and prune. The deployed running session is never rewritten or abandoned.

A direct-run marker stores only invocation ID, PID, and process-start token. Startup/teardown may reap a dead exact invocation label; it never acts on a reusable profile name. Corrupt-record recovery may clean exact syntactically derived session resources but never signal an unverified host PID. Orphan sweep stops on store corruption rather than treating the live set as empty.

### 3. Atomic locked session records

Split session storage into focused files and add an internal `record_revision` (legacy absence = 0), omitted from public session envelopes.

A stable per-session advisory `flock` under `<sessions>/.locks/` is held across fresh read, mutation, runtime transition, and commit. Kernel close/death releases it; no stale PID lock protocol is needed. The transaction commit checks the revision, validates the complete record, increments revision, then:

1. creates a unique same-directory temp with `O_EXCL`, mode 0600;
2. writes complete JSON+newline and checks short writes;
3. syncs and closes the file;
4. renames over the record;
5. syncs the parent directory.

A directory-sync failure is commit uncertainty, not success. Temp artifacts are not listable records and are cleaned on a later locked access. Unrestricted stale-object `Save` is removed; lifecycle, rename, lease, acknowledgement, grant/revoke, reconcile, remove, and prune use locked transitions. Typed corruption/stale/uncertain errors remain value-free. `List` fails loudly on a corrupt record rather than silently returning partial or empty state.

Apply the same exact-byte SHA-256 (`trust.Hash`) and lock/read/render/validate/temp-sync-rename-dir-sync pattern to engine-owned CUE mutations, preserving existing expected-policy-hash semantics.

### 4. Direction-aware, acknowledged egress updates

Mount a dedicated secret-free `proxy-overlay/` directory read-only into Squid and include its `session-grants.conf`. Atomic rename inside a directory bind is visible without exposing credentials and avoids the already-observed Docker Desktop individual-file inode race.

A live grant/revoke replaces the proxy service rather than treating `squid -k reconfigure` as an ACK. The candidate proxy carries generation/hash labels. ACK requires Compose replacement success, one proxy instance, matching labels, the existing bounded config/listener readiness check, and a proxy-local hash equal to the candidate overlay. Replacement also closes tunnels that a revoke must terminate.

Hold the session lock throughout:

- **Widen:** atomically persist the broader durable grant set plus a pending-widen transition; install overlay; replace+ACK proxy; atomically persist applied generation/hash and clear transition. Before ACK the old runtime is narrower than durable state.
- **Narrow:** persist only a pending-narrow transition while durable active grants remain the old upper bound; install overlay; replace+ACK narrower proxy; only then persist the narrower active set and clear transition.
- **Dismiss:** one locked record update; no proxy operation.

Any ambiguous candidate/restore/ACK state reaps the full session boundary and records the fixed value-free `network_authority_uncertain` failure best-effort. No success returns before the final durable commit.

Add internal applied generation/hash and transition fields, omitted from public output, so a later status/run/mutation can recover a crash without guessing. A legacy running session remains normally stoppable. Its first explicit egress mutation must bootstrap the unchanged current effective set into the generation-labelled proxy; bootstrap uncertainty tears down rather than granting. Normal stop needs no bootstrap.

### 5. Proxy and npm supply chain

Commit and embed a reviewed proxy lock containing one OCI index digest and its required linux/amd64+arm64 manifests. Compose receives the validated digest reference only; mutable tags and placeholder digests fail hermetic gates. Live acceptance must run exactly: Compose config, proxy start, `squid -k check`, listener probe, denied observation log, grant replacement, revoke replacement, and cleanup on supported platforms.

Always add `cap_drop:ALL`, `no-new-privileges`, read-only root, bounded PIDs, and explicit writable tmpfs for the pinned image's live-tested `/tmp`, log, run, and spool paths. Enable service-level non-root only if the exact entrypoint/config/listener/log/replacement smoke passes; otherwise retain root entrypoint with zero capabilities/read-only root while Squid drops internally. Pinning and the other restrictions are not conditional.

For each buildable npm catalog package (`claude-code`, `pi`, `pnpm`), commit a separate exact top-level `package.json` + `package-lock.json`. A closed code registry pins expected npm package, binary, and lifecycle policy. Build with `npm ci --omit=dev --no-audit --no-fund`; preserve Pi's ignore-scripts rule, and require an explicit per-package reviewed exception if another package demonstrably needs lifecycle scripts. Every registry lock entry must carry SRI integrity; git/file/workspace sources fail. Selected lock bytes feed recipe identity, and catalog↔lock↔binary/script-policy drift fails `make check`. Build networking remains honestly unenforced.

### 6. CI/docs and behavior-preserving decomposition

Delete the removed-Tart integration workflow. Replace stale image workflows with one target that materializes the real build context and validates the current representative recipes; remove obsolete agent-sandbox/CREWAI/Pydantic/AG2 commands. Add active-doc/workflow denylist checks for removed VM targets and obsolete image names, excluding historical specs.

After safety behavior is green:

- split `internal/cli/cli.go` into root/run/session/runtime/output/profile command files without changing package, constructors, flags, symbols, or envelopes;
- introduce per-root `AppDeps` and migrate mutable CLI seams in reviewed slices; immutable registries/regexes remain globals;
- split profile compose/evaluation and session terminal/egress Elisp into required feature files while preserving all interactive/internal front symbols and buffer-local state;
- make strict Elisp byte compilation green, then enforce it;
- add `t.Parallel` only to genuinely isolated tests—parallel count is not a goal.

## Implementation order

1. RED→GREEN atomic session state and policy writer.
2. RED→GREEN canonical workspace, safe mount plan, random direct identity, legacy cleanup.
3. RED→GREEN directory overlay, generation-labelled proxy replacement, direction-aware recovery.
4. Proxy digest/hardening and per-package npm locks/drift gates.
5. CI/docs repair and active drift gates.
6. Behavior-preserving Go/Elisp decomposition and dependency migration.
7. Full/race/strict/UI matrix plus real hostile-path, concurrent-run, current-session cleanup, proxy uncertainty, npm image, and progressive-egress gates.

## FLO result

Cross-family evaluator scores were 10/10 for all five criteria. Host reduced the result to **90.0/100** because the draft overreached into sealed `hostpath`, understated backend compatibility/layout-1 bootstrap complexity, and made the refactor wave too broad. Forced corrections here: workspace lives in its own package; `go.yaml.in/yaml/v3` is the repository's correct module path (the evaluator's `gopkg.in` warning was wrong); CUE expected hashes remain exact-byte `trust.Hash`; proxy smoke steps are explicit; and behavior fixes precede all decomposition. No deterministic LAW failed, so no second evaluator was required.
