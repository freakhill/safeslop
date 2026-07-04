# Non-Color Danger Channel (ecusson + Launch row) Implementation Plan

**Goal:** Make a profile's danger level legible without relying on color alone — add a danger WORD and a
shape (border-weight) channel to the Launch-tab ecusson, so risk survives colorblindness, grayscale, and
screenshots (ayo `specs/research/2026-06-21-gui-flow-ergonomics.md`, headline finding 2 + Actionable #1).

**Architecture:** The danger band already lives on `ProfileRef.riskLevel` ("high"/"elevated"/
"contained") and today is rendered *only* as the ecusson's background color (`RiskBadge`). Add two pure,
redundant non-color channels derived from that same field: `dangerWord` (an uppercase word) and
`dangerRank` (0/1/2, driving the ecusson's border weight). Render the word as a `symbol+word+color`
triad on the Launch row (the macOS TCC / Little Snitch pattern) and the rank as a white inset border on
the `RiskBadge`. Pure presentation derivation — no engine/proto change, consistent with the existing
`riskColor`/`netLabel` derivations on `ProfileRef`.

**Tech stack:** SwiftUI (macOS 15), Swift Testing. No Go/proto/gRPC changes.

**Scope note (deliberate split):** This plan is the **non-color danger channel only** — Actionable #1's
first half, which is the headline "single biggest legibility failure" and is pure client-side. Actionable
#1's second half — *"enumerate the meta line's unrestricted axes in amber/red"* — is **deferred to a
follow-on plan (0032)** because the honest implementation computes each axis's restriction status in the
**engine** (`internal/engine/policy`, surfaced via a new structured proto field), the same
single-source-of-truth discipline used for `risk_lines` and re-affirmed in specs/0030. Deriving
"unrestricted" client-side would duplicate `policy.networkReach`/`fileReach` in Swift — exactly the
duplication `cockpitListProfiles` exists to avoid. Recorded so the split is intentional, not a gap.

**File structure:**
- `app/Sources/SafeSlopCockpit/UI/SessionScene.swift` (modify) — add `dangerWord` + `dangerRank` to the
  `ProfileRef` struct (next to the existing `riskColor`/`tierSymbol` derivations).
- `app/Sources/SafeSlopCockpit/UI/RiskBadge.swift` (modify) — add a `rank` parameter that draws a
  danger-proportional border (the grayscale-survivable shape channel).
- `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (modify) — render the danger word as a chip next to
  the profile name; pass `rank: ref.dangerRank` to `RiskBadge`.
- `app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift` (modify) — assert `dangerWord` + `dangerRank`.

---

### Task 1: ProfileRef — dangerWord + dangerRank (pure, tested)

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/UI/SessionScene.swift` (the `ProfileRef` struct, after `riskColor`
  near line 47)
- Test: `app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift` (add two `@Test`s; uses the existing
  `ref(risk:)` helper at line 53)

- [ ] **Step 1: Write the failing tests**

In `app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift`, add these two tests after
`riskColorMatchesArbiterBand()` (after line 22):

```swift
    @Test
    func dangerWordIsRedundantWithColorBand() {
        // The word carries danger level without color — survives grayscale / colorblindness / screenshots.
        #expect(ref(risk: "high").dangerWord == "HIGH")
        #expect(ref(risk: "elevated").dangerWord == "ELEVATED")
        #expect(ref(risk: "contained").dangerWord == "CONTAINED")
        #expect(ref(risk: "anything-else").dangerWord == "CONTAINED") // default band is contained
    }

    @Test
    func dangerRankOrdersBySeverity() {
        // The rank drives the ecusson border weight — a second, shape-based danger channel.
        #expect(ref(risk: "high").dangerRank == 2)
        #expect(ref(risk: "elevated").dangerRank == 1)
        #expect(ref(risk: "contained").dangerRank == 0)
        #expect(ref(risk: "high").dangerRank > ref(risk: "elevated").dangerRank)
        #expect(ref(risk: "elevated").dangerRank > ref(risk: "contained").dangerRank)
    }
```

- [ ] **Step 2: Run the tests, verify they fail**

```bash
swift test --package-path app --filter ProfileRefTests 2>&1 | grep -iE 'error:|dangerWord|dangerRank'
```
Expected: build failure — `value of type 'ProfileRef' has no member 'dangerWord'` / `dangerRank`.

- [ ] **Step 3: Write the implementation**

In `app/Sources/SafeSlopCockpit/UI/SessionScene.swift`, add to the `ProfileRef` struct immediately after
the `riskColor` computed property (after line 47, before `isTrusted`):

```swift
    /// Danger level as a WORD — the non-color channel the ecusson's background color must not be the
    /// sole carrier of (ayo S2, headline finding 2). Mirrors `riskLevel`, uppercased for the badge, so
    /// risk reads in grayscale, for the ~8% red-green colorblind, and in a screenshot. Unknown bands
    /// fall back to the safe word, matching `riskColor`'s green default.
    var dangerWord: String {
        switch riskLevel {
        case "high": return "HIGH"
        case "elevated": return "ELEVATED"
        default: return "CONTAINED"
        }
    }

    /// Danger as a SHAPE channel: the ecusson's border weight scales with this rank (high 2 / elevated 1
    /// / contained 0), so the chip alone signals danger with color stripped. Redundant with `riskColor`.
    var dangerRank: Int {
        switch riskLevel {
        case "high": return 2
        case "elevated": return 1
        default: return 0
        }
    }
```

- [ ] **Step 4: Run the tests, verify they pass**

```bash
swift test --package-path app --filter ProfileRefTests 2>&1 | grep -iE 'Test run with|✘|error:'
```
Expected: PASS (the suite now includes `dangerWordIsRedundantWithColorBand` + `dangerRankOrdersBySeverity`).

- [ ] **Step 5: Commit**

```bash
git add app/Sources/SafeSlopCockpit/UI/SessionScene.swift \
        app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift
git commit -m "feat(cockpit): ProfileRef dangerWord + dangerRank — non-color danger channels"
```

---

### Task 2: RiskBadge border + Launch-row danger word (visual)

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/UI/RiskBadge.swift` (whole `RiskBadge` struct)
- Modify: `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift:43-53` (the row's label HStack) and the
  `RiskBadge(...)` call at line 45

- [ ] **Step 1: Add the danger-rank border to RiskBadge**

Replace the body of `app/Sources/SafeSlopCockpit/UI/RiskBadge.swift` (the whole `RiskBadge` struct,
lines 7-24) with:

```swift
struct RiskBadge: View {
    let symbol: String
    let color: Color
    /// Danger rank (0 contained / 1 elevated / 2 high) → border weight: the non-color, grayscale-
    /// survivable danger channel that makes the chip's color redundant rather than sole (ayo S2).
    var rank: Int = 0
    var size: CGFloat = 30

    private var borderWidth: CGFloat { CGFloat(rank) * 1.5 } // 0 / 1.5 / 3.0 pt

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.24, style: .continuous)
                .fill(color.gradient)
            RoundedRectangle(cornerRadius: size * 0.24, style: .continuous)
                .strokeBorder(.white.opacity(0.9), lineWidth: borderWidth)
            Image(systemName: symbol)
                .font(.system(size: size * 0.5, weight: .semibold))
                .foregroundStyle(.white)
        }
        .frame(width: size, height: size)
        // a faint same-color halo lifts the chip off the row background.
        .shadow(color: color.opacity(0.35), radius: 2, y: 1)
    }
}
```

- [ ] **Step 2: Pass the rank + add the danger word on the Launch row**

In `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift`, update the `RiskBadge(...)` call at line 45 and the
name row. Replace lines 44-53:

```swift
                // colored ecusson: danger level is the chip background; the glyph stays white.
                RiskBadge(symbol: ref.tierSymbol, color: ref.riskColor).help(ref.tierNote)
                VStack(alignment: .leading, spacing: 1) {
                    Text(ref.name).font(.headline)
                    Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                        .font(.caption).foregroundStyle(.secondary)
                    if !ref.riskHeadline.isEmpty {
                        Text(ref.riskHeadline).font(.caption2.weight(.medium)).foregroundStyle(ref.riskColor)
                    }
                }
```

with:

```swift
                // Ecusson: color is the chip background; the border WEIGHT (rank) is the non-color danger
                // channel so the chip reads in grayscale / for the colorblind (ayo S2). Glyph = tier.
                RiskBadge(symbol: ref.tierSymbol, color: ref.riskColor, rank: ref.dangerRank).help(ref.tierNote)
                VStack(alignment: .leading, spacing: 1) {
                    HStack(spacing: 6) {
                        Text(ref.name).font(.headline)
                        // symbol+word+color triad (macOS TCC / Little Snitch): the WORD carries danger,
                        // not the color alone.
                        Text(ref.dangerWord)
                            .font(.caption2.weight(.bold))
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(ref.riskColor.opacity(0.18), in: Capsule())
                            .foregroundStyle(ref.riskColor)
                    }
                    Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                        .font(.caption).foregroundStyle(.secondary)
                    if !ref.riskHeadline.isEmpty {
                        Text(ref.riskHeadline).font(.caption2.weight(.medium)).foregroundStyle(ref.riskColor)
                    }
                }
```

- [ ] **Step 3: Confirm RiskBadge has no other call sites that need the rank (or update them)**

```bash
grep -rn 'RiskBadge(' app/Sources/SafeSlopCockpit/
```
Expected: the only call is the one in `LaunchTab.swift` just updated. `rank` defaults to 0, so any other
call still compiles; if a second call site exists and renders a profile's danger, update it to pass
`rank: <ref>.dangerRank` too. (If the only hit is LaunchTab, nothing else to do.)

- [ ] **Step 4: Build + run the full Swift suite (no regression)**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app 2>&1 | grep -iE 'Test run with|✘|error:'
```
Expected: `Build complete!`; `Test run with N tests ... passed` (16 now — the prior 14 plus the two new).

- [ ] **Step 5: Screenshot the Launch tab and verify the non-color channels**

```bash
make cockpit-shot launch
```
Then Read `/tmp/safeslop-cockpit-launch.png` and confirm across the five seeded profiles
(safe/net/risky/box/boxnet span contained/elevated/high): each row shows a danger WORD chip next to the
name (HIGH / ELEVATED / CONTAINED), and the higher-danger ecussons carry a visibly thicker white border
than the contained ones. The danger level must be readable with color mentally removed.

- [ ] **Step 6: Commit**

```bash
git add app/Sources/SafeSlopCockpit/UI/RiskBadge.swift app/Sources/SafeSlopCockpit/UI/LaunchTab.swift
git commit -m "feat(cockpit): non-color danger channel on the Launch ecusson (word + border weight)"
```

---

### Task 3: Full verification + handoff

**Files:**
- Modify: `specs/research/2026-06-21-handoff.md` (note Actionable #1a done, 1b is plan 0032)

- [ ] **Step 1: Run the cockpit's automated gates (Swift side is what changed)**

```bash
swift build --package-path app && swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
make check        # Go untouched, but confirm nothing broke
```
Expected: Swift `Test run with 16 tests ... passed`; `make check` all ok.

- [ ] **Step 2: Update the handoff**

In `specs/research/2026-06-21-handoff.md`, under "Next" item 3 (ayo HIGH actionables), note that the
ecusson non-color danger channel (Actionable #1a) shipped in `specs/0031`, and that the
show-unrestricted-axes half (Actionable #1b) is the engine-side follow-on `specs/0032` (not yet written).

- [ ] **Step 3: Commit + push**

```bash
git add specs/research/2026-06-21-handoff.md
git commit -m "docs(spec): non-color danger channel shipped (specs/0031); 1b deferred to 0032"
SSH_AUTH_SOCK="$HOME/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock" \
  git push forgejo sp-cockpit-risk-legibility
```

---

## Self-review notes

- **Spec coverage** (Actionable #1, ayo S2): non-color danger channel via danger WORD (Task 1
  `dangerWord` + Task 2 row chip) ✓ and shape/border-weight (Task 1 `dangerRank` + Task 2 `RiskBadge`
  border) ✓ — both redundant channels the ayo asked for, native SF Symbols/SwiftUI, no custom glyph
  font ✓. The "enumerate unrestricted axes" half is explicitly deferred to 0032 (scope note above), not
  silently dropped ✓.
- **Placeholder scan:** every step has concrete code/commands; the one conditional (Task 2 Step 3) is a
  grep with both outcomes spelled out, not a "figure it out."
- **Name consistency:** `dangerWord` / `dangerRank` (Task 1) are the exact names consumed in Task 2's
  `RiskBadge(rank: ref.dangerRank)` and `Text(ref.dangerWord)`. `rank` is the `RiskBadge` parameter name
  in both the struct and the call site.
- **No engine/proto churn:** purely additive client derivations on an existing field — `make check`
  stays green by construction; the risk is only visual, which Task 2 Step 5 screenshot-verifies.
