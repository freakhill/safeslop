# Host-tier launch friction — decision (FLO-selected)

**Date:** 2026-06-21
**Method:** feedback-loop-optimization (FLO), premium K=2 cross-family. Workers = Opus subagents
(blind, parallel, distinct lenses); evaluators = Kimi K2.7 (criteria in order) + Gemini 3.1 Pro
(criteria reversed), averaged, blind to lane/iteration. 3 generations.
**Resolves:** the contested S3 decision flagged by the GUI-flow-ergonomics ayo
(`specs/research/2026-06-21-gui-flow-ergonomics.md`): how much friction for a host-tier launch
(Touch-ID-only vs type-to-confirm vs temporal scope).

## Rubric (locked)

C1 security comprehension (30) · C2 anti-habituation (25) · C3 honesty/no-dark-patterns (20) ·
C4 native-macOS-HIG (15) · C5 implementability (10). Adversarial calibration: a "remember for this
session" convenience that erodes the gate must score LOW on C2/C3.

## Result

| Gen | Design | C1 | C2 | C3 | C4 | C5 | Weighted |
|----|--------|----|----|----|----|----|----------|
| 0 | minimum-sufficient (picker→dangerous-value + hold-to-launch) | 9.5 | 7.5 | 10 | 7 | 10 | 87.75 |
| 0 | type-the-profile-name before biometric | 9 | 7 | 9 | 10 | 10 | 87.50 |
| 0 | Little-Snitch temporal scope (once/session/forever) | 8.5 | **4** | 7 | 8.5 | 10 | 72.25 |
| 1 | shuffled live-risk match toggles | 9.5 | 9.5 | 10 | 9 | 8.5 | 94.25 |
| 1 | engine-generated consequence-match picker | 10 | 8.5 | 8.5 | 10 | 10 | 93.25 |
| 2 | **synthesis: multi-item match + engine cross-tier decoys** | 9.5 | 9.5 | 10 | 10 | 10 | **97.25** |

Baseline (today: Touch-ID-only) is roughly the C1≈4 / C2≈4 floor — the whole exercise is about closing
that. Trajectory 87.75 → 94.25 → 97.25; converged (residual is a fundamental tradeoff, below).

### Load-bearing findings (cross-family consensus)

1. **Temporal/persistent scope is the wrong move and the data is unambiguous.** Both Kimi and Gemini
   independently slammed the Little-Snitch once/session/forever design's C2 to **4** — a "remember"
   hatch erodes the gate no matter how honestly it is labelled. **Decision: host consent is
   PER-LAUNCH, never persisted. No scope, no "remember", ever.** (The ayo's "temporal trust" lesson is
   explicitly REJECTED for the host tier by this FLO.)
2. **The anti-habituation breakthrough is to bind the comprehension act to the profile's LIVE,
   ENGINE-REPORTED risk** so the correct answer moves with the profile and cannot collapse into motor
   memory. Touch ID proves identity; the match proves comprehension; neither substitutes for the other.
3. **Touch ID's reason string and the comprehension act must carry the SPECIFIC consequence**, not a
   generic prompt — comprehension, not just authentication.
4. **Grant/revoke is risk-PROPORTIONAL, not symmetric-in-friction, and that is correct, not a dark
   pattern.** Grant adds unbounded privilege → comprehension + biometric. Revoke removes privilege →
   one visible click, no biometric. (Kimi flagged this as "asymmetric friction" on two designs;
   resolved by making the affordance equally VISIBLE and equally CHEAP-to-reach, with the extra step
   gating only the privilege-increasing direction.)

### Remaining weakness (accepted)

For a user launching the SAME profile on the SAME machine many times, the engine's statement pool is
finite and eventually familiar. This is a fundamental limit of any non-gimmick design; the cross-tier
false decoy means there is still no blind muscle path (the user must actively reject a falsehood each
time), which caps the erosion. Hardening further (re-randomized wording) crosses into busywork that
the judges penalize on C3/C4. Do not chase it.

---

## The selected design — host-tier launch confirmation

A single **in-window phase-view swap** (a `case` in the window-root `WindowPhase` enum, rendered by
swapping window content — NOT a presented `.sheet`/`.alert`), so the only system-modal surface in the
whole flow is the OS Touch ID dialog. **No modal-on-modal.**

### Flow

1. **READ.** A non-collapsible, always-visible headline card (fixed, always true, but **never an
   answerable row** — it is the honesty anchor):
   > Launch host profile "<name>"
   > This agent runs on your Mac as you — no isolation. It can read and write every file your account
   > can, use your logged-in credentials, and reach any network your Mac can reach. Nothing here is
   > sandboxed.

   Plus a per-launch-distinct **scope line** the engine derives from live machine state (varies run to
   run, so there is no fixed answerable text to memorise):
   > This run: home folder + 3 mounted volumes, 2 logged-in credentials, full network.

2. **MATCH (the comprehension act).** 3 statement rows, each a whole sentence **authored by the
   engine** + a `No`/`Yes` segmented picker, unset by default (a hidden sentinel = "not yet answered",
   so a stray default can't pre-arm). The 3 are drawn per launch with **≥1 TRUE this-profile statement
   and ≥1 FALSE cross-tier decoy** — a truthful-but-lesser statement true for a more-isolated tier but
   FALSE for host:
   - `This run is limited to the workspace folder.` → No  *(true for sandbox/container)*
   - `Network access is blocked except an allow-list.` → No  *(true for closed-egress container)*
   - `Files outside the project are invisible to this agent.` → No  *(true for vm/container)*

   Sentences + order shuffled every entry; each row's `expected: Bool` is set by the engine. The
   **Launch as me** button (`.borderedProminent`) is `.disabled` until every toggle matches ground
   truth, and becomes the Return-default only when armed. A wrong toggle immediately disarms, clears
   all answers, reshuffles, highlights the wrong row red:
   > That one's wrong — re-read the highlighted line.

   Reflexive single-click is defeated **by construction** (button disabled until matched) — no timer,
   no press-and-hold gesture.

3. **AUTHORIZE.** Armed **Launch as me** → `BiometricGate.confirm(reason:)` with
   > Authorize launching host profile "<name>" as you, with no isolation.

   → on success, gRPC `StartAgent`. Touch ID cancel/fail → return to MATCH still armed (no re-shuffle —
   comprehension was already proven; the biometric is identity, not comprehension).

**Abort (all symmetric, one action, always visible):** a `Cancel` button (`.bordered`, left of Launch,
same row, same size) → back to the profile list, no biometric; Esc / window-close = same; Touch ID
dismissed → stays in MATCH, nothing granted. After launch, a persistent `Stop agent` button in the
running view (`.bordered .tint(.red)`, one click, no biometric). Nothing persists; re-entering the
phase = full re-shuffle + fresh preflight.

### Mechanics (named APIs)

- New window phase `case hostConsent(preflight)`; `ConsentStatement { id, text, expected: Bool,
  tierOrigin, answer: Bool? = nil }`; `allMatched = statements.count == 3 && all answer == expected`.
- **One new read RPC** `PreflightHostLaunch` → `{ headline_body, scope_line, repeated ConsentStatement
  candidates (mixed true + cross-tier-false, each carrying expected + tier_origin), correct_count }`.
  The **engine authors every sentence and sets every `expected`** (single-source-of-truth — the
  affirmed text can't drift from what actually runs). The cross-tier decoys are built from the engine's
  tier-capability table (`internal/engine/policy`), which always knows what a sandbox/container/vm
  would restrict → **a false statement is always constructible, even for a max-permission host
  profile** (no all-Yes degradation).
- `BiometricGate.confirm` reused **unchanged** (wraps `LAContext.evaluatePolicy(
  .deviceOwnerAuthenticationWithBiometrics)`). Button: `.borderedProminent .disabled(!allMatched)
  .keyboardShortcut(allMatched ? .defaultAction : nil)` — a disabled default push button gated by a
  comprehension predicate is the native macOS replacement for a press-and-hold.
- Launch itself stays the existing `StartAgent`/`Launch` path.

### Implementation cost (honest)

This is NOT a pure client-side sheet. It needs engine work: the `PreflightHostLaunch` RPC, the
consequence-statement authoring, and the cross-tier capability table that generates decoys. That cost
is what buys the robustness (works for any profile) and the single-source-of-truth honesty guarantee —
the judges scored C5=10 because a small new read RPC was in-scope, but budget for the engine side, not
just SwiftUI. A cheaper first cut (the Gen-0 "minimum-sufficient picker", 87.75, no new gRPC) is a
valid MVP if the engine work must wait — it shares the per-launch / no-persistence / symmetric-revoke
spine and only loses the per-profile-live-risk anti-habituation.

## Net

Build the per-launch, never-persisted host gate whose comprehension act is matching this profile's
live engine-reported risk (with a guaranteed cross-tier false decoy), gated by an idiomatic
disabled-default button then Touch ID, with a one-click no-biometric revoke. Reject temporal/persistent
scope outright. MVP fallback if engine work slips: the Gen-0 minimum-sufficient picker.

## Method footer

FLO premium K=2: workers Opus (blind, parallel) · evaluators Kimi K2.7 (order) + Gemini 3.1 Pro
(reversed), averaged · 3 generations, 6 designs, 16 evaluations · ZDR/subscription routes only.
