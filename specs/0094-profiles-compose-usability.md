# 0094 — Profiles compose usability

Status: planned
Date: 2026-07-10

SCOPE: fix the Profiles compose-buffer interaction regression where `RET` and catalog refresh re-render at the top of the buffer; explain locked inherited/default rows without moving the operator; and expose a deliberate, safe all-or-nothing opt-out for the agent default bundle using the existing `--no-default-bundle` contract.

OFF-LIMITS: do not make inherited/default/required package rows directly togglable; do not weaken container/deny defaults, policy trust, host consent, network limits, credential handling, or workspace-only file reach. Do not add arbitrary mounts, change engine policy/schema behavior, or implement editable compose fields / create-vs-update flow in this slice; those are follow-up authoring work.

WORKTREE: `.worktrees/0094-profiles-compose-usability/`

## Design

The compose renderer currently erases the buffer and unconditionally moves point to `point-min`; toggle and refresh both call it, including locked no-ops. Capture the logical compose row at point and each showing window's logical point/start row before a rerender, then restore those rows after render so a selection or refresh keeps the operator in context. A locked-row action must leave the view untouched and emit a source-specific message instead of silently re-rendering.

Default agent bundles remain locked in the bundle list. Add a distinct, clearly labelled compose control for the all-or-nothing `no-default-bundle` state; toggling that control maps to the existing CLI flag and warns that omitting the agent runtime may prevent launch. It is not a shortcut that partially deselects inherited dependencies.

## Tasks

- [x] T1 — Specify the failing compose interaction regressions first
  FILE:     `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Add ERT coverage that opens a real compose buffer/window, targets a lower unlocked catalog row, and proves `RET` preserves its logical row and scroll context; prove refresh preserves the same context and selections; prove a locked default/inherited row emits an explanatory message without rerendering or moving point; and prove the separate default-bundle control toggles `no-default-bundle`, changes effective default selection, and reaches compose argv.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: New interaction assertions fail on the pre-fix renderer because it resets to the top, lacks a default-bundle control, and silently rerenders locked rows.

- [ ] T2 — Preserve compose context and give locked rows feedback
  FILE:     `emacs/safeslop-profiles.el`
  CHANGE:   Add narrowly scoped helpers to capture/restore logical compose row positions for every showing window around compose rerenders. Use them after actual toggles and catalog refreshes. Detect locked/default/inherited rows before state mutation; leave the buffer/view unchanged and message the lock source. Keep raw and Evil `RET` behavior unchanged.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs) && make test-emacs-ui-matrix`
  EXPECTED: Lower-row selections and refresh retain context; locked `RET` does not jump; raw, Doom-shim, Evil, and Doom+Evil key-resolution slots still pass.

- [ ] T3 — Expose the safe default-bundle opt-out
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Render a distinct `RET`-actionable default-agent-bundle control with visible on/off state and an advanced warning. Map it only to compose state's existing `no-default-bundle`; recompute bundle/package inheritance after a change. Keep the default `claude` bundle row locked while default inheritance is on, and allow ordinary explicit bundle selection only when the default is off.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: Operators can deliberately omit the default bundle through the UI; `--no-default-bundle` appears only in that intentional state; inherited rows remain protected otherwise.

- [ ] T4 — Synchronize operator documentation and the implementation record
  FILE:     `README.md`, `emacs/README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0094-profiles-compose-usability.md`
  CHANGE:   Document retained-context toggle/refresh behavior, the meaning of locked rows, and the explicit default-bundle opt-out; state that it can leave an agent without its runtime. Mark the spec complete and tick tasks only after their stated verification passes.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Documentation matches the compose UI and all repository gates pass.
