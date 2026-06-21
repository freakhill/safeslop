# Cockpit GUI-flow ergonomics (cross-model research)

**Date:** 2026-06-21
**Method:** ayo — host (Opus 4.8, synthesizer) + Gemini 3.1 Pro (ai-router ZDR) + Kimi K2.7 + GLM-5.1,
blind lanes, identical brief, grounded in real screenshots of all three tabs (`make cockpit-shot
{launch,installs,create}`), compiled + pertinence-triaged here.
**Surfaces:** S1 tab IA · S2 Launch risk-legibility · S3 trust gate / dangerous-action · S4 Installs
detect/no-clobber/stream · S5 Create authoring · S6 session window / terminal · S7 cross-cutting
(first-run, errors, empty, latency).

---

## Headline (load-bearing)

1. **The honesty mandate turns "dark-pattern avoidance" from nice-to-have into a HARD spec.** Four
   laws that ordinary apps skip, a security tool cannot, and all are cheap given our architecture:
   *symmetric* trust grant/revoke, show what's **unrestricted** (not only what's restricted), never
   show a stale "valid" while validating, and never inflate an "N available" counter into false
   urgency. These are the highest-ROI items here — they're mostly text/state changes, not new UI.
2. **Color is the cockpit's single biggest legibility failure mode.** Every lane independently flagged
   it: the ecusson encodes *tier* in its glyph but encodes *danger level* in **background color alone**
   (green/orange/red). That signal dies for the ~8% red-green-colorblind, in a print-to-PDF, and in a
   Slack screenshot. The consequence *sentence* is already redundant (its words carry the meaning); the
   **ecusson danger level and the meta line are not.** Make color the redundant channel, never the sole
   one.

---

## Triaged lessons  ([C]=cross-model consensus · [G]/[K]/[L]/[H]=Gemini/Kimi/GLM/host-unique)

### A. Risk legibility on Launch — S2

- **[C] HIGH — Danger level must be redundant with color.** The ecusson's red/orange/green background
  is the only carrier of danger level (the glyph carries tier, not danger). Add a second channel:
  a danger *word* (`high`/`elevated`/`contained`) or a shape/border-weight difference, so risk survives
  colorblindness, grayscale, and screenshots. EVIDENCE: HIG "color conveys no information alone"; macOS
  TCC privacy badges and Little Snitch (20 yrs) always pair symbol+word+color. Constraint: native SF
  Symbols, no custom glyph fonts.
- **[L] HIGH — Show what is UNRESTRICTED as loudly as what is restricted.** The meta line
  ("shell · egress-allowlisted · net:deny") states positives; absences (no file denylist, secrets
  reachable, no egress cap) are invisible. Enumerate all axes with an explicit `unrestricted` value,
  amber/red. EVIDENCE: Little Snitch lists DENY rules at equal prominence; hiding absence is a dark
  pattern even when unintentional. Native fit to the honesty mandate.
- **[L] HIGH — Make Launch's consequence line consistent with Create's arbiter framing.** Create says
  "If this agent is compromised, it can: …"; Launch uses a terser line. Adopt the same agentive,
  threat-named voice on Launch so the discover→understand step reads identically to the authoring
  preview. EVIDENCE: GitHub/Terraform/kubectl all use agentive declarative phrasing; passive voice
  trains skimming.
- **[K] MEDIUM — Put a heavier separator above the host tier**, physically exiling "no isolation"
  below a line rather than as just-another-row. EVIDENCE: macOS Gatekeeper grouping; reduces adjacent-
  row misclicks (motor-memory safety). Native SwiftUI section/separator; cheap.
- **[L] MEDIUM — Make the safest-first sort *visible*** (a thin rank, or an "isolation: strongest →
  weakest" caption) so the gradient reads as intentional, not alphabetical. EVIDENCE: Activity Monitor/
  htop show the sort column; an invisible sort implies "equivalent choices," which is dishonest.
- **[H] MEDIUM — Align the meta axes into columns, not free-flowing middle-dots.** Dense status rows
  (k9s, Activity Monitor) align each axis so the eye scans one dimension down the whole list; our
  "a · b · c" text defeats vertical comparison across profiles. S2.

### B. Trust gate & dangerous-action confirmation — S3

- **[C] HIGH — The Touch ID reason string must name the specific consequence**, not "safeslop wants to
  make changes." For host tier: *"Authenticate to run an AI agent with full account + network access
  (no isolation)."* EVIDENCE: LAContext; 1Password/sudo name the action; generic prompts habituate.
  Constraint: LocalAuthentication, no LAContext reuse across launches.
- **[C] HIGH — Auto-revoke trust when `safeslop.cue` changes outside the app** (hash the trusted bytes;
  the engine file-watcher clears trust on external edit, forcing a re-review of the arbiter). EVIDENCE:
  VS Code Workspace Trust suspends auto-exec on untrusted/changed workspaces; prevents drive-by edits.
  S3+S7.
- **[L] HIGH — Trust grant and revoke must cost the same.** The green "trusted" check should be a
  *toggle* (click → inline "revoke trust?" at the same friction as granting), not a one-way badge.
  EVIDENCE: TCC's grant-in-one-dialog / revoke-in-System-Settings asymmetry is the canonical privacy
  anti-pattern; 1Password trusted-devices grants+revokes in the same row. Honesty-mandate native.
- **[L] CONTESTED → FLO — Temporal-scoped trust (once / this session / until revoked).** GLM/Kimi want
  Little-Snitch-style scope chosen at the trust moment, defaulting to session; our current model is
  persistent `trust.json`. This is a real model change (and a contested friction call) — hand to a FLO
  before building. EVIDENCE: Little Snitch's once/forever radio is the most-copied firewall pattern.
- **[L]/[K] CONTESTED → FLO — How much friction for a host-tier launch?** GLM: consequence card →
  type-the-profile-name (comprehension) → Touch ID (identity). Kimi: never stack confirmations — the
  biometric is the final word. Synthesis: ONE flow (a consequence card with an inline type-to-confirm
  field, committed by Touch ID), not modal-on-modal. The exact friction is a judgment call → FLO.
  EVIDENCE: GitHub type-to-delete exists *because* click-confirm still mis-fires; Nielsen on stacked
  confirmations.

### C. Config-as-code authoring — S5

- **[C] HIGH — Route New/Delete/Merge through schema-aware AST edits in the engine, not text surgery.**
  Already the known fragility (specs/0029) and the open FLO hand-off; all three families confirm it
  independently. Until then, at minimum **show the exact text delta before committing** and disable
  New/Merge while the buffer is syntactically invalid. EVIDENCE: VS Code/JetBrains both abandoned string
  insertion for schema-aware edits; CUE unification errors span fields.
- **[C] HIGH — Debounce validation (~300–500ms) and show an explicit `validating…` state.** Never
  display a stale green "valid" mid-keystroke; flickering red on every char is hostile. States:
  validating (grey) → valid (green) → invalid (red+count). EVIDENCE: VS Code/JetBrains language-server
  spinners; a lying indicator corrodes trust in a security tool. S5+S7.
- **[K] HIGH — Render the generated `files:{}` block as literal monospaced CUE right under the drag-drop
  lanes**, so direct manipulation is paired with "here's exactly what you authorized." EVIDENCE: VS Code
  devcontainer hover shows the raw key; abstract color pills hide scope. Plain ASCII.
- **[C] HIGH — Arbiter preview should be a DIFF when editing an existing profile**, not just the new
  absolute state ("+ Network: open egress", "− deny: ~/.ssh"). EVIDENCE: Terraform plan / IAM
  visualizers — users can't spot one changed permission in absolute state. S5.
- **[L]/[K] MEDIUM — Presets and Delete are diff-previewed and reversible.** A preset must show a
  unified diff + "Apply" / "Apply as new profile" (non-destructive) rather than silently overwriting —
  a preset that quietly rewrites `egress: [api.anthropic.com]` → `[*]` is catastrophic. EVIDENCE:
  JetBrains "Show Diff" before intention; honesty mandate forbids magic buttons.
- **[L] MEDIUM — Risk-asymmetric drag gestures:** dropping a path INTO `deny` is gesture-light;
  pulling one OUT of `deny` needs a hold+confirm. EVIDENCE: asymmetry matched to the risk gradient is
  good UX (GitHub delete-vs-restore). Uses SwiftUI long-press.
- **[L] MEDIUM — Never present a blank editor.** Empty Create / missing `safeslop.cue` bootstraps from
  a one-click stdlib preset with a preview of the profiles it adds. EVIDENCE: Tailscale/devcontainer
  start from a working default; a blank CUE buffer is hostile to discovery. S5+S7.
- **[K] DEFERRED — Preset provenance marker** (`// safeslop:preset:<name>` stripped on save). Nice
  traceability; lower priority than the diff itself.

### D. Installs — detect / no-clobber / stream — S4

- **[K]/[L] HIGH — "which -a" multi-path detection; flag shadowed binaries.** Surface every path a tool
  resolves to and tag `ambiguous` when `which` and our engine disagree (e.g. `/usr/local/bin/docker`
  vs `/opt/homebrew/bin/docker`). Directly relevant: our new `hostenv` reconstructs PATH, so the engine
  already knows the resolution order — expose it. EVIDENCE: `brew doctor`, pyenv/nvm exist for shadowing;
  picking the wrong binary can silently downgrade isolation. S4 (+ ties to internal/engine/hostenv).
- **[L] HIGH — Don't inflate the "available" counter.** "4 available" out of a 27-tool catalog
  manufactures urgency. Scope it to what detected profiles actually need ("1 needed for your profiles")
  or drop "available" entirely. EVIDENCE: App Store badge counts are the canonical dark pattern; honesty
  mandate. Cheap.
- **[L] MEDIUM — Three tool states, not two:** present-compatible / present-incompatible (amber, no
  install, "what's wrong") / missing (install). Treating "wrong version" as "missing" invites a clobber.
  EVIDENCE: Docker/Terraform version pinning. Respects no-clobber's spirit.
- **[C] MEDIUM — Streaming install log: scroll-anchor + sticky errors + persistent (non-modal) drawer.**
  Suspend auto-scroll the instant the user scrolls up (Console.app/CI-log failure mode); pin warnings/
  errors to the top so line 247 isn't where the one that matters hides; keep the log readable across tab
  switches. EVIDENCE: Docker Desktop/OrbStack moved installs from modal sheets to persistent drawers.
- **[G] MEDIUM — "Re-detect" is async with a "last checked HH:MM" timestamp**, inline ProgressView not
  a blocked thread. EVIDENCE: scanning 27 tools takes seconds; blocking reads as frozen.

### E. Session window & terminal — S6

- **[C] HIGH — Persistent tier-colored window chrome + profile name on every session window.** A glance
  at Mission Control must identify the dangerous windows; tier color should reach into the terminal
  `PS1`/MOTD too. EVIDENCE: VS on Windows draws a yellow border when elevated; Terminal.app red dot for
  root; iTerm2 profile colors; VS Code Remote colors the status bar — all persist for the session's
  life, preventing "ran it in the wrong window." Native SwiftUI Window toolbar accessory; ASCII MOTD.
- **[C] MEDIUM — Window-close confirmation only when an agent is RUNNING, with specific copy.** "This
  will kill `claude` mid-session (host tier, runs as you)" — not a generic "are you sure." Lower
  friction for vm/container (sandboxed state), higher for host. EVIDENCE: Terminal.app confirms only on
  a live foreground process; generic confirms fatigue.
- **[H] MEDIUM — Stream the *provisioning steps* between click and terminal-ready**, don't show a bare
  spinner — especially the vm tier (Tart boot is slow): "resolving image → minting ephemeral creds →
  booting VM → attaching." Makes a 30s wait feel intentional and debuggable. EVIDENCE: VS Code Remote-
  Containers shows the dev-container startup log live. S6+S7.
- **[K]/[L] MEDIUM — Mark a session window `[stale]` when its profile is edited after launch**, with a
  plain banner: "Policy changed since launch; new sessions use the updated policy." Honesty: don't imply
  a live policy swap on a running process. EVIDENCE: Remote-Containers warns devcontainer.json changes
  need a rebuild.

### F. Cross-cutting: connection, errors, first-run — S1 / S7

- **[C] HIGH — Engine-down degrades gracefully to last-known state + timestamp, never empty grids.**
  Disable Launch arrows, freeze the editor read-only, keep showing "3 profiles (last sync 14:32)"; no
  blocking modal. EVIDENCE: Tailscale greys toggles + "engine not running"; 1Password-locked shows
  structure not blanks. Empty-state-on-disconnect erases the user's mental model. S1+S7.
- **[K] MEDIUM — Connection status is a three-state TEXT label** ("engine live v0.4.2 / syncing /
  unreachable"), not a colored dot — a binary dot collapses "slow" and "dead." Matches our no-decorative-
  glyph hygiene. (We already render text — keep it three-state, not two.)
- **[G] MEDIUM — Map gRPC failures to plain, actionable copy**, never raw "deadline exceeded": "The VM
  engine didn't start in 30s — check Tart is installed." EVIDENCE: a traceback is useless to the user;
  honesty + actionability.
- **[H] MEDIUM — Keyboard-first launch (⌘K fuzzy "launch profile…").** Power users launch by name, not
  mouse; the dock menu already lists profiles — add a command surface. EVIDENCE: Spotlight/Raycast/VS
  Code palette. S1/S7.
- **[G] DEFERRED — Skeleton/redacted rows during initial parse.** Already largely satisfied: the catalog
  paints instantly with deferred detection ("?" → detected); apply the same `.redacted` idiom to the
  Launch list on cold start if profile parse is ever slow.

---

## Actionables (numbered → surface)

1. **S2 — Add a non-color danger channel to the ecusson** (danger word / border-weight) + enumerate the
   meta line's *unrestricted* axes in amber/red. (HIGH, cheap, honesty-native.)
2. **S2 — Adopt Create's "If this agent is compromised, it can:" voice on the Launch consequence line**;
   make the safest-first sort visible (rank/caption); align the meta axes as columns. (HIGH/MED.)
3. **S3 — Consequence-specific Touch ID reason string** for host tier; **make the trusted badge a
   symmetric toggle** (grant == revoke clicks); **auto-revoke trust on external `safeslop.cue` change**
   (engine watches + hashes). (HIGH.)
4. **S3 — FLO hand-off:** host-launch friction (type-to-confirm + Touch ID, one flow) AND trust temporal
   scope (persistent vs once/session/forever). Two contested decisions — score before building.
5. **S5 — Show the literal generated `files:{}` CUE under the drag-drop lanes**; make the arbiter a
   **diff** when editing; debounce validation with an explicit `validating…` state; preset/Delete are
   diff-previewed + reversible; disable New/Merge on invalid buffers; risk-asymmetric `deny` gesture.
   (HIGH/MED — and the AST-edit FLO in specs/0029 remains the durable fix.)
6. **S4 — Surface multi-path/shadowed detection from `hostenv`'s resolved PATH** (`ambiguous` tag); stop
   inflating "available" (scope to needed); add present-incompatible as a third state; install-log
   scroll-anchor + sticky errors in a persistent drawer; async Re-detect + "last checked." (HIGH/MED.)
7. **S6 — Persistent tier-colored window chrome + profile name + PS1/MOTD**; agent-running-only close
   confirmation; stream provisioning steps (esp. vm); `[stale]` on post-launch policy edits. (HIGH/MED.)
8. **S1/S7 — Graceful engine-down (last-known + timestamp, read-only, no modal)**; gRPC→plain-English
   error map; never-blank Create (stdlib bootstrap); ⌘K launch palette. (HIGH/MED.)

## Net

The cross-model consensus is unusually tight, and it points one way: **for a security tool, the
ergonomic wins and the honesty wins are the same wins.** The biggest, cheapest gains are making risk
*redundantly* legible (kill color-only signalling; show the unrestricted, not just the restricted) and
making trust *symmetric and self-revoking* — almost all text/state changes, no new screens. Two
genuinely contested calls (host-launch friction; trust temporal scope) deserve a FLO before code. The
durable structural debt (AST-based New/Delete/Merge) is re-confirmed by every family and stays the one
big refactor. The new `hostenv` PATH-resolution work has a free dividend on S4 (shadowed-binary
detection) waiting to be surfaced.

## Method footer

Families: host (Opus 4.8, synthesizer) · Gemini 3.1 Pro (ai-router ZDR, 17 lessons) · Kimi K2.7 (~16) ·
GLM-5.1 (~19). Blind lanes, identical brief grounded in real screenshots of all three tabs. ZDR /
subscription routes only; no anthropic/* or moonshotai/* via OpenRouter.
