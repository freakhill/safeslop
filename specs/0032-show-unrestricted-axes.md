# Show Unrestricted Axes (engine-side) Implementation Plan

**Goal:** Make a profile's UNRESTRICTED capability dimensions visible on the Launch row as loud amber/red
chips — show what's *open* as prominently as the meta line already shows what's *bounded* — completing
ayo Actionable #1 (`specs/research/2026-06-21-gui-flow-ergonomics.md`, lesson [L] HIGH: "hiding an
absence is a dark pattern").

**Architecture:** The engine computes, per profile, a structured per-axis restriction status
(`policy.RiskAxes` → network + files, each `restricted` + a severity), exactly where `RiskSummary`
already lives — single source of truth, so the cockpit never re-derives "is this open" (the same
discipline as `risk_lines`/`risk_level`, re-affirmed in specs/0030). A new repeated `RiskAxis` field on
the `Profile` proto carries them; the Launch row renders the unrestricted ones (`restricted == false`)
as amber/red chips after the existing positive meta line. A fully-bounded profile shows no chips —
honest "this really is contained."

**Tech stack:** Go (`internal/engine/policy` + control plane, `protoc` stubs committed), SwiftUI +
grpc-swift-2 (cockpit), Swift Testing + Go `testing`.

**Scope:** network + files axes only — the two dimensions whose "unrestricted" state is high-impact and
hidden by the meta line's positives. Secrets/credentials stay in `RiskSummary.Lines` (the break-glass
enumeration shown in the ArbiterPane/trust sheet); surfacing them as row chips is a deliberate non-goal
here (avoids cluttering the compact row). Renders on the **Launch** tab; `ValidatePolicy` is populated
too for Create-tab parity, but no Create-tab rendering is in this plan.

**File structure:**
- `internal/engine/policy/risk.go` (modify) — add `RiskAxis` type + `RiskAxes(p)` + `networkAxis`/
  `filesAxis` helpers.
- `internal/engine/policy/risk_test.go` (modify) — assert the axis flags per tier.
- `internal/engine/control/control.proto` + `app/Sources/SafeSlopCockpit/proto/control.proto` (modify) —
  add `RiskAxis` message + `repeated RiskAxis risk_axes = 13` on `Profile`.
- `internal/engine/control/pb/*.go` (regenerated).
- `internal/engine/control/server.go` (modify) — `RiskAxesPB` policy→pb mapper; populate it in
  `ValidatePolicy`.
- `internal/cli/cli.go` (modify) — populate `RiskAxes` in `cockpitListProfiles` via `control.RiskAxesPB`.
- `internal/cli/cli_cockpit_smoke_test.go` (modify) — assert host axes unrestricted, sandbox+deny bounded.
- `app/Sources/SafeSlopCockpit/UI/SessionScene.swift` (modify) — `RiskAxis` Swift struct + `ProfileRef.riskAxes`.
- `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (modify) — render the unrestricted axis chips.
- `app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift` (modify) — round-trip the new field.

---

### Task 1: Engine — RiskAxis + RiskAxes (pure policy)

**Files:**
- Modify: `internal/engine/policy/risk.go` (append after `RiskSummary`, near line 46)
- Test: `internal/engine/policy/risk_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/policy/risk_test.go`:

```go
func axesByName(axes []RiskAxis) map[string]RiskAxis {
	m := map[string]RiskAxis{}
	for _, a := range axes {
		m[a.Name] = a
	}
	return m
}

func TestRiskAxesHostIsAllUnrestricted(t *testing.T) {
	by := axesByName(RiskAxes(Profile{Environment: "host"}))
	if n := by["network"]; n.Restricted || n.Severity != "high" {
		t.Errorf("host network axis = %+v, want unrestricted high", n)
	}
	if f := by["files"]; f.Restricted || f.Severity != "high" {
		t.Errorf("host files axis = %+v, want unrestricted high", f)
	}
}

func TestRiskAxesSandboxDenyIsAllRestricted(t *testing.T) {
	for _, a := range RiskAxes(Profile{Environment: "sandbox", Network: "deny"}) {
		if !a.Restricted {
			t.Errorf("sandbox+deny axis %q=%q should be restricted", a.Name, a.Value)
		}
	}
}

func TestRiskAxesOpenEgressIsLoudButFilesBounded(t *testing.T) {
	by := axesByName(RiskAxes(Profile{Environment: "sandbox", Network: "allow"}))
	if by["network"].Restricted || by["network"].Severity != "elevated" {
		t.Errorf("sandbox+allow network = %+v, want unrestricted elevated", by["network"])
	}
	if !by["files"].Restricted {
		t.Errorf("sandbox files should be bounded: %+v", by["files"])
	}
}
```

- [ ] **Step 2: Run the tests, verify they fail**

```bash
go test ./internal/engine/policy/ -run TestRiskAxes 2>&1 | head
```
Expected: build failure — `undefined: RiskAxes` / `undefined: RiskAxis`.

- [ ] **Step 3: Write the implementation**

Append to `internal/engine/policy/risk.go` (after `RiskSummary`, before `TechStack`):

```go
// RiskAxis is one capability dimension with its restriction status, so the cockpit can show what is
// UNRESTRICTED as loudly as what is restricted (ayo S2 — hiding an absence is a dark pattern). Computed
// engine-side alongside RiskSummary so the GUI never re-derives "is this open" (single source of truth).
type RiskAxis struct {
	Name       string // "network" | "files"
	Value      string // short status: "unrestricted" | "open egress" | "whole account" | "workspace-only" | ...
	Restricted bool   // true = bounded; false = unrestricted/open (the loud, amber/red case)
	Severity   string // "high" | "elevated" | "contained" — color only; Value carries the meaning
}

// RiskAxes returns the per-dimension restriction status for a profile — network + files, the two
// dimensions whose "unrestricted" state is the high-impact danger the meta line's positives hide.
// Secrets/credentials stay in RiskSummary.Lines (the break-glass enumeration); these two are the ones
// that need loud surfacing on the compact Launch row.
func RiskAxes(p Profile) []RiskAxis {
	env := p.Environment
	if env == "" {
		env = "sandbox"
	}
	return []RiskAxis{networkAxis(env, p.Network), filesAxis(env)}
}

func networkAxis(env, network string) RiskAxis {
	switch env {
	case "host":
		return RiskAxis{"network", "unrestricted", false, "high"}
	case "container":
		if network == "allow" {
			return RiskAxis{"network", "open egress", false, "elevated"}
		}
		return RiskAxis{"network", "egress-allowlisted", true, "contained"}
	case "vm":
		if network == "allow" {
			return RiskAxis{"network", "full VM network", false, "elevated"}
		}
		return RiskAxis{"network", "proxy-only", true, "contained"}
	default: // sandbox
		if network == "allow" {
			return RiskAxis{"network", "open egress", false, "elevated"}
		}
		return RiskAxis{"network", "offline", true, "contained"}
	}
}

func filesAxis(env string) RiskAxis {
	switch env {
	case "host":
		return RiskAxis{"files", "whole account", false, "high"}
	case "container":
		return RiskAxis{"files", "workspace-only", true, "contained"}
	case "vm":
		return RiskAxis{"files", "VM-only", true, "contained"}
	default: // sandbox
		return RiskAxis{"files", "workspace + temp", true, "contained"}
	}
}
```

- [ ] **Step 4: Run the tests, verify they pass**

```bash
go test ./internal/engine/policy/ -run TestRiskAxes -v 2>&1 | tail
```
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/policy/risk.go internal/engine/policy/risk_test.go
git commit -m "feat(policy): RiskAxes — per-axis restriction status (network/files) for the GUI"
```

---

### Task 2: Proto — RiskAxis message + Profile.risk_axes

**Files:**
- Modify: `internal/engine/control/control.proto` (the `Profile` message + a new `RiskAxis` message)
- Modify: `app/Sources/SafeSlopCockpit/proto/control.proto` (kept identical via copy)
- Regenerate: `internal/engine/control/pb/control.pb.go`

- [ ] **Step 1: Add the field + message to the canonical proto**

In `internal/engine/control/control.proto`, add `risk_axes` as field 13 of `Profile`. Change the end of
the `Profile` message:

```proto
  repeated string risk_lines = 11; // break-glass consequence sentences
  repeated string tech_stack = 12; // underlying technologies (policy.TechStack) — Launch hover tooltip
  repeated RiskAxis risk_axes = 13; // per-dimension restriction status — Launch shows the unrestricted ones loud
}
```

Then add the `RiskAxis` message immediately after the `Profile` message (before
`message ListProfilesResponse`):

```proto
// RiskAxis is one capability dimension's restriction status (policy.RiskAxes). The GUI shows the
// unrestricted ones (restricted=false) as loud amber/red chips — what's OPEN, as prominently as the
// meta line shows what's bounded (ayo S2).
message RiskAxis {
  string name = 1;       // "network" | "files"
  string value = 2;      // short status, e.g. "unrestricted", "open egress", "whole account"
  bool restricted = 3;   // true = bounded; false = unrestricted/open (the loud case)
  string severity = 4;   // "high" | "elevated" | "contained" — color only; value carries the meaning
}
```

- [ ] **Step 2: Sync the Swift copy and verify identical**

```bash
cp internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto
diff internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto && echo IDENTICAL
```
Expected: `IDENTICAL`.

- [ ] **Step 3: Regenerate Go stubs + confirm build**

```bash
make proto
grep -l 'RiskAxis' internal/engine/control/pb/control.pb.go && go build ./... && echo "BUILD OK"
```
Expected: `RiskAxis` present in the stub; `BUILD OK`.

- [ ] **Step 4: Commit**

```bash
git add internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto \
        internal/engine/control/pb/control.pb.go internal/engine/control/pb/control_grpc.pb.go
git commit -m "feat(proto): RiskAxis message + Profile.risk_axes (show-unrestricted axes)"
```

---

### Task 3: Engine — populate risk_axes (mapper + both call sites) + smoke

**Files:**
- Modify: `internal/engine/control/server.go` (add `RiskAxesPB`; use in `ValidatePolicy` near line 223-228)
- Modify: `internal/cli/cli.go` (`cockpitListProfiles`, the `&pb.Profile{...}` near line 390-402)
- Test: `internal/cli/cli_cockpit_smoke_test.go` (after the env-check loop, line 59)

- [ ] **Step 1: Extend the smoke test (the failing test)**

In `internal/cli/cli_cockpit_smoke_test.go`, insert after the env-check loop (after line 59, before the
blank line preceding the Create-tab block):

```go

	// risk_axes: the engine flags each profile's UNRESTRICTED dimensions so the Launch row can show
	// what's open as loudly as what's bounded (ayo S2 / specs/0032). host = all axes unrestricted;
	// sandbox+deny = all bounded.
	byName := map[string]*pb.Profile{}
	for _, p := range lp.Profiles {
		byName[p.Name] = p
	}
	if h := byName["h"]; h == nil || len(h.RiskAxes) == 0 {
		t.Errorf("host profile has no risk_axes (the Launch row can't flag what's unrestricted)")
	} else {
		for _, ax := range h.RiskAxes {
			if ax.Restricted {
				t.Errorf("host axis %q=%q marked restricted; host bounds nothing", ax.Name, ax.Value)
			}
		}
	}
	if s := byName["s"]; s != nil {
		for _, ax := range s.RiskAxes {
			if !ax.Restricted {
				t.Errorf("sandbox+deny axis %q=%q marked unrestricted; it is bounded", ax.Name, ax.Value)
			}
		}
	}
```

- [ ] **Step 2: Run the smoke test, verify it fails**

```bash
go test ./internal/cli/ -run TestCockpitBackendSmoke 2>&1 | tail
```
Expected: FAIL — host profile has no risk_axes (the field is empty; nothing populates it yet).

- [ ] **Step 3: Add the `RiskAxesPB` mapper in the control package**

In `internal/engine/control/server.go`, add this exported helper after `installEventToPB` (end of file):

```go
// RiskAxesPB maps a profile's policy-level RiskAxes onto the wire type, so cockpitListProfiles and
// ValidatePolicy emit the same per-axis restriction status (single source of truth: policy.RiskAxes).
func RiskAxesPB(p policy.Profile) []*pb.RiskAxis {
	axes := policy.RiskAxes(p)
	out := make([]*pb.RiskAxis, 0, len(axes))
	for _, a := range axes {
		out = append(out, &pb.RiskAxis{Name: a.Name, Value: a.Value, Restricted: a.Restricted, Severity: a.Severity})
	}
	return out
}
```

- [ ] **Step 4: Populate risk_axes in ValidatePolicy**

In `internal/engine/control/server.go`, the `ValidatePolicy` profile build (lines 223-228) — add the
field. Change:

```go
			resp.Profiles = append(resp.Profiles, &pb.Profile{
				Name: n, Agent: prof.Agent, Environment: env, Network: prof.Network,
				Tier: tier, TierNote: note,
				RiskHeadline: risk.Headline, RiskLevel: risk.Level, RiskLines: risk.Lines,
				TechStack: policy.TechStack(prof),
			})
```
to:
```go
			resp.Profiles = append(resp.Profiles, &pb.Profile{
				Name: n, Agent: prof.Agent, Environment: env, Network: prof.Network,
				Tier: tier, TierNote: note,
				RiskHeadline: risk.Headline, RiskLevel: risk.Level, RiskLines: risk.Lines,
				TechStack: policy.TechStack(prof),
				RiskAxes:  RiskAxesPB(prof),
			})
```

- [ ] **Step 5: Populate risk_axes in cockpitListProfiles**

In `internal/cli/cli.go`, the `cockpitListProfiles` profile build (the `&pb.Profile{...}` near line
390-402) — add the field after `TechStack`. Change the `TechStack: policy.TechStack(prof),` line to add:

```go
			TechStack:    policy.TechStack(prof),
			RiskAxes:     control.RiskAxesPB(prof),
```

- [ ] **Step 6: Run the smoke + full check, verify they pass**

```bash
go test ./internal/cli/ -run TestCockpitBackendSmoke -v 2>&1 | tail -3
make check
```
Expected: smoke PASS; `make check` all ok.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/control/server.go internal/cli/cli.go internal/cli/cli_cockpit_smoke_test.go
git commit -m "feat(control): populate Profile.risk_axes (RiskAxesPB) in list + validate + smoke"
```

---

### Task 4: Cockpit — RiskAxis model + Launch-row chips

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/UI/SessionScene.swift` (add `RiskAxis` struct; add `riskAxes` to
  `ProfileRef` — the struct fields, both inits, and the `.proto` getter)
- Modify: `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (render the unrestricted chips)
- Test: `app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift` (extend the round-trip)

- [ ] **Step 1: Extend the round-trip test (the failing test)**

In `app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift`, replace `protoRoundTripPreservesFields`
(lines 45-50) with a version that carries a risk axis:

```swift
    @Test
    func protoRoundTripPreservesFields() {
        let original = ProfileRef(name: "dev", agent: "claude", environment: "container",
                                  network: "allow", tier: "blast-box", riskLevel: "elevated",
                                  riskAxes: [RiskAxis(name: "network", value: "open egress",
                                                      restricted: false, severity: "elevated")])
        let restored = ProfileRef(original.proto)
        #expect(restored == original)
        #expect(restored.riskAxes.first?.value == "open egress")
    }
```

- [ ] **Step 2: Run it, verify it fails**

```bash
swift test --package-path app --filter protoRoundTripPreservesFields 2>&1 | grep -iE 'error:|RiskAxis|riskAxes'
```
Expected: build failure — `cannot find 'RiskAxis'` / `ProfileRef has no member 'riskAxes'`.

- [ ] **Step 3: Add the RiskAxis Swift struct + ProfileRef.riskAxes**

In `app/Sources/SafeSlopCockpit/UI/SessionScene.swift`, add the `RiskAxis` struct just above the
`ProfileRef` struct declaration (before `struct ProfileRef`):

```swift
/// RiskAxis mirrors the engine's Safeslop_Control_V1_RiskAxis: one capability dimension's restriction
/// status. The Launch row renders the unrestricted ones (restricted == false) as loud amber/red chips —
/// what's OPEN, shown as prominently as what's bounded (ayo S2, specs/0032).
struct RiskAxis: Codable, Hashable, Identifiable {
    let name: String
    let value: String
    let restricted: Bool
    let severity: String
    var id: String { name }

    /// Color band by severity (value carries the meaning; color is the redundant channel).
    var color: Color {
        switch severity {
        case "high": return .red
        case "elevated": return .orange
        default: return .green
        }
    }

    init(name: String, value: String, restricted: Bool, severity: String) {
        self.name = name; self.value = value; self.restricted = restricted; self.severity = severity
    }
    init(_ p: Safeslop_Control_V1_RiskAxis) {
        self.init(name: p.name, value: p.value, restricted: p.restricted, severity: p.severity)
    }
    var proto: Safeslop_Control_V1_RiskAxis {
        .with { $0.name = name; $0.value = value; $0.restricted = restricted; $0.severity = severity }
    }
}
```

Then add the stored field + wire the two inits and the `.proto` getter on `ProfileRef`. Change the
property block (after `var techStack: [String]` at line 17):

```swift
    var techStack: [String]  // underlying technologies (policy.TechStack) — Launch hover tooltip
    var riskAxes: [RiskAxis] // per-dimension restriction status — Launch shows the unrestricted ones loud
```

Change the designated `init` (lines 20-26) — add the parameter (default `[]`) and assignment:

```swift
    init(name: String, agent: String, environment: String, network: String,
         tier: String = "", tierNote: String = "", trustStatus: String = "untrusted", configDir: String = "",
         riskHeadline: String = "", riskLevel: String = "contained", riskLines: [String] = [], techStack: [String] = [],
         riskAxes: [RiskAxis] = []) {
        self.name = name; self.agent = agent; self.environment = environment; self.network = network
        self.tier = tier; self.tierNote = tierNote; self.trustStatus = trustStatus; self.configDir = configDir
        self.riskHeadline = riskHeadline; self.riskLevel = riskLevel; self.riskLines = riskLines; self.techStack = techStack
        self.riskAxes = riskAxes
    }
```

Change the `init(_ p:)` (lines 27-31) — add the mapped field:

```swift
    init(_ p: Safeslop_Control_V1_Profile) {
        self.init(name: p.name, agent: p.agent, environment: p.environment, network: p.network,
                  tier: p.tier, tierNote: p.tierNote, trustStatus: p.trustStatus, configDir: p.configDir,
                  riskHeadline: p.riskHeadline, riskLevel: p.riskLevel, riskLines: p.riskLines, techStack: p.techStack,
                  riskAxes: p.riskAxes.map(RiskAxis.init))
    }
```

Change the `.proto` getter (lines 32-38) — emit the field:

```swift
    var proto: Safeslop_Control_V1_Profile {
        .with {
            $0.name = name; $0.agent = agent; $0.environment = environment; $0.network = network
            $0.tier = tier; $0.tierNote = tierNote; $0.trustStatus = trustStatus; $0.configDir = configDir
            $0.riskHeadline = riskHeadline; $0.riskLevel = riskLevel; $0.riskLines = riskLines; $0.techStack = techStack
            $0.riskAxes = riskAxes.map(\.proto)
        }
    }
```

- [ ] **Step 4: Run the round-trip test, verify it passes**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app --filter ProfileRefTests 2>&1 | grep -iE 'Test run with|✘'
```
Expected: `Build complete!`; ProfileRefTests pass.

- [ ] **Step 5: Render the unrestricted chips on the Launch row**

In `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift`, add the chips right after the meta-line `Text` (the
`"\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)"` line) and before the `if !ref.riskHeadline`
block:

```swift
                    Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                        .font(.caption).foregroundStyle(.secondary)
                    // Show what's UNRESTRICTED as loudly as the line above shows what's bounded (ayo S2):
                    // a fully-contained profile shows no chips — honest, not scary.
                    let openAxes = ref.riskAxes.filter { !$0.restricted }
                    if !openAxes.isEmpty {
                        HStack(spacing: 4) {
                            ForEach(openAxes) { ax in
                                Text("\(ax.name): \(ax.value)")
                                    .font(.caption2.weight(.semibold))
                                    .padding(.horizontal, 5).padding(.vertical, 1)
                                    .background(ax.color.opacity(0.18), in: Capsule())
                                    .foregroundStyle(ax.color)
                            }
                        }
                    }
```

- [ ] **Step 6: Build, full suite, screenshot**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
make cockpit-shot launch
```
Then Read `/tmp/safeslop-cockpit-launch.png` and confirm: `risky` (host) shows two red chips
(`network: unrestricted`, `files: whole account`); `net`/`boxnet` (open egress) show an amber
`network: open egress` chip; `safe` (sandbox-deny) and `box` (container-deny) show NO open-axis chips.

- [ ] **Step 7: Commit**

```bash
git add app/Sources/SafeSlopCockpit/UI/SessionScene.swift app/Sources/SafeSlopCockpit/UI/LaunchTab.swift \
        app/Tests/SafeSlopCockpitTests/ProfileRefTests.swift
git commit -m "feat(cockpit): show unrestricted axes as amber/red chips on the Launch row"
```

---

### Task 5: Full verification + handoff

**Files:**
- Modify: `specs/research/2026-06-21-handoff.md` (mark Actionable #1 fully done)

- [ ] **Step 1: Run every gate**

```bash
make check ; make build
swift build --package-path app && swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
fish tests/run.fish 2>&1 | tail -3
fish scripts/slop-pinning.fish
```
Expected: all green (Go tests incl. the smoke + policy axes; Swift incl. the round-trip; fish suite;
pinning).

- [ ] **Step 2: Update the handoff**

In `specs/research/2026-06-21-handoff.md`, mark Actionable #1 as fully shipped (1a = specs/0031, 1b =
specs/0032), both on branch `sp-cockpit-risk-legibility`.

- [ ] **Step 3: Commit + push**

```bash
git add specs/research/2026-06-21-handoff.md
git commit -m "docs(spec): show-unrestricted axes shipped (specs/0032) — ayo Actionable #1 complete"
SSH_AUTH_SOCK="$HOME/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock" \
  git push forgejo sp-cockpit-risk-legibility
```

---

## Self-review notes

- **Spec coverage** (ayo Actionable #1b / lesson [L] HIGH "show unrestricted as loudly as restricted"):
  engine computes per-axis restriction (Task 1) ✓; carried over the wire single-source (Tasks 2-3) ✓;
  rendered amber/red on the Launch row, with fully-bounded profiles showing nothing (Task 4) ✓.
  Severity drives color but `value` carries the meaning — consistent with 0031's non-color principle ✓.
- **Placeholder scan:** every step has concrete code/commands; no TBDs.
- **Name consistency:** `RiskAxis`/`RiskAxes` (Go) ↔ `risk_axes`/`RiskAxis` (proto) ↔ `RiskAxesPB` (Go
  mapper) ↔ `RiskAxis`/`riskAxes` (Swift) ↔ `openAxes` (the `!restricted` filter in LaunchTab). Field
  names `name`/`value`/`restricted`/`severity` are identical across proto, Go, and Swift.
- **Single-source-of-truth:** the restriction logic exists only in `policy.RiskAxes`; both `pb` call
  sites go through `RiskAxesPB`; the cockpit only renders. No client-side risk re-derivation (the thing
  this plan exists to avoid).
- **Out of scope (intentional):** secrets/credentials axes (stay in `RiskSummary.Lines`), Create-tab
  rendering (proto carries the data for later), columnar meta-axis alignment (ayo MED, separate).
