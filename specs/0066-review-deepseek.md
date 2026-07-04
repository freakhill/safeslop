# Adversarial Review: specs/0066-remove-install-ambient-runtimes

**Verdict: SPEC NEEDS REWORK** — one security-critical egress ambiguity (BLOCKER), two missing fail-closed mechanisms, and incomplete deletion enumeration must be resolved before code.

---

## BLOCKER

### B1. Podman egress parity: `--internal` semantics unverified (security-critical)
**File:** `specs/0066-remove-install-ambient-runtimes.md` D4, D6, D8; `internal/engine/container/assets/compose.yml.tmpl:40-47`

The spec’s central safety claim is that `network: deny` guarantees NO direct egress on every runtime. For podman, it proposes the same pre-created `--internal` network workaround used for rootless-nerdctl. **This is unsafe by analogy** — the compose template’s own comment (:40-47) explains that rootless-nerdctl’s `internal: true` is ignored because *RootlessKit/slirp NATs traffic out regardless*. Rootless podman uses the **same class** of user-mode networking (pasta or slirp4netns), and there is concrete reason to suspect the same bypass:

- With `pasta` (podman’s default since 5.x), pasta provides the guest’s default gateway via DHCP and NATs outbound traffic through the host. A netavark `--internal` network *might* configure the bridge without external routing, but pasta may still provide a route since it acts as the complete user-mode network stack — analogous to how RootlessKit bypasses nerdctl’s `internal: true`.
- With `slirp4netns`, the same class of bypass is plausible: slirp4netns provides its own NAT and doesn’t consult netavark’s internal flag for per-packet routing.

The spec acknowledges this (Risk #1: “Must not ship podman deny-tier unverified”) but **does not resolve it** — it punts to D8 live validation without specifying what `Detect()` returns for podman *before* that validation passes. This is a security-critical design gap: the implementation MUST have a concrete answer for what happens when podman is detected but not yet validated. Currently the spec offers two options (opt-in flag vs “unsupported”) without choosing. **Worse**, the `Detect()` function prototype (`runtime.Detect() (Engine, error)`) has no parameter for “verified runtimes” — there’s no mechanism in the design to *enforce* that an unverified runtime is rejected.

**Fix:** Commit to fail-closed by default: `Detect()` returns an error for podman and lima (rejecting them as unsupported) until a live-validation pass records its result. Add a `VerifiedRuntimes` map or a `--allow-unverified-runtime` opt-in flag that gates the unverified selection. Alternatively, hardcode that podman and lima `InternalNetwork() == ""` (treat as unsafe/no-egress-unproven) until validated, and refuse deny profiles on them — but this path is harder: it must be wired through `launch.go` and the session creation path.

### B2. `podman compose` is not a single thing — capability probe is insufficient
**File:** D4; `internal/engine/container/runtime/system.go:37`

The spec proposes `PodmanEngine` with `podman compose`. But `podman compose` has **three delegation modes**:
1. **`podman-compose` (Python)** — distinct project, different YAML parsing, may not honor `external: true` at all.
2. **`docker-compose` (v1, Python)** — legacy, may not honor `external: true`.
3. **`docker compose` (v2, Go plugin)** — works if installed as a podman plugin.

Detection via `podman compose version` merely proves *some* compose implementation exists. It doesn’t prove it honors the `external: true` network reference in `compose.yml.tmpl`. If `podman-compose` (Python) is the active backend, the entire egress model collapses — the squid proxy sits on an `internal` network that the agent may not actually be bound to.

**Fix:** Specify a *minimum* compose capability: require podman-docker-compose-v2 or podman’s native `podman compose` (which uses the same docker-compose-v2 plugin). Add a detection probe that writes a minimal compose file with `external: true`, runs `podman compose config`, and checks the output. Fall back to “unsupported” if the compose backend can’t prove it understands external networks.

### B3. `engineForSession` cannot compile after lima.go deletion (DAG ordering + design gap)
**File:** `internal/cli/cli.go:409-412`; `specs/0066` D2, D4

```go
// cli.go:409-412 — today:
if sess.Backend == "lima" {
    if dirs, err := install.DefaultDirs(); err == nil {
        return runtimepkg.LimaNerdctlEngine{...}
    }
}
```

After D2 deletes `lima.go` (containing `LimaNerdctlEngine`) and D1 deletes `internal/engine/install` (containing `DefaultDirs()`), this code can’t compile. D4 introduces a *new* `LimaEngine` type but:
- The `LimaEngine` struct is differently shaped (no `Limactl`, `Instance`, `LimaHome` fields — the spec says it runs against the user’s default lima with no pinned limactl).
- `engineForSession` is called from `safeslop down` (:1177), session listing/reaping, and orphan sweep — all paths that need to reconstruct an engine from session metadata alone.
- The spec doesn’t address how the new `LimaEngine` is reconstructable from `Session.Backend=="lima"`. If lima is user-managed, the engine might need only `lima nerdctl` argv — but this needs to be designed, not hand-waved.

**Fix:** Design `LimaEngine` to be zero-config: no fields needed beyond what’s on PATH (just `lima nerdctl` argv). Remove the `engineForSession` lima branch entirely and let `runtime.Detect()` handle it. But this means `safeslop down` must call `Detect()` rather than reconstruct — ensure this doesn’t create circular dependencies or slow teardown.

### B4. Session.Backend default change is underspecified — breaks existing session data
**File:** `internal/engine/session/session.go:97`; D7

`Create()` hardcodes `Backend: "system"`. D7 says the default changes to “set at provision time from the detected engine.” But:
- `Create()` is called BEFORE the engine is provisioned (it creates a session record, then later the session is run).
- The actual engine detection happens deep inside `provision()` in `launch.go`, not at `Create()` time.
- `recordSessionBackend()` (:377) currently fills in the backend at session-run time, but only for `environment: "container"` sessions. What about host-environment sessions?
- Existing sessions with `Backend: "system"` must remain readable — the spec says “regenerate, don’t preserve” for goldens but real session JSON files on disk are not goldens.

**Fix:** Keep `Create()` default empty (`""`), set `Backend` in `recordSessionBackend()` (or equivalent) at the first point the engine is detected, and treat `"system"` as a legacy alias for `"docker"` when reading existing sessions. Add a migration note.

---

## SHOULD-FIX

### S1. DAG ordering: GO removal leaves the tree broken between GO and RT
The task DAG says GO ‖ EM → RT → V. GO deletes `install/uninstall`, `lima.go`, `nontouch.go`, `system.go` etc. from `runtime/`. This removes `Backend`, `Select()`, `HostDockerEngine`, `LimaNerdctlEngine`, `SystemBackend`. RT then adds `Detect()`, `DockerEngine`, `PodmanEngine`, `LimaEngine`. Everything that consumes `runtimepkg.Engine` (`launch.go`, `cli.go` engineForSession/down/GC paths) cannot compile between these steps. The spec’s Risk #5 asks “should removal + refactor be one atomic Go change?” but does not answer.

**Fix:** Either (a) merge GO+RT into a single atomic change for the `runtime` package, or (b) keep the `Engine` interface and `HostDockerEngine` alive through GO (only delete lima/system Backend implementations), then have RT replace them. The spec should explicitly prescribe the sequence.

### S2. Missing deletion targets — Emacs (substantial, not just the obvious files)
The spec’s EM task lists 5 items but misses critical references that will cause byte-compile failures or test failures:

| File | What | Issue |
|---|---|---|
| `emacs/safeslop-client.el:99-100` | `safe-rerun-p` patterns for `install status/plan/apply --dry-run` | Must drop install patterns |
| `emacs/test/safeslop-test.el:348` | `safeslop--safe-rerun-p '("install" "apply" "--dry-run" ...)`  | Test must be removed |
| `emacs/test/safeslop-test.el:375-381` | `safeslop-install-mode-map` TAB binding test; `safeslop-install` symbol-function mock | Must be removed |
| `emacs/test/safeslop-test.el:425` | `safeslop-portal-mode-map` I→safeslop-install binding test | Must drop the install binding assertion |
| `emacs/test/safeslop-test.el:675-676` | `safeslop-install-mode` + `safeslop-install--render` call in error-banner test | Must be rewritten with a different surface |
| `emacs/test/safeslop-profiles-test.el:58` | `safeslop-profiles-mode-map` I→safeslop-install | Must drop the I binding assertion |
| `emacs/safeslop-surface.el:47` | `safeslop-surface--order` install entry | Reduction from 3 surfaces to 2; the tab strip, TAB cycling, and breadcrumb tests all assume 3; the spec doesn't address whether Profiles stays |
| `emacs/safeslop-surface.el:61` | `derived-mode-p 'safeslop-install-mode` check | Must be removed |
| `emacs/safeslop.el:24,36,104` | require + command-map binding for `safeslop-install` | Must be removed |
| `emacs/safeslop-doom.el:38,54,81-86` | autoload + shared-keys + evil-mode-keys for install | Must be removed |
| `Makefile:29` | `-l emacs/test/safeslop-install-test.el` in test invocation | Must be removed |
| `emacs/README.md:26,131` | install row in module table + Install section | Must be removed |

### S3. Missing deletion targets — Go tests
| File | What | Issue |
|---|---|---|
| `internal/engine/session/session_test.go:150-164` | `TestCreateDefaultsBackendSystem` expects `"system"` | Must update or remove |
| `internal/engine/container/runtime/runtime_test.go` | Uses `Select()`, `install.Dirs{}`, `NewLimaBackend()` | Must be rewritten for `Detect()` |
| `internal/engine/container/runtime/ensure_test.go` | Uses `install.Dirs` fixture | Must be deleted or rewritten |
| `internal/engine/container/runtime/imagepolicy_test.go` | Imports `install` (probably for Pin types) | Check if this can be deleted or must be updated |
| `internal/cli/cli_gc_test.go:17` | `t.Setenv("SAFESLOP_CONTAINER_BACKEND", "lima")` | Must be updated/removed |
| `internal/cli/cli_uninstall_test.go` | Whole file | Must be deleted (in CLI deletion list but worth calling out: contains `TestRenderUninstallPlanJSONShape`, `TestRenderUninstallPlanJSONEmptyReceipt`, confirmation-matching tests) |
| `internal/engine/uninstall/*_test.go` | Multiple test files | Must be deleted with the package |
| `internal/engine/install/*_test.go` | Multiple test files | Must be deleted with the package |

### S4. `SAFESLOP_CONTAINER_RUNTIME` override has no runtime-validation step
D3 says “honor `SAFESLOP_CONTAINER_RUNTIME=docker|podman|lima`; else auto-detect.” But if the user sets `SAFESLOP_CONTAINER_RUNTIME=podman` and podman isn’t functional, `Detect()` must fail closed with an actionable error. The spec doesn’t say this explicitly, and the `Detect() (Engine, error)` signature allows it, but the implementation must NOT return a non-functional Engine.

**Fix:** Add a “validate the selected engine can run a trivial command” step to `Detect()` for the override path.

### S5. Lima detection: docker-template vs nerdctl-template ambiguity
D4 says `LimaEngine` runs `lima nerdctl` against the user’s default instance. But lima instances can run different templates: the `docker` template provides a docker daemon inside the VM (accessed via `docker context`), not nerdctl. If a user has a docker-template lima instance, `lima nerdctl` will fail. The spec acknowledges this (Risk #3) but doesn’t specify how detection distinguishes them.

**Fix:** `Detect()` for lima must probe BOTH `lima nerdctl info` (nerdctl template) and fall back to the docker context. If neither works, fail closed with a message naming both templates. Alternatively, specify that only the nerdctl template is supported and detection explicitly checks for it.

### S6. `launch.go` provision() still creates a lima-specific code path after refactor
The `provision()` function in `launch.go` (:122-131) calls `install.DefaultDirs()`, then optionally creates a `LimaBackend`. After D5 collapses Backend→Detect, this whole code block simplifies to `runtime.Detect()`. But currently `provision()` also wires the egress pre-create (`eng.InternalNetwork()`) — this is correct and stays. The refactor must ensure the consent gate (`confirmLimaBlastRadius`) is deleted cleanly, as it currently blocks on TTY input. This is implicit in the spec but the spec doesn’t explicitly say “remove the first-run consent gate.”

**Fix:** Add an explicit deletion item for `confirmLimaBlastRadius` and `preferLimaBackend` functions in `launch.go`.

---

## NIT

### N1. The spec says `SAFESLOP_CONTAINER_BACKEND` “is deleted, not aliased” but doesn’t enumerate all references to it
Current references to the old env var (all must be removed):
- `internal/cli/cli.go:371` — `selectedContainerBackendName()`
- `internal/cli/cli.go:420` — sweepManagedOrphans guard
- `internal/cli/cli_gc_test.go:17` — test env set
- `internal/engine/container/launch.go:20-21` — `preferLimaBackend()`

### N2. `internal/engine/receipt/receipt.go:2-3` mentions `install.DesiredState` in a comment — not a code dependency, but should be updated for accuracy
The receipt package has no Go import of `install` — it only references it in documentation comments. These comments will become stale.

### N3. The spec says “Prefer deleting `Backend` outright in favor of `Detect() (Engine, error)`” but `launch.go:129` still declares `var backend runtime.Backend`
This is the only remaining Backend consumer outside of tests. If Backend is deleted, this line changes naturally. But worth noting that `provision()` currently calls `backend.Ensure()` and `backend.Teardown()` — both become no-ops for ambient runtimes (`Detect()` does no setup). The refactor must move `eng.InternalNetwork()` pre-create after `Detect()` without the Backend indirection.

### N4. Unclear whether `emacs/safeslop-profiles.el` stays — the spec says "No change to the catalog / image-build path" and profiles are a separate surface
The Install surface deletion reduces `safeslop-surface--order` from 3 entries to 2. The spec doesn’t explicitly say whether the profiles surface is retained. Given non-goals mention “catalog/image-build path” unchanged, profiles likely stays — but the surface navigation tests and tab strip tests assume exactly 3 surfaces.

### N5. The spec’s D8 says “live validation… does not block the hermetic merge” but podman/lima must stay “fail-closed/gated until it passes”
This puts the burden on the implementation to design the gate. As noted in BLOCKER B1, the gate mechanism is not designed in the spec.

---

## Summary

The spec correctly identifies the pieces to delete and has the right instinct about egress being security-critical. But it has **four blockers** that are design-level gaps, not implementation details:

1. **Podman egress is unverified** and the design doesn’t specify a fail-closed mechanism.
2. **`podman compose` is ambiguous** — the detection doesn’t verify compose capability.
3. **`engineForSession` can’t compile** after lima.go deletion without a redesign.
4. **Session.Backend change** isn't designed end-to-end.

The should-fixes are mostly about completeness of the deletion enumeration (10+ missing references across Emacs and Go) and DAG ordering risk. All are actionable with concrete file:line evidence.
