# Project Status

**Date**: 2026-06-28

### 1. Repository & Branch State
* **Branch**: `main` is clean.
* **Code Health**: `make check` and `make build` pass cleanly. The full suite — Go engine/CLI tests, the pivot denylist, `gofmt`/`go vet`, and the **35 Emacs ERT tests** — is green. Go and Emacs validate against the same **9 shared golden JSON fixtures** (`internal/jsoncontract/testdata/*.golden.json`) to prevent cross-language drift.

### 2. The specs/0049 Pivot is Complete
The repository has successfully pivoted away from the Swift macOS cockpit and OpenCode towards a focused Emacs + Go architecture:
* **Purged**: The `app/` Swift cockpit, Go control plane UI, gRPC synchronization, and all OpenCode/VS Code policy configurations.
* **Retained Agents**: Specifically narrowed to `claude` (Claude Code) and `pi`. A `claude-code` alias has been added for UX convenience but maps canonically to `claude`.
* **Cross-Language Contract**: A stable, version-1 JSON contract (`internal/jsoncontract`) now brokers all communication between the Go CLI and Emacs. Both Go and Emacs test suites validate against the exact same shared golden JSON fixtures to prevent drift.

### 3. Emacs Frontend Capabilities
The `safeslop` Emacs package (`~/.local/share/safeslop/emacs`) is the primary operator surface:
* **Portal** (`M-x safeslop`, alias `safeslop-portal`, `C-c s P`): a `tabulated-list` session dashboard — id, agent, environment, network, colour-coded status, PID, age, workspace — with in-place actions (open/attach, reattach, status, stop, new, refresh). It opens full-window with a slopmaxx-style in-buffer shortcut legend and re-fetches the session list in place.
* **Debug buffer** (`C-c s L`): `*safeslop debug*`, a redacted, timestamped client diagnostics log — one line per CLI call and its result, allowlisted non-secret fields only (safeslop never passes secret values as argv).
* **Daemonless command path**: commands run as direct subprocesses. The legacy daemon-autostart machinery has been removed, so there is no daemon round-trip, no stale-socket guesswork, and no misleading "no daemon binary" message.
* **Core Commands**: `safeslop-doctor`, `safeslop-policy-check-file`, and the full session lifecycle (`new`, `attach`, `status`, `list`, `stop`, `reattach`), each rendering the envelope's full `data` payload (not just `ok:`). Non-JSON output from a stale binary degrades to a clear `CLIENT_NON_JSON` message instead of crashing.
* **Session UX**: PTY attachment runs through exactly-routed argv into the built-in `term-mode`; falls back to a read-only `compilation-mode` JSONL monitor when no PTY is available; reattach rejoins a detached supervisor over its per-session socket.
* **Doom/Evil Support**: optional `safeslop-doom.el` binds the command map under `C-c s` and Doom's leader at `SPC o s` (a deliberate override of the `:os macos` prefix), with Evil normal-state bindings for the portal and output buffers.
* **Hermetic Testing**: ERT runs offline against a fake CLI, proving contract parsing and shell-injection guardrails without executing the real binary.

### 4. Session Lifecycle Engine
The Go side natively manages durable agent sessions (`internal/engine/session`):
* Agents are isolated by safe defaults (`environment: sandbox`, `network: deny`) and emit state changes via the V1 JSON envelope.
* **Session runtime (`specs/0050`, landed)**: `safeslop session run` is a real execution plane — it execs the agent under its declared boundary (sandbox/container/VM), reconciles a running-but-dead session back to `stopped`, tears the boundary down on `SIGTERM`/`SIGHUP` (so `session stop` and buffer-close never orphan a container/VM holding staged secrets), and emits `PTY_UNAVAILABLE` with a JSONL status fallback when there is no usable controlling terminal.
* **Detached supervisor (`specs/0051`, landed)**: `session run --detach` gives a session a life independent of the Emacs buffer that started it. A per-session supervisor (re-exec'd `session supervise`, never a central daemon) owns the agent + its single PTY and serves it over a per-session unix socket; `session attach` rejoins and exits with the agent's code (one active attach at a time). `session stop` signals the supervisor's whole process group (graceful `SIGTERM`, then `SIGKILL`); reconcile sweeps a dead supervisor's stale socket. Host, sandbox, container, and VM detached agents all receive a controlling terminal, and the per-session JSONL event log is byte-capped. `data.socket` is stat-gated, so only a socket that is really there is advertised.
* **VM SSH key JIT (`specs/0051` follow-up, landed)**: the disposable-VM tier reads its SSH key just-in-time from 1Password (`SAFESLOP_VM_SSH_KEY_OP`) into a transient `0600` file, never staged alongside the scp'd payload or written to a durable path.
* **Security invariant enforced**: `session stop` cleanly and idempotently revokes any staged credentials (SSH/Forgejo tokens) before killing the agent. Trade-off: a detached agent holds its staged secrets for its whole life (documented); `stop --revoke-credentials` still revokes before the kill.

### 5. Open Blockers / Known Debt
* **Emacs 32.1 Pinned CI**: the strictly pinned test builder `ci/emacs32/build-emacs.sh` is wired up but **fail-closed** because Emacs 32.1 is not yet generally available. It still carries the sentinel SHA (`PENDING_32_1_GA_REPLACE_WITH_REAL_SHA256`) and refuses to build mutable upstream candidates. Local development floors at 32.0 (`EMACS_MIN`). We are waiting on GNU to publish the tarball.

### 6. What's Next
The pivot, CLI surface, session state machine, detached supervisor, and Emacs operator portal are all in place. Near-term:
* **Portal polish in flight**: live auto-refresh on a timer (PR #70). Candidate follow-up: a `RET`-to-detail per-session view (full status, socket/log paths, recent log tail).
* **Emacs 32.1 GA**: replace the CI sentinel SHA once GNU publishes the tarball, flipping the pinned builder from fail-closed to enforcing.
