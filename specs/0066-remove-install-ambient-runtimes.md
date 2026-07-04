# 0066 — Remove the Install subsystem + managed-lima; drive ambient container runtimes

**Status:** reviewed + reworked (flo-evaluator-deepseek: 4 blockers + 6 should-fix, all folded — see specs/0066-review-deepseek.md) **Date:** 2026-07-02
Follows specs/0057 (removed the VM isolation *tier*). This removes the self-installer
subsystem and the safeslop-*managed* lima *runtime backend* (specs/0043/0044), and
pivots the container tier to whatever container runtime the user already has.

## Decision

safeslop **no longer installs, upgrades, or uninstalls anything**, and **no longer
boots or manages its own VM/runtime**. The container boundary runs on an **ambient,
user-provided container runtime**: docker (incl. OrbStack / Docker Desktop / any
docker-compatible CLI on PATH — today's behaviour), **podman**, or a **user-managed
lima** (`lima nerdctl`). safeslop detects one and drives it; it never provisions one.

`make install` (the Makefile target that builds + copies the `safeslop` binary and the
Emacs package) is **unaffected** — that is how the binary itself is built, not an
in-app feature.

## Why

- The self-installer (`install status/plan/apply/rollback`, `uninstall`) and the
  managed-lima backend exist to *provision safeslop's own toolchains/runtime*. That is
  a large supply-chain surface (pinned downloads, cosign verification, receipts,
  consent gate, a whole Emacs surface) for a capability most users don't need: they
  already run OrbStack/Docker Desktop, or can run podman/lima themselves.
- The managed-lima backend is **opt-in** (`SAFESLOP_CONTAINER_BACKEND=lima`),
  **undocumented** outside specs/0044, and **net-new** (landed 2026-06-22). Dropping it
  removes safeslop's only reason to carry the `install` supply-chain substrate
  (`Pin`/`Apply`/`Dirs`/`State`).
- Users who lack Docker can install podman or lima themselves (both are one `brew
  install` away) — safeslop meets them where they are instead of shipping a bespoke,
  pinned VM manager it must maintain and re-pin forever.

## Non-goals

- No change to the isolation model: container-only, read-only rootfs, cap-drop,
  no-new-privileges, squid egress-allowlist (specs/0057). This spec changes *which
  engine runs the container*, not *what the container is*.
- No change to the catalog / image-build path (`internal/engine/container/assets/*`,
  specs/0058/0059). Those stay.
- `make install` / dev-build flow unchanged.

## Decisions (D)

- **D1 — Delete the self-installer subsystem entirely.** Remove the Emacs Install
  surface, the `install` + `uninstall` CLI commands, and the `internal/engine/install`
  and `internal/engine/uninstall` packages. No replacement.
- **D2 — Delete the safeslop-managed lima backend.** Remove `LimaBackend`, its pinned
  `limactl`/`nerdctl`/`cosign` blobs, the first-run consent gate
  (`confirmLimaBlastRadius`/`NeedsConsent`), `nontouch.go`, and the
  `SAFESLOP_CONTAINER_BACKEND=lima` managed path. safeslop boots no VM.
- **D3 — Ambient-runtime detection.** Selection order:
  1. `SAFESLOP_CONTAINER_RUNTIME=docker|podman|lima` (explicit override); else
  2. auto-detect in fixed precedence **docker → podman → lima** (first whose CLI +
     *working* compose capability is present wins — CLI-on-PATH is necessary but not
     sufficient; see D4 for the per-runtime capability probes);
  3. none present/working → **fail closed** with an actionable error naming all three.
  - **Override is validated (S4):** an explicit `SAFESLOP_CONTAINER_RUNTIME` naming a
    runtime whose capability probe FAILS (CLI absent, daemon down, wrong lima template)
    is a **hard error**, never a silent fallback to another runtime — an override means
    "use exactly this or fail."
  - **Daemon-down:** `docker` CLI on PATH with no reachable daemon fails its `docker
    compose version` probe and is treated as not-available (detection moves on / fails
    closed), never as "docker selected but broken."
  (Renames the old `SAFESLOP_CONTAINER_BACKEND`; that env is deleted, not aliased —
  pre-alpha, no back-compat owed.)
- **D4 — One `Engine` per runtime; egress mechanism + capability probe declared per
  engine.** Keep the `runtime.Engine` seam (`Name`/`Argv`/`Command`/`InternalNetwork`).
  Every engine is **zero-config** (built from PATH/ambient state, no pinned paths or
  `install.Dirs`), so `runtime.Detect()` can reconstruct it anywhere (run, `down`,
  sweep — see D5/B3). Implementations:
  - `DockerEngine` — `docker …` / `docker compose …`; `InternalNetwork() == ""`
    (rootful/VM-backed docker + OrbStack honor compose inline `internal: true`).
    Unchanged from today's `HostDockerEngine`. Probe: `docker compose version` succeeds
    (implies a reachable daemon).
  - `PodmanEngine` — `podman …` / `podman compose …`; `InternalNetwork() ==
    "safeslop-internal"` (rootless podman uses RootlessKit + pasta/slirp4netns, so
    inline `internal: true` does **not** isolate egress — same failure class as
    rootless-nerdctl; must join a pre-created `--internal` network). Probe (B2): NOT
    merely `podman compose version` — `podman compose` may delegate to podman-compose
    (Python) / docker-compose v1 / the v2 plugin, each with different external-network
    semantics. The probe MUST assert the external-network split parses: render a minimal
    compose referencing an `external: true` network and run `podman compose -f - config`
    (or equivalent); reject podman if it does not. **Deny-tier requires live egress
    verification — see D6/D8.**
  - `LimaEngine` — `lima nerdctl …` against the user's **own default** lima instance
    (no `LIMA_HOME` override, no pinned `limactl`, no VM boot by safeslop);
    `InternalNetwork() == "safeslop-internal"` (rootless nerdctl, proven 2026-06-22).
    Probe (S5): `lima nerdctl info` must succeed — a lima on the *docker* template (no
    containerd/nerdctl) or with no started instance FAILS with actionable guidance
    (name both fixes: start an instance; use a containerd/nerdctl template). **Deny-tier
    requires live egress verification — see D6/D8.**
- **D5 — Collapse the `Backend` interface; single detection entry point (B3).** With
  nothing managed, drop `Pins()`/`StateDir()` and the `install.Dirs` parameter. Replace
  `runtime.Select(preferLima, dirs)` and the `Backend` two-stage shape with a single
  `runtime.Detect(policy NetworkPolicy) (Engine, error)` that runs the D3 precedence +
  D4 probes + D6 egress gate and returns a ready, zero-config Engine or a fail-closed
  error. **Every** engine-needing site routes through `Detect`, including the ones that
  today reconstruct a lima engine by hand: `engineForSession` (`cli.go:409`), `safeslop
  down` teardown (`cli.go:1177`), and the managed-orphan sweep — those currently build
  `LimaNerdctlEngine` from `install.DefaultDirs`, which after D2 no longer exists;
  because D4 engines are zero-config, `Detect` reconstructs the right engine for
  teardown too. `Ensure`→a pure availability check folded into the probe; `Teardown` is
  a no-op for every ambient runtime.
- **D6 — Egress isolation is a fail-closed capability gate INSIDE `Detect` (B1).**
  (AGENTS.md: *network limiting is first-class; never weaken defaults silently*.) For a
  `network: deny` profile safeslop MUST place the agent on a network with **no default
  route**; for engines with a non-empty `InternalNetwork()` it pre-creates the
  `--internal` network before `compose up` (already wired in `launch.go`). The gate is
  not advisory prose — it is enforced in `Detect(policy)`:
  - `policy == deny` **and** the detected engine is a rootless runtime whose no-egress
    guarantee is **not** recorded as live-validated (podman, lima) → `Detect` returns a
    **hard error** ("runtime X is not egress-verified for deny-tier; run the
    live-validation acceptance, or set `SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1` to accept
    the risk"). No deny profile launches on an unverified rootless runtime by default.
  - `policy == allow` → any detected engine is fine (egress is intended).
  - docker/OrbStack are on the verified list (rootful/VM-backed, `internal:true`
    honored); podman + lima join it only after D8 records a passing acceptance run.
  Fail-closed is the **default**, with one explicit, logged opt-in — no code path lets
  a deny profile silently get egress on a leaky runtime.
- **D7 — `Session.Backend` field (B4).** Repurpose from `"system"|"lima"` to the
  detected engine name (`"docker"|"podman"|"lima"`). `session.Create` (`session.go:97`)
  runs BEFORE engine detection, so its hardcoded `Backend:"system"` becomes
  **`Backend:""`** (unknown-until-provisioned); `recordSessionBackend` (`cli.go:377`)
  fills it from the detected engine at run time. On READ, a legacy on-disk
  `"backend":"system"` normalizes to `"docker"` (its only prior meaning). **Golden
  fixtures:** regenerate every session/status golden that pins `"backend"` — at minimum
  `session_test.go`'s `TestCreateDefaultsBackendSystem` (rename/retarget) and any
  status/list golden. Pre-alpha: regenerate, don't preserve.
- **D8 — Live-validation acceptance gate.** podman and lima deny-tier egress
  correctness cannot be proven by hermetic `make check`; it needs a real host. Each
  gets an acceptance test: on a host with that runtime, launch a `network: deny`
  profile and assert the agent (a) **cannot** reach a non-allowlisted host directly and
  (b) **can** reach an allowlisted host via squid. Until run+recorded, podman/lima ship
  under the D6 fail-closed/gated posture.

## Scope — deletions (exhaustive)

**Emacs (task EM) — full reference sweep (S2):**
- `emacs/safeslop-install.el`, `emacs/test/safeslop-install-test.el` — delete.
- `emacs/safeslop-surface.el` — drop the `(install "Install" "I" safeslop-install)`
  registry row (:47) and collapse the 3-surface order → 2 (tab-strip text/order at
  :11-12, :61, :65).
- `emacs/safeslop.el` — drop `safeslop-install` require + binding (:24, :36, :104).
- `emacs/safeslop-doom.el` — drop the Install autoload + evil bindings (:38, :54,
  :81-86).
- `emacs/safeslop-client.el` — drop Install entries from the safe-rerun-p patterns
  (:99-100).
- `emacs/test/safeslop-test.el` — drop Install-surface assertions (:348, :375, :425,
  :675-676); `emacs/test/safeslop-profiles-test.el` — the `I`→`safeslop-install`
  assertion (:58).
- `Makefile` — drop `safeslop-install-test.el` from the ERT list (:29).
- `emacs/README.md` (:26, :131), `emacs/examples/doom/config.el` — drop Install
  rows/refs.
  (Line numbers are review-sourced hints; the executor greps to confirm before editing.)

**CLI (task GO):**
- `internal/cli/cli.go` — remove `cmdInstall`, `installPlanResult`, `printTools`, the
  `cmdInstall()`/`cmdUninstall()` entries in `root.AddCommand` (line 71), and all
  `install.*` uses (status/plan/apply/rollback/freshness/DefaultDirs).
- `internal/cli/uninstall.go` — delete.
- `internal/cli/cli_install_apply_test.go`, `cli_install_envelope_test.go`,
  `cli_install_plan_test.go`, `cli_install_test.go`, `cli_uninstall_test.go` — delete.
- Registration test: drop `install`/`uninstall` from the expected command set.

**Engine (task GO):**
- `internal/engine/install/` — delete the whole package.
- `internal/engine/uninstall/` — delete the whole package.
- `internal/engine/container/runtime/lima.go`, `limayaml.go`, `nontouch.go`,
  `ensure_test.go`, `imagepolicy*.go` (lima blob policy), `lima_integration_test.go`,
  `limayaml_test.go` — delete/rework (anything managed-lima-only).
- `internal/engine/container/runtime/runtime.go`, `system.go`, `engine.go` — remove the
  `install` import; `Select`→`Detect`; add `PodmanEngine`/`LimaEngine`.
- `internal/engine/container/launch.go` (S6) — **delete** `preferLimaBackend`,
  `confirmLimaBlastRadius`, and the `install.DefaultDirs()` call + the whole
  backend-select block (~115-140); replace with a single `eng, err :=
  runtime.Detect(policyFromNetwork(network))`.
- `internal/cli/cli.go` `engineForSession` (:409) + down-teardown (:1177) — drop the
  lima branch; use `runtime.Detect()`.
- Delete/retarget tests tied to removed code (S3): `internal/engine/install/**` and
  `internal/engine/uninstall/**` (whole packages); `cli_install_*_test.go` +
  `cli_uninstall_test.go`; `runtime/{runtime_test.go,ensure_test.go,imagepolicy_test.go,
  lima_integration_test.go,limayaml_test.go}` (use `Select`/`install.Dirs`/managed-lima);
  `session_test.go`'s `TestCreateDefaultsBackendSystem` (D7); `cli_gc_test.go` backend
  assertion (:17); the command-registration test (drop `install`/`uninstall`). Fix
  fallout in `golden_base_test.go`, `tools_image_test.go` (any install refs).

**Docs (all tasks):**
- `README.md` — remove lines 86-87 (`safeslop install` / `safeslop uninstall`);
  document runtime detection + `SAFESLOP_CONTAINER_RUNTIME`; document that safeslop
  needs one of docker/podman/lima present.
- `skills/agent-sandbox-ops/SKILL.md` — remove install/uninstall workflow; add runtime
  requirement + detection note.

## Scope — refactor (task RT)

- `runtime.Detect() (Engine, error)`: honor `SAFESLOP_CONTAINER_RUNTIME`; else probe
  docker → podman → lima; return the Engine or a fail-closed error.
- `PodmanEngine`: `podman`/`podman compose`; `InternalNetwork()=="safeslop-internal"`.
  Verify `podman compose` capability the way `systemDockerAvailable()` checks
  `docker compose version`.
- `LimaEngine`: `lima nerdctl`; `Argv` = `["lima","nerdctl", …]`;
  `InternalNetwork()=="safeslop-internal"`. Availability = `lima` on PATH AND a default
  instance running AND `nerdctl` usable inside it.
- Egress wiring in `launch.go` is already engine-driven (`eng.InternalNetwork()` →
  pre-create `--internal`); no template change expected. Confirm `network create
  --internal` + `compose` external-network reference work under `podman` and `lima
  nerdctl` (D8).

## Verification

- **Hermetic (task V, `make check && make build`):** package deletions compile; command
  registry no longer lists install/uninstall; runtime detection unit tests
  (env-override + precedence + fail-closed) pass with an injected PATH/Runner; Emacs
  byte-compile clean + ERT green (Install surface tests gone, no dangling refs).
- **Live (task D8, operator-run, non-hermetic):** podman + lima deny-tier egress
  acceptance on a real host.

## Risks / open questions (for adversarial review)

1. **Podman rootless egress (security-critical).** Does a pre-created `--internal`
   podman network truly deny egress under RootlessKit/pasta, or does pasta/slirp still
   NAT it out (the exact bug called out for rootless-nerdctl in `compose.yml.tmpl`)? If
   `--internal` is insufficient on podman, we need an alternative (e.g. drop the egress
   bridge entirely + squid-only, or a netavark `--internal` specific check). **Must not
   ship podman deny-tier unverified.**
2. **`podman compose` variance.** `podman compose` may delegate to `podman-compose`
   (Python) or docker-compose; capability + `--internal`/external-network semantics
   differ. Detection must assert a working compose, not just `podman` on PATH.
3. **User-lima assumptions.** A user's lima may run the *docker* template (not
   containerd/nerdctl), or no instance is started. Detection must fail closed with a
   clear message rather than mis-drive it.
4. **`Session.Backend` value change** ripples into session JSON + any golden fixtures
   (D7). Enumerate every golden that pins `backend`.
5. **Fail-closed default** (D6) must be the *default*, not an afterthought: verify no
   code path lets a deny profile launch on an unverified/leaky runtime silently.

## Task DAG

- **EM** (Emacs Install-surface removal) ‖ **GR** (single **atomic** Go change — S1:
  the GO deletions [install/uninstall + managed-lima] AND the RT refactor [multi-runtime
  `Detect` + Podman/Lima engines + egress gate] land in ONE commit, because removing the
  `Engine` impls and adding the new ones cannot be split without the tree failing to
  compile in between; `DockerEngine` stays alive throughout).
- **V** (`make check && make build`) — after EM + GR.
- **D8** live validation — operator-run, tracked separately (does not block the
  hermetic merge; podman/lima stay D6 fail-closed/gated until it passes).
- **F** finalize — signed commit/merge/push (1Password wall → operator terminal per
  [[git-signing-1password-wall]]).
