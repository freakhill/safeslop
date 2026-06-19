# 2026-06-19 — safeslop design: promise vs. pain (cross-model `ayo`)

Cross-family research pass on two questions about the *whole* safeslop design:

- **Q1 — safety-real:** does it actually make agentic/manual dev safer, or is it
  security theatre that lulls users into false confidence?
- **Q2 — too-painful:** will friction push the two audiences to route around it (the
  classic security-tool failure mode)?

Three blind lanes mined mature prior art (sandboxing, dev-tool UX, supply-chain, egress,
"why security tools fail") and triaged each lesson against the named design surfaces
(1 sandbox · 2 egress · 3 credentials · 4 policy · 5 installer · 6 GUI/control-plane ·
7 WARP/TLS). Provenance tags: **[C]** = cross-family consensus (high confidence),
**[G]** Gemini-unique, **[D]** DeepSeek-unique, **[H]** Host-unique.

---

## Headline (the two load-bearing insights)

1. **safeslop's safety is real but TIERED, and the design's actual sin is that the
   *default* environment is the *weakest* one while being sold as "safe."** `sandbox-exec`
   is the default, requires no policy authoring — and is the least confining option, with
   no real egress control. The strongest env (Tart VM) is the one nobody will use daily.
   So the safe-by-default promise is inverted: the path of least resistance is the least
   safe. **Fix the inversion and label every control's tier honestly** and safeslop is a
   genuine guardrail against the *common* threat (an agent making a mistake — `rm`-ing the
   wrong tree, reading `~/.ssh`, beaconing a secret to a typo'd domain, `curl | sh`).
   Leave it and it is theatre against the *determined-adversary* threat it implicitly
   claims. The honest product claim is **"guards against agent mistakes and accidental
   exfiltration; not a malicious-code escape jail"** — say that out loud.

2. **The two audiences have opposite threat models, and one CUE schema + one default
   cannot serve both. [H]** Audience A (non-technical, corporate, behind WARP) needs
   strong defaults and *zero choices*; Audience B (power-user running agents) needs
   escape hatches and speed. Requiring either to *author CUE* to get protection guarantees
   A copies a permissive template and B bypasses. The GUI audience must never see CUE; the
   product must ship role-based preset profiles and auto-select a strong one when no policy
   exists.

The single biggest *adoption* killer is concrete and unaddressed: **the WARP cert-trust
plumbing for the toolchains safeslop installs** (Q2). The single biggest *safety* hole is
also concrete and unaddressed: **the sandboxed agent can rewrite its own `safeslop.cue`,
and a cloned repo can ship a permissive one** (Q1).

---

## Triaged lessons

### HIGH — act on these

**H1. Label the tiers; stop selling the default env as "safe." [C]**
- EVIDENCE: Chrome/Firefox use Seatbelt only as *one* layer around a tiny syscall surface
  with no reachable IPC brokers; Apple deprecated the public profile language and escapes
  via Mach IPC / IOKit / AppleEvents are routine. A *full coding agent* (broad fs, spawns
  subprocesses, talks to many services) is the worst possible thing to put behind a coarse
  Seatbelt profile and call contained.
- RELEVANCE: Surface 1. Q1. **Don't "abandon" sandbox-exec** (the externals overshoot —
  it's a credible *filesystem + exec* guardrail against the common accidental case). The
  fix is honest tier labels in `doctor`/`run` output and docs: sandbox = mistake-guard,
  container+squid = network-enforced, vm = adversary-grade. Mislabeling is what turns a
  useful tool into theatre.

**H2. The sandboxed agent can escalate by editing its own policy; untrusted repos ship
their own. [G+H]**
- EVIDENCE: Devcontainers are routinely exploited because cloning a repo auto-executes its
  embedded lifecycle config without host verification. safeslop's orchestrator reads
  `./safeslop.cue` from cwd/parents — so (a) the agent, running inside the writable repo
  mount, can rewrite the very file that governs its run, and (b) `git clone <evil>` lands a
  repo carrying a permissive `safeslop.cue` that would be honored.
- RELEVANCE: Surface 4. Q1 — a policy the confined process can edit is not a boundary.
  Resolve + hash the policy at launch from *outside* the writable mount; refuse to re-read
  on mid-run modification; and treat a repo-supplied `safeslop.cue` as **untrusted until
  the host approves it once** (devcontainer trust-prompt model), distinct from a
  user's own `~/.config/safeslop` policy.

**H3. The control socket lets the agent command its own jailer. [C+H]**
- EVIDENCE: `LOCAL_PEERCRED` uid-only auth = confused deputy (Zoom's local web server;
  firejail D-Bus CVE-2021-26910; macOS XPC enforces code-signing per message for exactly
  this reason). Any same-uid process can drive the engine — *including the agent safeslop
  is sandboxing*, which can `connect()` `~/.slop/s.sock` and ask the engine to launch an
  **un**sandboxed profile or dump secrets.
- RELEVANCE: Surface 6. Q1. The deferred codesign check is the load-bearing one and is
  **reversible without CGO** (verify the peer's audit-token code requirement via a
  `codesign`/`csops` shellout, staying `CGO_ENABLED=0`). Un-defer it before the GUI ships
  to Audience A; additionally, the engine must refuse control-plane connections originating
  from within a sandbox / its own spawned process tree.

**H4. WARP cert plumbing for installed toolchains + the bootstrap chicken-and-egg. [D+H]**
- EVIDENCE: certifi (Python) and Node ignore the system keychain; behind a TLS-intercepting
  proxy every `npm`/`pip`/`uv`/`cargo` call fails with an opaque cert error and users reach
  for `--insecure` (and leave it on). The single-binary rewrite fixes *safeslop's own*
  downloads (a CGO-free Go binary on darwin does consult the system trust store, so WARP's
  CA in the keychain is honored — a real point in its favor) but **not the toolchains it
  installs**, nor the very first bootstrap if the CA isn't trusted yet.
- RELEVANCE: Surfaces 7 + 5. Q2 — the #1 adoption killer for Audience A. `install apply`
  must export the system-keychain bundle and wire each toolchain's cert env
  (`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `PIP_CERT`, `UV_NATIVE_TLS=1`,
  `CARGO_HTTP_CAINFO`). The existing fish stack's 4-strategy uv TLS fallback
  (`scripts/slop.fish`, per CLAUDE.md) is hard-won knowledge — **port it into the Go
  installer, don't re-discover it.**

**H5. Scope-first, decay-second. [C+H]**
- EVIDENCE: scripted exfiltration completes in seconds (Codecov, recent Okta/AWS
  incidents); a 15–60 min TTL is an eternity, and re-auth fatigue makes users *lengthen*
  TTLs. TTL alone is a weak primary control.
- RELEVANCE: Surface 3. Q1. Read-only deploy keys already do this right; cloud creds do
  not — they stage broad SSO/ADP tokens. Push least-privilege to the *minting* step
  (downscoped AWS session policy / permission boundary, narrowest GCP scopes) so even a
  full-TTL reuse is bounded to what the task needed. Reframe the model **"scope-first,
  decay-second."** Lighter than Gemini's full metadata-server: stage a short-lived
  `credential_process` the SDK calls, so tokens never sit in the process tree / crash dumps.

**H6. The honest justification for "no naive Homebrew" is notarization — and it needs the
upstream signature too. [D+H]** *(directly answers jojo's note)*
- EVIDENCE: brew's trust model isn't nothing (version-controlled audited formulae +
  checksummed bottles). Refusing it and downloading from GitHub releases against a
  README-advisory hash would be *worse* (no root of trust) — DeepSeek's sharpest point.
  BUT safeslop's pins are **compiled into the notarized binary**, so they inherit Apple's
  code-signing root of trust: tampering with the pin set breaks the signature. *That* is
  the real reason to not delegate to brew, and it's defensible — but only because of
  notarization, so document the chain explicitly.
- RELEVANCE: Surfaces 5. Q1. Pinning + embedded-in-notarized-binary defends *substitution
  and tampering*, not an upstream maintainer compromise (mise shipping malware at a pinned
  version, faithfully checksummed — TUF/SLSA's "provenance ≠ honesty"). Cheap upgrade that
  beats the unusable VM-eval: **verify upstream's own maintainer signature** (mise ships
  `SHASUMS256.asc` / minisig; tart has signed releases) *in addition to* the sha safeslop
  copied, and prefer versions aged > N days (TUF freshness / time-delay) so a poisoned
  release has a detection window.

**H7. Secure-by-default must mean strong-default + zero authoring. [C+H]**
- EVIDENCE: Tailscale's adoption lever was zero-config that's useful on its own, not a
  security knob you opt into; seccomp/AppArmor profiles are "almost never customised"
  (DeepSeek); Nix/Bazel pay heavy adoption penalties for bespoke config languages.
- RELEVANCE: Surfaces 4 + 1. Q1+Q2. `safeslop run` with **no** `safeslop.cue` must
  auto-select a sane, reasonably-strong profile (detect language / presence of cloud
  configs / whether secrets are declared) — never require authoring to get protection. If
  a profile declares secrets or cloud creds, default its env to **container (squid-
  enforced)**, not bare sandbox. The GUI generates/edits policy via a wizard; Audience A
  never sees CUE.

**H8. Don't gate installs on behavioral VM-eval. [C+H]**
- EVIDENCE: Cuckoo/automated malware sandboxes are slow, false-positive-heavy, and evaded
  by sleep timers; Qubes users abandoned per-install disposable-VM execution as too slow
  for routine software. A VM diff for a CLI update is excruciating and will be disabled.
- RELEVANCE: Surface 5. Q2. Keep it strictly opt-in for a *first-time, high-risk* artifact
  at most; the routine "honesty" gap is better closed by H6 (signatures + freshness) than
  by a VM nobody waits for.

### MEDIUM — actionable, secondary or needs design

- **M1. Docker Desktop is a licensing + speed landmine for Audience A. [H]** Docker Desktop
  needs a paid license at >250 employees / >$10M revenue and is the reason teams fled to
  colima/OrbStack. Surface 1/5, Q2 — the installer must not assume Docker Desktop; default
  the container backend to a colima/OrbStack-friendly path.
- **M2. Harden the container mounts. [G]** Firejail's CVEs were bind-mount symlink escapes.
  Surface 1, Q1 — mount only the repo root, `noexec,nosuid` on dependent mounts, no
  symlink-following out of the sandbox.
- **M3. Tell the agent it's sandboxed; give it an ephemeral scratchpad. [G]** LLMs loop
  hallucinating workarounds when they hit unexplained network/fs blocks. Surface 1, Q2 —
  inject the active constraints into the agent's system prompt and map a known-ephemeral
  writable scratch dir. (Novel, agent-specific — neither other lane nor the design covers
  the *agent's* experience of the cage.)
- **M4. Egress allowlist = blast-radius + audit tier, not anti-exfil. [C]** A determined
  agent exfils via DNS tunneling or hides payloads in calls to *allowed* domains (GitHub
  issues, S3). Surface 2, Q1 — keep it (default-deny is real value against `curl|sh` and
  accidental beaconing), label its tier, and note loudly that the **default sandbox env has
  no egress enforcement at all** — the env most users get is the one most lacking it.
- **M5. Tart is a rare-use validation tier, not a daily env. [C]** Full-macOS VMs take
  minutes + tens of GB vs Qubes' sub-second minimal templates. Surface 1, Q1+Q2 — position
  it as a "run this risky thing once in a real VM" step; investigate fast-resume snapshots
  before pretending it's a daily driver.
- **M6. Embedded-CUE means security-logic ships only via a full binary release. [D]**
  Surface 4/5, Q1 — a new sandbox-escape mitigation can't be hot-fixed; users ignore
  updates. Add an update-staleness nudge in `doctor`/GUI and a signed-manifest channel for
  policy/pin updates decoupled from the engine release cadence.
- **M7. TouchID / user-presence for critical control-plane state changes. [G]** `sudo` and
  1Password gate destructive local actions behind biometric proof-of-intent. Surface 6,
  Q1 — require LocalAuthentication for "launch unsandboxed" / "grant write creds" over the
  socket, so automated same-uid malware can't silently invoke them.

### DEFERRED / reframed — named, not acted on now

- **1Password agent-socket consent portal [G] — contradicts a settled decision.** Gemini
  proposes a Flatpak-`xdg-desktop-portal`-style consent bridge instead of banning the
  socket. The design *already* FLO-resolved this (2026-06-18,
  `specs/research/2026-06-18-ssh-auth-flo-decision.md`): a caged key file is a strictly
  smaller attack surface than a live signing oracle. Don't relitigate — but the *real*
  friction it points at (read-only-default breaks autonomous agents → users inject
  long-lived write tokens, [D]) is genuine and is the FLO hand-off below.
- "Abandon Seatbelt entirely" / "the planned NetworkExtension will never ship low-friction"
  — both externals overshoot into absolutes. Reframed into H1 (tier-labeling) and a
  realistic read on Surface 2: the NetworkExtension egress filter genuinely faces Apple
  entitlement/approval friction for a standalone signed binary (a hard constraint worth
  recording), so don't let the roadmap imply the *sandbox* env gets real egress control
  "soon."

---

## FLO hand-off (one genuinely contested decision)

**Read-only-default deploy keys vs. autonomous-agent ergonomics.** [D] argues read-only
defaults break fully autonomous agent workflows, pushing power users to inject long-lived
write tokens that defeat the whole model; the design deliberately defaults read-only and
lint-gates write on `network:deny` + forge-only egress. Whether (and how) to make *bounded*
write available to unattended runs without the long-lived-token escape hatch is a real
design fork — score candidate designs (host-side per-push approval; very-short-TTL write
key minted on demand; a forge-side ephemeral fine-grained token) with `feedback-loop-
optimization` rather than deciding it here.

---

## Actionables (numbered → surface)

1. **Tier labels everywhere** (`doctor`, `run` banner, README): sandbox = mistake-guard,
   container+squid = network-enforced, vm = adversary-grade. → Surface 1. (H1)
2. **Policy integrity:** hash `safeslop.cue` at launch from outside the writable mount;
   refuse mid-run mutation; treat repo-supplied policy as untrusted-until-host-approved. →
   Surface 4. (H2)
3. **Control-plane:** un-defer the codesign/audit-token peer check (CGO-free shellout);
   refuse connections from within a sandbox/own process tree; TouchID-gate privileged
   verbs. → Surface 6. (H3, M7)
4. **WARP toolchain TLS:** `install apply` exports the keychain CA bundle + wires
   `SSL_CERT_FILE`/`NODE_EXTRA_CA_CERTS`/`PIP_CERT`/`UV_NATIVE_TLS`/`CARGO_HTTP_CAINFO`;
   port the fish 4-strategy fallback. → Surfaces 7+5. (H4)
5. **Scope-first creds:** downscope cloud tokens at minting; stage via `credential_process`
   not raw env vars. → Surface 3. (H5)
6. **Installer trust (SP7b-3):** document the notarized-binary → embedded-pin trust chain
   as the no-brew justification; add upstream maintainer-signature verification + version
   freshness delay; demote VM-eval to opt-in-first-use. → Surface 5. (H6, H8)
7. **Strong zero-authoring default:** `run` with no policy auto-selects a strong profile;
   secrets/creds ⇒ container env by default; GUI wizard, never raw CUE for Audience A. →
   Surfaces 4+1. (H7)
8. **Container hardening + backend:** repo-root-only `noexec,nosuid` mounts; don't assume
   Docker Desktop (licensing) — colima/OrbStack-friendly default. → Surface 1. (M2, M1)
9. **Agent-aware UX:** inject active constraints into the agent system prompt + map an
   ephemeral scratchpad. → Surface 1. (M3)
10. **Decoupled update channel** for policy/pin/mitigation updates + staleness nudge, so
    security logic isn't hostage to the engine release cadence. → Surfaces 4+5. (M6)

---

## Net

safeslop is **not** security theatre *if* it stops mislabeling its tiers and fixes the
default-is-weakest inversion: as an honest guardrail against agent *mistakes* and
*accidental* credential/exfil sprawl — the overwhelmingly common real-world failure when
you point a coding agent at a repo — it delivers genuine value that nothing else on macOS
packages this cleanly. It becomes theatre only if it lets users believe the default
sandbox contains a *determined adversary*, lets the caged agent edit its own policy or
drive its own control socket, or ships cloud creds broad-and-long. On pain: the architecture
is sound, but adoption dies at the WARP cert wall and the CUE-authoring wall unless A4 and
H7 land — both are concrete and cheap. The corrections are surgical, not structural; the
threat-model *honesty* (H1) and the two-audiences split (Headline 2) are the load-bearing
reframes everything else hangs off.

---

## Method footer

Cross-family `ayo`. Lanes: **Host** (Anthropic, Opus 4.8 — orchestrator's own mining),
**Gemini 3.1 Pro** (Google, via `ai-router` OpenRouter, ZDR enforced), **DeepSeek V4 Pro**
(via `ai-router` OpenRouter, ZDR enforced). **Kimi K2.7 (Moonshot) was unavailable** this
pass — `kimi_status` healthy but the research call timed out, and per session policy a
timed-out Kimi is treated as down for the rest of the session (not retried). DeepSeek was
brought in as the second independent family so the pass stayed genuinely cross-vendor.
Lanes were blind (identical brief, none saw another's output); the orchestrator alone
compiled and triaged. Source of truth for the design: `specs/0001`, `specs/0012`,
`specs/0002`–`0019`, and repo `CLAUDE.md`/`AGENTS.md`/`README.md`.
