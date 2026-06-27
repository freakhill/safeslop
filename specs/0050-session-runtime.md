# 0050 — Session runtime execution plane

## Goal

Make `safeslop session run` an **honest, correct, and secure** execution plane:
the agent runs under its declared isolation boundary, the recorded session
state reflects reality, `session stop` actually tears the boundary down (no
orphaned container/VM holding staged secrets), and the PTY presented to Emacs
behaves uniformly across all four boundaries — with a wired `PTY_UNAVAILABLE`
fallback to the JSONL status monitor.

This spec also designs (but defers) the detached-supervisor model that gives
sessions a life independent of the Emacs buffer that started them.

## Reality check (correct the record)

The handoff and `STATUS.md` §6 describe `session run` as "just scaffolding"
that "does not actually exec the agent." **That is overstated.** Verified
against live code on `main`:

- `cmdSessionRun` (`internal/cli/cli.go:469-504`) loads the session, builds a
  `policy.Profile` from it, resolves `agentArgv`, calls `MarkRunning`, then
  calls `runProfile(...)` and `Finish(...)`.
- `runProfile` (`internal/cli/cli.go:984-1042`) stages credentials, seeds agent
  defaults, snapshots the git exec surface, and **execs the agent under the
  declared boundary**: `sandbox.Launch` / `engexec.RunInTerminal` (host) /
  `container.Launch` / `vm.Launch`.
- The boundary launchers are real: sandbox wraps `sandbox-exec -f <profile>`
  (`internal/engine/sandbox/sandbox.go:255`), container runs `compose run`
  under a PTY (`internal/engine/container/launch.go:196` → `exec.RunInPTY`), VM
  runs `ssh -t` into a disposable clone (`internal/engine/vm`).

So the *exec* already happens. What is missing is everything that makes it a
*session runtime* rather than a one-shot foreground launch. The gaps below are
the real scope of 0050.

## Verified gaps

1. **PID identifies the wrong process.** `MarkRunning(id, os.Getpid(), ...)`
   (`internal/cli/cli.go:486`) records the PID of the `safeslop session run`
   wrapper, not the agent and not the boundary process. The agent is a
   grandchild (sandbox-exec → claude), a `docker compose run` child tree, or a
   remote process behind an `ssh` client. Consumers of `session.PID` are
   therefore lied to.

2. **`stop` orphans the boundary and leaks staged secrets.**
   `sessionKillProcess` (`internal/cli/cli.go:333-342`) sends `SIGTERM` to the
   recorded PID — the wrapper. The wrapper's deferred teardown in `runProfile`
   (`os.RemoveAll(stageDir)`, `creds.RevokeSSH/Forgejo`, `Down()`/`Destroy()`)
   runs on normal return, **not on SIGTERM**. Result:
   - container: `compose run` is not in the wrapper's signalled set; the
     container keeps running with `secrets.env` mounted at `/safeslop/runtime`.
   - vm: the disposable clone is not destroyed; `secrets.env` scp'd into the
     guest persists on a live VM.
   This contradicts the project's honest-isolation and short-lived-secret
   invariants. `Store.Stop` does call `sessionRevokeCredentials`
   (`internal/cli/cli.go:326-331`), which re-derives the stage dir and revokes
   SSH/Forgejo deploy keys — good — but the *running agent* and its mounted
   secrets survive the revoke.

3. **No liveness reconciliation.** If the wrapper dies (crash, `kill -9`,
   laptop sleep killing the SSH session, Emacs killed), `Finish` never runs and
   the session JSON stays `status: "running"` forever. `session status`
   (`internal/cli/cli.go:410-439`) reports the stale value; nothing checks
   whether the process is actually alive.

4. **PTY model differs per boundary and nests.** Under Emacs `make-term` (which
   gives the child a PTY), the boundaries diverge:
   - host/sandbox: `RunInTerminal` does no PTY of its own; the agent inherits
     make-term's PTY directly (one hop — correct).
   - container: `RunInPTY` allocates a *second* PTY inside make-term's PTY
     (`exec.RunInPTY` does `MakeRaw` + SIGWINCH forwarding on its stdin), so
     resize crosses two PTY hops.
   - vm: `ssh -t` requests a remote PTY sized from the local terminal.
   There is no single statement of what `session run` guarantees to its caller,
   and no defined behaviour when `session run` is invoked **without** a
   controlling terminal.

5. **`PTY_UNAVAILABLE` is declared but never emitted.** The contract defines
   `CodePTYUnavailable` and the golden fixture `error-pty-unavailable.golden.json`
   carries `details.fallback = "status-jsonl"`; Emacs has the matching
   `compilation-mode` JSONL monitor (`emacs/safeslop-session.el`
   `safeslop-session-status-fallback`). But `session run` never detects "no
   usable PTY" and never emits the envelope, so the fallback is dead code on the
   Go side.

6. **The `socket` field is aspirational.** `ok-session-create.golden.json`
   includes `data.session.socket`, and Emacs carries dormant daemon-autostart
   scaffolding (`emacs/safeslop.el`, `safeslop-daemon-args '("serve")`, whose
   own docstring says "Current safeslop releases may not ship a daemon … until
   safeslop grows a checked-in daemon"). But `sessionData`
   (`internal/cli/cli.go:518`) emits no `socket`, and there is **no daemon, no
   `serve` subcommand, and no socket listener anywhere in the Go tree**. The
   fixture and the runtime disagree; one of them must move.

## Locked decisions

### D1 — Coupled lifecycle now; detached supervisor designed but deferred

`session run` stays **foreground and coupled**: the agent's lifetime equals the
run process's lifetime, which equals the Emacs `term` buffer. We do **not**
introduce a central always-on daemon. Rationale:

- An always-on `safeslop serve` is standing attack surface and a credential
  custodian that outlives every session — the opposite of the short-lived,
  honest-isolation ethos. The dormant Emacs daemon scaffolding stays dormant
  and optional.
- The coupled model is what exists and what Emacs already drives via
  `make-term`. The high-value work is making it *correct and honest*, not
  rebuilding the topology.

Detach/reattach (an agent that survives closing its Emacs buffer) is real value
but a larger lift. It is designed in "Stage 2 (deferred)" below so Stage 1's
interfaces don't foreclose it, and is split into its own follow-up spec.

### D2 — `session run` owns a process group and tears the boundary down

The run wrapper launches the boundary process in its **own process group**
(`Setpgid`) and installs a signal handler so that `SIGTERM`/`SIGINT` to the
wrapper runs full teardown before exit: kill the process group, then the
boundary-specific teardown (`compose down` / `vm Destroy`), then stage wipe and
credential revoke — the same teardown `runProfile`'s defers do today on normal
return. No boundary or staged secret may survive a stop.

### D3 — `stop` targets the boundary, not a bare PID

`session stop` signals the run wrapper's process group and waits (bounded) for
it to confirm teardown, rather than blindly `SIGTERM`-ing one PID. Credentials
are still revoked **before** the kill (existing order in `Store.Stop`,
`internal/engine/session/session.go:155-189`), and the operation stays
idempotent. The recorded PID becomes the wrapper's PGID-leader so the signal is
unambiguous.

### D4 — One PTY contract for all boundaries

`session run`'s job is to hand the agent **exactly one** correctly-sized,
raw-mode, signal-forwarding controlling PTY, regardless of boundary, and to
propagate the agent's exit code verbatim. When `session run`'s own stdout is
already a terminal (the Emacs `make-term` case), host/sandbox keep inheriting it
directly and we do **not** add a second PTY. The container path's inner
`RunInPTY` is only used when the wrapper is *not* already on a terminal; the
nesting case (terminal-in, container) reuses the inherited terminal instead of
allocating a second one. (Implementation detail proven by test, not by
inspection — see PR3.)

### D5 — Reconcile the contract with reality

Drop `socket` from `ok-session-create.golden.json` (and the Emacs parse
expectations) for v1, since no socket is emitted. The field returns only if and
when Stage 2 ships a per-session socket. This keeps the "one source of truth,
Go and Emacs parse the same fixtures" invariant honest.

## PR sequence

Each PR is independently shippable, TDD-first, and hermetic (no live forge,
container daemon, or VM in unit tests — fake the boundary via the existing
`engexec`/runner seams and a stub agent binary).

### PR1 — Honest liveness (implemented)

Purpose:

- Add liveness reconciliation: a `running` session whose recorded PID is no
  longer alive is reported (and persisted once) as `stopped` with
  `last_error: "run process exited without recording status"`. A pure
  `reconcile(sess, now, isAlive)` helper, unit-testable with an injected
  `isAlive func(int) bool`, plus `Store.GetReconciled`/`ListReconciled` wrappers
  that persist the correction, and a default `ProcessAlive` probe (signal 0).
- `session status` and `session list` report the reconciled state
  (`sessionProcessAlive`, overridable in tests).

Decomposition refinement: PR1 anchors liveness on the **run-wrapper PID** that
`MarkRunning` already records — in the coupled lifecycle (D1) the wrapper holds
the agent for the whole run, so a dead wrapper PID faithfully means the run
ended. Surfacing the *boundary* process PID requires an `onStart` callback
through the blocking launchers, which is the same plumbing PR2 needs for
`Setpgid`/group-kill — so boundary-PID recording moves to PR2, where it belongs.
PID reuse is a known, accepted limitation of the wrapper-PID anchor.

Files:

- `internal/engine/session/session.go`
- `internal/engine/session/session_test.go`
- `internal/cli/cli.go`
- `internal/cli/cli_session_test.go`

Required tests:

- `TestReconcileMarksDeadRunningSessionStopped`
- `TestReconcileLeavesLiveSessionRunning`
- `TestReconcileIsIdempotentOnStopped`
- `TestGetReconciledPersistsDeadTransition`
- `TestListReconciledCorrectsDeadSessions`
- `TestSessionStatusReportsReconciledState`

### PR2 — Boundary teardown on signal (implemented)

Closes gap #2: `session stop` sends `SIGTERM` to the run wrapper, but the
wrapper used `context.Background()` (never cancelled), so the signal killed the
wrapper with its defers unrun — leaving a live VM clone / container with
`secrets.env` still staged.

Implemented design (chosen over the original group-signal-from-stop sketch
because it is smaller, needs no tty/process-group surgery on the interactive
path, and keeps the wrapper PID as the single control point — see below):

- `runProfile` wraps the run in `signal.NotifyContext(ctx, SIGTERM, SIGHUP)` and
  delegates to `runProfileCtx(ctx, …)`. An explicit `SIGTERM` (`session stop`)
  or `SIGHUP` (terminal / Emacs-buffer close) **cancels the run context**. The
  cancellation propagates to the boundary launchers' `exec.CommandContext`,
  which kills the child; the launcher returns normally, so the existing deferred
  teardown runs: `vm.Launch`'s `defer Destroy`, container `--rm`, the stage-dir
  wipe, and SSH/Forgejo credential revoke.
- `SIGINT` is deliberately **not** caught: `runProfile` is shared with
  `safeslop run`, and interactive Ctrl-C must reach the agent, not tear the
  session down. `SIGKILL` is uncatchable — PR1's liveness reconcile is the
  backstop for a wrapper that dies without cleaning up.

Why no boundary PID / `Setpgid` / `stop`-signals-the-group:

- The wrapper PID stays the correct anchor (liveness, PR1) and target (`stop`):
  the wrapper now self-tears-down on signal, so nothing needs to reach into the
  boundary process tree from `stop`.
- The container (PTY) path already cascades teardown to the child's group: a
  `pty.Start` child is a session leader, so when it is killed the kernel
  `SIGHUP`s the pty's foreground group — no explicit `Setpgid`/group-kill
  needed. `TestRunInPTYCancelTearsDownProcessGroup` guards this property.
- Adding `Setpgid` to the host/sandbox `RunInTerminal` path would background the
  child relative to the controlling tty and risk `SIGTTIN`/`SIGTTOU` stops — a
  real interactive regression we avoid.

Files:

- `internal/cli/cli.go` (the `runProfile` → `runProfileCtx` split + signal wiring)
- `internal/cli/cli_runprofile_test.go`
- `internal/engine/exec/exec_test.go` (regression guard for the relied-upon
  cancel→PTY-group teardown; `exec.go` itself needs no change)

Required tests:

- `TestRunProfileCtxTeardownOnCancel` (host env + stub agent: cancel kills the
  agent and runs the deferred teardown)
- `TestRunInPTYCancelTearsDownProcessGroup` (cancel kills the whole pty session,
  grandchild included)

Deferred (need a live runtime, so not unit-tested here): asserting that a
cancelled container run actually `compose down`s the agent container, and that a
cancelled vm run leaves no tart clone — covered by the existing `defer Destroy`
/ `--rm` and the integration suite, not new hermetic tests. Surfacing the
boundary PID via `onStart` is no longer needed and is dropped.

### PR3 — Uniform PTY contract and exit-code fidelity

Purpose:

- Define and enforce the D4 contract: when the wrapper's stdout is a terminal,
  host/sandbox inherit it; container does not allocate a redundant second PTY;
  vm `ssh -t` continues to size from the inherited terminal. When the wrapper's
  stdout is **not** a terminal, allocate a PTY where the boundary needs one.
- Guarantee verbatim exit-code propagation from agent → wrapper → `Finish`
  across all four boundaries (already mostly true; lock it with tests).
- Forward window-resize (SIGWINCH) end to end with a single PTY hop in the
  common case.

Files:

- `internal/engine/exec/exec.go`
- `internal/engine/exec/exec_test.go`
- `internal/cli/cli.go`
- `internal/engine/sandbox/sandbox_test.go`

Required tests:

- `TestRunInheritsTerminalNoDoublePTY` (stdout is a PTY → no second PTY allocated; assert via the inner runner seam)
- `TestRunAllocatesPTYWhenNotOnTerminal`
- `TestExitCodePropagatesAcrossBoundaries` (table: host/sandbox/container/vm via fakes, codes 0/1/42)
- `TestResizeForwardsToAgent`

### PR4 — `PTY_UNAVAILABLE` detection and JSONL fallback wiring

Purpose:

- When `session run` cannot obtain a usable PTY for a boundary that requires one
  (e.g. stdin/stdout not a tty and no PTY allocatable), emit the contract error
  envelope `PTY_UNAVAILABLE` with `details.fallback = "status-jsonl"` to stdout
  and exit non-zero **without** marking the session running — matching
  `error-pty-unavailable.golden.json` exactly.
- Confirm the Emacs side already switches to `safeslop-session-status-fallback`
  (`--output jsonl`, `compilation-mode`) on that code; add the Go→Emacs
  round-trip to the hermetic ERT harness if not covered.

Files:

- `internal/cli/cli.go`
- `internal/cli/cli_session_test.go`
- `internal/jsoncontract/contract_test.go` (assert run-path emission matches the golden)
- `emacs/test/safeslop-contract-test.el` (fake CLI returns `PTY_UNAVAILABLE`; assert fallback monitor launches)

Required tests:

- `TestSessionRunEmitsPTYUnavailableWhenNoTTY`
- `TestSessionRunDoesNotMarkRunningOnPTYUnavailable`
- `safeslop-test-pty-unavailable-triggers-jsonl-fallback` (ERT)

### PR5 — Contract reconciliation and docs

Purpose:

- Remove the aspirational `socket` field from `ok-session-create.golden.json`
  and any Emacs parse expectation, restoring "Go and Emacs parse the same
  fixtures with no drift."
- Update `README.md` (session command surface, stop semantics, fallback
  behaviour), `STATUS.md` (correct the "just scaffolding" line), and the session
  skill under `skills/` to match the implemented runtime.

Files:

- `internal/jsoncontract/testdata/ok-session-create.golden.json`
- `internal/jsoncontract/contract_test.go`
- `emacs/safeslop-contract.el` / `emacs/test/*` (if they reference `socket`)
- `README.md`
- `STATUS.md`
- `skills/` (session-related skill files)

Required tests:

- `TestGoldenSessionCreateHasNoSocketField`
- existing golden round-trip tests (Go + ERT) stay green

## Stage 2 (deferred) — detached supervisor and reattach

Designed here so Stage 1 doesn't paint us into a corner; **implemented in a
later spec (0051)**, not in 0050.

- `session run --detach`: the wrapper double-forks / `setsid`, drops its
  controlling terminal, keeps the agent + boundary alive, and exposes the
  agent's PTY master over a **per-session** unix socket under the state dir
  (`$SAFESLOP_STATE_DIR/sessions/<id>.sock`) plus a per-session JSONL event log.
- `session attach --session-id <id>`: a thin client that bridges the local
  Emacs `make-term` PTY ↔ the session socket (the elisp keeps using `make-term`;
  only the argv changes from `run` to `attach`).
- This is per-session, not a central daemon — no always-on custodian. It is the
  natural home for re-introducing `data.session.socket` (D5).
- It reuses Stage 1's `teardown` closure, PGID ownership, liveness reconcile,
  and PTY contract wholesale.

## Non-goals

- A central `safeslop serve` daemon / control plane (explicitly dropped in
  0049; not revived here).
- Multiplexing multiple agents into one session.
- Changing the credential staging or boundary-construction mechanisms — 0050
  governs the *process lifecycle and I/O contract* around launches that already
  work.

## Open questions

- **Q1 — stop wait bound.** How long does `session stop` wait for teardown
  confirmation before escalating group `SIGTERM` → `SIGKILL`? Proposal: 5s
  graceful, then `SIGKILL` the group, then force boundary teardown regardless.
- **Q2 — reconcile write-back.** Should `status`/`list` *persist* a reconciled
  `stopped` transition (write the JSON) or report it transiently? Proposal:
  persist, so a dead session is corrected exactly once and credential revoke can
  be triggered for it.
- **Q3 — host/sandbox stop teardown.** Group kill is sufficient for
  host/sandbox (no external resource); confirm there is no residual stage dir
  when the wrapper is `SIGKILL`ed before its handler runs (a `creds gc`-style
  sweep on next `session` invocation may be the honest backstop).
