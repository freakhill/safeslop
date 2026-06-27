# 0051 — Detached supervisor and reattach (Stage 2)

## Goal

Give a session a life **independent of the Emacs buffer that started it**: start
an agent, close the buffer (or Emacs, or the laptop lid), and reconnect later to
the same running agent — without ever introducing a central always-on daemon.

This is the "Stage 2 (deferred)" design from `specs/0050-session-runtime.md`
(§"Stage 2 (deferred) — detached supervisor and reattach"), now promoted to its
own implementable spec. It builds **on top of** the Stage 1 runtime (0050,
merged): liveness reconcile, signal teardown, the `runProfileCtx` teardown
closure, the one-PTY contract, and the `PTY_UNAVAILABLE` fallback are all reused
unchanged. Stage 1's coupled `session run` is **not** removed — detach is opt-in.

## Reality check (ground against live code on `main`)

Verified against the merged 0050 series, not the handoff summary:

- `cmdSessionRun` (`internal/cli/cli.go:476-524`) is **foreground and coupled**:
  it `MarkRunning(id, os.Getpid(), …)` with the *wrapper* PID, calls
  `runProfile(...)` (which blocks for the agent's whole life), `Finish(...)`, then
  `os.Exit(code)`. The agent's lifetime equals this process equals the Emacs
  `make-term` buffer. Closing the buffer sends `SIGHUP`, which `runProfile`'s
  `signal.NotifyContext` (`cli.go:1025`) turns into a context cancel → teardown →
  agent dies. **There is no way to keep the agent alive past the buffer.**
- `runProfileCtx` (`cli.go:1030-1086`) is the reusable core: it stages creds,
  seeds defaults, snapshots the git exec surface, and dispatches to the boundary
  launcher, with **all teardown deferred** (`os.RemoveAll(stageDir)`,
  `creds.RevokeSSH/Forgejo`, the launchers' own `defer Destroy`/`--rm`). Stage 2's
  supervisor calls this same function; the teardown closure is inherited for free.
- The PTY plumbing already exists: `exec.RunInPTY` (`internal/engine/exec/exec.go:67`)
  allocates a host-side PTY for a child, proxies `stdin/stdout`, does `MakeRaw` on a
  real local terminal, and forwards `SIGWINCH` via `pty.InheritSize`. This is
  exactly the master-side machinery the supervisor needs — for **every** boundary,
  not just container.
- `sessionStore()` (`cli.go:536-546`) derives the state root from
  `SAFESLOP_STATE_DIR` (or `os.UserConfigDir()/safeslop`) and sessions live at
  `<root>/sessions/<id>.json`. The per-session socket has an obvious home next to
  it: `<root>/sessions/<id>.sock`.
- `reconcile` (`internal/engine/session/session.go:181-191`) flips a `running`
  session whose recorded PID is dead to `stopped`. It keys purely on
  `sess.PID` + `isAlive`; once the recorded PID is the **supervisor's**, it works
  unchanged as the detached-liveness backstop.
- The error registry (`internal/jsoncontract/errors.go`) is append-only and
  already declares `CodeSessionAlreadyRunning` and `CodeSessionCancelled`
  (currently unused) — both land naturally in Stage 2.
- D5 of 0050 removed `data.session.socket` from the create golden with: "*socket
  returns only if and when Stage 2 ships a per-session socket.*" This spec is
  where it returns.

## What Stage 2 adds (scope)

Three cooperating processes plus a per-session socket:

1. **`session run --detach`** — the launcher. Short-lived. Re-execs itself as a
   detached supervisor, records the **supervisor** PID, waits (bounded) for the
   socket to appear, prints the session envelope (now carrying `socket`), and
   returns. The issuing Emacs buffer is immediately free.
2. **`session supervise --session-id <id>`** — the supervisor. Long-lived,
   **hidden** subcommand (re-exec target, not user-facing). Owns the agent + its
   PTY + the boundary, serves the PTY over the per-session unix socket, tees a
   per-session JSONL event log, and runs Stage 1 teardown when the agent exits.
3. **`session attach --session-id <id>`** — the reattach client. Foreground, one
   per attach. Bridges the local terminal (Emacs `make-term` PTY) ↔ the session
   socket, forwards window-size changes, and exits with the agent's code.

Coupled `session run` (no `--detach`) is unchanged.

## Locked decisions

### D1 — Per-session supervisor via re-exec, never a central daemon

Detach spawns **one supervisor per session**, not a shared `safeslop serve`. This
preserves 0050's D1 rationale (no always-on credential custodian, no standing
attack surface): the supervisor lives exactly as long as its one agent, holds
only that session's staged secrets, and dies (with full teardown) when the agent
exits or `stop` arrives.

Go cannot `fork()` safely (the runtime owns threads), so daemonization is a
**re-exec**: `session run --detach` launches `os.Args[0] session supervise
--session-id <id>` via `exec.Cmd` with `SysProcAttr{Setsid: true}` (new session,
no controlling tty) and `Setpgid` (own process group — see D4), detaches the
child's stdio (`stdin` ← `/dev/null`, `stdout`/`stderr` → the per-session JSONL
log), and `Start()`s without waiting. The launcher records the child PID and
returns. This is the canonical Go daemon pattern; `supervise` is marked hidden so
it never appears in help.

**Test seam:** the re-exec target is an overridable package var
(`launchSupervisor func(id string) (int, error)`) defaulting to the real
self-re-exec. Hermetic tests replace it (or call `Supervise(...)` in-process in a
goroutine) so no second binary or `setsid` is needed under `go test`.

### D2 — One controlling PTY for the agent, served over the socket (extends 0050 D4)

The supervisor allocates **exactly one** PTY and binds the agent's stdio to its
slave — on **all four boundaries**, because a detached supervisor has no inherited
terminal to pass through. This generalises 0050's "container gets an intermediate
`RunInPTY` PTY for a uniform TTY even when the wrapper has none" to every
boundary; host/sandbox detach now goes through the same PTY path rather than
inheriting stdio. The agent still sees exactly one controlling terminal (its
own), satisfying 0050 D4.

The attach client owns a *second* PTY locally (Emacs `make-term`), joined to the
supervisor's master by a raw byte bridge over the socket. Two PTYs in the chain,
one controlling terminal for the agent — the same shape as container in Stage 1,
now spanning a socket. Exit code and window size cross the socket as control
frames (D3).

### D3 — Minimal length-prefixed frame protocol on the socket

The socket carries a tiny framed protocol, not raw bytes, so data, resize, and
exit are unambiguous and the supervisor can tee output to JSONL:

- `D <len> <bytes>` — PTY data, both directions.
- `R <rows> <cols>` — resize, client → supervisor (applied via `pty.Setsize`).
- `X <code>` — agent exit, supervisor → client (the client exits with `code`).

Rejected alternative — **passing the PTY master fd over `SCM_RIGHTS`** so the
client owns the PTY directly: zero-copy and elegant, but it cannot tee to the
JSONL log, cannot support reconnect or future multi-viewer, and complicates
exit-code delivery (the supervisor would have to signal exit out of band). The
byte-proxy is simpler and strictly more capable; we accept its copy cost (PTY
throughput is human-interactive, not a bottleneck).

### D4 — Supervisor owns a process group; `stop` signals the group

The supervisor is a process-group leader (`Setpgid` at re-exec, D1). `session
stop` for a detached session signals the **group** (`kill(-pgid, …)`), not a bare
PID, so the boundary process tree (sandbox-exec → agent, `compose run` tree, the
`ssh` client) is reached. This is finally the D2/D3 process-group ownership that
0050 *designed but deferred* — Stage 1's coupled model didn't need it (the wrapper
self-tore-down on signal); the detached model does, because nothing else holds
the tree.

Stop escalates: group `SIGTERM`, wait bounded (D6/Q-from-0050: 5s), then group
`SIGKILL`, then force boundary teardown and remove the socket regardless. Credit
revoke still runs **before** the kill (existing `Store.Stop` order,
`session.go:243-249`). `sessionKillProcess` learns a detached path that targets
the negative PGID; the coupled path is unchanged.

### D5 — `data.session.socket` returns, derived and stat-gated

`sessionData` (`cli.go:548`) re-adds `socket` **only** when the session is
`running` *and* the socket file exists on disk. The path is **derived** from the
same state root the supervisor binds (`<root>/sessions/<id>.sock`), not persisted
in the session JSON — so there is no new schema field to keep in sync and no
stale-path footgun: we only ever advertise a socket that is actually there. A
coupled (non-detached) running session has no socket and the field stays absent,
exactly as today.

This restores 0050 D5's deferred field and keeps the "Go and Emacs parse the same
golden fixtures" invariant honest via a new `ok-session-detached.golden.json`
pinned byte-exact to the real emission.

### D6 — Detach extends staged-secret lifetime to the agent's life (documented, not hidden)

A detached agent holds its staged secrets (`secrets.env`, deploy keys, kubeconfig)
for its whole — possibly long — life, where coupled runs were bounded by the
buffer. This is **by design** (the agent needs them to work) but is a real
weakening of the short-lived-secret posture, so it is documented in `README.md`
and surfaced in the detach envelope. `stop --revoke-credentials` still revokes
before kill; liveness reconcile + the stale-resource sweep (D7) bound the leak if
the supervisor dies uncleanly.

### D7 — Stale-resource sweep backstops an uncleanly-dead supervisor

If the supervisor dies without running teardown (`SIGKILL`, OOM, power loss),
reconcile already flips the session to `stopped` (0050 PR1). Stage 2 adds, to the
same reconcile path, removal of the orphaned `<id>.sock`, and a best-effort
`creds gc`-style note that the stage dir may persist (the honest backstop raised
as 0050 Q3). The boundary resource itself (a leaked container/VM) is the same
residual concern 0050 left to the integration suite + `--rm`/`defer Destroy`; not
unit-tested here.

### D8 — Single active attach in v1 (resolves Q2 + Q3)

At most one client is attached to a session's socket at a time. The supervisor's
accept loop serves one connection; a second `session attach` while one is live is
rejected with `SESSION_ALREADY_RUNNING` (an existing code) until the first
disconnects — socket EOF frees the writer slot. This keeps input authority
unambiguous (one keyboard, no steal/arbitration) and matches the v1 scope.
Broadcast (one writer, N read-only viewers) is a later, purely additive change to
the accept loop and is explicitly out of scope (Non-goals); the framed protocol
(D3) and a bounded readiness handshake already leave room for it.

## PR sequence

Each PR is independently shippable, TDD-first, and hermetic — no live forge,
container daemon, or VM in unit tests. Fakes: a host-tier stub agent (a tiny
echo/`cat`-style loop, or the 0050 non-existent-`$SHELL` seam for the negative
paths), `net.Pipe`/a tmpdir unix socket for the wire, and the `launchSupervisor`
test seam (D1) for the re-exec.

### PR1 — Socket wire protocol + PTY⇄conn bridge (the risky IO core, first)

Purpose: the framed protocol (D3) and a `Bridge` that proxies a PTY master ↔ a
`net.Conn`, applying resize frames and emitting an exit frame. Pure and fully
hermetic — no daemon, no boundary. Highest-risk IO, so it leads and gets the most
rigor.

Files:

- `internal/engine/session/wire/wire.go` (frame encode/decode: `D`/`R`/`X`)
- `internal/engine/session/wire/wire_test.go`
- `internal/engine/session/bridge.go` (`Bridge(conn, ptmx, onResize)` +
  client-side `Attach(conn, localPTY)`)
- `internal/engine/session/bridge_test.go`

Required tests:

- `TestFrameRoundTrip` (table: `D`/`R`/`X` encode→decode, incl. empty + large data)
- `TestFrameDecodeRejectsTruncated` / `TestFrameDecodeRejectsUnknownType`
- `TestBridgeProxiesBytesBothWays` (`net.Pipe` + a `pty` pair + a stub; bytes flow
  master⇄conn)
- `TestBridgeAppliesResizeFrame` (an `R` frame calls `pty.Setsize`/the injected
  resizer)
- `TestBridgeEmitsExitFrameWithCode` (child exits 42 → an `X 42` frame is written)

### PR2 — Supervisor: PTY + boundary + socket lifecycle (in-process testable)

Purpose: `Supervise(ctx, id, store, …)` allocates the agent PTY, runs
`runProfileCtx` with stdio bound to the PTY slave, binds `<root>/sessions/<id>.sock`,
accepts a client and bridges (PR1), tees a JSONL event log, and on agent exit runs
the inherited teardown, removes the socket, and `Finish`es with the real code.
Records the supervisor PID and owns its PGID.

Files:

- `internal/cli/supervise.go` (`Supervise`, the socket listener, JSONL tee)
- `internal/cli/cli.go` (`runProfileCtx` gains an optional `Stdio`/PTY-slave
  binding so host/sandbox can run under a supervisor PTY — additive, coupled path
  unchanged)
- `internal/cli/supervise_test.go`

Required tests:

- `TestSuperviseRunsAgentAndServesSocket` (host stub agent; a test client connects
  over the real tmpdir socket and reads the agent's output)
- `TestSuperviseExitRunsTeardownAndRemovesSocket` (agent exits → stage dir wiped,
  socket gone, `Finish` recorded with the agent's code)
- `TestSuperviseRecordsSupervisorPIDAlive` (the recorded PID is the supervisor and
  is alive while the agent runs)

### PR3 — `run --detach` re-exec daemonization + readiness

Purpose: the launcher. `session run --detach` re-execs the hidden `session
supervise` with `Setsid`+`Setpgid`+detached stdio (D1), records the supervisor
PID via `MarkRunning`, waits (bounded) for the socket to appear so the printed
envelope's `socket` is real, prints the envelope, and returns 0. Wire the hidden
`supervise` subcommand.

Files:

- `internal/cli/cli.go` (`--detach` flag, the `launchSupervisor` seam, readiness
  wait, hidden `cmdSessionSupervise`)
- `internal/cli/cli_session_test.go`

Required tests (via the `launchSupervisor` seam — no real `setsid`/second binary):

- `TestRunDetachRecordsSupervisorPIDAndReturns` (returns promptly; recorded PID is
  the supervisor, not the wrapper; status == `running`)
- `TestRunDetachWaitsForSocketBeforeSuccess` (envelope is printed only after the
  socket exists; on readiness timeout, a contract error, session not left running)
- `TestSuperviseSubcommandHidden` (`supervise` is registered but `Hidden`)

### PR4 — `session attach` client + PTY_UNAVAILABLE + exit-code fidelity + Emacs

Purpose: the reattach client. `session attach` reuses the 0050
`sessionHasInteractivePTY` guard (emits `PTY_UNAVAILABLE` → JSONL fallback when no
local tty), connects to the socket, bridges local stdio ↔ socket (PR1 client
side), forwards `SIGWINCH` as `R` frames, and exits with the code from the `X`
frame. Designed to **return** the code (testable), with `os.Exit` only at the
cobra boundary — dodging the 0050 `os.Exit`-on-success gotcha. Replace the stale
`safeslop-session-restart` placeholder (`emacs/safeslop-session.el:134-137`,
which references a nonexistent "PR5 PTY model") with a real
`safeslop-session-attach`-style reattach keyed on argv `attach`.

Files:

- `internal/cli/cli.go` (`cmdSessionAttach`, exit-code return seam)
- `internal/cli/cli_session_test.go`
- `emacs/safeslop-session.el` (reattach command; argv `run`→`attach`; drop the
  placeholder)
- `emacs/test/safeslop-contract-test.el`

Required tests:

- `TestAttachBridgesIOAndPropagatesExitCode` (attach to a supervised stub session;
  IO round-trips; exit code 42 propagates from the `X` frame)
- `TestAttachWithoutTTYEmitsPTYUnavailable` (no-tty seam → byte-exact
  `error-pty-unavailable.golden.json`, no connect attempt)
- `safeslop-test-reattach-uses-attach-argv` (ERT: the reattach command builds
  `session attach --session-id …` and uses `make-term`)

### PR5 — `data.session.socket` reinstated + group stop + sweep + docs

Purpose: surface the socket (D5), make `stop` target the supervisor group (D4)
with bounded escalation (D6), add the stale-socket sweep to reconcile (D7), and
reconcile docs + specs.

Files:

- `internal/cli/cli.go` (`sessionData` socket field, derive+stat; `sessionKillProcess`
  detached group path + escalation)
- `internal/engine/session/session.go` (reconcile removes the stale socket)
- `internal/jsoncontract/testdata/ok-session-detached.golden.json`
- `internal/jsoncontract/contract_test.go`, `internal/cli/cli_session_test.go`
  (byte-exact golden ↔ emission pin, mirroring 0050 PR5)
- `README.md`, `STATUS.md`, `skills/agent-sandbox-ops/SKILL.md`
- `specs/0051-detached-supervisor.md` (sync to implemented reality, the 0050 habit)

Required tests:

- `TestSessionDataSocketPresentWhenRunningDetached` / `…AbsentWhenCoupled`
- `TestDetachedGoldenMatchesEmittedEnvelope` (byte-exact pin)
- `TestStopSignalsSupervisorGroupAndRemovesSocket` (injected killer sees a negative
  PGID; socket removed)
- `TestReconcileRemovesStaleSocket` (dead supervisor → socket swept on reconcile)

## Non-goals

- A central `safeslop serve` daemon / control plane (dropped in 0049, reaffirmed
  in 0050 D1; not revived).
- Multiplexing several agents into one session.
- **Multiple concurrent attaches** to one session in v1 (D8: single active
  attach; broadcast is a later additive change).
- Changing credential staging or boundary construction — like 0050, this spec
  governs the *process lifecycle and I/O contract*, here extended across a socket.

## Open questions — resolved

- **Q1 — readiness race (resolved, PR3).** `run --detach` polls for the socket up
  to `detachReadyTimeout` (2s); on timeout it kills the half-born supervisor and
  emits an `IO_ERROR` contract error, leaving the session not-running (no phantom).
- **Q2 — attach error code (resolved, follow-up).** Attach now distinguishes the
  dial failure from a bridge failure: a dial that never reaches a live supervisor
  is wrapped in `errSupervisorUnreachable` and reported as the new
  `SESSION_NOT_RUNNING` (added to the append-only registry, mirrored in
  `safeslop-contract.el`); a failure on an already-live bridge keeps
  `SESSION_STOPPED`. `attach` stays a pure client — it never loads the store — so a
  never-created id and a stopped session both honestly read as "not running"
  (distinguishing `SESSION_NOT_FOUND` would need a store lookup and is out of
  scope). `attachFailureContract` is the pinned mapping.
- **Q3 — JSONL event log retention (left for v1).** The per-session `<id>.jsonl`
  is a provisional event log: one JSON line per PTY-output chunk (base64), written
  by the supervisor's continuous reader and removed on teardown. Format and
  cap/rotate are revisited if it bites.

## Implementation notes (as-built, PR1–PR5)

Where the build diverged from the sketch above, recorded here per the 0050
"reconcile the spec in the PR" habit:

- **Bridge signature (PR1, D3).** `Bridge(conn, ptmx, onResize)` gained
  `waitExit func() int` (it supplies the exit code the `X` frame carries) and
  returns `(Outcome, error)` — `ChildExited` vs `ClientGone` — so a consumer can
  tell agent-exit from client-disconnect. `Attach(conn, in, out, resize)` is the
  client side.
- **The supervisor's IO is a hub, not `Bridge` (PR2, D2).** A faithful detached
  supervisor must drain the agent PTY continuously (so it never blocks with no
  client attached), tee the JSONL log, and serve reattach over a swappable current
  connection — none of which `Bridge` (one PTY read bound to one conn) can do. So
  `Supervise` (`internal/cli/supervise.go`) owns a continuous PTY reader that tees
  JSONL + forwards to the attached client, plus a per-attach input pump
  (`Data`→PTY, `Resize`→`pty.Setsize`) and a single-active-attach accept loop (D8).
  `Bridge`/`Attach` remain the lower-level primitives (`Attach` is the PR4 client).
- **One PTY slave for host/sandbox (PR2, D2).** `runProfileCtx` gained an additive
  variadic `runIO`; when set, host/sandbox bind the agent's stdio to the
  supervisor's PTY slave (`RunInTerminal`/`sandbox.Launch` already forward stdio).
  Container/VM keep their own tty (`RunInPTY` / `ssh -t`); their detached PTY
  binding is a noted follow-up.
- **Host controlling terminal (`Setctty`, follow-up).** Binding the PTY slave as
  the agent's *stdio* (PR2) did not make it the agent's *controlling terminal*:
  under the daemon there is no inherited tty, so the host agent could not open
  `/dev/tty`, receive terminal-generated signals, or hang up cleanly. `LaunchSpec`
  gained `ControllingTTY`; the detached host path (`rio.Stdin != nil`) now launches
  the agent with `SysProcAttr{Setsid, Setctty, Ctty: 0}`, making the PTY slave its
  controlling terminal. The coupled path (`rio` zero) is untouched — it inherits
  the user's real terminal and a `TIOCSCTTY` there would steal it. Teardown is not
  regressed: the agent is now a session leader, so killing it on ctx-cancel hangs
  up the terminal and the kernel `SIGHUP`s its foreground group — the same subtree
  teardown `RunInPTY` already relies on (proven by a mirrored test). Sandbox ctty
  is the next bullet; container/VM remain follow-ups.
- **Sandbox controlling terminal (`Setctty`, follow-up).** The sandbox launch path
  (`sandbox.Launch`) already forwards the whole `LaunchSpec` to `RunInTerminal`
  (`inner := spec`), so the only change is `runProfileCtx`'s sandbox branch setting
  `ControllingTTY: rio.Stdin != nil` like host. The `sandbox-exec` child then
  becomes the session leader that owns the PTY, and the agent it execs inherits the
  controlling terminal — the Seatbelt profile already permits the tty ioctls and
  `/dev` reads it needs. Verified end to end through `sandbox.Launch` and through a
  detached sandbox `Supervise` run (real Seatbelt; `/dev/tty` opens). Container/VM
  remain the last detached-PTY follow-up.
- **Daemonization is `Setsid` only (PR3, D1).** `SysProcAttr{Setsid: true}` alone
  makes the supervisor a new session **and** process-group leader (`pgid == pid`) —
  exactly what D4's `kill(-pgid)` needs. `Setsid + Setpgid` together fails
  `fork/exec` with EPERM (a session leader cannot `setpgid`). The detached
  supervisor's stdio is `/dev/null`; the `<id>.jsonl` event log (not the
  supervisor's own stdout) is the observability channel.
- **`MarkRunningDetached` (PR3/PR5, D4).** Both the launcher and `Supervise` mark
  the session running with the supervisor PID and `Detached: true`; `Detached`
  routes `stop` to signal `-pgid`. The double-mark is harmless (same PID, and each
  path is exercised independently).
- **Group stop + sweep (PR5, D4/D6/D7).** `Store.Stop` signals `-pid` for a
  detached session — the real `sessionKillProcess` does group `SIGTERM`, a bounded
  `stopGraceTimeout` (5s) wait, then group `SIGKILL` — and removes the socket;
  reconcile sweeps a dead supervisor's stale socket on the next `status`/`list`.
- **`data.session.socket` (PR5, D5).** `sessionData` advertises `socket` only when
  the session is running and the file exists, derived from the state root (never
  persisted). Pinned byte-exact by `ok-session-detached.golden.json`.
- **`sun_path` overflow hardening (follow-up).** A unix socket path must fit
  `sockaddr_un.sun_path` (104 bytes on macOS, incl. NUL); `net.Listen("unix", …)`
  on a longer path fails with `bind: invalid argument`. The default state root
  keeps `<root>/sessions/<id>.sock` at ~92 bytes, but a long `$SAFESLOP_STATE_DIR`
  (or a deep test temp dir) overflows. `Store.SocketPath(id)` is now the single
  canonical derivation used by the supervisor (bind), the attach client (dial), the
  status surfacing, and the reconcile sweep: it returns the natural in-state-dir
  path when it fits (the common case, unchanged) and otherwise relocates the socket
  to a short, private per-user runtime dir (`$XDG_RUNTIME_DIR`, else the OS temp
  dir) under a name hashed from `(Dir, id)` — deterministic and per-id distinct, so
  every caller agrees without persisting it. The supervisor `chmod`s the bound
  socket to `0600` so a socket relocated under a shared runtime dir stays
  owner-only. This retired the tests' `shortStateDir` necessity (kept only to pin
  the natural-path branch).
