# 0061 — Emacs refresh stability + session-corpse cleanup

SCOPE:
- Fix the operator-reported Emacs UI problems, taking the pattern from slopmaxx's
  "avoid blocking Emacs refreshes" fix (slopmaxx `a6d3840`):
  1. Cursor randomly jumps to the top of a dashboard during refresh.
  2. Surface tab switching (Sessions │ Install │ Profiles) is undiscoverable.
  3. Session-buffer shortcuts feel broken (a symptom of #1: point lands on the
     header, so row action keys report "No session on this line").
  4. No way to clear failed/stopped sessions out of the portal — the list fills
     with dead-session "corpses".

OFF-LIMITS:
- Do not weaken network/isolation defaults or host/container boundaries.
- Do not add runtime dependencies outside the Go binary / existing Emacs package.
- Do not bind global `C-c s D` (existing tests keep it unbound).
- Removing a session record must never orphan staged credentials.

## Root causes

- **Cursor jump / broken shortcuts.** Each dashboard refreshes by
  `tabulated-list-print` (which erases the buffer) then re-inserting the header
  block at `point-min`. In a window that is not the selected one this collapses
  `window-point` to the top and drops `window-start` (scroll); and even in the
  selected window, when the kept row is the *first* row, inserting the header
  before it strands point on the header. This is the same class of bug slopmaxx
  fixed by snapshotting and restoring `window-point`/`window-start` and never
  re-popping a visible buffer.
- **No cleanup.** `internal/engine/session.Store` had `Create/Get/List/Stop` but
  no `Remove`/`Prune`; `stop` only marks a session `stopped`, so it lives in the
  list forever. There was no `session rm`/`prune` CLI and no portal key.
- **Tab switching undiscoverable.** The strip rendered labels but not the switch
  keys, and only `[`/`]` cycled (no `TAB`).

## Tasks

- [x] Task 1 — Engine: removable sessions
  FILE: `internal/engine/session/session.go`, `internal/engine/session/session_test.go`
  CHANGE: Add `Store.Remove` (refuses running via `ErrSessionRunning`; revokes
  still-live credentials + reaps + sweeps socket, then deletes the record) and
  `Store.PruneStopped` (removes every stopped record, returns removed ids).
  VERIFY: `go test ./internal/engine/session -count=1`
  EXPECTED: New Remove/Prune tests pass; running sessions are never deleted.

- [x] Task 2 — CLI: `session rm` / `session prune`
  FILE: `internal/cli/cli.go`, `internal/cli/cli_session_test.go`
  CHANGE: Add `cmdSessionRemove` (`rm --session-id <id>`) and `cmdSessionPrune`
  (`prune`), both `--output json`; prune reconciles liveness first so a crashed
  session is persisted stopped and swept in the same pass. Contract errors:
  `SESSION_NOT_FOUND`, `SESSION_ALREADY_RUNNING`.
  VERIFY: `go test ./internal/cli -run 'TestSession(Remove|Prune|RmAndPrune)' -count=1`
  EXPECTED: rm deletes+revokes, refuses running, 404s missing; prune clears
  stopped incl. crashed, leaves created/running.

- [x] Task 3 — Emacs: shared window-view preservation + discoverable tabs
  FILE: `emacs/safeslop-surface.el`, `emacs/test/safeslop-test.el`
  CHANGE: Add `safeslop-surface--capture-views` / `--restore-views` / `--goto-id`
  helpers; render the switch key before each tab label + a "TAB/[] cycle surface"
  hint; bind `TAB`/`<backtab>` in the shared surface map.
  VERIFY: `make test-emacs`
  EXPECTED: view-restore + tab-key + strip-shows-keys tests pass.

- [x] Task 4 — Emacs: apply the fix in all three renders + portal cleanup keys
  FILE: `emacs/safeslop-portal.el`, `emacs/safeslop-profiles.el`,
  `emacs/safeslop-install.el`, `emacs/safeslop-session.el`, `emacs/safeslop-doom.el`
  CHANGE: Each keep-point render snapshots views, re-finds the kept row *after*
  the header re-insert, and restores scroll+cursor; portal auto-refresh skips a
  tick on `input-pending-p` / in-flight fetch; add `x` remove and `X` prune
  (with running-refusal + confirmation), `safeslop-session-remove/prune`, and
  sync Doom/Evil bindings. Portal row actions (`k`, `D`, `x`, `X`) use quiet
  session callbacks so success refreshes the portal in place instead of popping a
  JSON result buffer over the operator's dashboard.
  VERIFY: `make test-emacs`
  EXPECTED: 100 ERT pass; portal remove/prune + refresh-preserves-view +
  no-result-popup tests pass.

- [x] Task 5 — Docs sync + full verification
  FILE: `README.md`, `emacs/README.md`, `skills/agent-sandbox-ops/SKILL.md`
  CHANGE: Document `session rm`/`prune`, portal `x`/`X`, tab-strip switch keys,
  and refresh stability.
  VERIFY: `make check && make build`
  EXPECTED: All green; new CLI verified end-to-end against the built binary.
