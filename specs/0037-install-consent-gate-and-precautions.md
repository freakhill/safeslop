# Install Consent Gate + Per-Tool Precautions (specs/0036 Task 7 + the hover ask)

**Goal:** two related cockpit-safety features sharing one backend:
1. **Per-tool hover precautions** — mousing over any tool in the Installs tab explains the extra
   precautions safeslop takes for *that* tool (verified-pin vs brew vs unverified remote script vs
   already-present no-clobber). The user's explicit ask.
2. **Install consent gate (0036 Task 7)** — clicking Install no longer runs immediately; it shows a
   proportionate consent sheet first: a **verified-pin** or **brew** install is a single Confirm; an
   **unverified remote-script** install (the curl|sh / npm tools that have no pin) shows the exact
   command + a prominent warning and requires typing a short confirm phrase before it can run.

**Single source of truth:** an engine `tools.InstallPreview(Status)` + `tools.Precautions(Status)`
drive BOTH surfaces, so the tooltip and the gate can never disagree about what an install does.

**Architecture:** purely additive. New engine functions in `internal/engine/tools`, six new fields on
the `ToolStatus` proto message (so the already-loaded Installs list carries everything — NO new RPC,
no extra round-trip on hover or on the gate), and a small SwiftUI confirm sheet. The proportionate
"typed phrase for unverified" is a lighter, self-contained comprehension friction; wiring the full
specs/0030 decoy-statement gate for installs is left as optional future hardening (Task 7b).

**Off-limits:** the verify/apply engine (`apply.go`, `verify.go`), the pin manifest values, the
launch/trust code, the host-launch gate (`HostConsent*`). Don't change `InstallByName`'s routing.

---

## Task A — Engine: InstallPreview + Precautions (the shared backend)

- [ ] **Add the preview/precautions model + classifier**
  FILE: `internal/engine/tools/tools.go`
  CHANGE: Add a `Verification` string type with constants `VerifiedPin` ("verified-pin"), `BrewManaged`
  ("brew"), `UnverifiedRun` ("unverified-run"). Add a `Preview` struct {Name, Route, Command, SourceURL,
  SHA256, Version, Verification, Precautions string; NeedsConsent bool}. Add `InstallPreview(s Status)
  Preview` that mirrors `installArgv`'s route order (brew/cask → BrewManaged; embedded pin → VerifiedPin
  with the pin's URL/SHA256/Version; script → UnverifiedRun, NeedsConsent=true) and fills Command from
  `InstallRouteHint`/argv. Add `Precautions(s Status) string` covering present (no-clobber, plus a
  shadow note when `ShadowedPaths` non-empty), installable (delegates to `InstallPreview().Precautions`),
  and manual (no route). Precaution text per verification is spelled out (verified = HTTPS + sha256 vs a
  checksum compiled into the notarized binary + kept-backup rollback; brew = delegated to Homebrew, no
  remote code by safeslop; unverified = runs a remote script with your privileges, exact command shown,
  explicit confirm required, nothing verified beyond TLS).
  VERIFY: `go test ./internal/engine/tools/ -run TestInstallPreview -v 2>&1 | tail -5`
  EXPECTED: PASS.

- [ ] **Test the classifier**
  FILE: `internal/engine/tools/tools_test.go`
  CHANGE: `TestInstallPreview` — uv (missing) → VerifiedPin, NeedsConsent=false, SHA256/Version/SourceURL
  populated from the pin, Precautions mentions "checksum"; a script-only-no-pin tool (Rust/rustup or
  Claude Code) → UnverifiedRun, NeedsConsent=true, Precautions warns about a remote script; a present
  tool → Precautions says safeslop won't clobber it. Use catalog lookups; force brew on/off via the
  `installArgv`-style path where needed (InstallPreview can take the same brew-availability split if
  required — add an inner `installPreview(s, brewAvail)` mirroring `installArgv`).
  VERIFY: `go test ./internal/engine/tools/ -run TestInstallPreview && make check`
  EXPECTED: PASS; `make check` green.

- [ ] **Commit Task A** — `feat(tools): InstallPreview + Precautions — one source of truth for the install gate + hover`.

---

## Task B — Proto + control: carry the preview on ToolStatus

- [ ] **Extend the ToolStatus message**
  FILE: `internal/engine/control/control.proto` AND the byte-identical Swift copy
  `app/Sources/SafeSlopCockpit/proto/control.proto`
  CHANGE: append to `ToolStatus`: `string precautions = 10;`, `string verification = 11;`,
  `string source_url = 12;`, `string sha256 = 13;`, `string pinned_version = 14;`,
  `bool needs_consent = 15;`. Keep the two files identical. Then `make proto` to regenerate the Go stubs.
  VERIFY: `make proto && git diff --stat internal/engine/control/*.pb.go | tail -2`
  EXPECTED: the generated `.pb.go` updates with the new fields; build still compiles.

- [ ] **Populate the new fields in ListTools**
  FILE: `internal/engine/control/server.go` (`ListTools`)
  CHANGE: for each status, compute `pv := tools.InstallPreview(st)` and set
  `precautions = tools.Precautions(st)`, `verification = string(pv.Verification)`,
  `source_url = pv.SourceURL`, `sha256 = pv.SHA256`, `pinned_version = pv.Version`,
  `needs_consent = pv.NeedsConsent`. Keep `install_hint` as-is (the command). Precautions is set for ALL
  tools (present + installable + manual); the rest only matter when installable.
  VERIFY: `make build && ./safeslop --json … ` is not a clean check here — instead `go test ./internal/engine/control/ 2>&1 | tail -3`
  EXPECTED: control tests PASS; build compiles.

- [ ] **Commit Task B** — `feat(control): carry install preview (precautions, verification, pin url/sha) on ToolStatus`.

---

## Task C — Swift: hover precautions + the consent sheet

- [ ] **Hover precautions on every row**
  FILE: `app/Sources/SafeSlopCockpit/UI/InstallsTab.swift`
  CHANGE: add `.help(t.precautions)` to the row container (when precautions non-empty), so mousing over
  any tool explains its precautions. Keep the existing source/shadow/installHint `.help`s on their chips.
  VERIFY: `swift build --package-path app 2>&1 | tail -3`
  EXPECTED: builds.

- [ ] **Consent sheet before install**
  FILE: `app/Sources/SafeSlopCockpit/UI/InstallsTab.swift` (+ a new `InstallConsentSheet.swift` if it
  keeps the tab readable)
  CHANGE: the Install button now sets `@State private var pending: ToolStatus?` instead of installing
  directly; a `.sheet(item:)` presents the consent sheet. The sheet shows: tool name + note, the
  verification badge (green "sha256-verified pin" / blue "Homebrew" / orange "⚠︎ unverified remote
  script"), the precautions text, and the exact command (`install_hint`) + for a verified pin the
  source URL, version, and short sha. Footer: Cancel + Install. For `needs_consent` (unverified) the
  Install button is disabled until the user types a confirm phrase (e.g. the tool name, shown in the
  prompt) into a field — proportionate friction; verified/brew enable Install immediately. Confirming
  runs the existing `install(name)` flow.
  VERIFY: `swift build --package-path app && swift test --package-path app 2>&1 | tail -5`
  EXPECTED: builds + tests pass.

- [ ] **Screenshot smoke of the gate**
  FILE: `app/screenshot-cockpit.sh` (only if a new preview hook is needed; the sheet can be shot by
  driving the installs tab) — OPTIONAL.
  CHANGE: confirm `make cockpit-shot installs` still renders; manually verify the sheet by clicking in
  `make cockpit` (a human/click check, noted for jojo).
  VERIFY: `make cockpit-shot installs 2>&1 | tail -2`
  EXPECTED: writes a PNG without error.

- [ ] **Commit Task C** — `feat(cockpit): install consent gate + per-tool hover precautions`.

---

## Verify before "done"
```
make check ; make build ; fish scripts/slop-pinning.fish ; fish scripts/slop-sync-help.fish check
swift build --package-path app && swift test --package-path app
make cockpit-shot installs
```

## Deferred (Task 7b, optional hardening)
Reuse the full specs/0030 decoy-statement comprehension gate for unverified installs (author install
consent statements engine-side, return via a PreflightInstall RPC, present the existing HostConsentView).
The typed-phrase friction in Task C already gives proportionate comprehension friction; the decoy gate is
a stronger upgrade, not a correctness gap.
