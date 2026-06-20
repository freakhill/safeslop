# Cockpit GUI — safe-by-design (cross-model research)

**Date:** 2026-06-20
**Method:** ayo cross-model research. Four independent families — host (Claude/Opus, orchestrator),
Gemini 3.1 Pro (via ai-router, ZDR), z.ai GLM-5.1, Moonshot Kimi K2.7 — each given the *identical*
blind brief + output contract, then compiled and triaged here. See method footer.

**Pertinence question each lesson had to answer:** *does it make safeslop's safe path the simplest path?*

---

## Headline (the two load-bearing insights)

1. **The policy is the product; the GUI is a least-privilege *generator* over it.** CUE text is the
   single source of truth (it's what the trust gate hashes). The visual editor is a constrained,
   *lossless* subset that can only emit safe shapes; the moment text uses constructs the visual mode
   can't represent, visual mode **locks** with an "advanced CUE active" badge rather than round-trip
   and silently corrupt a hand-written guard. *(unanimous across all 4 families — highest confidence
   finding in the whole pass.)*

2. **Safety must be shown as concrete consequence, never as a score or a tier name.** The arbiter and
   every trust/approval surface render *"if this agent is compromised it can: read `~/.ssh`, reach
   `*.openai.com`"* — a break-glass blast-radius sentence with a one-click narrow-it fix. Abstract
   "High/Medium/Low" or "Sandbox tier" is habituated away; specific exploit paths get fixed.
   *(Gemini + GLM + Kimi all independently; mirrors our existing honest-tier-label core.)*

Everything below serves these two.

---

## Triaged lessons

Tags: **[C]** = cross-model consensus (≥2 families), **[U]** = single-model novel. Surfaces S1–S12 per
the brief (S1 tabs, S2 tool detect, S3 git login/repos, S4 ephemeral keys, S5 visual+text editor, S6
CUE LSP, S7 AI assist, S8 dynamic network UI, S9 files/net add-remove, S10 arbiter, S11 dock launch,
S12 CLI launch).

### A. The dual editor (S5/S6) — HIGH

**[C] HIGH — CUE text is canonical; visual mode is a lossless safe subset that locks on advanced constructs.**
Tailscale's ACL editor learned round-trip visual↔text editing destroys comments/intent and lets a
visual save clobber a hand-written guard. → S5. Visual mode reads *from* text; when a block uses
imports/comprehensions/list-math it can't represent, show a non-dismissible "Controlled via code"
badge and disable those controls. The trust gate hashes the *text* bytes, so the text must always win.

**[C] HIGH — Autocomplete is choice architecture: sort safest-narrowest first, not alphabetically.**
Alphabetical puts `allow` before `deny`. Make the CUE LSP return custom `sortText` so `deny: []`,
specific GitHub domains, `network: "deny"` are the pre-selected first completion; wildcards/`allow`
sink. → S6. The easiest keystroke must select the *narrowest* permission.

**[C] HIGH — LSP quick-fixes (⌘.) that downgrade privilege, plus tier/feature mismatch diagnostics.**
Typing `network: "*"` should offer "restrict to detected domains" / "deny-by-default" as a one-key
fix. Granular egress rules under `tier: sandbox` should throw an inline diagnostic ("sandbox only does
coarse deny/allow — switch to container/vm") with a quick-fix. → S6.

**[U] HIGH — Render `op://`/`env:` secret refs as locked pills, and never paint a secret value in
either mode.** The agent we're sandboxing can *scrape the terminal/screen buffer*; a secret ever
rendered is a secret leaked. Mask by default, show only name + last-4, reveal/copy gates TouchID. → S5/S6.

**[U] MEDIUM — Shared CUE modules must be explicitly pinned/hashed; an unpinned/locally-overridden
module blocks launch.** Terraform-style supply-chain guard: a referenced module is part of the policy's
effective bytes. → S6. Render imported modules in a sidebar; unpinned ones warn + block until re-trusted.

### B. The safety arbiter (S10) — HIGH

**[C] HIGH — Output a concrete break-glass scenario + one-click narrow, not a number.** AWS IAM Access
Analyzer abandoned scores for specific findings; OPA Playground shows the satisfying input set. The
arbiter does static reachability over the *compiled* policy (EnvTier capabilities, not raw CUE) and
prints "If compromised, this profile allows: [outbound to any host] [read of ~/]" each with a fix. → S10.

**[C] MEDIUM — An interactive "can it do X?" box.** Let the user type `read ~/.ssh/id_rsa` or
`connect smtp.evil.com` and get a red YES / green NO, evaluated against the same compiled policy. → S10/S6.
Builds trust in the boundary and is the natural home for the dry-run engine (below).

**[C] HIGH — Label tiers by what they BLOCK, not what they lack; keep sandbox visually "honest-amber".**
"Sandbox: blocks accidental exfil + broad network" beats "Sandbox: no VM, limited network" — users
avoid options framed as deficits. The arbiter must stay brutally honest about Seatbelt's limits
(mistake-guard, not malicious-escape). → S10. Aligns with our existing `policy.EnvTier` labels.

### C. Empirical authoring — the killer feature (S2/S9/S10) — HIGH

**[C] HIGH — Learning / dry-run mode: observe the agent's first N seconds of file+net access, then
propose a deny-by-default policy from *observed* behavior.** Little Snitch "Suggest" mode + OPA
discovery: users *cannot* predict an AI agent's dependency graph (which registries, git hosts, temp
dirs it touches), so any hand-authored policy is over-permissive. Run a log-only Seatbelt/squid pass,
diff observed vs proposed, offer the delta as allowlist additions. **Learning mode must auto-expire
(e.g. 5 min) and revert fail-closed** — users turn it on, forget it, and leave the boundary open. → S2/S9/S10.
This is the single biggest "make the safe path the simplest path" lever found.

**[C] HIGH — Auto-deny high-value targets (`.git`, `.ssh`, `.env`, `.aws`, `.npmrc`) when a parent dir
is scoped; force a manual un-deny.** The #1 dev-tool exfil vector is scoping `~/` (or a repo dir
containing `.git` creds). Codify the guard by default — this is literally what "mistake-guard" must
guard. → S9.

**[U] HIGH — Drag-and-drop folders (NSItemProvider) as the *primary* file-scope method; on drop,
propose the dropped path but warn if dev tools need a broader parent (`~/.npm`, `/tmp`, `.cargo`).**
Exact paths like `~/src/project` break real toolchains → users rage-quit to `host` tier. Eliminate
path typing; suggest the minimal-working scope with an explicit acknowledgment. → S9.

**[C] MEDIUM — Repo metadata → auto-scoped starter.** On connecting a repo (S3), parse
`.gitignore`/`package.json`/`.nvmrc`/`Cargo.toml` to pre-populate file scope + tool needs, so "New
Profile" yields a tight, accurate draft with zero typing. → S3/S9/S1.

### D. Dynamic network UI (S8) — design now, ship when the NE lands

**[C] HIGH — Temporary-allow is the big default button; permanent costs more interaction + writes CUE
+ re-trusts.** Little Snitch's chronic failure is "Allow Forever" to silence pop-ups. Default every
alert to "Allow until session ends"; bury "Save to profile…" behind naming + an explicit commit. A
user mashing Return must not permanently puncture the policy. → S8.

**[C] HIGH — Attribute + group alerts by *profile/intent*, not by binary/PID.** AI agents spawn
node/uv/cargo/git; per-binary rules are noise. Roll `api.github.com`+`codeload.github.com`+
`raw.githubusercontent.com` into one "GitHub access" rule owned by the profile. Requires tracking
profile→PID lineage. → S8/S2.

**[U] HIGH — Gate creation of any network-confined profile on the NetworkExtension already being
installed/approved.** A deferred system-extension permission makes the user's first run fail silently
and feel like a product bug; they abandon the tier. Onboarding must deep-link System Settings and
block until the entitlement is live. → S1/S2.

### E. Trust gate, secrets, ephemeral keys (S1/S3/S4) — HIGH

**[C] HIGH — Show a semantic *diff* before the hash + TouchID; build the TouchID reason string from
the policy.** "Network: changed `deny` → `allow: github.com`" then a ≥1s enforced delay then TouchID
whose `localizedReason` names agent+tier+change. A hash-only or generic "wants to make changes" prompt
is performative — the hash is for the machine, the diff is for the human. → S1/trust gate.

**[C] HIGH — Pre-select "create ephemeral key"; collapse "use my existing SSH key" into a
warning-bordered advanced disclosure.** Credential reuse is the unsafe path; make the ephemeral
deploy-key/token API call the primary one-click path, the personal-key picker the friction path. → S3/S4.

**[C] HIGH — Ephemeral keys must be ambiently visible + instantly killable.** A menu-bar item
("🔥 2 keys active", countdown) + a "Revoke All" panic button. Invisible expiry breeds anxiety →
users bypass the system and paste a permanent PAT. Revoke on app exit / agent crash / panic. → S4/S1/S11.

### F. AI-assisted authoring (S7) — HIGH

**[C] HIGH — Treat AI output as untrusted: never auto-apply; force a per-permission review checklist;
tier-cap suggestions to ≥ the AI's own tier.** Coding assistants optimize for task completion and
will happily suggest loosening the very sandbox they run in. Parse AI output to an AST, render each
granted capability as a checkbox the human must tick, gate any escalation behind TouchID, and run the
authoring AI itself sandboxed (read-only on the target). → S7.

### G. Launch surfaces — dock + CLI (S11/S12) — MEDIUM

**[U] MEDIUM — Pre-seed the dock right-click menu with a safe default ("Quick Start: Claude in VM,
disposable") and sort profiles safest-tier-first.** Reward the safest tier with the fastest launch. → S11.

**[U] HIGH — Muscle-memory launchers must not silently degrade posture.** Re-running a profile in a
*weaker* tier than its last-used tier (or any untrusted/changed policy) requires an explicit
`--downgrade`/modifier + the trust gate. Dock/CLI repeat-launch otherwise erodes security invisibly. → S11/S12.

**[C] — CLI/hotkey launch bridges to the GUI trust gate via a native notification when (and only when)
a gate is actually required.** A background `skhd` launch that needs approval should raise a macOS
notification that foregrounds the app for TouchID, not hang a terminal silently. → S12.
**CONTRADICTION RESOLVED:** GLM/Kimi proposed "fail-closed if the GUI isn't running / TouchID every
launch." That breaks jojo's hotkey ergonomics. Resolution consistent with our existing trust model
(specs/0022, specs/0028): **an already-trusted, unchanged policy launches freely with no TouchID** —
trust was the privilege boundary and it was already crossed. TouchID/notification is required *only*
at a boundary: first-trust, re-trust after a policy change, or a tier downgrade. So CLI launch is
fail-*open* for trusted-unchanged, fail-*closed* (with a notification→GUI bridge) for everything else.

### H. Tool detection & tier honesty (S2) — MEDIUM

**[U] HIGH — Show a per-tier tool compatibility matrix at creation time.** A tool detected on the host
may not exist in the container/vm image. Display "cargo: host ✓ · sandbox (bind ~/.cargo) · container
→ add to image · vm → pre-installed" *before* tier selection, and wire "add to image" to the existing
`image.extra-{apt,pip,npm}` tailoring. Hiding the mapping → runtime breakage → tier abandonment. → S2.
*(host-lane: this connects S2 directly to our existing content-hashed image-tailoring + pinning gate.)*

**[C] MEDIUM — Color-code tiers consistently everywhere (host/sandbox/container/vm) so you always know
*where* a tool runs.** Extends our existing host-drawn, un-spoofable trust chrome into the
create/launch tabs. → S2/S1.

### I. Host-lane additions (orchestrator family)

**[U] HIGH — Reuse, don't reinvent: the Install tab IS the existing pinned installer.** S1a should be
the GUI front-end of the already-built `InstallPlan`/`InstallApply` control RPCs (fail-closed verify,
pinned desired-state) — not a new mechanism. → S1.

**[U] HIGH — One capability vocabulary across editor, arbiter, dynamic-network UI, and trust sheet.**
A network rule a user approves live (S8) must map 1:1 to a CUE field (S5) and the same plain-English
capability the arbiter (S10) and trust sheet show. Avoid a separate "firewall rules" model divorced
from the profile — that's how the dynamic state and the policy drift apart.

**[U] MEDIUM — The cockpit chrome is host-drawn and un-spoofable (the agent owns only the terminal
buffer); make that the consistent trust language across all three tabs.** Already true for the session
window; extend the ambient tier-tint + header identity to Launch/Create so trust cues are uniform.

---

## Actionables (numbered, each → a surface)

1. **Dual editor with text-canonical + lock-on-advanced** (S5/S6). Visual = lossless safe subset;
   "Controlled via code" lock; text bytes are what the trust gate hashes.
2. **CUE LSP with safe-first `sortText`, privilege-downgrade quick-fixes, tier/feature-mismatch
   diagnostics, secret-pill rendering** (S6).
3. **Arbiter = compiled-policy reachability → break-glass sentences + one-click narrow + "can it do X?"
   box** (S10), built on `policy.EnvTier` capabilities.
4. **Learning/dry-run mode** (log-only Seatbelt/squid observe → proposed deny-by-default delta,
   auto-expiring) (S2/S9/S10).
5. **File-scope: drag-drop primary; auto-deny `.git/.ssh/.env/.aws/.npmrc` on parent scope; suggest
   minimal-working parent with explicit ack** (S9).
6. **Dynamic-network UI spec (build-ready for when the NE lands): temporary-default, permanent=friction
   +re-trust, group-by-profile/intent, gate creation on NE-installed** (S8) — shares the capability
   vocab with the editor.
7. **Trust gate: semantic diff before hash, ≥1s delay, policy-built TouchID reason** (S1).
8. **GitHub/Forgejo: device-flow login → ephemeral deploy-key/token pre-selected; repo metadata →
   auto-scoped starter** (S3/S4).
9. **Ephemeral key HUD: menu-bar countdown + Revoke-All panic; revoke on exit/crash** (S4/S11).
10. **AI assist: AST→per-capability review checklist, tier-cap to ≥ own tier, run authoring AI
    sandboxed, TouchID on escalation** (S7).
11. **Dock menu + CLI: safe default pre-seed, safest-tier-first sort, downgrade/changed ⇒ gate via
    notification→GUI; trusted-unchanged ⇒ launch free** (S11/S12).
12. **Tool detection: per-tier compatibility matrix + "add to image" wired to `image.extra-*`** (S2).
13. **Install tab = GUI over existing `InstallPlan`/`InstallApply`** (S1).
14. **Shared capability vocabulary + consistent host-drawn tier chrome across all three tabs** (S1, cross-cut).

## FLO hand-offs (contested, defer to a feedback-loop pass before building)

- **How much can visual mode author vs force-to-text?** Gemini says force *all* dangerous grants to
  text; Kimi/GLM say allow them in visual behind a mechanical-cost confirm. Pick the line once.
- **Information architecture of "Create": wizard vs free-form editor-first.** Repo-metadata starters
  and learning-mode imply a guided flow; power users want editor-first. Likely both (starter → editor).

## Net

The research is unusually unanimous: build the GUI as a **least-privilege generator over a
text-canonical policy**, make **safety legible as concrete blast-radius consequence**, and let
**empirical learning-mode** author tight policies the user couldn't have predicted. Friction is a
*tool*, spent precisely at privilege boundaries (permanent network grants, tier downgrades, secret
reveals, policy changes) and removed everywhere else — which is exactly "the safe path is the simplest
path." None of this requires the network extension to start: the editor, arbiter, learning-mode
(Seatbelt/squid log), install tab, dock/CLI launch, and ephemeral keys are all buildable now; the
LuLu-style dynamic UI is designed here and slots in when the NE ships.

## Method footer

Families: host (Claude Opus 4.8, orchestrator/synthesizer) · Gemini 3.1 Pro (ai-router, ZDR enforced,
18 lessons) · z.ai GLM-5.1 (subscription, ~16 lessons) · Moonshot Kimi K2.7 (subscription, ~14
lessons). Each lane blind to the others; compiled + triaged by the host. Privacy: no `anthropic/*` or
`moonshotai/*` via OpenRouter; Gemini via ai-router ZDR; GLM/Kimi via their own subscription endpoints.
All four families available; none unavailable this pass.
