# SP7c — SafeSlop cockpit: embedded terminal + multi-session control plane (design)

**Status:** Design, approved in brainstorm 2026-06-18. **Supersedes `specs/0012` §3** (the "data plane = spawn a separate terminal window" model). The external-terminal launch from §3/§4 survives as an **opt-out**, not the default.

**Idea:** Instead of `slop launch` opening a *separate* terminal app, **SafeSlop.app embeds its own terminal** (a `SwiftTerm` view) and wraps it in decorated, colored **chrome** — a native SwiftUI frame around the terminal with room for future tracking/network panels. The agent still runs inside its isolation boundary; only the terminal *rendering* moves into the app.

**macOS reality this is built around:** there is no supported API to reparent another app's window (your real Ghostty/iTerm2) into SafeSlop.app. So "embed the terminal" means the app hosts its *own* emulator (`SwiftTerm`, mature/production-used) and renders the agent's PTY. The trade-off — you don't get your configured Ghostty-with-plugins in the embedded view — is accepted; the external terminal stays available as an opt-out.

"Chrome" here = **UI framing** (border, header/footer), not a browser. The cockpit is 100% native SwiftUI/AppKit — no web view, no Chromium/Electron.

---

## 1. Session model

A **session** = one isolated agent run + its host-side PTY, identified by a `session_id`. The engine keeps a concurrency-safe **session registry**: `session_id → {profile, env, pty, agent-or-bridge handle, state}`. A single `slop serve` (one `~/.slop/s.sock`) multiplexes **all** sessions, so **multiple app windows open at once is just N concurrent sessions** — no extra sockets, no per-window engine process.

State machine per session: `opening → running → closing → closed` (plus `error`). The registry is the single source of truth; the app reflects it.

---

## 2. Control plane — `.proto` additions

Extends the SP7a `Control` service (`Ping`, `ListProfiles`, `Launch` stay). New:

```proto
// Open an isolated agent session and allocate its PTY. Non-blocking.
rpc OpenSession(OpenSessionRequest) returns (OpenSessionResponse);
// Attach to a session's terminal: server streams PTY output; client streams input + resize.
rpc Attach(stream ClientFrame) returns (stream ServerFrame);
// Terminate a session: kill the agent, free the PTY, run the env teardown.
rpc CloseSession(CloseSessionRequest) returns (CloseSessionResponse);

message OpenSessionRequest { string profile = 1; string config_path = 2; uint32 cols = 3; uint32 rows = 4; }
message OpenSessionResponse { string session_id = 1; }

message ClientFrame {
  oneof msg {
    string attach_session_id = 1; // MUST be the first frame
    bytes  input = 2;             // bytes typed into the PTY
    Resize resize = 3;            // terminal resized
  }
}
message Resize { uint32 cols = 1; uint32 rows = 2; }
message ServerFrame {
  oneof msg {
    bytes output = 1;            // PTY output bytes
    Exited exited = 2;           // agent exited; stream ends after this
  }
}
message Exited { int32 exit_code = 1; }

message CloseSessionRequest { string session_id = 1; }
message CloseSessionResponse {}
```

**Data-plane mode is per request:** `OpenSession` + `Attach` = the embedded cockpit (default). The existing `Launch` = the **external-terminal opt-out** (spawns Terminal.app/iTerm2/Ghostty/WezTerm/kitty via the SP7a adapters). The app picks per session based on a setting (§6).

`Attach` is a long-lived bidirectional stream. The first `ClientFrame` carries `attach_session_id`; subsequent client frames are `input`/`resize`, server frames are `output` until an `exited`, then the stream ends.

---

## 3. PTY + per-environment boundary bridging (engine)

The engine **owns the PTY** (host-side, via `creack/pty` — already the §6.2 wrapped/container fallback) and attaches the agent's stdio + controlling terminal to the slave, **preserving the real §6.2 ctty handoff** (`setpgid`/`tcsetpgrp`). What differs from SP7a is who reads the master: the `Attach` pump, not an inherited terminal.

Bridging is per environment, reusing the existing launchers:
- **sandbox / host:** spawn the agent directly on the PTY slave (the agent is a host process under the Seatbelt profile or plain host).
- **container:** `docker … run -it` (or `exec -it`) with stdio = the host PTY slave; docker bridges it to the in-container agent. (Refactor `container.Launch` to attach to a provided PTY instead of the inherited terminal.)
- **vm:** `ssh -t` with stdio = the host PTY slave; ssh allocates the remote PTY and bridges. (Refactor `vm.Launch` similarly.)

In **all four**, the PTY master is host-side and the agent's terminal — even across a boundary — is bridged to it by the env's own `-it`/`-t` mechanism. A per-session goroutine pumps `master ↔ Attach stream` and applies `Resize` via `TIOCSWINSZ` on the master (propagated into the container/vm by docker/ssh). On agent exit the engine sends `Exited`, then runs the env teardown (stageDir wipe / `container down` / `vm destroy`) and removes the session from the registry.

---

## 4. Multi-window / multi-session

- **App:** SwiftUI `WindowGroup` — each window is one session: it calls `OpenSession`, then holds an `Attach` stream, rendering output in `SwiftTerm` and sending input/resize. Closing a window → `CloseSession`.
- **Engine:** the session registry + one pump goroutine per session make N concurrent sessions independent. `slop serve` is the single long-running host process for all windows; the app ensures it's running (launch-on-demand: if `Ping` fails, the app starts `slop serve` and retries).

No global terminal state is shared between sessions; each has its own PTY, agent, and teardown.

---

## 5. Cockpit chrome (app/Swift — designed here, built in Xcode)

Per window:
- A **`SwiftTerm` view** fills the center, fed by the `Attach` stream.
- A **decorated, colored border** around it — the embellishment. The border **color encodes trust**: e.g. red = `network: allow` (open egress), green = `network: deny`, amber = vm/container — surfacing slop's security posture at a glance.
- A slim **header/footer** showing session identity: profile name, environment badge, network mode, status.

**v1 = this shell only.** The tracking/network **panels** the idea mentioned ("track things, open network, …") are *scaffolded for* (the chrome reserves layout regions + the control plane can grow status RPCs) but **deferred** (YAGNI). Adding a panel later = a new SwiftUI view in a reserved region + (if it needs live data) a new streaming RPC.

---

## 6. External-terminal opt-out

A per-profile/per-launch setting selects the data-plane mode. Proposed: extend the user-level `~/.config/slop/config.cue` with `cockpit: "embedded" | "external"` (default `"embedded"`), overridable per launch from the app UI. `"external"` routes to `Launch` (the SP7a adapters open Terminal.app/iTerm2/Ghostty/WezTerm/kitty); `"embedded"` routes to `OpenSession`/`Attach`. The SP7a adapter work is the opt-out's implementation — not wasted.

---

## 7. Engine-vs-app split (what THIS repo builds)

| Piece | Engine (Go, this repo) | App (Swift, Xcode — jojo) |
|---|---|---|
| `.proto` OpenSession/Attach/CloseSession + frames | ✓ author + commit | consumes |
| Generated **Go** stubs (`make proto`) | ✓ committed | — |
| Session registry (N concurrent) | ✓ | — |
| PTY allocation + per-env bridging (sandbox/host/container/vm) | ✓ | — |
| Attach pump + resize + teardown | ✓ | — |
| `cockpit: embedded\|external` setting | ✓ (schema + routing) | sets it |
| `SwiftTerm` view, `WindowGroup`, chrome/border, gRPC client | — | ✓ |

The app is still **thin**: it renders bytes and sends input; all isolation, PTY, and lifecycle logic is engine-side.

---

## 8. Decomposition / build order

Three units, each its own plan:
- **SP7c-1 (engine):** session control plane (`OpenSession`/`Attach`/`CloseSession` + frames + generated stubs), the session registry, PTY allocation, the Attach pump + resize, and **sandbox/host** bridging + teardown. End-to-end provable with a Go gRPC test client. ← first.
- **SP7c-2 (engine):** **container** (`docker -it`) and **vm** (`ssh -t`) PTY bridging — refactor `container.Launch`/`vm.Launch` to attach to a provided PTY.
- **App (Swift):** the `SwiftTerm` cockpit, `WindowGroup` multi-window, chrome, gRPC client — jojo's Xcode track, against the committed `.proto`.

---

## 9. Testing

- **Go gRPC client** drives `OpenSession → Attach` against a trivial PTY agent (e.g. `cat` / a tiny echo) over the real `~/.slop/s.sock`: assert input→output round-trip and that a `Resize` reaches the child (`stty size`).
- **Session registry** concurrency test: open N sessions, drive each independently, close in arbitrary order; assert isolation + no leaks.
- **Per-env bridging:** docker-guarded + tart-guarded tests (mirroring the existing container/vm guarded tests) assert the agent's PTY round-trips through `-it`/`-t`.
- **§6.2 ctty** keeps its static guard; add one asserting the embedded path still does the foreground handoff.
- Same-uid `LOCAL_PEERCRED` peer-auth (SP7a) already covers `Attach` — cross-uid `Attach` is rejected at accept.

---

## 10. Open decisions / deferred

1. **`slop serve` lifecycle:** app launches it on demand (recommended) vs a user-managed daemon vs a launchd agent. v1: launch-on-demand from the app (start if `Ping` fails). Codesign-identity peer-auth (beyond uid) remains the SP7a-flagged follow-on.
2. **Scrollback ownership:** `SwiftTerm` keeps scrollback (app-side); the engine streams live bytes only, no replay buffer. (Revisit if reconnect-after-drop is wanted.)
3. **Panels** (tracking/network/…): deferred; chrome reserves regions, control plane can add streaming status RPCs later.
4. **One window = one session** in v1; tabs (multiple sessions per window) deferred — the engine already supports N sessions, so it's purely an app-side addition.
5. **External-terminal opt-out granularity:** global config vs per-profile vs per-launch — leaning per-launch override on top of a config default.

---

## Appendix A — app-side memory / concurrency tooling (Swift track)

Reference for the Xcode work (§5, §7). **There is no useful `valgrind` on modern macOS** (effectively unsupported on Apple Silicon, and noisy under ARC anyway). Swift is **ARC, not GC**, so "leaks" are almost always **retain cycles**, and the native toolset covers more than valgrind would:

- **Memory errors** (use-after-free, overflow, double-free) — **AddressSanitizer** is the direct valgrind-memcheck analog: Xcode scheme → Diagnostics → Address Sanitizer, or `swift build -Xswiftc -sanitize=address`. **UndefinedBehaviorSanitizer** (`-sanitize=undefined`) for the C-interop edges (SwiftTerm's PTY/byte handling, the gRPC framing).
- **Data races** — **ThreadSanitizer** (`-sanitize=thread`). This is the high-value one here: the `Attach` stream is read on a background task and must hand bytes to the `@MainActor` SwiftTerm view; TSan catches the seams. Run it while wiring the stream, not after.
- **Leaks / retain cycles** — the **Memory Graph Debugger** (the cube icon while paused) is *the* tool; nothing in the valgrind world matches it for SwiftUI closure/delegate cycles. Plus **Instruments → Leaks / Allocations** (generational: mark heap → act → mark → see survivors), and the scriptable **`leaks <pid>`** CLI (set `MallocStackLogging=1` for backtraces; `NSZombie`/`MallocScribble` for UAF on the AppKit/CF objects SwiftTerm bridges).

**Cockpit-specific check:** when a window closes (`CloseSession`), confirm the client-side `Attach` task **and** the SwiftTerm view actually deallocate (Memory Graph Debugger) — a leaked stream subscription is the app-side mirror of the engine-side teardown leak fixed in SP7c-2 (the engine reaps its pump goroutine + container/VM; the app must reap its subscription + view).
