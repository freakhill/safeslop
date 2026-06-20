# 0029 — Cockpit GUI: the safe-by-design profile manager

**Goal:** Turn the cockpit from a launcher into a full native macOS app that makes authoring, vetting,
and launching sandbox profiles safe-by-design — where the safe path is the simplest path.

**Architecture:** SwiftUI app (`app/`) over the existing Go engine via the gRPC control plane
(`internal/engine/control`, unix socket). Three tabs (Installs / Launch / Create), plus dock-menu and
CLI launch surfaces. CUE policy text stays the single source of truth (it's what the trust gate
hashes); the GUI is a *least-privilege generator* over it. All capability framing derives from the
engine's compiled `policy.EnvTier`, never raw CUE — one capability vocabulary shared by the editor,
the arbiter, the trust sheet, and (later) the dynamic-network UI.

**Tech stack:** SwiftUI + SwiftTerm (sessions) · grpc-swift v2 · the Go engine (`cmd/safeslop`,
`internal/engine/*`) · CUE via `cuelang.org/go` (embedded) · a CUE LSP for text mode · macOS
LocalAuthentication (TouchID), NSStatusBar (key HUD), NSMenu (dock), NetworkExtension (Phase 3).

**Design foundation:** `specs/research/2026-06-20-cockpit-gui-safe-by-design.md` (cross-model research;
read it first — the lesson IDs and surface IDs S1–S12 below reference it).

---

## Safe-by-design principles (load-bearing — every surface obeys these)

1. **Text-canonical.** CUE bytes are truth; the visual editor is a lossless *safe subset* and **locks**
   ("Controlled via code") when text exceeds what it can represent. Never round-trip-corrupt a guard.
2. **Safety as consequence, not score.** Every approval/arbiter surface renders a concrete break-glass
   sentence ("if compromised, can read `~/.ssh`, reach `*.openai.com`") + a one-click narrow. No
   numeric scores, no bare tier names.
3. **Friction is a precision tool.** Spent *only* at privilege boundaries — permanent network grants,
   tier downgrades, secret reveals, policy changes, host tier — and removed everywhere else. An
   already-trusted, unchanged policy launches with zero friction.
4. **Least-privilege is the default keystroke.** Pre-selected options, autocomplete sort, drag-drop
   scope, ephemeral keys, and templates all bias to the narrowest grant; breadth costs extra interaction.
5. **Honest tiers.** Label by what a tier *blocks*, propagate `policy.EnvTier`'s honest caveats; sandbox
   stays visually "mistake-guard amber", never dressed up as escape-proof.
6. **Un-spoofable host chrome.** The agent owns only the terminal buffer; tier tint + identity are
   host-drawn across all tabs.

---

## Information architecture

- **Tab 1 — Installs (S1a):** GUI over the existing `InstallPlan`/`InstallApply` control RPCs. Shows the
  pinned desired-state diff, fail-closed verify, progress. Also surfaces NetworkExtension/system-extension
  install state and deep-links System Settings (gates Phase-3 tier creation).
- **Tab 2 — Launch (S1b):** profile list, safest-tier-first, "last used", grayed-out when a referenced
  secret/path/key is missing or revoked. Per-profile tier chrome + the arbiter's one-line consequence.
  Click → the existing session window (SwiftTerm + trust sheet + ctty, already shipped).
- **Tab 3 — Create/Edit (S1c):** the dual editor (visual + CUE text), the arbiter pane, the file/network
  scope editors, repo+key flows, AI assist, and learning-mode capture.
- **Dock menu (S11):** right-click → safest-tier-first profiles + a pre-seeded "Quick Start: Claude in VM
  (disposable)". **CLI (S12):** `safeslop launch <profile> --config <dir>` (specs/0028); both gate via a
  notification→GUI TouchID bridge *only* when trust is required (changed/untrusted/downgrade).
- **Menu-bar HUD (S4):** "🔥 N keys active" + countdown + Revoke-All, present whenever a session runs.

---

## Phased roadmap

**Phase 1 — buildable now, no new OS entitlements (this spec details it as tasks):**
Launch tab polish, Install tab over existing RPCs, the dual editor skeleton (visual subset + text with
CUE validation/diagnostics), arbiter v1 (compiled-policy consequence sentences), file-scope editor with
drag-drop + auto-deny-secrets, dock menu + CLI gate-bridge, ephemeral-key HUD. Trust-gate semantic diff.

**Phase 2 — next, needs design/integration but no NE:**
CUE LSP (safe-first `sortText`, downgrade quick-fixes, tier-mismatch diagnostics, secret pills, module
pinning), GitHub/Forgejo device-flow + ephemeral org keys + repo-metadata starters, AI-assisted authoring
(AST→checklist, tier-cap, sandboxed authoring agent), **learning/dry-run mode** (log-only Seatbelt/squid
observe → proposed deny-by-default delta, auto-expiring). Per-tier tool compatibility matrix + "add to
image" wired to `image.extra-*`.

**Phase 3 — gated on the NetworkExtension shipping:**
LuLu-style dynamic network UI: temporary-allow default, permanent=friction+re-trust, group-by-profile/
intent, gate profile creation on NE-installed. Shares the Phase-1 capability vocabulary; slots in.

**FLO hand-offs before building the contested bits** (run a feedback-loop pass, don't decide ad hoc):
(a) how much visual mode may author vs force-to-text; (b) Create flow: wizard-first vs editor-first.

---

## Phase 1 tasks

Conventions: Swift code under `app/Sources/SafeSlopCockpit/`; Go engine changes under
`internal/engine/`; gate every change with `make check` + `swift build`; commit per task.

### Task 1: Tab shell + Launch/Install tabs over existing RPCs

**Files:**
- Create: `app/Sources/SafeSlopCockpit/UI/RootTabs.swift` — `TabView` with Installs/Launch/Create.
- Create: `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` — profile list (reuses `cockpitListProfiles`
  data: tier, tierNote, trustStatus, configDir), safest-tier-first sort, last-used, missing-dep graying.
- Create: `app/Sources/SafeSlopCockpit/UI/InstallsTab.swift` — calls `InstallPlan`/`InstallApply`.
- Modify: `app/Sources/SafeSlopCockpit/SafeSlopCockpitApp.swift` — root scene → `RootTabs`.

- [ ] Launch tab renders the existing profile refs with tier chrome + the arbiter one-liner (Task 4),
      sorted vm→container→sandbox→host, host rows visually "mistake/none" framed.
- [ ] Install tab streams `InstallApply` progress; shows NE/system-extension status with a System
      Settings deep-link (no-op until Phase 3, but the slot exists).
- [ ] `swift build`; commit.

### Task 2: Dual editor skeleton (text-canonical)

**Files:**
- Create: `app/Sources/SafeSlopCockpit/UI/ProfileEditor.swift` — split view: visual form (left) | CUE
  text (right) with a mode toggle (Tailscale-style).
- Create: `internal/engine/control/control.proto` (modify) — add `ValidatePolicy(text) → {ok, diagnostics[], compiled}`
  and `CompilePolicy` returning the `EnvTier` capabilities per profile (for the arbiter). Regenerate (`make proto`).
- Create: `internal/engine/control/server.go` (modify) — handlers calling the existing
  `policy` package (parse/compile) and returning diagnostics + compiled capabilities.

- [ ] Text edits are canonical; the visual form re-renders *from* parsed text. When parse yields
      constructs the form can't model, the affected controls show a non-dismissible "Controlled via
      code" lock (principle 1).
- [ ] Invalid CUE shows inline diagnostics (line/col) from `ValidatePolicy`; valid CUE updates the
      visual form + the arbiter.
- [ ] Go: `ValidatePolicy`/`CompilePolicy` unit tests (valid, invalid, advanced-construct cases).
- [ ] `make check` + `swift build`; commit.

### Task 3: File-scope editor — drag-drop + auto-deny secrets

**Files:**
- Create: `app/Sources/SafeSlopCockpit/UI/FileScopeEditor.swift` — drop target (NSItemProvider) + a
  scoped tree view (target green, dangerous parents red, `~`/`/` hard-disabled from the picker).
- Modify: `internal/engine/policy/*` — a helper that, given a scoped dir, returns the secret-bearing
  children to auto-deny (`.git`, `.ssh`, `.env`, `.aws`, `.npmrc`, `id_*`).

- [ ] Dropping a folder adds it as the file scope; if dev-tool caches likely sit in a parent, propose
      the minimal-working parent with an explicit acknowledgment banner (research C/drag-drop).
- [ ] Scoping any dir auto-inserts `deny` rules for its secret-bearing children into the CUE; the user
      must manually un-deny (and that un-deny is a privilege boundary → arbiter flags it).
- [ ] Go test for the auto-deny child enumeration; `make check` + `swift build`; commit.

### Task 4: Arbiter v1 — consequence sentences from the compiled policy

**Files:**
- Create: `app/Sources/SafeSlopCockpit/UI/ArbiterPane.swift` — renders break-glass sentences + one-click
  "narrow" actions; a "can it do X?" query box.
- Modify: `internal/engine/policy/*` + the `CompilePolicy` RPC (Task 2) — emit the capability set
  (file read/write roots, network mode + allowlist, secrets exposed, tier caveat) the arbiter renders.

- [ ] For a profile, the arbiter prints concrete sentences ("If compromised, this profile can: reach any
      host; read `~/`") — derived from `EnvTier` + the compiled policy, never raw CUE, never a score.
- [ ] "Can it do X?" evaluates `read <path>` / `connect <host>` against the compiled policy → red YES /
      green NO. Each risky finding has a one-click narrow that edits the canonical CUE text.
- [ ] Go tests for the capability extraction across all four tiers; `make check` + `swift build`; commit.

### Task 5: Trust gate — semantic diff before TouchID

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/UI/SessionScene.swift` (`TrustSheet`) — show a diff vs the
  last-trusted policy ("Network: `deny` → `allow: github.com`") above the hash, enforce a ≥1s delay,
  build the TouchID `localizedReason` from agent+tier+change.
- Modify: `internal/engine/trust/*` — return the previously-trusted bytes (or a structured diff) so the
  sheet can render the change, not just the new hash.

- [ ] Re-trusting a changed policy shows *what changed* before the hash + TouchID; first-trust shows the
      full capability summary. Reason string is policy-derived, never generic.
- [ ] `make check` + `swift build`; commit.

### Task 6: Dock menu + CLI gate-bridge + ephemeral-key HUD

**Files:**
- Create: `app/Sources/SafeSlopCockpit/UI/DockMenu.swift` — `applicationDockMenu` → safest-tier-first
  profiles + "Quick Start: Claude in VM (disposable)".
- Create: `app/Sources/SafeSlopCockpit/UI/KeyHUD.swift` — NSStatusBar item: active-key count, countdown,
  Revoke-All.
- Modify: `internal/cli/cli.go` — `safeslop launch <profile> --config <dir>` (specs/0028 step 1):
  `launchProfile` uses the explicit dir for `findConfig` *and* the workspace (not `os.Getwd()`).
- Modify: the launch path — when trust is required (untrusted/changed/downgrade), raise a macOS
  notification that foregrounds the app for TouchID; trusted-unchanged launches with no prompt.

- [ ] `safeslop launch review --config ~/work/repo` works from an arbitrary cwd (skhd-ready), honoring
      `canonicalPolicyPath` so `/tmp` vs `/private/tmp` map to one trust key.
- [ ] Dock menu launches a profile; a downgrade or changed policy routes through the notification→GUI
      gate; trusted-unchanged is immediate.
- [ ] Key HUD shows active ephemeral keys with countdown + Revoke-All (revokes via the engine).
- [ ] Go tests for `--config` resolution + the trusted/untrusted launch branch; `make check` +
      `swift build`; commit.

---

## Out of scope for this spec (named, not silently dropped)

- The **CUE LSP** internals (Phase 2) — needs a language-server choice + the safe-first `sortText` work.
- **GitHub/Forgejo device-flow + ephemeral org keys** (Phase 2) — reuses `slop-gh-key`/`slop-forgejo-key`
  lifecycle; the GUI is a front-end, but the org-level ephemeral-key API is new design.
- **AI-assisted authoring** (Phase 2) — AST→checklist + tier-cap + sandboxed authoring agent.
- **Learning/dry-run mode** (Phase 2) — log-only Seatbelt/squid capture + diff; the single biggest
  ergonomics lever, but it's its own subsystem and deserves its own spec.
- **Dynamic network-extension UI** (Phase 3) — gated on the NE shipping; designed in the research note.
