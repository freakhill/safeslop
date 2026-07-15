# 0104 — Editable profile compose fields and engine-owned deletion

Status: complete
Date: 2026-07-15

SCOPE: make every field of a newly composed profile editable from the Emacs compose buffer, and replace the Profiles surface's guided-manual CUE deletion with an explicit, validated CLI deletion contract.

OFF-LIMITS: do not add an edit-existing-profile compose mode (the current create/upsert contract would drop profile fields not represented in the compose UI); do not weaken policy-byte trust, host consent, egress constraints, credential handling, or CUE validation; do not add arbitrary mounts or live remote discovery.

WORKTREE: `.worktrees/0104-profile-compose-fields/`

## Design

Problem: compose renders Name, Agent, Environment, Network, and Workspace as inert text, so only catalog bundles/packages can be changed; profile deletion then requires manual CUE editing. Success means `RET` on a visible field prompts with field-appropriate validation/completion and updates compose state before the existing engine dry-run/save review, while `D` calls an engine-owned delete that atomically validates the remaining CUE and returns a JSON envelope.

Chosen approach: render each compose field as a `RET`-actionable row and keep the compose buffer creation-only. This avoids unsafe partial overwrites of existing profiles (which may carry credentials, egress, or other fields not authorable in compose), retains the current CLI create/dry-run contract, and makes agent/default-package inheritance recompute when agent changes. Add `safeslop profile delete <name> [safeslop.cue] --output json`; it loads the project config, removes exactly one profile, renders/validates the complete result before writing it, and returns value-free `{removed, profile, path}` data.

- [x] T1 — Specify the profile-delete contract with hermetic CLI regressions
  FILE:     `internal/cli/cli_profile_test.go`
  CHANGE:   Add tests that delete one named profile while preserving another, reject an unknown name without changing bytes, and require `--output json`.
  VERIFY:   `go test ./internal/cli/ -run 'TestProfileDelete' -v`
  EXPECTED: New tests fail because `profile delete` does not exist.

- [x] T2 — Implement atomic engine-owned profile deletion
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_profile_test.go`
  CHANGE:   Register `profile delete`; load an existing project `safeslop.cue`, delete the exact profile map entry, render and validate the complete config before writing, and emit structured success/errors without touching builtins or unrelated profiles.
  VERIFY:   `go test ./internal/cli/ -run 'TestProfileDelete' -v`
  EXPECTED: The command preserves remaining profiles, refuses missing targets without a write, and emits the required JSON envelope.

- [x] T3 — Specify and implement editable creation fields in compose
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Render Name, Agent, Environment, Network, and Workspace as field rows; `RET` opens the relevant prompt, validates profile names, uses completion for finite choices, normalizes workspace, and rerenders while preserving context. Changing agent recomputes package rows/default bundle; the existing `C-c C-c` dry-run/save uses the changed state. Keep compose creation-only.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT first proves each field changes compose state and resulting argv, invalid names are rejected without mutation, and agent changes update automatic-bundle/package inheritance.

- [x] T4 — Route Profiles deletion through the CLI and refresh in place
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Make `D` require an explicit confirmation, invoke `profile delete <name> <known-config> --output json` asynchronously, surface failures, and rerender the Profiles list with point/scroll preservation on success. Keep raw CUE `e` as an advanced escape hatch, not the required deletion path.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT proves the exact argv, no subprocess on declined confirmation, and successful deletion refreshes the list without opening the CUE file.

- [x] T5 — Synchronize help and operator docs
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `emacs/safeslop-profiles.el`, `specs/0104-profile-compose-fields.md`
  CHANGE:   Document `profile delete`, editable compose fields/keys, and that compose is creation-only; update surface help/docstrings and mark this spec complete only after verification.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Documentation describes the actual command and UI, and all repository gates pass.
