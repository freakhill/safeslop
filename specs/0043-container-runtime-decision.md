# 0043 — Container runtime: the architecture decision (FLO verdict)

**Status:** decision record (FLO-scored; resolves specs/0042 actionable #7). **Date:** 2026-06-22.
Settles the one contested call left open by the cross-model research note
specs/0042-container-runtime-safest.md: *reuse tart's Virtualization.framework directly, or drive a
dedicated lima VM?* Decided by a scoped Feedback-Loop-Optimization pass (method footer below).

## Verdict

**Adopt B — a dedicated, pinned lima/colima vz VM manager — as the container tier**, with two grafts:

1. **Graft from A (close the one safety gap):** pre-bake the container engine (rootless
   containerd + nerdctl + cosign) into the *pinned* VM OS image, so lima's first-boot
   `provision:` does **no** unverified in-guest `apt/curl` fetch. This lifts B's only weak axis
   (supply-chain, scored 7/10 because of that fetch) toward 10 **without** taking on A's
   build-and-forever-maintain-a-Linux-distro burden.
2. **Graft from C (cheap hedge toward the north star):** put the lima backend behind a small
   internal `container.Backend` interface (`Provision/Start/Socket/Teardown/Pins/StateDir`) so the
   agent-facing contract is only a scoped per-session socket. This keeps a one-impl migration path
   to a single tart-Linux VM (one hypervisor, one receipt model) open at ~+200 LOC, and makes the
   bet reversible — if the migration never justifies itself, delete the dormant adapter.

So: **lima now, engine baked into the pinned image, behind a thin backend seam; tart-Linux as the
north star, not built yet.** Docker Desktop / OrbStack remain rejected for managed install and
untouched-if-detected (specs/0042).

## Why — the scores

Cross-family K=2 panel (Kimi K2.7 original order + Gemini 3.1 Pro reversed order, both non-host,
ZDR), per-criterion averaged then weighted Σ(score/10 × weight). **Both families independently
ranked B > C > A with zero inversions**; audit deltas ≤ 2 pts (uncontested, far under the 20-pt
flag threshold).

| Criterion (weight) | A: reuse-tart | B: dedicated-lima | C: phased-hybrid |
|---|---|---|---|
| C1 supply-chain control & pinnability (25%) | 10 | 7 | 7 |
| C2 isolation / host blast-radius (20%) | 10 | 10 | 10 |
| C3 uninstall + receipt symmetry (15%) | 7 | 7 | 7 |
| C4 engineering + maintenance cost (20%) | 4 | 7 | 6.5 |
| C5 darwin-arm64 maturity / time-to-working (15%) | 4 | 10 | 10 |
| C6 single-trust-boundary coherence (5%) | 10 | 7 | 6 |
| **Weighted score / 100** | **74.5** | **80.5** | **79.0** |

- **A (reuse tart, 74.5)** wins the two pure-safety axes (C1, C6) but loses decisively on cost
  (C4=4) and maturity (C5=4): owning a custom Nix-built Linux guest means safeslop inherits a
  kernel-CVE surface forever and has the slowest time-to-first-working-runtime. Its supply-chain
  win is real but bought at an *unbounded, perpetual* maintenance liability.
- **B (dedicated lima, 80.5 — winner)** is the most mature, battle-tested rootless-container path on
  Apple Silicon (lima + vz + Rosetta), lowest build cost, and fits the existing Path A / receipt /
  consent machinery with near-zero new packages. Its only weak axis is C1=7 (lima's in-guest engine
  provisioning is an unverified fetch) — *closable* by graft #1.
- **C (phased hybrid, 79.0)** is B plus a backend interface and a dormant tart-Linux v2. It scores
  marginally below B because the phasing is a *cost now* (C4 two backends, C6 two hypervisors during
  transition) whose payoff (eventual single boundary) doesn't materialise until later — and "score
  what's present" doesn't credit a dormant adapter. We keep C's *interface* (graft #2, cheap) but
  not its second backend (deferred until the migration is justified).

## Adversarial verification

The decisive challenge — *you asked for "the safest", and A wins both safety axes, so why not A?* —
was stress-tested and the verdict held: A's C1=10 trades a **bounded, in-VM, closable** provisioning
fetch (B's gap, which lives inside the disposable guest, not on the host like a `curl|sh`) for an
**unbounded, perpetual** distro-maintenance liability (a stale unpatched guest kernel is itself
unsafe). With graft #1 closing B's fetch gap, **B is the safest *sustainable* posture**, not merely
the cheapest. The "safest" answer is B-hardened, not A.

## Non-negotiables carried from specs/0042 (unchanged)

rootless in-VM engine · host `$HOME` unmounted by default (workspace-only if any, UID/GID mapped) ·
user-mode networking (no root `socket_vmnet`) · `docker.sock` kept in-VM, never handed to the agent ·
VM OS image pinned as a Path A artifact · VM disk receipted + first-start gated as a second consent
event · agent pulls forced to sha256 digests + cosign policy · no Docker Desktop/OrbStack managed
install · reuse the Path A / receipt (specs/0041) / consent (specs/0037) machinery. A fail-closed
pre-start invariant gate (mounts empty, images local+digest-matched, networks empty,
`containerd.user`) re-runs on every start and after any lima version bump.

## Next

`/writing-plans` a container-runtime implementation plan with this spine: (1) pin `limactl` + the
**engine-baked** VM OS image in `install.DesiredState()`; (2) the `container.Backend` interface +
the lima impl; (3) the owned lima YAML + fail-closed pre-start invariant gate; (4) second consent +
VM-disk receipt; (5) in-VM hardening (rootless, no `$HOME`, user-mode net, in-VM socket); (6) image
policy (digest + cosign); (7) explicit non-touch of Docker Desktop/OrbStack. The tart-Linux backend
is a later, separately-triggered slice.

## Method footer

Scoped FLO (Tier 2, bounded 3-candidate design selection): 3 blind steelman workers (host/Opus
flo-worker subagents, one per architecture, none seeing another's proposal) → cross-family K=2
evaluation (Eval-1 Kimi K2.7 original criterion order; Eval-2 Gemini 3.1 Pro reversed order, via
ai-router/OpenRouter, ZDR-enforced) → orchestrator (host) computed all weighted scores from the
returned per-criterion breakdowns and adversarially verified the winner. No Fable (standing policy).
`anthropic/`/`moonshotai/`/`zai/` never routed via OpenRouter. Converged after the baseline scoring
round — a fixed candidate set has nothing to evolve.
