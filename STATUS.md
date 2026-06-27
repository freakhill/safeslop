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
* **Session Runtime Depth**: Currently, `safeslop session run` is just scaffolding. The next logical architectural feature is the actual execution plane: spawning the PTY, bridging the agent into its target isolation environment (sandbox/container/VM), and piping standard I/O to the Emacs terminal. 
* This likely requires a new spec (e.g., `specs/0050-session-runtime.md`) to define the precise `exec` handoff.