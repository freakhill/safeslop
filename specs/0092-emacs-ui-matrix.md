# 0092 — Emacs UI matrix and Evil binding regression tests

Status: complete
Date: 2026-07-09

Follow-up: `specs/0093` keeps the UI matrix but moves the compose toggle from
`SPC` to `RET`; the matrix now asserts `RET` and rejects a safeslop-owned `SPC`
toggle binding.

SCOPE: add a reproducible Emacs UI compatibility test matrix and fix the reported Profiles compose-buffer `SPC` toggle regression under Evil/Doom. The matrix must cover raw Emacs, Doom-shim Emacs, Emacs with real Evil when available, Doom-shim Emacs with real Evil when available, and an opt-in personal-config probe. This builds on `specs/0063` and `specs/0091`.

OFF-LIMITS: do not install Emacs packages, fetch packages from the network, weaken safeslop safety defaults, or make `make check` depend on a user's private Doom/personal config. Do not read or log personal config contents; the personal slot only runs a caller-provided command and reports pass/fail.

WORKTREE: `.worktrees/0092-emacs-ui-matrix/`

## Problem

The new Profiles compose buffer documents `SPC` as the checkbox toggle, but in Evil normal state the active binding is Evil's normal-state `SPC` command instead of `safeslop-profiles-compose-toggle`. The existing tests only inspect raw keymaps or stub Evil registration, so they did not catch the real key-resolution failure.

Reproduced on `main` @ `31d31d0` with raw Emacs + real Evil from the local Doom straight build:

```text
local=safeslop-profiles-compose-toggle key=evil-forward-char evil-key=nil
```

Manual experiment confirming the likely root cause:

```elisp
(evil-set-initial-state 'safeslop-profiles-compose-mode 'normal)
(evil-define-key* 'normal safeslop-profiles-compose-mode-map
  (kbd "SPC") 'safeslop-profiles-compose-toggle)
```

After that, `(key-binding (kbd "SPC"))` resolves to `safeslop-profiles-compose-toggle` in the compose buffer.

## Design

Chosen approach: add a small batch-mode UI probe plus a shell matrix runner. Keep the default `make check` hermetic, but add an explicit local target that exercises real key resolution in more environments.

- `emacs/test/safeslop-ui-probe.el` is a reusable ERT/probe file. It loads safeslop, optionally enables Evil, optionally stubs Doom's `map!`, opens the relevant safeslop modes, and asserts active key resolution for user-facing actions. Its critical regression is: in `safeslop-profiles-compose-mode`, `SPC` must resolve to `safeslop-profiles-compose-toggle` both raw and under Evil normal state.
- `ci/emacs-ui-matrix.sh` runs the probe in named slots:
  1. `raw` — `emacs -Q --batch -L emacs`.
  2. `doom-shim` — raw Emacs with a minimal `map!` stub to prove `safeslop-doom-bind-leader` and Doom integration load without full Doom.
  3. `evil` — raw Emacs plus real Evil load paths. The runner auto-detects local straight/elpaca Evil build dirs and accepts `SAFESLOP_EVIL_LOAD_PATH`; no network or package install.
  4. `doom-evil` — Doom-shim plus real Evil.
  5. `personal` — opt-in: if `SAFESLOP_UI_PERSONAL_CMD` is set, the runner appends the probe load/eval to that command. If `SAFESLOP_UI_REQUIRE_PERSONAL=1`, missing personal command is a failure; otherwise it is reported as skipped.
- `make test-emacs-ui-matrix` invokes the runner. It is documented as a local compatibility gate, not part of `make check`, because real Evil/Doom/personal configs are machine-specific.
- `make check` keeps the existing portable `make test-emacs` gate. The new probe's raw slot or a narrowed ERT test should still be covered by `make test-emacs` so CI catches the compose `SPC` regression without requiring private packages.

Alternatives considered:

1. Only add `safeslop-profiles-compose-mode` to the existing fake Evil table tests. This is cheap but would still miss real key precedence, which is the bug the operator saw.
2. Make `make check` run personal Doom/Evil. This would be stronger locally but non-hermetic and would break contributors/CI without the same config.
3. Use an external UI automation framework. Rejected: extra dependency and unnecessary for batch key-resolution regressions.

## Tasks

- [x] Add a UI probe and matrix runner
  FILE:     `emacs/test/safeslop-ui-probe.el`, `ci/emacs-ui-matrix.sh`, `Makefile`
  CHANGE:   Add a batch-mode probe that checks safeslop loads, Doom-shim leader binding works when `map!` exists, core surface keys resolve, and Profiles compose `SPC` resolves to `safeslop-profiles-compose-toggle` in raw mode. Add a shell runner with slots `raw`, `doom-shim`, `evil`, `doom-evil`, and optional `personal`. Add `make test-emacs-ui-matrix`.
  VERIFY:   `make test-emacs-ui-matrix`
  EXPECTED: The raw and Doom-shim slots pass. On this machine, auto-detected local Evil slots run and the compose `SPC` check fails before the Evil binding fix, proving the regression is covered. Personal is skipped unless `SAFESLOP_UI_PERSONAL_CMD` is set.

- [x] Fix Evil normal-state bindings for the compose buffer
  FILE:     `emacs/safeslop-doom.el`, `emacs/test/safeslop-test.el`, `emacs/test/safeslop-profiles-test.el` if needed
  CHANGE:   Add `safeslop-profiles-compose-mode` to the data-driven Evil binding table. Bind `SPC` to `safeslop-profiles-compose-toggle`, `?` to help, `gr` to refresh, `C-c C-c` to preview/save, and `q` to cancel. Keep Evil motion discipline from `specs/0063`: do not bind bare `g`, `j`, `k`, `n`, `f`, or `a` in Evil normal state. Extend fake Evil/table tests so future compose-mode actions are registered.
  VERIFY:   `make test-emacs-ui-matrix && make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: The real Evil slots now pass, and existing Emacs ERT/byte-compile gates still pass.

- [x] Add an opt-in personal-config probe path
  FILE:     `ci/emacs-ui-matrix.sh`, `emacs/test/safeslop-ui-probe.el`, `emacs/README.md`
  CHANGE:   Support `SAFESLOP_UI_PERSONAL_CMD` as the caller-provided command prefix for loading the user's personal config. The runner appends repository `-L emacs`, loads the probe, and runs the same key-resolution assertions. Document `SAFESLOP_UI_REQUIRE_PERSONAL=1` for making this slot mandatory locally. Ensure logs print the slot name and result but never dump personal config files or environment secrets.
  VERIFY:   `SAFESLOP_UI_REQUIRE_PERSONAL=1 SAFESLOP_UI_PERSONAL_CMD='<documented local command>' make test-emacs-ui-matrix`
  EXPECTED: With a valid local command the personal slot runs the probe; without one and `SAFESLOP_UI_REQUIRE_PERSONAL=1`, the target fails with an actionable message.

- [x] Document the UI matrix and keep standard gates green
  FILE:     `README.md`, `emacs/README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0092-emacs-ui-matrix.md`
  CHANGE:   Document when to run `make test-emacs-ui-matrix`, how to provide Evil load paths or a personal config command, and that `make check` remains hermetic. Mark this spec complete only after all verification passes.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Whitespace, full check, and build pass. `make check` does not require personal Doom/Evil state; the explicit matrix target covers local UI compatibility.
