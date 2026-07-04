# 2026-06-20 — safeslop cockpit: safe-by-design GUI (cross-model `ayo`)

A cross-family research pass on **how the native-macOS cockpit's design should make the safe
path the simplest path** — ergonomy and safety from the same decisions, not traded off. Mined
native-macOS security UX (1Password, Tailscale, Little Snitch, TCC, Gatekeeper), workspace-trust
UX (VS Code "trust this workspace", git dubious-ownership), and permission-habituation research,
triaged against the seven cockpit surfaces.

Provenance: **[C]** cross-family consensus (Gemini + GLM independently) · **[G]** Gemini-unique ·
**[Z]** GLM-unique · **[H]** host/engine-grounded.

---

## Headline — the three load-bearing insights

1. **Calibrated friction is the whole design.** The safe default (`sandbox`, `network:deny`) is
   *zero* friction — one click, no prompt. Every step *up* in authority adds exactly **one**
   specific, native friction, and never the same one twice: `host` tier / write-creds → **TouchID**
   (proof-of-intent at the privilege boundary, never per-session); a *changed* policy → an
   **in-place PTY freeze + capability diff**; closing a live-egress window → **hold-to-confirm**.
   Friction tracks consequence. Uniform friction (a modal on everything) is the documented failure
   mode — VS Code's content-free "trust this workspace" got a reflexive *Trust* click ~78% of the
   time. **[C]**

2. **The trust signal must be host-drawn and ambient, never inside the cage.** A coding agent owns
   the PTY and can emit ANSI to paint a fake green "safe" border *inside* the terminal. So the
   isolation posture must live where the agent can't reach: a **window-level material tint** (the
   terminal scrolls *under* it — un-spoofable), a **titlebar SF-Symbol**, and a **menu-bar item**.
   `network:allow` sessions are globally ambient (floating window + red material + a live menu-bar
   row) so an open-egress agent is *impossible to lose behind another window.* **[C, H-confirmed]**

3. **Speak capabilities, not CUE — and let the engine be the single source of truth.** Audience A
   never sees CUE: the trust sheet and tier indicator render **plain-language capabilities** ("can
   reach the internet", "can write to git", "runs outside its folder") with the approve button
   naming the **highest-risk delta** ("Approve — adds `network: allow`"). Audience B presses Space
   for the raw byte diff (Quick Look). Both render from the **engine's** authoritative
   `policy.EnvTier` + the resolved profile + the existing `sandbox-open-egress-with-creds` lint —
   the GUI must not re-derive labels (that's exactly the honest-tier discipline specs/0023 bought).
   **[C + H]**

---

## Triaged lessons (by surface)

### Surface 1 — launcher / profile-picker

- **HIGH [C] Menu-bar-first; the default tier is the one-click target, alternatives are muted.**
  Tailscale's *useful* state is one glance from the menu bar; status behind a Dock icon gets
  ignored. The most-recently-approved/safe profile is the default-focused `↵` target
  (`.borderedProminent`); non-default tiers render `.secondary` and need explicit selection.
  NATIVE: `MenuBarExtra(... .menuBarExtraStyle(.window))`; `List(.sidebar)`.
- **HIGH [Z, H] Absence of a `safeslop.cue` is *not* consent.** No policy found ⇒ untrusted; repos
  discovered on disk are *listed but greyed* with a per-row **Approve**, never auto-run (git
  dubious-ownership avoids a whole CVE class this way). NATIVE: `NSMetadataQuery`/Spotlight scope +
  per-row approve. (The engine already fails closed here — this is the GUI making it legible.)
- **MED [G] Pre-compute trust state and badge it *before* launch**, so the user is never ambushed
  by a block-modal post-click (ambush trains blind-approve). NATIVE: `List` row + amber
  `exclamationmark.triangle.fill` when bytes changed since approval.

### Surface 2 — session window (terminal + trust chrome)

- **HIGH [C, H] Trust chrome = un-spoofable window material tint, not a border.** Borders stop
  registering (~30 min, the TCC-dot effect) and an in-PTY agent can fake one. A host-drawn
  `.regularMaterial` tint that the SwiftTerm view sits *inside* "turns the lights on in the room"
  and can't be spoofed. NATIVE: `.safeAreaInset` + `.background(.regularMaterial)` +
  semantic `.tint`; SwiftTerm background inherits it. **Replaces today's `SessionScene.trustColor`
  border — and sources the color from the engine's tier/network, not re-derived client-side.**
- **HIGH [Z] Live-egress sessions are globally ambient.** `network:allow` ⇒ `NSWindow.level =
  .floating` + a live menu-bar row, so a red open-egress session can't hide. NATIVE: window level +
  `MenuBarExtra` session list.
- **MED [Z] Calibrate the close gesture.** `⌘W` on a `network:allow` window = **hold-to-confirm**
  ("hold to close — egress tears down"); `sandbox`/`vm` close instantly. NATIVE:
  `windowShouldClose` + a hold-continuous button.
- **HIGH [G] Never overlay app-clickable controls inside the SwiftTerm bounds** — an agent can print
  text that looks like a button. App chrome (titlebar/toolbar) is strictly separated from the PTY view.

### Surface 3 — trust-approval flow

- **HIGH [C] Render a capability/byte diff, never a generic "Trust this config?".** VS Code's
  content-free dialog ⇒ ~78% reflexive Trust. The approve button text is the highest-risk change.
  Audience A: plain-language capability rows (`Label` + SF Symbols `network`, `key.fill`); Audience
  B: raw diff via **Quick Look (Space)**. NATIVE: `.confirmationDialog` whose title is the delta;
  `QLPreviewPanel` for the raw bytes. **Driven by the Trust RPC already on `main` (this branch):
  the engine returns the not-trusted error + path; on approve the GUI calls `Trust`.**
- **HIGH [C] A policy change *during* a session freezes the PTY in place — no sibling modal.** Input
  disabled, a top banner offers *Review & re-approve* / *Kill session*. The violation and its
  resolution live in the same surface (iOS freezes the offending app, not a sibling). NATIVE:
  `isUserInteractionEnabled=false` on the terminal + a `NSVisualEffectView` top strip.

### Surface 4 — isolation-tier indicator

- **HIGH [Z] Use Apple's own glyphs + semantic colors, not custom art** (years of free training,
  legible at 16px, auto-adapts to Dark/accessibility): e.g. `lock.fill`/green = sandbox,
  `scope`/amber = container, `externaldrive…`/amber = vm, `exclamationmark.octagon`/red = host.
  NATIVE: `NSTitlebarAccessoryViewController` + `Image(systemName:)` + `NSColor.system*`. **The tier
  string + caveat come from the engine's `EnvTier`, surfaced over gRPC — one source of truth.**
- **MED [Z] The tier hover lists what the tier *cannot* do first** (least-authority framing reads as
  an enforced promise; positive "can…" lists read as marketing). The engine's `EnvTier` note already
  carries the honest caveat — render it.
- **LATER [G] Downgrade authority is one click; upgrade requires the full flow** (the panic action —
  "kill egress now" — must be the easiest thing on screen).

### Surface 5 — privileged actions (host launch, write creds)

- **HIGH [C] TouchID only at the privilege boundary, remembered per-policy until the cue changes.**
  Biometrics are proof-of-intent *only while rare* (per-command TouchID → ~50% reflex-tap in two
  weeks). Request it for: launch `host`, grant write-creds, approve an *edited* policy. **Never** for
  opening a sandbox session, switching profiles, or attaching. NATIVE: `LAContext.evaluatePolicy
  (.deviceOwnerAuthenticationWithBiometrics)`, cache "approved at" keyed to the policy hash. (This is
  the review's M7, and it composes with the engine's S1b peer check.)
- **HIGH [C] No "Forever" for write creds — force a decay TTL with a ceiling**, and name the exact
  repo + cred + TTL in the confirm. Users pick "Forever" if offered. A `.destructive`
  `confirmationDialog` with a ~3s countdown disabling Confirm defeats reflex-taps without blocking
  intent. NATIVE: `Picker` of fixed `TimeInterval`s; `role: .destructive` + countdown. (Matches the
  decay-first credential model + the read-only-default deploy key.)

### Surface 6 — install / bootstrapper

- **HIGH [C] Bidirectional codesign peer-trust.** The GUI verifies the **engine's** codesign
  (Team ID + bundle) *before the first gRPC byte* — else a swapped binary drives the trusted GUI into
  TouchID approvals; and the engine verifies the **GUI** (the deferred S1 codesign residual, now
  actionable because the GUI is what gets signed). NATIVE: `SecCodeCopyGuestWithAttributes`
  (`kSecGuestAttributePid` from `getpeereid`) + `SecRequirementCreateWithString`. **This unblocks the
  codesign half of the control-plane fix once the app is signed.**
- **HIGH [C] Install the engine via `SMAppService`, never a sudo shell script or legacy `launchd`
  plist** (the latter trips "Background Items Added" fatigue; the former is the modern, OS-managed
  path). NATIVE: `SMAppService.daemon/loginItem`.

### Surface 7 — blocked-state surfacing

- **HIGH [Z] Egress denials are inline at the terminal cursor, not banners/sheets.** Denials are
  *frequent*; a separate channel per denial is textbook habituation. A red glyph + hover
  "blocked: api.openai.com:443" + a session "N blocked" toolbar badge keeps it where attention
  already is (git's inline refusals beat VS Code's modal). NATIVE: SwiftTerm annotation + toolbar
  badge. (The engine's squid/`Decide` denials feed this.)
- **MED [C] Credential decay = ambient battery-style escalation, not a modal.** Silent badge at 25%
  remaining → `.systemYellow` at 10% → pulsing `.systemRed` at 3% → only *at* expiry a
  `UNUserNotificationCenter` banner with an **Extend** action (TouchID-gated), and only if the app
  isn't frontmost. NATIVE: `UNTimeIntervalNotificationTrigger` + frontmost check.
- **LATER [G] Window-shake for a denied action** (the native wrong-password shake) — visceral "no"
  without a dialog to dismiss.

---

## Contested / deferred (named, not acted on)

- **Trust store in Keychain vs `~/.config/safeslop/trust.json` [Z].** GLM argues the only
  tamper-resistant store is a signed Keychain item (`SecItemAdd`, ECC-signed), not JSON. But our
  JSON store already lives outside the agent-writable workspace and the **sandbox can't reach it**
  (verified, specs/0024) — it's adequate for the *agent* threat. Keychain additionally resists
  *same-uid host malware* rewriting trust — the same residual class as the codesign peer-auth gap,
  and a bigger lift from Go (Keychain needs a `security`/cgo path). **Defer**; revisit with the
  codesign work, not now.
- **Quarantine-xattr re-prompt on external edit [Z].** Clever, but our sha256 trust gate *already*
  detects any byte change (vim/VS Code/`cat >`); quarantine would be a redundant detector. Keep the
  hash; skip the xattr.

---

## Actionables (numbered → surface; ✦ = ties to something already built)

1. **Menu-bar-first shell** with the safe default as the one-click `↵` target; live-egress sessions
   floating + ambient. → S1/S2/S4.
2. **Host-drawn material trust tint** replacing the border, **sourced from the engine** (add tier +
   network + the `sandbox-open-egress-with-creds` lint to a gRPC `SessionInfo`/`ListProfiles` field;
   the GUI never re-derives labels). ✦ EnvTier (specs/0023), egress lint. → S2/S4.
3. **Capability-diff trust sheet** (A: plain-language + SF Symbols; B: Quick Look raw), button = the
   highest-risk delta, driven by the **Trust RPC** + the not-trusted error. ✦ Trust RPC (this branch,
   `sp-cockpit-trust`). → S3.
4. **In-place PTY freeze** on mid-session policy change (banner: re-approve / kill), no sibling modal.
   → S3/S7.
5. **TouchID at the privilege boundary only** (`host`, write-creds, approve-edited-policy),
   per-policy memory; **no "Forever" creds**, countdown confirm naming repo+cred+TTL. ✦ review M7,
   decay-first creds. → S5.
6. **Bidirectional codesign peer-trust** + `SMAppService` install. ✦ closes the S1 codesign residual
   (specs/0024) once signed. → S6.
7. **Inline egress denials** at the cursor + session badge; **ambient cred-decay escalation** with a
   TouchID-gated Extend at expiry. ✦ squid `Decide`, decay TTLs. → S7.

---

## Net

The cockpit's safety and its ergonomy come from one rule: **make the safe default frictionless and
charge exactly one calibrated, native friction per step up in authority** — TouchID at the privilege
boundary, an in-place freeze on a changed policy, hold-to-confirm to drop egress — while the trust
signal lives where the agent can't paint it (host-drawn material + menu bar) and every label speaks
capabilities sourced from the engine's already-honest `EnvTier`, not re-invented in Swift. Three
pieces are already in hand to build on: the honest tier labels (specs/0023), the Trust RPC (this
branch), and the codesign residual that the signed app finally lets us close. The load-bearing
reframe — friction calibrated to consequence, and an un-spoofable ambient trust signal — is what
turns the GUI from "a window with a terminal" into the thing that makes the safe path the only
obvious one.

---

## Method footer

Cross-family `ayo`. Lanes: **Host** (Anthropic, Opus 4.8 — own mining + grounding against the engine:
`EnvTier`, the Trust RPC, the egress lint, the SwiftTerm embedding), **Gemini 3.1 Pro** (Google, via
`ai-router`/OpenRouter, ZDR), **GLM-5.1** (Zhipu, z.ai). Kimi (Moonshot) was down for the session
(timed out earlier). Lanes were blind (identical brief, none saw another's output); the Host lane
alone compiled and triaged. Source of truth for the cockpit: `app/Sources/SafeSlopCockpit/*`,
`internal/engine/control/*`, `internal/engine/policy/policy.go` (`EnvTier`), specs/0012–0017, 0023,
0024.
