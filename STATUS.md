# Project Status

**Date**: 2026-06-26

### 1. Repository & Branch State
* **Branch**: `main` is clean.
* **Code Health**: `make check` and `make build` pass cleanly. The full test suite—including Go engine/CLI tests, the pivot denylist, and the 18 Emacs ERT tests—is green.

### 2. The specs/0049 Pivot is Complete
The repository has successfully pivoted away from the Swift macOS cockpit and OpenCode towards a focused Emacs + Go architecture:
* **Purged**: The `app/` Swift cockpit, Go control plane UI, gRPC synchronization, and all OpenCode/VS Code policy configurations.
* **Retained Agents**: Specifically narrowed to `claude` (Claude Code) and `pi`. A `claude-code` alias has been added for UX convenience but maps canonically to `claude`.
* **Cross-Language Contract**: A stable, version-1 JSON contract (`internal/jsoncontract`) now brokers all communication between the Go CLI and Emacs. Both Go and Emacs test suites validate against the exact same shared golden JSON fixtures to prevent drift.

### 3. Emacs Frontend Capabilities
The foundational `safeslop` Emacs package is shipped (`~/.local/share/safeslop/emacs`) and operational:
* **Core Commands**: `safeslop-doctor`, `safeslop-policy-check-file`, and session lifecycle commands (`new`, `attach`, `status`, `list`, `stop`).
* **Session UX**:
  * PTY attachment runs through exactly-routed argv into the built-in `term-mode`.
  * Fallbacks to read-only `compilation-mode` (consuming `jsonl` streams) when PTY isn't available.
* **Doom/Evil Support**: Transparently loads Doom extensions (`safeslop-doom.el`) and Evil normal-state bindings for output buffers when available.
* **Hermetic Testing**: Emacs ERT tests run offline against a fake CLI, proving contract parsing and shell-injection guardrails without executing the real binary.

### 4. Session Lifecycle Engine
The Go side now natively manages durable agent sessions (`internal/engine/session`):
* Agents are isolated by safe defaults (`environment: sandbox`, `network: deny`).
* Emits state changes via the V1 JSON envelope.
* **Security invariant enforced**: Calling `safeslop session stop` will cleanly and idempotently revoke any staged credentials (like SSH/Forgejo tokens) before killing the agent process.

### 5. Open Blockers / Known Debt
* **Emacs 32.1 Pinned CI**: The strictly pinned test builder `ci/emacs32/build-emacs.sh` is wired up but currently configured to **fail-closed** because Emacs 32.1 is not yet generally available. It contains a sentinel SHA (`PENDING_32_1_GA_...`) and will refuse to build mutable upstream candidates. We are waiting on GNU to publish the tarball.

### 6. What's Next (Uncharted Territory)
With the UX, CLI surface, and state machine built, the immediate engine boundary is reached:
* **Session Runtime (`specs/0050`, landed)**: `safeslop session run` is a real execution plane, not scaffolding — it execs the agent under its declared boundary (sandbox/container/VM), reconciles a running-but-dead session back to `stopped`, tears the boundary down on `SIGTERM`/`SIGHUP` (so `session stop` and buffer-close don't orphan a container/VM holding staged secrets), and emits the `PTY_UNAVAILABLE` contract error with a JSONL status fallback when there is no usable controlling terminal.
* **Detached supervisor (`specs/0051`, landed)**: `session run --detach` gives a session a life independent of the Emacs buffer that started it. A per-session supervisor (re-exec'd `session supervise`, never a central daemon) owns the agent + its single PTY and serves it over a per-session unix socket; `session attach` rejoins the running agent over that socket and exits with its code (one active attach at a time). `session stop` signals the supervisor's whole process group (graceful `SIGTERM`, then `SIGKILL`), and reconcile sweeps a dead supervisor's stale socket. `data.session`'s `socket` field returns — derived from the state root and stat-gated, so only a socket that is really there is advertised. Trade-off: a detached agent holds its staged secrets for its whole life (documented; `stop --revoke-credentials` still revokes before the kill).