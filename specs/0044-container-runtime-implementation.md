# 0044 — Container runtime (lima): implementation plan

**Status:** implemented (opt-in lima backend, live-validated) — see the "Live validation & architecture
correction" section. **Date:** 2026-06-22. Implements the FLO verdict
specs/0043-container-runtime-decision.md (winner **B**: a dedicated, pinned lima/colima vz VM manager,
engine pre-staged into the pinned image, behind a thin `container.Backend` seam). Built on the research
note specs/0042, the install arc (specs/0036–0039), the uninstall arc (specs/0040–0041), and the
consent gate (specs/0037).

A fresh agent should execute these top-to-bottom. Each task carries its own verification. **Refactor and
behaviour are separate tasks.** Run `make check` after each phase.

## Live validation & architecture correction (2026-06-22)

The engine slices were validated on a REAL lima 2.1.3 vz boot of the pinned alpine-lima `.iso` on
darwin/arm64 (macOS 26.5.1). The boot **superseded three assumptions** in the original plan below — the
"docker-compatible socket, compose path unchanged" design (next section) does **not** survive contact
with the minimal Alpine guest, and jojo chose **Path 2** (keep the minimal verified image, change the
engine + tier):

- **No Docker-API socket.** The Alpine guest is musl + OpenRC: glibc `dockerd` static binaries don't
  exec, and both docker's and nerdctl's rootless *setuptools* require systemd. So the engine is
  **rootless containerd/nerdctl** (nerdctl-full is musl-static and brings up rootless via
  `containerd-rootless.sh`, no systemd). nerdctl speaks the containerd API, not the Docker API.
- **Therefore the container tier must move docker→nerdctl** (NOT "unchanged"): `Ensure` returns an
  `Engine` (in-guest `nerdctl` for lima, host `docker` for SystemBackend), and the tier will run the
  engine instead of a hardcoded `docker`. This contradicts the "compose unchanged" line below — that
  line is **obsolete**.
- **limactl is a tool tree, not a bare binary** (resolves its guest agent + templates relative to its
  path) → new `install.FormatToolTree` (see the install fix commit).

**Status: implemented (opt-in), live-validated.** Phases 0,1,2,3,5; the `vmOpts.vz.rosetta`/`mountPoint`/
`LIMA_HOME` schema corrections; `FormatToolTree` (limactl needs its tree); the container tier routed
through a `runtime.Engine` seam; Phase 4 fully — `container.provision` selects the backend
(`SAFESLOP_CONTAINER_BACKEND=lima` opt-in, never an automatic boot), `Ensure(workspace)` mounts the repo
writable at its identity path, the **egress fix** (rootless nerdctl ignores compose `internal: true`, so
a pre-created `--internal` network is used — the integration test asserts the agent cannot egress
directly), the first-run **consent gate** (4.2, typed confirmation itemising the blast radius), and the
**VM-disk receipt** (4.3, `lima-runtime` Path A = StateDir as a sha-less dir). The integration test
(boot → workspace write-back → egress block → teardown) PASSES on real lima.

**Notes / refinements left:** the container-tier compose still runs `docker compose run` for the *agent*
via the engine argv (validated TTY passthrough) but the full in-guest build of the agent image under
nerdctl is exercised only by the live path, not yet by an automated build test; and `safeslop uninstall`
of `lima-runtime` trashes StateDir without first stopping a running VM, so run `safeslop down` before
uninstall while the VM is up (the down path reaps it). The embedded cockpit uses its own preflight RPC
rather than the TTY consent prompt. The tart-Linux north star remains a later, separate backend impl.

## What this delivers and what it does NOT change

safeslop's `internal/engine/container` package today **assumes** a docker daemon + `docker compose` are
already on PATH (`container.go:39` `Available()`) and runs the agent via `docker compose run` — it does
not provision an engine. This plan adds a **container-runtime provider**: lima boots a rootless,
hardened, pinned vz Linux VM that exposes a docker-compatible socket, and the existing compose path runs
against it via `DOCKER_HOST`. The compose/squid/policy code (`compose.go`, `policy.go`, `launch.go`'s
compose assembly) is **unchanged** — it just gets a socket to talk to.

## Load-bearing decisions (from specs/0043 — do not re-litigate)

- **lima now**, behind a `container.Backend` interface so the **tart-Linux north star** is a one-impl
  migration, not a rewrite (graft #2). The tart-Linux backend is a LATER, separately-triggered slice —
  **not in this plan**.
- **Engine pre-staged, no unverified in-guest fetch** (graft #1): containerd/nerdctl/cosign are pinned
  Linux-arm64 artifacts that lima installs from a **local** source at provision; the guest does **zero**
  internet fetch for the engine (no `apt`/`curl`). This is the lighter, honest reading of "bake into the
  image" — it closes the supply-chain gap without safeslop becoming a Linux-distro builder.
- Non-negotiables: rootless in-VM engine · host `$HOME` unmounted by default (workspace-only if any,
  UID/GID-mapped) · user-mode networking (no root `socket_vmnet`) · `docker.sock` kept in-VM, exposed to
  the agent only as a scoped per-session `DOCKER_HOST` · VM OS image pinned as a Path A artifact · VM disk
  receipted + first-start gated as a second consent event · agent pulls forced to sha256 digests + cosign
  policy · NO Docker Desktop / OrbStack managed install · reuse Path A / receipt (0041) / consent (0037).

## Scope / off-limits

- **Do NOT** touch `compose.go`, `policy.go`, `squid.conf.tmpl`, the Dockerfiles, or the agent egress
  model — this plan only provides the engine socket they consume.
- **Do NOT** build the tart-Linux backend (north star; later slice). Only the `Backend` interface + the
  lima impl + a trivial `SystemBackend` (existing behaviour) ship here.
- **Do NOT** managed-install or uninstall Docker Desktop / OrbStack. No `--force`.
- **Do NOT** fabricate pin values. Tasks that add a `Pin` require a REAL download to compute the
  sha256 (same discipline as every existing entry in `desired.go`); those tasks are flagged
  **[REAL-ARTIFACT]** and their VERIFY checks pin *shape* (64-hex, non-latest), not correctness — a human
  or a fetch step supplies the true digest, dated in the comment like the existing pins.

---

## Phase 0 — Place pinned non-executable artifacts (new install capability)

The VM OS image and the Linux engine tarballs are NOT executables placed in BinDir — they are verified
blobs cached at a known path. install today has no such format.

- [ ] **0.1 Add `FormatBlob` — a verified artifact placed at a cache path**
  FILE:     `internal/engine/install/plan.go`, `internal/engine/install/apply.go`
  CHANGE:   Add `FormatBlob = "blob"` to the Format consts (`plan.go:12`-block) with a comment: a
            fetched+sha256-verified non-executable artifact written under the safeslop cache dir, not
            chmod-+x'd, not on PATH. Accept it in `ValidateDesired`'s format switch (`plan.go:~86`). In
            `applyOne` (`apply.go:139` switch), add a `case FormatBlob:` that writes the verified bytes to
            `<CacheDir>/<name>` (add a `CacheDir` field to `Dirs`, default `~/.cache/safeslop`, mirroring
            how `ReceiptPath` was added) via `commitStaged`, mode 0o644, and returns a `receipt.File`
            with the blob path + sha256. No extraction.
  VERIFY:   `go test ./internal/engine/install/`
  EXPECTED: PASS — existing tests unaffected; build clean.

- [ ] **0.2 Test: a FormatBlob pin is placed + receipted, not made executable**
  FILE:     `internal/engine/install/apply_blob_test.go` (new)
  CHANGE:   Drive `Apply` with a fake fetcher for one `FormatBlob` Pin, `Dirs.CacheDir` + `ReceiptPath`
            under `t.TempDir()`. Assert: the blob lands at `CacheDir/<name>` with mode 0o644 (NOT
            executable), and the receipt records a Path A `File` with the matching sha256.
  VERIFY:   `go test ./internal/engine/install/ -run Blob`
  EXPECTED: PASS.

---

## Phase 1 — Pin the runtime artifacts

- [ ] **1.1 [REAL-ARTIFACT] Pin `limactl` in DesiredState**
  FILE:     `internal/engine/install/desired.go`
  CHANGE:   Add a `Pin{Name:"limactl", Kind:"runtime", Format:FormatBinaryTarball, Version:<real>,
            URL:<lima signed GitHub release darwin-arm64 tarball>, SHA256:<real>,
            Provenance:ProvenanceVendor}`. Comment: fetched from lima's signed upstream release (NOT a
            Homebrew bottle — bottles lack SLSA/cosign provenance), sha verified on <date>; note lima also
            publishes a SLSA attestation (a later `Sig`-style verification slice). `installBinary`'s
            `findFile` resolves `bin/limactl` in the tarball.
  VERIFY:   `go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed`
  EXPECTED: PASS — manifest still fully pinned (64-hex sha, exact version).

- [ ] **1.2 [REAL-ARTIFACT] Pin the VM OS image (FormatBlob)**
  FILE:     `internal/engine/install/desired.go`
  CHANGE:   Add a `Pin{Name:"lima-guest-image", Kind:"runtime", Format:FormatBlob, Version:<real>,
            URL:<a specific lima-published Ubuntu/Alpine cloud image, arm64>, SHA256:<real>,
            Provenance:ProvenanceVendor}`. Comment with the image's published digest + verify date. This
            is the artifact lima starts against locally (Phase 3), closing the "first-start pulls an
            unpinned multi-GB image" gap.
  VERIFY:   `go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed`
  EXPECTED: PASS.

- [ ] **1.3 [REAL-ARTIFACT] Pin the Linux engine artifacts (FormatBlob ×N)**
  FILE:     `internal/engine/install/desired.go`
  CHANGE:   Add `FormatBlob` pins for the **linux-arm64** release tarballs of `containerd`, `nerdctl`
            (the full/`nerdctl-full` bundle includes containerd+buildkit+CNI+rootlesskit — prefer it to
            cut the count), and `cosign`, each sha256-pinned from the upstream signed release. Comment
            each with verify date. These are staged into the guest at provision (Phase 3) so the VM never
            fetches the engine from the internet.
  VERIFY:   `go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed && grep -c 'FormatBlob' internal/engine/install/desired.go`
  EXPECTED: PASS; grep ≥ 2 (image + engine bundle).

---

## Phase 2 — The container-runtime backend seam

- [ ] **2.1 Define the `Backend` interface**
  FILE:     `internal/engine/container/runtime/runtime.go` (new package)
  CHANGE:   New package `runtime`. Define `type Backend interface { Name() string; Ensure(ctx context.Context, emit func(string)) (dockerHost string, err error); Teardown(ctx context.Context) error; Pins() []install.Pin; StateDir() string }`. `Ensure` is idempotent: provisions + boots if needed and returns a `DOCKER_HOST` value (a unix socket path or tcp endpoint) the compose path can use; it NEVER returns `/var/run/docker.sock`. Package doc cites specs/0043.
  VERIFY:   `go build ./internal/engine/container/runtime/`
  EXPECTED: compiles.

- [ ] **2.2 `SystemBackend` — preserve today's behaviour**
  FILE:     `internal/engine/container/runtime/system.go` (new)
  CHANGE:   `SystemBackend` whose `Ensure` checks `container.Available()`-equivalent (docker + `docker
            compose version` on PATH) and returns an empty `dockerHost` (meaning "use the ambient docker
            context") or errors if absent; `Pins()` returns nil; `Teardown` is a no-op; `StateDir` "".
            This is the backend for users who already run OrbStack/Docker Desktop — safeslop uses their
            engine, never installs/removes it.
  VERIFY:   `go test ./internal/engine/container/runtime/ -run System`
  EXPECTED: PASS (test in 2.3).

- [ ] **2.3 Test: backend selection + SystemBackend probe**
  FILE:     `internal/engine/container/runtime/runtime_test.go` (new)
  CHANGE:   Add `func Select(preferLima bool) Backend` (lima when asked or when no system docker; else
            system) and test it: with `PATH=""`, `SystemBackend.Ensure` errors; `Select` returns the lima
            backend when `preferLima` true. No real VM — assert selection + the empty/absent-docker paths
            only.
  VERIFY:   `go test ./internal/engine/container/runtime/`
  EXPECTED: PASS.

- [ ] **2.4 `LimaBackend` skeleton (no real boot yet)**
  FILE:     `internal/engine/container/runtime/lima.go` (new)
  CHANGE:   `LimaBackend` with `Name()="lima"`, `StateDir()` = `~/.config/safeslop/lima` (owned, not the
            user's `~/.lima`), `Pins()` = the three pins from Phase 1 (looked up from
            `install.DesiredState()` by name). `Ensure`/`Teardown` shell out to the pinned `limactl`
            (resolved from `Dirs.BinDir`) — leave the actual `limactl start` call behind a small injected
            runner (like `uninstall.Runner`) so unit tests don't boot a VM. The real boot is exercised
            only by the Phase 6 integration test.
  VERIFY:   `go build ./internal/engine/container/runtime/`
  EXPECTED: compiles; `grep -n 'func.*LimaBackend.*Ensure' internal/engine/container/runtime/lima.go` matches.

---

## Phase 3 — Owned lima config + fail-closed invariant gate

- [ ] **3.1 Generate the owned lima instance YAML**
  FILE:     `internal/engine/container/runtime/limayaml.go` (new) + `assets/lima.yaml.tmpl` (new, embedded)
  CHANGE:   `renderLimaYAML(cfg)` produces the instance config safeslop OWNS (never `limactl create`
            defaults): `vmType: vz`, `rosetta: {enabled: true, binfmt: true}`, `images: [{location:
            <local CacheDir path to the Phase-1.2 image>, arch: aarch64, digest: sha256:<pinned>}]`,
            `mountType: virtiofs`, `mounts: []` (NO `$HOME`), `networks: []` (vz user-mode NAT, no
            `socket_vmnet`), `ssh: {forwardAgent: false}`, `containerd: {system: false, user: true}`
            (rootless), and a `provision:` step that installs the engine from the **local** pinned tarballs
            (Phase 1.3) copied in — containing NO `curl`/`apt`/`wget`. Verify the exact key names against
            the pinned lima version's schema docs.
  VERIFY:   `go test ./internal/engine/container/runtime/ -run YAML`
  EXPECTED: PASS (test in 3.2).

- [ ] **3.2 Test: rendered YAML satisfies every hardening invariant**
  FILE:     `internal/engine/container/runtime/limayaml_test.go` (new)
  CHANGE:   Assert the rendered YAML: `mounts:` is empty (or solely an opted-in workspace, UID/GID-mapped);
            `networks:` empty; `vmType: vz`; `containerd.user: true` and `system: false`;
            `images[].location` is a local path under the cache dir with a `digest:`; and the `provision:`
            script contains none of `curl`, `apt`, `wget`, `http://`, `https://` (no in-guest internet
            fetch). Parse the YAML (`gopkg.in/yaml.v3` if vendored, else string assertions).
  VERIFY:   `go test ./internal/engine/container/runtime/ -run YAML`
  EXPECTED: PASS.

- [ ] **3.3 Fail-closed pre-start invariant gate**
  FILE:     `internal/engine/container/runtime/lima.go`
  CHANGE:   `assertInvariants(yamlBytes) error` run inside `Ensure` immediately before any `limactl start`
            (and re-run after a `limactl` version bump, keyed off the receipted pin version): fail closed
            unless mounts empty/workspace-only, every `images[].location` is local + digest-matched,
            networks empty, `containerd.user==true && system==false`. A drift trips the gate at start, not
            at escape time (mirrors `ValidateDesired` / `slop-pinning` discipline).
  VERIFY:   `go test ./internal/engine/container/runtime/ -run Invariant`
  EXPECTED: PASS (test in 3.4).

- [ ] **3.4 Test: the invariant gate rejects every relaxation**
  FILE:     `internal/engine/container/runtime/lima_test.go` (new)
  CHANGE:   Cases that each MUST error: a `~`/`$HOME` mount present; a remote `images[].location`
            (`https://…`); a non-empty `networks:`; `containerd.system: true`; a missing image digest.
            The clean owned YAML passes.
  VERIFY:   `go test ./internal/engine/container/runtime/ -run Invariant`
  EXPECTED: PASS.

---

## Phase 4 — Wire the backend in + consent + receipt

- [ ] **4.1 Route `container.provision` through the backend**
  FILE:     `internal/engine/container/launch.go`
  CHANGE:   In `provision` (`launch.go:52`), before assembling the compose argv, select a `runtime.Backend`
            (lima when system docker is absent or the profile/config asks; else `SystemBackend`), call
            `Ensure(ctx, emit)`, and if it returns a non-empty `dockerHost`, set it in the compose
            command's environment (`DOCKER_HOST=<…>`) so `docker compose` targets the lima socket. Empty
            dockerHost = ambient docker (today's behaviour) — unchanged. Do NOT alter the compose
            assembly itself.
  VERIFY:   `go build ./... && go test ./internal/engine/container/`
  EXPECTED: PASS — existing container compose tests still green (they don't boot docker).

- [ ] **4.2 Second consent gate at first lima provision**
  FILE:     `internal/engine/container/runtime/lima.go` + the consent surface (mirror `cockpitPreflightHostLaunch` in `internal/cli/cli.go:~437` and `tools.InstallPreview`/`NeedsConsent`)
  CHANGE:   On the FIRST `Ensure` for a profile (no receipt yet), require a consent event itemising the
            blast radius: the pinned VM image (name + sha + size), that it boots a vz Linux guest, the
            disk path, and "no host $HOME mount / user-mode net / socket in-VM". CLI: typed-confirmation
            (reuse the `confirmationMatches` helper from `internal/cli/uninstall.go`); cockpit: a
            `NeedsConsent` preflight RPC like host-launch. Subsequent starts skip the gate (receipt
            present).
  VERIFY:   `go test ./internal/engine/container/runtime/ -run Consent`
  EXPECTED: PASS (gate logic unit-tested via the injected runner + a fake consent reader).

- [ ] **4.3 Receipt the VM disk + pinned artifacts**
  FILE:     `internal/engine/container/runtime/lima.go`
  CHANGE:   After a successful first `Ensure`, record a `receipt.Entry{Tool:"lima-runtime", Path:"A",
            Files: [...]}` where the pinned blobs (image, engine) are hashed `File`s and the live VM disk
            dir (`StateDir()`/<instance>) is a **sha-less directory** `File` (the existing `f.SHA256==""`
            branch in `uninstall.applyPathA` removes it like an `.app` bundle — a live disk mutates every
            boot and cannot be hash-verified; the immutable pinned seed image keeps the hashed guarantee).
            `Teardown` + `uninstall` then reverse it symmetrically.
  VERIFY:   `go test ./internal/engine/container/runtime/ -run Receipt`
  EXPECTED: PASS (assert the recorded Entry shape with HOME isolated).

---

## Phase 5 — Image-pull policy + explicit non-touch

- [ ] **5.1 Digest-pin + cosign policy for agent image pulls**
  FILE:     `internal/engine/container/runtime/imagepolicy.go` (new) + the provision step / a guest
            `policy.json`
  CHANGE:   Generate a containerd/nerdctl signature `policy.json` staged into the guest that rejects
            unsigned images, and a pull rewrite that rejects mutable tags (`:latest`) — only `@sha256:`
            digests pass (nerdctl `--verify=cosign`). Unit-test the generated `policy.json` content + the
            tag-rejection helper (`rejectsMutableTag("node:latest")==true`, `…@sha256:…==false`).
  VERIFY:   `go test ./internal/engine/container/runtime/ -run ImagePolicy`
  EXPECTED: PASS.

- [ ] **5.2 Explicit non-touch of Docker Desktop / OrbStack**
  FILE:     `internal/engine/container/runtime/lima.go` (+ reuse `receipt.NoteUnmanaged`)
  CHANGE:   When selecting the lima backend, if Docker Desktop / OrbStack is detected
            (`install.Status`/tools catalog), `receipt.NoteUnmanaged("docker"|"orbstack", path)` and
            surface it as untouched — never managed-install or uninstall it. No flag overrides this.
  VERIFY:   `go test ./internal/engine/container/runtime/ -run Unmanaged`
  EXPECTED: PASS.

---

## Phase 6 — Integration idempotency + close-out

- [ ] **6.1 Integration test: provision → run a container → teardown (gated)**
  FILE:     `internal/engine/container/runtime/lima_integration_test.go` (new; first line `//go:build integration`)
  CHANGE:   Guard with the `integration` tag AND a `limaAvailable()` skip (limactl present + vz). Test:
            `install apply` the three pins → `LimaBackend.Ensure` boots the VM → over the returned
            `DOCKER_HOST`, run a digest-pinned container that prints a token and assert it → assert the
            engine socket is NOT on the host (`/var/run/docker.sock` absent/unchanged) → `Teardown` →
            assert the VM + disk are gone and the receipt is cleared. Mirrors the specs/0041 VM
            idempotency test; runs only via `make test-integration` (already wired,
            `.woodpecker/integration.yml`).
  VERIFY:   `go test -tags integration -c -o /dev/null ./internal/engine/container/runtime/`
  EXPECTED: compiles under the tag; excluded from the default `go test ./...`.

- [ ] **6.2 README + status + memory**
  FILE:     `README.md`, `specs/0044-container-runtime-implementation.md`
  CHANGE:   Document that safeslop can provision a hardened lima container runtime (vz, rootless, in-VM
            socket, pinned image/engine, receipted, gated) and that it uses an existing OrbStack/Docker
            Desktop untouched if present. Flip this plan's Status to implemented with the merge commits;
            save the load-bearing decisions to project memory.
  VERIFY:   `make check && make build && fish scripts/slop-pinning.fish`
  EXPECTED: all green; no `latest` introduced.

---

## Execution notes

- **Order:** Phase 0 before 1 (FormatBlob is needed to pin the image/engine). Phase 2 (interface) before 3/4. Phase 4 consumes 1–3. Phase 5/6 last. Within a phase, distinct new files are independent.
- **Real artifacts:** do the three **[REAL-ARTIFACT]** pins by downloading the actual releases and recording the true sha256 + a verify date in the comment — exactly as every existing `desired.go` entry was done. Never invent a digest; `slop-pinning` + `TestDesiredStateIsFailClosed` enforce shape, not truth.
- **Suggested commit boundaries:** Phase 0 (FormatBlob), Phase 1 (pins), Phase 2 (backend seam), Phase 3 (lima config + gate), Phase 4 (wiring + consent + receipt), Phase 5 (policy + non-touch), Phase 6 (integration + docs). Atomic, scoped; never `git add -A`.
- **Lima specifics** (config keys, the local-image + provision mechanics, the socket-forward path) MUST be verified against the pinned lima version's docs during 3.1/2.4 — flagged inline; the Go-side scaffolding (FormatBlob, Backend, invariant gate, receipt, consent, policy) is exact.
- Hand to **executing-plans** (sequential — later phases consume the Backend type + the pins).
