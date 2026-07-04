# Graceful Engine-Down Implementation Plan

**Goal:** When the engine is unreachable, the Launch tab keeps showing the last-known profiles with a
"last sync HH:MM" banner and disabled launch — never a misleading empty "add a safeslop.cue" grid — and
distinguishes "engine down" from "engine up, no profiles" (ayo Actionable #8 / `[C] HIGH`: "engine-down
degrades gracefully to last-known state + timestamp, never empty grids; empty-state-on-disconnect erases
the user's mental model").

**Architecture:** `EngineModel.refresh()` already preserves `profiles` across a failed refresh (the
unreachable early-return and the list-error `catch` never clear the array). Add a `lastSync: Date?`
stamped on each *successful* list plus a `lastSyncLabel` for display, then teach `LaunchTab` three states
it currently collapses: (1) reachable + empty → "no profiles, add a safeslop.cue" (today's copy);
(2) unreachable + have-profiles → show the last-known rows dimmed + disabled, under an "engine
unreachable — last sync HH:MM" banner; (3) unreachable + empty → an honest "engine unreachable" message,
not "add a safeslop.cue". Launch taps are gated on `engine.reachable` so a stale row can't spawn a
session against a dead socket. The degraded *behavior* is verified by an `EngineModel` unit test (the
state machine is the source of truth); the degraded *visuals* are low-risk and confirmed by a manual
"kill `safeslop serve` + Refresh" step, since the screenshot harness self-spawns the engine and can't
easily render the down state.

**Tech stack:** SwiftUI (macOS 15), Swift Testing. No Go/proto/gRPC changes.

**Scope:** the Launch tab's graceful degradation only. The rest of ayo #8 — gRPC-error→plain-English
mapping, never-blank Create bootstrap, the ⌘K launch palette — are separate items, noted as follow-ons.
The Installs/Create tabs' own disconnect handling is out of scope.

**File structure:**
- `app/Sources/SafeSlopCockpit/Engine/EngineModel.swift` (modify) — add `lastSync` + `lastSyncLabel`;
  stamp `lastSync` on a successful list.
- `app/Tests/SafeSlopCockpitTests/EngineModelTests.swift` (modify) — assert last-known preservation +
  `lastSync` across a success→failure sequence.
- `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (modify) — the three-state body + the reachable-gated
  launch + unreachable dimming.

---

### Task 1: EngineModel — lastSync timestamp + preservation guarantee

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/Engine/EngineModel.swift`
- Test: `app/Tests/SafeSlopCockpitTests/EngineModelTests.swift`

- [ ] **Step 1: Write the failing test**

In `app/Tests/SafeSlopCockpitTests/EngineModelTests.swift`, add after `refreshListErrorSurfacesInStatus`
(after line 100):

```swift

    @Test @MainActor
    func refreshFailureKeepsLastKnownProfilesAndSync() async {
        let fake = FakeEngineClient()
        fake.profilesResult = .success([profile("a", env: "sandbox"), profile("b", env: "host")])
        let model = EngineModel(engine: fake)

        await model.refresh() // success
        #expect(model.profiles.count == 2)
        #expect(model.reachable == true)
        #expect(model.lastSync != nil)
        #expect(model.lastSyncLabel != nil)

        // engine goes down on the next refresh
        fake.serving = false
        await model.refresh() // failure

        #expect(model.reachable == false)
        #expect(model.profiles.count == 2) // last-known preserved, NOT an empty grid
        #expect(model.lastSync != nil)     // the prior sync time is retained
    }
```

- [ ] **Step 2: Run it, verify it fails**

```bash
swift test --package-path app --filter refreshFailureKeepsLastKnownProfilesAndSync 2>&1 | grep -iE 'error:|lastSync|has no member'
```
Expected: build failure — `value of type 'EngineModel' has no member 'lastSync'` / `lastSyncLabel`.

- [ ] **Step 3: Write the implementation**

In `app/Sources/SafeSlopCockpit/Engine/EngineModel.swift`, add the stored property after `reachable`
(line 12):

```swift
    /// When the last SUCCESSFUL profile list landed — stamped only on success, so it survives a later
    /// unreachable refresh and the Launch tab can show "last sync HH:MM" over the last-known rows
    /// instead of an empty grid (ayo #8). nil until the first successful sync.
    var lastSync: Date?
```

Stamp it in the success path — change the success branch (line 36) from:

```swift
            status = profiles.isEmpty ? "connected — no profiles found" : "connected"
```
to:
```swift
            lastSync = Date()
            status = profiles.isEmpty ? "connected — no profiles found" : "connected"
```

Add the display helper after `refresh()` (before the closing brace of the class):

```swift
    /// The last successful sync as a short local time (e.g. "2:31 PM"), or nil if never synced.
    var lastSyncLabel: String? {
        lastSync?.formatted(date: .omitted, time: .shortened)
    }
```

(The unreachable early-return and the list-error `catch` already leave `profiles` and `lastSync`
untouched, so last-known state is preserved by construction — this task only adds the timestamp the
banner needs, plus the regression test that locks the preservation in.)

- [ ] **Step 4: Run the test, verify it passes**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app --filter EngineModelTests 2>&1 | grep -iE 'Test run with|✘'
```
Expected: `Build complete!`; EngineModelTests pass (now 6 tests in the suite).

- [ ] **Step 5: Commit**

```bash
git add app/Sources/SafeSlopCockpit/Engine/EngineModel.swift \
        app/Tests/SafeSlopCockpitTests/EngineModelTests.swift
git commit -m "feat(cockpit): EngineModel.lastSync — preserve last-known profiles + stamp sync time"
```

---

### Task 2: LaunchTab — three-state degradation + reachable-gated launch

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (the `body`'s profiles/empty branch; the
  `row(_:)` launch gate + dimming)

- [ ] **Step 1: Replace the empty/list branch with the three-state version**

In `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift`, the `body` currently has:

```swift
            if engine.profiles.isEmpty {
                ContentUnavailableView("No profiles", systemImage: "tray",
                                       description: Text("Add a safeslop.cue with profiles, then Refresh."))
                    .frame(maxHeight: .infinity)
            } else {
                List(engine.profiles) { ref in
                    row(ref)
                }
            }
```

Replace it with:

```swift
            if engine.profiles.isEmpty {
                if engine.reachable {
                    ContentUnavailableView("No profiles", systemImage: "tray",
                                           description: Text("Add a safeslop.cue with profiles, then Refresh."))
                        .frame(maxHeight: .infinity)
                } else {
                    // Distinguish "engine down" from "no profiles" — the wrong message sends the user to
                    // edit a safeslop.cue when the real fix is the engine reconnecting (ayo #8).
                    ContentUnavailableView("Engine unreachable", systemImage: "bolt.horizontal.circle",
                                           description: Text("Couldn't reach `safeslop serve` — it reconnects automatically when the engine is back."))
                        .frame(maxHeight: .infinity)
                }
            } else {
                if !engine.reachable {
                    // Last-known rows, not a blank grid: say so honestly, with when they were last fresh.
                    Label("Engine unreachable — showing last sync \(engine.lastSyncLabel ?? "—")",
                          systemImage: "bolt.horizontal.circle")
                        .font(.caption.weight(.medium)).foregroundStyle(.orange)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                List(engine.profiles) { ref in
                    row(ref)
                }
            }
```

- [ ] **Step 2: Gate launch on reachability + dim unreachable rows**

In the same file, in `row(_:)`, change the `.onTapGesture` so a stale row can't launch against a dead
socket, and dim the rows when unreachable. Change:

```swift
        .opacity(missing ? 0.4 : (ref.isTrusted ? 1 : 0.6))
        .contentShape(Rectangle())
        .onTapGesture { if !missing { openWindow(id: "session", value: ref) } }
```
to:
```swift
        .opacity(missing ? 0.4 : (!engine.reachable ? 0.5 : (ref.isTrusted ? 1 : 0.6)))
        .contentShape(Rectangle())
        .onTapGesture { if !missing && engine.reachable { openWindow(id: "session", value: ref) } }
```

- [ ] **Step 3: Build + full Swift suite**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
```
Expected: `Build complete!`; `Test run with 17 tests ... passed` (16 prior + the new EngineModel test).

- [ ] **Step 4: Screenshot — confirm the normal Launch tab is unregressed**

```bash
make cockpit-shot launch
```
Read `/tmp/safeslop-cockpit-launch.png`: the engine is up (the harness starts it), so the rows render
exactly as before (ecusson, danger word, meta, open-axis chips, trust control, arrow) with no banner —
confirming the three-state branch didn't disturb the healthy path. (The unreachable banner/empty states
are verified by the Task 1 unit test + the Task 3 manual step; the harness self-spawns the engine, so it
can't render the down state.)

- [ ] **Step 5: Commit**

```bash
git add app/Sources/SafeSlopCockpit/UI/LaunchTab.swift
git commit -m "feat(cockpit): graceful engine-down on Launch — last-known rows + banner, no empty grid"
```

---

### Task 3: Full verification + handoff

**Files:**
- Modify: `specs/research/2026-06-21-handoff.md` (note ayo #8 graceful engine-down done)

- [ ] **Step 1: Run the gates**

```bash
swift build --package-path app && swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
make check    # Go untouched — confirm nothing broke
```
Expected: `Test run with 17 tests ... passed`; `make check` all ok.

- [ ] **Step 2: Manual degraded-state check (the engine-down visuals are interactive)**

Launch the app and let it connect (profiles listed). Then in a terminal kill the engine:
`pkill -f 'safeslop serve'`. Back in the app, click **Refresh**: confirm the rows stay visible but dim,
an orange "Engine unreachable — showing last sync HH:MM" banner appears, and clicking a row does NOT open
a session window. Bring the engine back (the next Refresh re-spawns it via ensureServing) and confirm the
banner clears and launch works again.

- [ ] **Step 3: Update the handoff**

In `specs/research/2026-06-21-handoff.md`, note ayo Actionable #8's graceful engine-down (Launch tab)
shipped in `specs/0034` on `sp-cockpit-risk-legibility`; the remaining #8 sub-items (gRPC→plain-English
error map, never-blank Create bootstrap, ⌘K launch palette) stay open.

- [ ] **Step 4: Commit + push**

```bash
git add specs/research/2026-06-21-handoff.md
git commit -m "docs(spec): graceful engine-down shipped (specs/0034, ayo Actionable #8)"
SSH_AUTH_SOCK="$HOME/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock" \
  git push forgejo sp-cockpit-risk-legibility
```

---

## Self-review notes

- **Spec coverage** (ayo #8 `[C] HIGH`): last-known profiles preserved on disconnect (Task 1, locked by
  the regression test) ✓; "last sync HH:MM" timestamp (Task 1 `lastSyncLabel` + Task 2 banner) ✓; no
  empty grid / honest "engine unreachable" vs "no profiles" (Task 2 three-state) ✓; launch arrows
  disabled when unreachable (Task 2 reachable-gated tap + dimming) ✓.
- **Placeholder scan:** every step has concrete code/commands; no TBDs.
- **Name consistency:** `lastSync` / `lastSyncLabel` defined in Task 1 are exactly what Task 2's banner
  reads; `engine.reachable` is the existing field gating both the empty-state branch and the row tap.
- **Verification honesty:** the degraded state is asserted by a unit test (authoritative for the state
  machine) rather than claimed via a screenshot the harness can't produce; the visual is a manual step.
  The screenshot only proves the healthy path is unregressed.
- **Out of scope (intentional, noted):** gRPC→plain-English error mapping, never-blank Create, ⌘K
  palette (the rest of #8); Installs/Create disconnect handling.
