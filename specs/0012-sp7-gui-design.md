# SP7 — GUI portal + installer: design

**Status:** Design (locked in brainstorm 2026-06-17; this doc is the written record). Folds the cross-model prior-art actionables (`specs/research/2026-06-17-startup-usecase-prior-art.md`). Execution split into SP7a (control plane + portal + terminal-launch) and SP7b (installer); see "Build order". **Amended 2026-06-19** with the promise-vs-pain `ayo` HIGH actionables (§10, from `specs/research/2026-06-19-design-promise-vs-pain.md`).

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

- **Preferred terminal**, from a supported list: `Terminal.app`, `iTerm2`, `Ghostty`, `WezTerm`, `kitty`, `generic`. **Terminal.app** (and `generic`/unknown — the always-present fallback) opens via AppleScript `do script`; **iTerm2** via AppleScript `create window with default profile command`; both run in the new window's login shell. **Ghostty** uses `open -na Ghostty --args -e <shell> -lc <command>`; **WezTerm** uses `open -na WezTerm --args start -- <shell> -lc <command>`; **kitty** uses `open -na kitty --args <shell> -lc <command>` — each runs the command through the user's preferred shell. The session command is built shell-quoted (`internal/engine/launch`).
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
4. **Terminal adapters scope for SP7a:** `Terminal.app` + `iTerm2` (AppleScript) and `Ghostty` + `WezTerm` + `kitty` (`open -na … --args` running `<shell> -lc <command>`), with `generic`→Terminal fallback — all v1. `iTerm2`-native tagging (badge/tab color) is the remaining follow-on. **Recommendation:** as written.
5. **`make proto` toolchain:** require `protoc` + plugins locally; commit generated code so CI/`make build` never needs protoc. **Recommendation:** as written.

---

## 10. Amendments — 2026-06-19 (cross-model `ayo`)

Folds the HIGH actionables from `specs/research/2026-06-19-design-promise-vs-pain.md`
(blind Host/Gemini/DeepSeek pass on "promise vs pain") that land on SP7 surfaces. Each
item cites the § it changes. Cross-cutting items that fall outside SP7 are tracked at the
bottom as **separate slices** (not folded here).

### 10.1 Control-plane peer-auth — revises §9.1 (no longer "deferred as written")

The ayo's sharpest Q1 finding: with uid-only `LOCAL_PEERCRED`, **the very agent SP7
sandboxes can `connect()` `~/.slop/s.sock` and ask the engine to `Launch` an *un*sandboxed
profile or surface secrets** — the tool's control channel is reachable by the thing it
cages (confused deputy; cf. Zoom's local web server, firejail D-Bus CVE-2021-26910, why
macOS XPC enforces code-signing per message). So §9.1's "uid-only v1, codesign deferred"
is **downgraded from a clean recommendation to a known hole that must close before the GUI
ships to Audience A**:

- **Un-defer the codesign / audit-token peer check.** It is achievable **without CGO** via
  a `codesign`/`csops` verification of the peer's audit token (assert Team ID + bundle id
  == `SafeSlop.app`), keeping `CGO_ENABLED=0`. uid-only is acceptable only for a
  power-user-CLI-only build with no GUI installed.
- **Refuse control-plane connections originating from inside a sandbox / the engine's own
  spawned process tree** — a launched agent must never be able to talk back to `serve`.
- **User-presence gate (LocalAuthentication / Touch ID)** on privileged verbs
  (`Launch` of a weaker-than-default profile, granting write creds) so same-uid automation
  can't silently invoke them. (Q1; H3/M7.)

### 10.2 Installer trust chain + provenance — extends §5

- **Document the load-bearing justification for "no naive Homebrew":** the pins are
  **compiled into the notarized binary**, so the pin set inherits Apple's code-signing
  root of trust (tampering breaks the signature). A GitHub-release download against an
  advisory README hash would be *weaker* than brew, not stronger — the notarized-binary
  chain is the whole reason the refusal is defensible. State it explicitly in the installer
  docs. (Q1; H6.)
- **Verify the upstream maintainer signature**, not only the sha256 safeslop copied: sha +
  embedded-in-notarized-binary defends *substitution/tampering*, never an upstream
  maintainer compromise shipping malware at a faithfully-checksummed pinned version
  (TUF/SLSA "provenance ≠ honesty"). mise publishes `SHASUMS256.asc`/minisig; tart signs
  releases — verify them. Prefer artifacts aged `> N` days (freshness/time-delay) so a
  poisoned release has a detection window. (Q1; H6 — folded into SP7b-3 `apply`.)
- **Demote the reserved behavioral VM-eval to opt-in-first-use only.** Cuckoo/Qubes show
  per-install VM diffing is too slow/noisy/evadable to gate routine updates; it will be
  disabled. The routine honesty gap is better closed by the signature + freshness checks
  above. (Q2; H8.)

### 10.3 WARP toolchain TLS plumbing — extends §5 (the #1 adoption gate)

The single-binary rewrite fixes *safeslop's own* downloads (a `CGO_ENABLED=0` Go binary on
darwin consults the system trust store, so WARP's CA in the keychain is honored), **but not
the toolchains it installs** (`npm`/`pip`/`uv`/`cargo`/Node ignore the keychain and fail
opaquely behind WARP → users reach for `--insecure` and leave it on). `install apply` must
export the system-keychain CA bundle and wire each toolchain's cert env
(`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `PIP_CERT`, `UV_NATIVE_TLS=1`, `CARGO_HTTP_CAINFO`).
**Port the existing fish 4-strategy uv TLS fallback (`scripts/slop.fish`) — do not
re-discover it.** (Q2; H4.)

### 10.4 Strong, zero-authoring default + GUI never shows CUE — extends §4/§5

Secure-by-default must mean **strong-default + zero authoring**, not "the weakest env, free
of charge." The GUI wizard (§5) **generates/edits policy; Audience A never sees raw CUE**
(Tailscale's zero-config adoption lever; Nix/Bazel's config-language adoption penalty). A
profile that declares secrets/creds should **default its environment to container
(squid-enforced), not bare sandbox.** (Q1+Q2; H7. The engine-side "auto-select a strong
profile when no `safeslop.cue` exists" is the cross-cutting SP-run item below.)

### 10.5 Cross-cutting — tracked as separate slices (NOT folded into SP7)

These ayo HIGH items touch already-shipped SP1–SP3 surfaces, not SP7; recorded here so
they're not lost, to be scoped as their own plans:

- **Tier labels** in `doctor`/`run`/README: sandbox = mistake-guard, container+squid =
  network-enforced, vm = adversary-grade. The honest product claim is "guards agent
  *mistakes* + accidental exfil, **not** a malicious-code escape jail." (Q1; H1 — the
  load-bearing reframe; SP1/SP3 + docs.)
- **Policy integrity:** hash `safeslop.cue` at launch from *outside* the writable mount,
  refuse mid-run mutation, and treat a **repo-supplied** policy as untrusted-until-host-
  approves (devcontainer trust-prompt model) — the sandboxed agent can otherwise rewrite
  the file that governs its own run, and `git clone <evil>` ships a permissive one.
  (Q1; H2 — orchestrator/policy.) **Realized: specs/0022** — host-side
  `~/.config/safeslop/trust.json`, `safeslop trust`, and a fail-closed `run` gate on the
  policy's sha256 (untrusted *or* changed blocks; `validate`/`list`/`--dry-run` stay open).
  Remaining fast-follow: the gRPC `Launch`/`OpenSession` cockpit chokepoint (the GUI surface).
- **Scope-first, decay-second creds:** downscope cloud tokens at the *minting* step
  (AWS session policy / permission boundary, narrowest GCP scopes) so even full-TTL reuse
  is bounded; stage via a short-lived `credential_process` rather than raw env vars.
  (Q1; H5 — SP2 creds.)
- **FLO hand-off:** read-only-default deploy keys vs autonomous-agent ergonomics is a
  genuine contested fork (DeepSeek: read-only pushes power users to long-lived write
  tokens) — score with `feedback-loop-optimization`, don't decide ad hoc.
