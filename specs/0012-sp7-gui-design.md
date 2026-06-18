# SP7 — GUI portal + installer: design

**Status:** Design (locked in brainstorm 2026-06-17; this doc is the written record). Folds the cross-model prior-art actionables (`specs/research/2026-06-17-startup-usecase-prior-art.md`). Execution split into SP7a (control plane + portal + terminal-launch) and SP7b (installer); see "Build order".

**Audience for the GUI:** the non-technical coworker who can't run fish/uv/cue installers behind corporate Cloudflare WARP TLS interception, on a MacBook deploying to AWS/GCP. The CLI (SP1–SP6) is the shipped power-user surface; the GUI is the *second* surface (the dropped SP6 terminal TUI is **not** revived — direction is "CLI or GUI").

---

## 1. Two signed artifacts

slop ships as **two** independently signed + notarized artifacts:

1. **`slop`** — the Go engine + CLI (today's binary), gaining `slop serve`, `slop launch`, `slop install`.
2. **`SafeSlop.app`** — a native macOS **SwiftUI** app, **thin presentation only**. It holds no policy logic; it drives the engine over the control plane.

The app and engine are versioned together (the `.proto` contract is the compatibility boundary). The app build/sign/notarize is done in Xcode with jojo's Apple Developer cert; the **Go engine side + the `.proto` + generated Go server stubs** are in this repo and are what SP7a/SP7b build. Swift client stubs are generated from the same `.proto` and vendored into the app project.

---

## 2. Control plane — gRPC over a Unix domain socket

**Decision (locked):** the app ↔ engine control channel is **gRPC over a Unix-domain socket**. The `.proto` is the **single contract** → generated Go server (engine) + Swift client (app) stubs, with **server-streaming** for progress (install steps, run lifecycle events). jojo **explicitly rejected** CLI-stdout / `--json` string parsing between app and engine — `--json` stays for *humans*, but the app never parses CLI text.

- New engine command **`slop serve`**: binds the UDS, registers the gRPC services, serves until signalled. Idempotent; one socket per user.
- **Socket path:** `~/.slop/s.sock` — deliberately **short** (macOS `sun_path` is 104 bytes; `~/Library/Application Support/...` + a long username silently overflows → `bind: invalid argument`). Created `0700` dir, `0600` socket.
- **Peer authentication (research HIGH):** any user process can `connect()` a user-owned socket, so the server authenticates the peer:
  - **v1 (this SP, CGO-free):** `LOCAL_PEERCRED` (`getsockopt`/`unix.GetsockoptXucred` on darwin) → assert the peer **uid == server uid** (same-user). Reject cross-uid. No CGO needed.
  - **Hardening (follow-on):** verify the peer's **code-signing identity** (Team ID + bundle id == `SafeSlop.app`) via the audit token. This needs Security.framework (CGO) or a `codesign`/`csops` shell-out — deferred to keep `CGO_ENABLED=0`. Flagged so the v1 uid check is not mistaken for full peer-auth.

**Dependency cost:** adds `google.golang.org/grpc` (pure Go; `protobuf` is already an indirect dep). Stub generation needs `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` at dev time; the **generated `.pb.go` is committed** (CI does not run protoc). A `make proto` target regenerates.

---

## 3. Data plane — `slop launch` spawns a terminal (ctty intact)

The **interactive agent session is NOT a gRPC-parsed channel.** The agent (claude/shell) needs the terminal foreground (ctty, the §6.2 #1 risk). So:

- New engine command **`slop launch <profile>`**: resolves the profile (same path as `slop run`) and **spawns a terminal window** running the agent, with the ctty handoff intact (reuses `exec.RunInTerminal` / the §6.2 two-path launch). The app calls `Launch(profile)` over gRPC; the engine opens a *real terminal*, not a pipe the app reads.
- This keeps the app out of the PTY business entirely and preserves the existing, tested ctty guarantees. The gRPC `Launch` returns once the terminal is spawned (+ streams lifecycle events: spawned / exited).

---

## 4. Terminal-launch subsystem + `~/.config/slop/config.cue`

`slop launch` needs to know *which terminal* and *how to make the session recognizable*. New **user-level** config `~/.config/slop/config.cue` — distinct from the per-repo `slop.cue` (which is policy); this is per-user preference.

- **Preferred terminal**, from a supported list: `Terminal.app`, `iTerm2`, `Ghostty`, `generic` (`kitty`/`wezterm` later). **Terminal.app** (and `generic`/unknown — the always-present fallback) opens via AppleScript `do script`; **iTerm2** via AppleScript `create window with default profile command`; both run in the new window's login shell. **Ghostty** opens via `open -na Ghostty --args -e <shell> -lc <command>` (its `-e` takes a program, so the command is wrapped in the user's preferred shell). The session command is built shell-quoted (`internal/engine/launch`).
- **Preferred shell** (wraps the command for the Ghostty adapter; Terminal.app uses its own login shell).
- **Recognizability tagging** so a user can tell slop windows apart:
  - **Baseline (all terminals):** OSC window-title sequence + `SLOP_SESSION` / `SLOP_CWD` env in the child.
  - **Native (where supported):** iTerm2 badge / tab color.
  - **Opt-in:** a shell-prompt marker.
  - **WM integration:** a *documented* yabai/aerospace rule recipe keyed on the **stable window title** — a recipe the user applies, **NOT** engine-applied (the engine never edits the user's WM config).

Schema for `config.cue` lives alongside the policy schema (`internal/engine/policy/schema/` or a new `internal/engine/userconfig/`); validated like `slop.cue`.

---

## 5. Installer — `slop install` (the app is a wizard over it)

New engine commands (the `.app` installer screen is a thin wizard over these; CLI-usable standalone):

- **`slop install status`** — what's present/missing/outdated (engine version, toolchains, container runtime, the app, signing).
- **`slop install plan`** — the ordered, **pinned + checksummed** actions to reach the desired state (no `latest`; every artifact has a sha256 — mise has no checksum verify, so slop adds it and **fails closed**).
- **`slop install apply`** — execute the plan; stream progress over gRPC.
- **Optional behavioral VM-eval** (research): run a candidate installer step inside a throwaway VM and diff fs+net (offline-after-download) before trusting it — *checksum proves provenance, not honesty.* Opt-in, heavier; deferred within SP7b but the schema reserves it.

---

## 6. Research actionables folded in (from 2026-06-17 prior-art)

- **Socket peer-auth** (LOCAL_PEERCRED + codesign) and the **104-byte `sun_path`** short-socket constraint → §2.
- **Pinned + checksum, fail-closed**; **VM-eval = behavioral diff** (provenance ≠ honesty) → §5.
- **Provision toolchains in-boundary from a clean home** (exclude host `~/.npmrc`/`~/.cargo/credentials`/`~/.config/gcloud`) → relevant to `install apply` running toolchain provisioning; carried as an SP7b constraint.
- Egress baseline / OrbStack bridge / per-language build-script knobs are **SP3/SP8 concerns**, not SP7 — noted here only to disclaim scope.

---

## 7. Build order

- **SP7a (this next plan):** the **control-plane spine + data plane + terminal-launch** — `slop serve` (gRPC-over-UDS, uid peer-auth, the `.proto`, generated Go server stubs), `slop launch` (terminal spawn over the existing ctty path), and the `~/.config/slop/config.cue` terminal-launch subsystem. Pure Go + proto; TDD like the cred providers (a UDS-guarded serve test, a config parse test, a launch-adapter argv test). The `.app` is **not** built here, but the `.proto` + Go server are the contract it will target.
- **SP7b:** `slop install` (`status`/`plan`/`apply`, pinned+checksum, optional VM-eval).
- **SwiftUI `SafeSlop.app`:** scaffolded against the committed `.proto` (generated Swift client stubs); built/signed in Xcode by jojo. Out of scope for the engine plans beyond providing + versioning the contract.

---

## 8. Scope boundary (what the engine plans build vs not)

| Piece | In SP7a/SP7b (this repo, Go) | Done by jojo (Xcode/Swift) |
|---|---|---|
| `.proto` contract | ✓ authored + committed | consumes it |
| Generated **Go** server stubs | ✓ committed (`make proto`) | — |
| Generated **Swift** client stubs | recipe documented; `make proto` can emit if `protoc-gen-swift` present | vendored into the app |
| `slop serve` / `slop launch` / `slop install` | ✓ | — |
| `~/.config/slop/config.cue` + adapters | ✓ | — |
| `SafeSlop.app` SwiftUI UI, build, sign, notarize | — | ✓ |

---

## 9. Open design decisions flagged for SP7a planning

1. **Peer-auth v1 = uid-only** (CGO-free); codesign-identity hardening deferred. **Recommendation:** as written.
2. **gRPC vs a hand-rolled length-prefixed protobuf-over-UDS.** gRPC is locked (jojo's call) — accept the `grpc` dep + committed generated code. **Recommendation:** gRPC as decided.
3. **`config.cue` location:** new `internal/engine/userconfig/` package (parallels `policy/`) vs folding into `policy/`. **Recommendation:** new `userconfig/` package — it is user-level, not policy.
4. **Terminal adapters scope for SP7a:** `Terminal.app` (AppleScript `do script`) + `iTerm2` (AppleScript `create window … command`) + `Ghostty` (`-e <shell> -lc`) + `generic`→Terminal fallback in v1; `iTerm2`-native tagging (badge/tab color) + `kitty`/`wezterm` follow-on. **Recommendation:** as written.
5. **`make proto` toolchain:** require `protoc` + plugins locally; commit generated code so CI/`make build` never needs protoc. **Recommendation:** as written.
