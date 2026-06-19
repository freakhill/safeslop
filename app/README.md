# SafeSlop cockpit (SwiftUI app)

Native macOS app that **embeds a terminal** (SwiftTerm) and drives the `safeslop` engine over
**gRPC on a Unix-domain socket** (`~/.safeslop/s.sock`). It is the app side of the embedded
cockpit designed in [`specs/0014`](../specs/0014-sp7c-embedded-cockpit-design.md); the engine
side (session control plane, PTY + `Attach`) is SP7c-1/2/3, already on `main`.

This is a **scaffold** — a buildable starting point. The engine wiring is real; the chrome is a
v1 shell. The Apple/codesign/release work is the human track from here.

## Layout

| Path | Role |
|---|---|
| `Package.swift` | SwiftPM executable; deps: grpc-swift v2, swift-protobuf, SwiftTerm |
| `Sources/SafeSlopCockpit/proto/control.proto` | **verbatim copy** of the engine's `internal/engine/control/control.proto` (keep in sync) |
| `Sources/SafeSlopCockpit/proto/grpc-swift-proto-generator-config.json` | codegen config — the `GRPCProtobufGenerator` build plugin regenerates the Swift client at build time (needs `protoc` on PATH) |
| `Engine/EngineConnection.swift` | UDS transport, `Ping`/`ListProfiles`, launch-on-demand of `safeslop serve` |
| `Engine/CockpitSession.swift` | one session: `OpenSession` + the long-lived `Attach` bidi pump (PTY out → terminal, input/resize → engine), `CloseSession` |
| `UI/TerminalBridge.swift` | `NSViewRepresentable` wrapping SwiftTerm's `TerminalView`; keystrokes/resize → session |
| `UI/SessionScene.swift` | the cockpit chrome — trust-colored border + header/footer (`specs/0014` §5) |
| `SafeSlopCockpitApp.swift` | `@main` App: a launcher window + a per-session `WindowGroup` |

## Build & run

```bash
cd app
swift build          # first build compiles the gRPC/NIO graph + runs protoc codegen (slow once)
swift run            # launches the app; the launcher starts `safeslop serve` if needed
```

`safeslop` must be on `PATH` (the launcher spawns `safeslop serve` on demand). Have a
`safeslop.cue` with at least one profile in the dir the engine runs from.

## Trust chrome (`specs/0014` §5)

The window border color encodes the isolation posture at a glance:
- **amber** — `environment: vm` or `container`
- **red** — `network: allow` (open egress)
- **green** — `network: deny`

The header shows profile / environment / network / agent; the footer shows session state + id.

## What's a stub vs. real

- **Real:** the gRPC client (UDS, `OpenSession`/`Attach`/`CloseSession`/`Trust`), the bidi I/O pump,
  the SwiftTerm bridge (input/output/resize), the trust-color logic, launch-on-demand, and the
  **trust flow** — an untrusted `OpenSession` surfaces an in-place capability sheet (plain-language
  posture, highest-risk in the button) that calls the `Trust` RPC and retries (specs/research/
  2026-06-20-cockpit-safe-by-design.md). Quick Look raw-diff + EnvTier-sourced capability text are
  next.
- **Stub / next:** one gRPC connection **per session** (sharing a single client across windows is a
  follow-up); no reconnect-after-drop (engine streams live bytes only, `specs/0014` §10); no
  settings/`cockpit: external` opt-out yet; no app icon / codesigning / notarization; the
  tracking/network side panels are reserved-for, not built (`specs/0014` §5).

## Keeping the proto in sync

`proto/control.proto` is a copy of the engine's. When the engine's `.proto` changes
(`make proto` there), copy it here and rebuild so the Swift client regenerates. (A future
build step could symlink or fetch it to remove the copy.)

## Signing / distribution

`swift run` works unsigned for local dev. For a distributable `.app` (entitlements, hardened
runtime, notarization), wrap this package in an Xcode app target — Xcode opens `Package.swift`
directly, so the sources here are reusable as-is.
