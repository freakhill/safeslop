# Symmetric Trust Revoke Implementation Plan

**Goal:** Make trust *revocable* at the same visibility as it is granted — a trusted profile on the Launch
row carries a clickable "trusted" control that reveals **Revoke trust**, wired to a new engine `Untrust`
RPC, so the green badge is a toggle and not a one-way door (ayo Actionable #3 / lesson [L] HIGH: TCC's
grant-in-a-dialog / revoke-in-System-Settings asymmetry is the canonical privacy anti-pattern).

**Architecture:** The trust store (`internal/engine/trust`) has `Approve` but no removal, so trust today
is irreversible from the UI. Add `Store.Revoke(absPath)` (delete the entry + persist, mirroring
`Approve`), a `cockpitUntrust` engine fn that revokes using the *same* canonical path key `enforceTrust`
approves under, and an `Untrust` control-plane RPC. The Launch row gains a trailing trust control: a
`Menu` showing "Revoke trust" when trusted (one click, no biometric — revoke removes privilege, the
risk-proportional rule from specs/0030), the existing badge when untrusted/changed. Revoke then
re-`refresh`es so the row reflects the new state. Grant stays exactly as-is (the session-window
TrustSheet); only the reverse direction is added.

**Tech stack:** Go (`internal/engine/trust` + control plane, `protoc` stubs committed), SwiftUI +
grpc-swift-2 (cockpit), Go `testing` + Swift Testing.

**UI decision (settled):** the revoke affordance lives on the **Launch row** as a trailing clickable
control (jojo's pick — "equally visible," matching the ayo's cited 1Password same-row pattern). The row
switches from a single wrapping `Button` to an `HStack` + `.onTapGesture` for launch so the trailing
`Menu` is a separate tap target; the menu consumes its own clicks. (Minor tradeoff: the row loses
default Button keyboard-activation — acceptable; the dock menu already lists profiles and a ⌘K palette
is a separate ayo item.)

**File structure:**
- `internal/engine/trust/trust.go` (modify) — add `Store.Revoke`.
- `internal/engine/trust/trust_test.go` (modify) — approve → revoke → untrusted.
- `internal/engine/control/control.proto` + `app/Sources/SafeSlopCockpit/proto/control.proto` (modify) —
  `Untrust` RPC + request/response.
- `internal/engine/control/pb/*.go` (regenerated).
- `internal/engine/control/serve.go` (modify) — thread `untrustFn` through `Serve` + `NewControlServer`.
- `internal/engine/control/server.go` (modify) — `untrustFn` field + `Untrust` handler.
- `internal/cli/cli.go` (modify) — `cockpitUntrust`; wire into `cmdServe`.
- `internal/cli/cli_cockpit_smoke_test.go` (modify) — wire fn; assert Untrust flips trust_status.
- `app/Sources/SafeSlopCockpit/Engine/EngineConnection.swift` (modify) — `untrust(configPath:)`.
- `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (modify) — row refactor + `trustControl` Menu + `revoke`.

---

### Task 1: Engine — trust.Store.Revoke (pure)

**Files:**
- Modify: `internal/engine/trust/trust.go` (after `Approve`, end of file)
- Test: `internal/engine/trust/trust_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/engine/trust/trust_test.go`:

```go
func TestRevokeReturnsToUntrusted(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "trust.json"), entries: map[string]string{}}
	policy := []byte("safeslop: {version: 1}")
	abs := "/repo/safeslop.cue"

	if err := store.Approve(abs, policy); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if store.Check(abs, policy) != Trusted {
		t.Fatalf("precondition: want Trusted after Approve")
	}
	if err := store.Revoke(abs); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := store.Check(abs, policy); got != Untrusted {
		t.Errorf("after Revoke = %v, want Untrusted", got)
	}
	// Revoking an absent entry is a no-op success (idempotent).
	if err := store.Revoke(abs); err != nil {
		t.Errorf("second Revoke should be a no-op, got %v", err)
	}
	// The removal persisted: a fresh Load no longer trusts it.
	reloaded, err := Load(store.path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Check(abs, policy) != Untrusted {
		t.Errorf("revocation did not persist")
	}
}
```

The test references `filepath`; ensure `trust_test.go`'s imports include `"path/filepath"` (add it if the
file doesn't already import it).

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/trust/ -run TestRevoke 2>&1 | head
```
Expected: build failure — `store.Revoke undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/engine/trust/trust.go`, add after `Approve` (end of file):

```go
// Revoke removes absPath's approval and persists the store, so a previously trusted policy returns to
// Untrusted (the symmetric reverse of Approve — ayo Actionable #3). Revoking an entry that isn't present
// is a no-op success. It rewrites the file rather than mutating in place, so a crash can't half-revoke.
func (s *Store) Revoke(absPath string) error {
	if _, ok := s.entries[absPath]; !ok {
		return nil
	}
	delete(s.entries, absPath)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storeFile{Version: storeVersion, Entries: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/trust/ -run TestRevoke -v 2>&1 | tail
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/trust/trust.go internal/engine/trust/trust_test.go
git commit -m "feat(trust): Store.Revoke — remove a policy's approval (symmetric to Approve)"
```

---

### Task 2: Proto — Untrust RPC

**Files:**
- Modify: `internal/engine/control/control.proto` (service block + Trust messages region)
- Modify: `app/Sources/SafeSlopCockpit/proto/control.proto` (copy)
- Regenerate: `internal/engine/control/pb/*.go`

- [ ] **Step 1: Add the RPC to the service block**

In `internal/engine/control/control.proto`, add after the `rpc Trust(...)` line (line 13):

```proto
  rpc Untrust(UntrustRequest) returns (UntrustResponse);
```

- [ ] **Step 2: Add the messages next to the Trust messages**

In `internal/engine/control/control.proto`, after the `TrustResponse` message (line 91), add:

```proto
// Untrust removes a repo's recorded approval (trust.Store.Revoke), so the next list shows it untrusted
// and a launch re-gates through the trust sheet. The reverse of Trust — symmetric revoke (ayo #3). The
// peer is uid/process-tree-checked at Accept, so a sandboxed agent can't revoke (or grant) on its own.
message UntrustRequest { string config_path = 1; }  // dir/file holding safeslop.cue; empty = server cwd
message UntrustResponse { string untrusted_path = 1; } // absolute path whose approval was removed
```

- [ ] **Step 3: Sync the Swift copy + verify identical**

```bash
cp internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto
diff internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto && echo IDENTICAL
```
Expected: `IDENTICAL`.

- [ ] **Step 4: Regenerate stubs + confirm build**

```bash
make proto
grep -l 'Untrust' internal/engine/control/pb/control_grpc.pb.go && go build ./... && echo "BUILD OK"
```
Expected: `Untrust` present in the grpc stub; `BUILD OK` (the handler is still missing but
`UnimplementedControlServer` covers it).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto \
        internal/engine/control/pb/control.pb.go internal/engine/control/pb/control_grpc.pb.go
git commit -m "feat(proto): Untrust RPC (symmetric trust revoke)"
```

---

### Task 3: Engine — Untrust handler + cockpitUntrust + wiring + smoke

**Files:**
- Modify: `internal/engine/control/serve.go` (`Serve` + `NewControlServer` signatures + struct wiring)
- Modify: `internal/engine/control/server.go` (`untrustFn` field + handler after `Trust`)
- Modify: `internal/cli/cli.go` (`cockpitUntrust`; `cmdServe` wiring)
- Test: `internal/cli/cli_cockpit_smoke_test.go` (wire fn into `startCockpitBackend`; assert revoke)

- [ ] **Step 1: Extend the smoke test (the failing test)**

In `internal/cli/cli_cockpit_smoke_test.go`, add `cockpitUntrust` to the `NewControlServer` wiring in
`startCockpitBackend`. Change:

```go
		resolveSession, cockpitTrust, cockpitListProfiles, cockpitPreflightHostLaunch,
	))
```
to:
```go
		resolveSession, cockpitTrust, cockpitListProfiles, cockpitPreflightHostLaunch, cockpitUntrust,
	))
```

Then, after the existing Trust assertion block (the `tr, err := cl.Trust(...)` block), add:

```go

	// Symmetric revoke (ayo #3): Untrust removes the host approval, so a re-list shows the profiles
	// untrusted and the next launch would re-gate through the trust sheet.
	if _, err := cl.Untrust(ctx, &pb.UntrustRequest{ConfigPath: cuePath}); err != nil {
		t.Fatalf("Untrust: %v", err)
	}
	relisted, err := cl.ListProfiles(ctx, &pb.ListProfilesRequest{ConfigPath: cuePath})
	if err != nil {
		t.Fatalf("ListProfiles after Untrust: %v", err)
	}
	for _, p := range relisted.Profiles {
		if p.TrustStatus == "trusted" {
			t.Errorf("after Untrust, profile %q still reports trusted", p.Name)
		}
	}
```

- [ ] **Step 2: Run the smoke, verify it fails to compile**

```bash
go test ./internal/cli/ -run TestCockpitBackendSmoke 2>&1 | head
```
Expected: FAIL — `undefined: cockpitUntrust` and `cl.Untrust undefined` ... `too many arguments`.

- [ ] **Step 3: Thread `untrustFn` through serve.go**

In `internal/engine/control/serve.go`, add the parameter to `Serve` (after the `preflightFn` param) and
pass it through, and the same to `NewControlServer` + the struct literal. The `Serve` signature becomes:

```go
func Serve(version string,
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error,
	resolveFn func(profile, configPath string) (SessionSpec, error),
	trustFn func(configPath string) (string, error),
	listFn func(configPath string) ([]*pb.Profile, error),
	preflightFn func(profile, configPath string) (*pb.PreflightHostLaunchResponse, error),
	untrustFn func(configPath string) (string, error),
) error {
```
its `NewControlServer(...)` call gains a trailing `untrustFn`:
```go
	pb.RegisterControlServer(gs, NewControlServer(version, launchFn, resolveFn, trustFn, listFn, preflightFn, untrustFn))
```
and `NewControlServer` mirrors it:
```go
func NewControlServer(version string,
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error,
	resolveFn func(profile, configPath string) (SessionSpec, error),
	trustFn func(configPath string) (string, error),
	listFn func(configPath string) ([]*pb.Profile, error),
	preflightFn func(profile, configPath string) (*pb.PreflightHostLaunchResponse, error),
	untrustFn func(configPath string) (string, error),
) pb.ControlServer {
	return &server{
		version:        version,
		launchFn:       launchFn,
		mgr:            NewManager(),
		resolveFn:      resolveFn,
		trustFn:        trustFn,
		listFn:         listFn,
		preflightFn:    preflightFn,
		untrustFn:      untrustFn,
		installApplyFn: defaultInstallApply(version),
	}
}
```

- [ ] **Step 4: Add the struct field + handler in server.go**

In `internal/engine/control/server.go`, add the field after `preflightFn`:

```go
	untrustFn      func(configPath string) (string, error)
```

Add the handler after the `Trust` handler:

```go

// Untrust removes the host-side approval of the safeslop.cue at req.ConfigPath (trust.Store.Revoke), so
// the next ListProfiles reports it untrusted and a subsequent launch re-gates through the trust sheet —
// the symmetric reverse of Trust (ayo Actionable #3). Revoke removes privilege, so it needs no biometric
// (specs/0030 risk-proportional rule). The peer is uid/process-tree-checked at Accept.
func (s *server) Untrust(_ context.Context, req *pb.UntrustRequest) (*pb.UntrustResponse, error) {
	if s.untrustFn == nil {
		return nil, status.Errorf(codes.Unimplemented, "untrust not wired")
	}
	abs, err := s.untrustFn(req.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &pb.UntrustResponse{UntrustedPath: abs}, nil
}
```

- [ ] **Step 5: Add cockpitUntrust + wire cmdServe**

In `internal/cli/cli.go`, add next to `cockpitTrust`:

```go
// cockpitUntrust removes the host approval of the safeslop.cue at configPath (the Launch row's Revoke).
// It revokes under the SAME canonical path key enforceTrust approves under, so the status flips cleanly
// back to untrusted. Returns the absolute path whose approval was removed.
func cockpitUntrust(configPath string) (string, error) {
	path, err := findConfig(configPath)
	if err != nil {
		return "", err
	}
	abs := canonicalPolicyPath(path)
	storePath, err := trust.DefaultPath()
	if err != nil {
		return "", err
	}
	store, err := trust.Load(storePath)
	if err != nil {
		return "", err
	}
	if err := store.Revoke(abs); err != nil {
		return "", err
	}
	return abs, nil
}
```

Then wire it into `cmdServe`'s `control.Serve(...)` call — add after `cockpitPreflightHostLaunch,`:

```go
				cockpitListProfiles,
				cockpitPreflightHostLaunch,
				cockpitUntrust,
			)
```

- [ ] **Step 6: Run the smoke + full check**

```bash
go test ./internal/cli/ -run TestCockpitBackendSmoke -v 2>&1 | tail -3
make check
```
Expected: smoke PASS; `make check` all ok.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/control/serve.go internal/engine/control/server.go \
        internal/cli/cli.go internal/cli/cli_cockpit_smoke_test.go
git commit -m "feat(control): Untrust handler + cockpitUntrust + smoke (symmetric revoke)"
```

---

### Task 4: Cockpit — untrust RPC + Launch-row revoke control

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/Engine/EngineConnection.swift` (add `untrust` after `trust`)
- Modify: `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift` (row refactor + `trustControl` + `revoke`)

- [ ] **Step 1: Add the untrust RPC call**

In `app/Sources/SafeSlopCockpit/Engine/EngineConnection.swift`, add after the `trust(configPath:)`
method:

```swift

    /// Removes the host approval of the safeslop.cue at configPath (the Launch row's Revoke). The
    /// reverse of `trust` — a re-list then reports the profiles untrusted. Returns the path revoked.
    @discardableResult
    static func untrust(configPath: String = "") async throws -> String {
        let transport = try makeTransport()
        return try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            let resp = try await control.untrust(.with { $0.configPath = configPath })
            return resp.untrustedPath
        }
    }
```

- [ ] **Step 2: Refactor the Launch row for a separate revoke target + add trustControl/revoke**

In `app/Sources/SafeSlopCockpit/UI/LaunchTab.swift`, replace the whole `row(_:)` function with the
version below (switches the wrapping `Button` to `.onTapGesture` so the trailing `Menu` is its own tap
target; keeps the danger word, meta line, and open-axis chips from 0031/0032):

```swift
    @ViewBuilder
    private func row(_ ref: ProfileRef) -> some View {
        let missing = ref.configDirMissing
        HStack {
            // Ecusson: color is the chip background; the border WEIGHT (rank) is the non-color danger
            // channel so the chip reads in grayscale / for the colorblind (ayo S2). Glyph = tier.
            RiskBadge(symbol: ref.tierSymbol, color: ref.riskColor, rank: ref.dangerRank).help(ref.tierNote)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text(ref.name).font(.headline)
                    // symbol+word+color triad (macOS TCC / Little Snitch): the WORD carries danger.
                    Text(ref.dangerWord)
                        .font(.caption2.weight(.bold))
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(ref.riskColor.opacity(0.18), in: Capsule())
                        .foregroundStyle(ref.riskColor)
                }
                Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                    .font(.caption).foregroundStyle(.secondary)
                // Show what's UNRESTRICTED as loudly as the line above shows what's bounded (ayo S2).
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
                if !ref.riskHeadline.isEmpty {
                    Text(ref.riskHeadline).font(.caption2.weight(.medium)).foregroundStyle(ref.riskColor)
                }
            }
            if missing {
                badge("missing path", .secondary)
            } else {
                trustControl(ref)
            }
            Spacer()
            Image(systemName: missing ? "exclamationmark.triangle" : "arrow.up.forward.app")
                .foregroundStyle(.secondary)
        }
        // muted until approved; grayed harder when its config dir is gone.
        .opacity(missing ? 0.4 : (ref.isTrusted ? 1 : 0.6))
        .contentShape(Rectangle())
        .onTapGesture { if !missing { openWindow(id: "session", value: ref) } }
        // hover shows the underlying technologies powering this profile (policy.TechStack).
        .help(ref.techStack.isEmpty ? ref.tierNote : ref.techStack.joined(separator: "\n"))
    }

    /// The trailing trust control. When trusted it is a clickable menu whose one action is Revoke (the
    /// symmetric reverse of granting — ayo #3; one click, no biometric, since revoke removes privilege).
    /// When untrusted/changed it is the existing badge. The Menu consumes its own clicks, so tapping it
    /// never falls through to the row's launch gesture.
    @ViewBuilder
    private func trustControl(_ ref: ProfileRef) -> some View {
        if ref.isTrusted {
            Menu {
                Button("Revoke trust", role: .destructive) { Task { await revoke(ref) } }
            } label: {
                Label("trusted", systemImage: "checkmark.shield.fill")
                    .font(.caption2.weight(.semibold)).foregroundStyle(.green)
            }
            .menuStyle(.borderlessButton).fixedSize()
            .help("Trusted — click to revoke")
        } else if let b = ref.trustBadge {
            badge(b.text, b.color)
        }
    }

    /// Revoke this profile's trust, then refresh so the row reflects the new (untrusted) state.
    private func revoke(_ ref: ProfileRef) async {
        do {
            try await EngineConnection.untrust(configPath: ref.configDir)
        } catch {
            // Best-effort: a failed revoke leaves trust intact; the refresh below re-reads ground truth.
        }
        await engine.refresh()
    }
```

- [ ] **Step 3: Build + full Swift suite**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
```
Expected: `Build complete!`; `Test run with 16 tests ... passed` (no Swift-test change; the new code is
exercised by the Go smoke + manual click-test).

- [ ] **Step 4: Screenshot — confirm the trusted control renders**

```bash
make cockpit-shot launch
```
Read `/tmp/safeslop-cockpit-launch.png`: the seeded profiles are untrusted (the screenshot repo isn't
pre-trusted), so each row shows the orange "not trusted" badge as before — confirm the row still renders
correctly with the refactor (ecusson, danger word, meta, open-axis chips, badge, arrow). The trusted
"Revoke" menu path is verified by the Go smoke (`Untrust` flips the status) and the manual click-test in
Task 5 (a screenshot can't drive a trusted-state menu without a pre-trusted repo).

- [ ] **Step 5: Commit**

```bash
git add app/Sources/SafeSlopCockpit/Engine/EngineConnection.swift app/Sources/SafeSlopCockpit/UI/LaunchTab.swift
git commit -m "feat(cockpit): symmetric trust revoke — clickable trusted control on the Launch row"
```

---

### Task 5: Full verification + handoff

**Files:**
- Modify: `specs/research/2026-06-21-handoff.md` (Actionable #3 trust-revoke done)

- [ ] **Step 1: Run every gate**

```bash
make check ; make build
swift build --package-path app && swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
fish tests/run.fish 2>&1 | tail -3
fish scripts/slop-pinning.fish
```
Expected: all green (Go incl. trust + smoke; Swift 16; fish suite; pinning).

- [ ] **Step 2: Manual click-test (revoke is interactive)**

Launch the app against a repo with a trusted profile (trust one first via a session-window launch +
approve, or `safeslop trust <dir>`), then on the Launch row: click the green **trusted** control →
**Revoke trust** → confirm the badge flips to "not trusted" (the row refreshes). Confirm clicking the
menu does NOT open a session window (the gesture separation works).

- [ ] **Step 3: Update the handoff**

In `specs/research/2026-06-21-handoff.md`, note Actionable #3's symmetric trust revoke shipped in
`specs/0033` on `sp-cockpit-risk-legibility`; the consequence-specific Touch ID string (#3) was already
done in specs/0030, and auto-revoke-on-external-edit is already covered by the fail-closed `changed`
detection (a live file-watcher remains an optional follow-on).

- [ ] **Step 4: Commit + push**

```bash
git add specs/research/2026-06-21-handoff.md
git commit -m "docs(spec): symmetric trust revoke shipped (specs/0033, ayo Actionable #3)"
SSH_AUTH_SOCK="$HOME/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock" \
  git push forgejo sp-cockpit-risk-legibility
```

---

## Self-review notes

- **Spec coverage** (ayo Actionable #3 "trust grant and revoke must cost the same"): revoke now exists
  and is equally visible (Launch-row trusted control, Task 4) ✓; engine-backed removal (Tasks 1-3) ✓;
  one click, no biometric — risk-proportional per specs/0030 ✓. The other two #3 sub-items are noted as
  already-done / covered (consequence Touch ID = specs/0030; auto-revoke-on-edit = the fail-closed
  `changed` status), so #3 is closed without re-doing them.
- **Placeholder scan:** every step has concrete code/commands; the one conditional (Task 1 Step 1 import
  note) spells out the action.
- **Name consistency:** `Revoke` (trust) ↔ `Untrust`/`UntrustRequest`/`UntrustResponse`/`untrusted_path`
  (proto) ↔ `untrustFn` (Go wiring) ↔ `cockpitUntrust` (cli) ↔ `untrust` (Swift RPC) ↔ `trustControl`/
  `revoke` (LaunchTab). The revoke uses `canonicalPolicyPath`, the same key `enforceTrust`/`Approve` use.
- **Risk-proportional, not naively symmetric:** revoke is one click/no-biometric (removes privilege);
  grant keeps the TrustSheet review (adds privilege) — consistent with specs/0030, and the ayo's "equal
  visibility/reachability," not "equal friction in the dangerous direction."
- **Out of scope (intentional):** live file-watcher auto-revoke (the `changed` fail-closed already
  blocks a launch on external edit), temporal-scoped trust (that was the contested item the FLO in
  specs/0030 *rejected* for host; persistent trust stays).
