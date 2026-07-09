# 0091 — Profile authoring cockpit

Status: complete
Date: 2026-07-09

SCOPE: implement the first product-activation slice of the Profiles authoring surface: safe default profile creation, checkbox-driven bundle/package selection with inline help, inherited/default package legibility, project-language bundle suggestions, and an engine-authored safety preview before saving. This builds on `specs/0058`, `specs/0087`, and the completed credential picker in `specs/0090`.

OFF-LIMITS: do not weaken policy-byte trust, host consent, network defaults, host-helper shadow refusal, credential value-free guarantees, or CUE-as-source-of-truth. Do not add live repo/package discovery, credential minting, external runtime dependencies, or agent-triggered network prompts. Do not invent a custom host-mount schema in this slice; custom mount authoring is a file-sharing capability-boundary change and needs its own decision/spec before code.

WORKTREE: `.worktrees/0091-profile-authoring-cockpit/`

## Design

Problem: the current Profiles surface can create a profile, but it is still a sequential prompt that hides inherited packages, offers no checkbox/help affordance, and confirms a save before the engine has rendered a safety preview for the exact profile being written.

Success criteria:

- A new operator can create a useful container profile from defaults without hand-writing CUE.
- Bundle/package selection is checkbox-driven, shows package help, and makes default/bundle/required packages selected and locked rather than invisible.
- Project-language suggestions (`go`, `web`, `python`, `rust`) are offered from local repo markers only; they are suggestions, not automatic authority expansion.
- Before writing `safeslop.cue`, Emacs shows the engine-produced risk/authority summary for the exact profile args and asks for final confirmation.
- The saved profile still flows through `safeslop profile create`; Emacs does not hand-render CUE and does not re-derive risk.

Chosen approach: add a CLI `profile create --dry-run` preview contract plus a richer Emacs compose buffer. Emacs may compute catalog selection/inheritance from the catalog JSON, but safety/risk comes from Go (`policy.RiskSummary`/`RiskAxes`) in the dry-run envelope. This avoids CUE editing in the UI and keeps the engine authoritative while giving the operator a reviewable UI before save.

Deferred explicitly: custom mounts. The compose buffer should show the current file boundary as `workspace only` and link/word the limitation clearly, but must not offer arbitrary host-path mount authoring until a mount capability model is specified.

## Tasks

- [x] Add a dry-run profile-create contract with engine risk data
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_profile_iw3_test.go`, `internal/engine/policy/risk.go`
  CHANGE:   Add `--dry-run` to `safeslop profile create`. It must accept the same profile args, resolve packages/recipe, include `risk` and `risk_axes` in the shared `profileResolvedData` envelope, and return without writing `safeslop.cue` when dry-run is true. Keep normal create behavior unchanged except for the additional risk fields. Add JSON tags or explicit lower-camel maps so the contract is `headline/lines/level` and `name/value/restricted/severity`, not Go field names.
  VERIFY:   `go test ./internal/cli/ -run 'Profile(CreateDryRun|CreateWritesNewCue|ShowEnvelopeIncludesResolvedRecipe)' -v`
  EXPECTED: Dry-run test proves no `safeslop.cue` is written, the exact profile args are echoed under `data.profile`, `data.risk.headline` is present, and existing create/show tests still pass.

- [x] Expose catalog defaults for UI inheritance
  FILE:     `internal/cli/cli_catalog.go`, `internal/cli/cli_catalog_test.go`, `README.md`, `skills/agent-sandbox-ops/SKILL.md`
  CHANGE:   Include the catalog `defaults` map in `catalog list --bundles --output json` so Emacs can identify the agent default bundle without hardcoding policy internals. Preserve existing `data.bundles` rows and package-list behavior. Document that the bundle list envelope carries `defaults` for UI inheritance.
  VERIFY:   `go test ./internal/cli/ -run 'CatalogList' -v`
  EXPECTED: The bundle-list JSON contract contains `data.defaults.claude` and `data.defaults.pi`; existing catalog list tests remain green.

- [x] Model selected, inherited, locked, and suggested packages in Emacs
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Add pure helpers that merge `catalog list --bundles` and `catalog list` envelopes into indexes, compute the selected default bundle unless `--no-default-bundle` is set, expand selected bundles and package `requires` recursively, and return package rows with source labels (`default:<bundle>`, `bundle:<bundle>`, `requires:<pkg>`, `direct`) plus a locked flag for inherited/default/required rows. Add local marker-based bundle suggestions for `go.mod`, `package.json`, `pyproject.toml`, and `Cargo.toml`; suggestions must be preselected only after visible operator review or visibly marked as suggested.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT proves default `claude` locks `node`/`claude-code`, selecting `web` locks its bundle packages, direct packages remain togglable, requires are locked with a source, and marker suggestions map to the expected bundle names.

- [x] Replace the interactive create prompt with a checkbox compose buffer
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Keep the noninteractive `safeslop-profiles-create` function signature working for tests/automation, but make interactive `c` open a `*safeslop profile compose*` buffer. The buffer must show profile fields, bundle rows, package rows, lock/source columns, and key hints. `RET` toggles unlocked rows (changed from `SPC` by `specs/0093` after Evil/Doom operator feedback), `?` opens help for the row (bundle description/packages or package kind/version/requires/conflicts/runtime egress/note), `g` refreshes catalog data, `C-c C-c` proceeds to preview/save, and `q` cancels without writing.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT covers key bindings, rendering of checked/locked rows, help text for bundle/package rows, and confirms locked rows cannot be toggled directly.

- [x] Preview safety before save and write only after confirmation
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   On `C-c C-c`, call `safeslop profile create ... --dry-run --output json` with the compose state, render the returned risk lines/resolved packages/recipe in a confirmation buffer or prompt, then call the existing non-dry-run `profile create` only after an explicit yes. The summary must label host as unconfined, container deny as allowlisted, credential scope as value-free, and mounts/file reach as workspace-only for this slice. Emacs must not compute risk itself.
  VERIFY:   `make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: ERT proves the dry-run argv precedes the write argv, declining confirmation prevents the write call, and the displayed safety text is sourced from the dry-run envelope.

- [x] Keep docs, skills, and roadmap in sync
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0087-product-activation.md`, `specs/0091-profile-authoring-cockpit.md`
  CHANGE:   Update the Profiles surface docs to describe the compose buffer, checkbox/help keys, dry-run safety preview, catalog defaults, and the custom-mount deferral without using legacy removed-surface terminology outside specs. Keep `0087` pointing at this implementation spec; after implementation and verification land, set this spec's `Status` to complete and tick its task boxes as each task verifies.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Whitespace check, full repository check, and build all pass; README/skill examples match real commands.
