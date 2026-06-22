# 0042 — The safest container runtime for safeslop — research-derived design note (ayo)

**Status:** design note (research-derived; precedes any implementation). **Date:** 2026-06-22.
Resolves the parked "Docker daemon" decision from specs/0040 / `install/desired.go` ("docker is
genuinely unmanaged"). Sibling of the install arc (specs/0036–0039), the consent + blast-radius gate
(specs/0037), the uninstall arc (specs/0040–0041), and the VM isolation tier (`internal/engine/vm`,
tart). Produced by a cross-model `ayo` pass (method footer below).

## Headline — the load-bearing insights

1. **A container runtime is intrinsically VM + image system-state, never a single pinnable binary.**
   The `docker`/`nerdctl`/`podman`/`colima`/`lima` *CLIs* are static Go binaries that fit Path A
   perfectly — but they are **inert**. The working runtime is a Linux VM plus a multi-GB OS image that
   is fetched *at first `start`*, after the install gate, from an unpinned source. Pinning the CLI does
   **nothing** for the runtime's real supply chain. (Cross-model consensus, all four families.) So the
   safe design must pin the *VM image* as a first-class Path A artifact and treat first-start as a
   **second** consent + receipt event — not just pin a binary and call it done.

2. **The safest posture keeps the entire engine — and especially its socket — inside a
   safeslop-owned VM boundary, with no privileged macOS host state.** `docker.sock` is not an API; it
   is host-root-equivalent (`docker run -v /:/host`). Docker Desktop and OrbStack are categorically out
   for a *managed* install: GUI casks with privileged LaunchDaemons (`com.docker.vmnetd`,
   `com.docker.backend`), network/system extensions, and a documented-but-incomplete uninstall —
   structurally incompatible with "clean, receipt-driven, never-remove-what-you-didn't-install."
   A CLI-only, **rootless** engine in a **vz** Linux VM, with **no host `$HOME` mount**, **user-mode
   networking** (no root `socket_vmnet` daemon), and the socket **never exposed to the agent by
   default**, has the smallest host blast radius available on darwin-arm64.

3. **The recommendation: pin a CLI-only manager (lima/colima) AND its VM OS image via Path A; run a
   rootless engine in a vz VM; gate + receipt the VM disk; keep the socket in-VM.** With a north-star
   of collapsing onto the Virtualization.framework that safeslop *already* runs via tart, so there is
   eventually one hypervisor and one receipt model. Docker Desktop / OrbStack stay **unmanaged +
   honest** — detected, surfaced, never touched.

## Should safeslop manage a container runtime at all?

Yes, but only on its own terms. The container isolation tier needs *a* docker-compatible engine, and
leaving it fully unmanaged means either (a) reusing the human's Docker Desktop — where an agent's
`docker system prune -a` nukes the developer's unrelated work, and the host socket is a host escape —
or (b) no container tier at all. The safe middle is a safeslop-**owned**, VM-confined, rootless engine
whose entire blast radius is a single user-owned directory + one VM disk image it can receipt and
reverse. That fits the existing Path A / receipt / consent machinery if — and only if — the VM image is
pinned and first-start is gated.

## Triaged lessons

`[C]` = cross-model consensus (high confidence); `[U:x]` = unique to one lane (higher novelty/risk).
HIGH = act on it; MEDIUM = actionable, needs design; DEFERRED = noted, not acted on.

### Supply chain: the binary is Path A, the runtime is not
- **HIGH [C, all]** — Pin the engine/manager CLI via Path A (static darwin-arm64 Go binary: lima,
  colima, podman, nerdctl — sha256 + the receipt-driven uninstall already built). *Surface:* [Path A].
- **HIGH [C, all]** — Close the "VM-image fetch gap": pre-fetch, sha256-verify, and inject the Linux VM
  OS image as a pinned artifact (`limactl … --set` / `colima --image` / podman image ref), instead of
  letting `start`/`init` pull an unpinned multi-GB image from a CDN after the gate. *Surface:*
  [Path A] [Image/registry] Constraint: the chosen tool must accept a local, verified image path.
- **HIGH [C: Kimi/GLM]** — Do not trust Homebrew bottles as the Path A source: bottles ship a download
  sha256 but **no** SLSA/cosign build provenance — you inherit Homebrew's CI, not the maintainer's.
  Fetch the upstream signed GitHub release directly. *Surface:* [Path A] [Stay unmanaged + honest].
- **MEDIUM [U: Gemini]** — Prefer tools that publish SLSA L3 provenance (lima, nerdctl do) and verify
  the attestation, not just the sha, in safeslop's fetch routine. *Surface:* [Path A]. FLO/later: worth
  building a `slsa-verifier` step into the fetch path generally (applies beyond containers).

### Socket + daemon isolation (the actual escape surface)
- **HIGH [C, all]** — Never give the agent `docker.sock` / `DOCKER_HOST` by default; the socket is a
  host-filesystem capability, not an API. If the engine runs in a VM, keep the socket *inside* the VM;
  expose it to the agent only via an explicit, gated, per-session scoped path — never a symlinked
  `/var/run/docker.sock`. *Surface:* [Socket/daemon isolation] [Consent gate].
- **HIGH [C, all]** — Run the in-VM engine **rootless**, so a container escape lands as an unprivileged
  VM user, not VM-root. *Surface:* [Socket/daemon isolation]. Constraint (Kimi): rootless is a
  *defense-in-depth inner layer*, NOT the boundary — the real boundary is the hypervisor, because the
  VM user can still reach any host mount.
- **HIGH [C: Kimi/GLM, + Gemini VirtioFS]** — Do **not** mount the host `$HOME` into the VM (the
  lima/colima/Docker default reverse-sshfs/virtiofs mount). That mount is the escape: a compromised
  container writes `~/.ssh/authorized_keys` on the host through it. Default to **no mount**; if a
  workspace mount is needed, scope it to the agent's workspace only, with explicit UID/GID mapping.
  *Surface:* [Socket/daemon isolation] [Consent gate].
- **MEDIUM [U: GLM]** — Force user-mode networking (gvproxy/slirp); bridged networking needs a root
  `socket_vmnet` system LaunchDaemon — that's a Path B host mutation, so only behind its own consent.
  *Surface:* [Consent gate] [Stay unmanaged + honest].

### Reject the GUI casks for managed install
- **HIGH [C, all]** — Docker Desktop and OrbStack are Path-A-incompatible: privileged helpers,
  network/system extensions registered with the kernel, and an uninstall that leaves LaunchDaemons,
  `~/Library/Group Containers/com.docker`, keychain certs, and routing state. safeslop cannot receipt
  or cleanly reverse any of it. *Surface:* [Stay unmanaged + honest] [Path B].
- **HIGH [C, all]** — If Docker Desktop/OrbStack is detected on the host, **never** touch it (no reuse,
  no "tidy", no uninstall) — route the agent to safeslop's own isolated runtime and surface the
  existing one as untouched, unmanaged system state (mirrors specs/0041's untouched list). *Surface:*
  [Stay unmanaged + honest] [Consent gate].

### Reuse tart / Virtualization.framework — the contested call
- **MEDIUM/HIGH [contested]** — Run the engine inside a safeslop-managed **vz** Linux VM so there is
  one hypervisor and one receipt model, rather than bolting a second VM manager next to tart. *Surface:*
  [Reuse tart] [Path A]. See the contradiction section below — this is the one genuinely debated lesson.
- **HIGH [C: GLM/Gemini, host]** — Require the **vz** backend (Virtualization.framework), not QEMU:
  near-native speed + Rosetta2 amd64 translation, no kernel module, smaller third-party surface than a
  large unauditable QEMU binary. *Surface:* [Reuse tart]. Counter-nuance (Kimi): vz shares a hypervisor
  interface with the host kernel (smaller TCB but a host-adjacent boundary); QEMU is a stronger process
  boundary but a heavier artifact. Net: vz is the right default on darwin-arm64; note the tradeoff.
- **MEDIUM [U: Gemini]** — If amd64 images are needed, inject RosettaLinux into the guest
  (`softwareupdate --install-rosetta` dependency); fail cleanly if absent. *Surface:* [Reuse tart]
  [Image/registry].
- **MEDIUM [U: Gemini]** — Pre-seed a registry mirror / local image cache: anonymous Docker Hub is
  100 pulls / 6h / IP, and an agent in a retry loop exhausts it and fails cryptically. *Surface:*
  [Image/registry] [Reuse tart].

### Image-pull supply chain (decoupled from the binary)
- **HIGH [C, all]** — Install-time and run-time supply chains are **separate**: a cosign-verified engine
  binary still `docker pull ubuntu:latest`-es an unsigned, mutable image. Rewrite agent pulls to **sha256
  digests** (reject `:latest`) and enforce a **cosign/sigstore `policy.json`** in the VM so unsigned
  images are refused at pull time. *Surface:* [Image/registry] [Consent gate].
- **MEDIUM [U: GLM]** — nerdctl integrates cosign natively (`nerdctl … --verify=cosign`); Docker needs
  enterprise plugins. A bias toward nerdctl/containerd if image verification is core. *Surface:*
  [Image/registry] [Socket/daemon isolation].

### Consent + receipt honesty
- **HIGH [C, all]** — Gate the blast radius at **first engine start**, not just safeslop-install time:
  the heavy VM + image fetch is deferred, so install-time consent (a small binary placed) misrepresents
  the real network + multi-GB-disk cost. Treat `start`/`init` as a nested install event with its own
  consent. *Surface:* [Consent gate] [Path B].
- **HIGH [C, all]** — Receipt the **VM disk image** (10GB+ sparse file) and all deferred state, not just
  the CLI binary — else uninstall leaves hidden storage bloat and violates symmetric removal. Localized
  single-dir state (`~/.lima`, `~/.colima`, `~/.local/share/containers`) makes this a clean
  receipt-driven `rm`, *if* the receipt records the dir. *Surface:* [Path A] [Stay unmanaged + honest].
- **MEDIUM [C: GLM/Kimi]** — Scope the engine to the agent via process-env `DOCKER_HOST` only; never
  mutate the user's `~/.docker/config.json` or `docker context`. *Surface:* [Stay unmanaged + honest].

### Deferred / emerging
- **DEFERRED [U: host]** — Apple's open-source **`container`** framework (one lightweight vz VM *per
  container*, daemonless, macOS 26+) is the strongest isolation model on the horizon, but it is new, not
  docker-API-compatible, and version-gated. Re-evaluate once mature; don't build on it now. *Surface:*
  [Reuse tart].
- **DEFERRED [U: Gemini]** — Map VirtioFS UID/GID explicitly to avoid agent file-permission deadlocks —
  relevant only if/when a workspace mount is enabled (default is no mount). *Surface:* [Socket isolation].

## Contradiction surfaced & resolved — reuse tart, or a dedicated VM manager?

- **Gemini & GLM:** reuse tart — boot a pinned Linux VM (ship a sha256'd `rootfs`/image), run
  containerd+nerdctl inside, forward the socket. One hypervisor, no second VM manager.
- **Kimi:** do **not** — "tart runs macOS guests; it has no Linux container stack, no containerd
  integration, no socket forwarding. Reusing it makes safeslop a Linux-distro builder maintaining an
  in-VM engine — a whole new supply chain + receipt model."
- **Factual error flagged (cross-model value):** GLM claimed "Apple's vz strictly requires Linux kernel
  images cryptographically signed by Apple to boot." That is **false** — Virtualization.framework boots
  arbitrary Linux kernels/initrds (Kimi and the host lane both state this correctly). Discarded; do not
  let it shape the design.

**Resolution.** Both framings are half-right. Kimi is right that *today's tart usage* is macOS-guest
oriented and that an in-VM engine is a real new supply chain — it is **not** "free reuse." Gemini/GLM
are right that the *Virtualization.framework capability safeslop already depends on* is the cleanest
single-boundary home for the engine, and that recent tart does run Linux guests. So: **start with a
purpose-built, pinnable CLI manager (lima/colima) that drives its own vz VM** — mature, container-ready,
least to build — while **pinning its VM image** and **receipting its VM dir**. Hold **"collapse onto a
single tart-Linux VM + one receipt model"** as the north star, and hand *that* specific
build-vs-reuse decision to a **FLO** when it's scheduled (it trades engineering cost against a unified
trust boundary — a genuine design contest, not a research gap).

## Actionables (numbered → surface / file)

1. **Pick lima (or colima) as the pinned manager**, fetched from its signed upstream GitHub release (not
   a Homebrew bottle), sha256 + codesign-verified, added to `install.DesiredState()` as a Path A pin
   with `vz` as the required backend. → [Path A] / `internal/engine/install/desired.go`.
2. **Pin the Linux VM OS image** as a Path A artifact (pre-fetched + sha256) and start the engine
   against the local image, closing the VM-image-fetch gap. → [Path A] [Image/registry].
3. **Second consent gate at first engine start** (`start`/`init`) for the VM + image blast radius, and
   **receipt the VM disk dir** (`~/.lima`/`~/.colima`) so uninstall is symmetric. → [Consent gate] /
   reuse specs/0037 + specs/0041 receipt store.
4. **Harden the in-VM engine by default:** rootless, no host `$HOME` mount (workspace-only if any),
   user-mode (gvproxy) networking, and **socket kept in-VM** — exposed to the agent only via an
   explicit gated per-session `DOCKER_HOST`, never `/var/run/docker.sock`. → [Socket/daemon isolation].
5. **Image-pull policy:** rewrite agent pulls to sha256 digests, reject `:latest`, and mount a
   cosign/sigstore `policy.json` so unsigned images are refused at pull time (bias to nerdctl/containerd
   for native cosign). → [Image/registry].
6. **Explicit non-touch:** detect Docker Desktop / OrbStack, surface them as untouched unmanaged system
   state, never reuse/modify/uninstall them; no `--force`. → [Stay unmanaged + honest] (mirrors 0041).
7. **FLO hand-off (later):** the build-vs-reuse decision — dedicated lima VM vs collapsing onto a single
   safeslop-owned tart-Linux VM with one receipt model. → [Reuse tart].

## Net

The safest container runtime for safeslop is **not a product choice — it's a confinement posture**: a
CLI-only, rootless engine confined to a safeslop-**owned** vz Linux VM, with the VM image pinned like
any Path A artifact, the VM disk receipted, first-start gated, the host `$HOME` unmounted, user-mode
networking, and the socket kept inside the VM. Docker Desktop and OrbStack are rejected for managed
install (privileged helpers + extensions + dirty uninstall) and left honestly untouched if present. The
load-bearing realization is that pinning the *binary* secures almost nothing — the VM image and the
image-pull path are the real supply chain, and both must be pinned/gated explicitly. The one open
contest — reuse tart's Virtualization.framework directly vs. drive a dedicated lima VM — is deferred to a
FLO, with lima as the pragmatic start and a single-hypervisor tart-Linux as the north star.

## Method footer

Cross-model blind research (`ayo`, non-premium): host (Anthropic, Opus 4.8) + Gemini 3.1 Pro (Google,
via ai-router/OpenRouter, ZDR) + Kimi K2.7 (Moonshot, flat-rate subscription) + GLM-5.1 (z.ai, flat-rate
subscription). Four independent families, none seeing another's output; the host synthesized +
pertinence-triaged. No Fable lane (standing policy). All OpenRouter calls ZDR-enforced;
`anthropic/`/`moonshotai/`/`zai/` are never routed via OpenRouter. One factual error (GLM: vz requires
Apple-signed Linux kernels) was caught by cross-family disagreement and discarded — the canonical case
for running more than one family.
