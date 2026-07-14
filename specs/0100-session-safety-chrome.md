# 0100 — Persistent session safety chrome

Status: planned
Date: 2026-07-14
Follows: `specs/0086-session-legibility.md`, `specs/0087-product-activation.md` track 5.

SCOPE: make the environment, network posture, and value-free credential posture persistently visible in every live Emacs session buffer, with the same posture available from portal status help.

OFF-LIMITS: no Go, policy, session-record, or JSON-contract changes; no tab-line/global-frame chrome; no polling or extra CLI calls; no secret values, secret refs, staged paths, or credential material in rendered text, help, logs, or tests; do not replace user mode-line content.

WORKTREE: `.worktrees/0100-session-safety-chrome/`

## Contract

- A live session buffer with status data gets a buffer-local mode-line segment shaped as `safeslop[container/deny creds:2]` (or `creds:none`). Environment and network words are always present; color only reinforces them using the existing tier/network faces.
- The segment's help text expands the same environment/network posture and full value-free credential scopes already used by the 0086 header. Unsafe-looking refs, token markers, private-key text, and staged/host paths remain suppressed by the existing defensive formatter.
- Installing the segment preserves the terminal mode's existing `mode-line-format`, is idempotent, and remains buffer-local. A best-effort status miss retains the legacy buffer behavior and installs no chrome.
- The portal status tooltip starts with the same posture help text, followed by its existing lifecycle details. Existing Env, Net, Creds, and Status visible columns remain unchanged.
- Raw Emacs, Evil, and Doom compatibility remain covered by the existing UI matrix.

## Tasks

- [x] T1 — Add and install the buffer-local safety mode-line segment
  FILE:     `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`
  CHANGE:   TDD pure helpers for a safe credential count, shared posture help, and a propertized `safeslop[env/net creds:N|none]` segment. Add a buffer-local segment variable and an idempotent installer that prepends its symbol to a copied local `mode-line-format` without replacing existing entries. Call it from `safeslop-session--launch-term` only when status data exists, beside the existing header installation. Assert literal environment/network words, existing faces, full value-free help, unsafe-field suppression, preservation/idempotence, and fallback-without-data behavior.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-session-.*\\(safety-chrome\\|launch-term\\)")'`
  EXPECTED: command exits 0; live buffers with data carry one local, color-redundant mode-line segment with value-free help, existing mode-line content remains, and status-miss launches carry no segment.

- [ ] T2 — Surface the same posture help from portal status
  FILE:     `emacs/safeslop-portal.el`, `emacs/test/safeslop-test.el`
  CHANGE:   Prefix `safeslop-portal--status-help` with the shared session posture help while retaining coupled/detached, credential-file lifecycle, and last-error details. Add tests for exact environment/network/scope text, old credential-less records, and defensive suppression of refs/values/paths.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-portal-status-help-.*posture")'`
  EXPECTED: command exits 0; portal status help exposes the same value-free safety posture without changing visible columns or leaking forbidden material.

- [ ] T3 — Synchronize operator documentation and roadmap status
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0087-product-activation.md`, `specs/0100-session-safety-chrome.md`
  CHANGE:   Document the persistent mode-line segment and portal posture tooltip; mark 0087 session safety chrome covered by 0100 while leaving profile safety evaluation open; set this spec complete only after T4 passes.
  VERIFY:   `rg -n 'safety chrome|mode-line|posture tooltip|0100|Profile safety evaluation' README.md skills/agent-sandbox-ops/SKILL.md specs/0087-product-activation.md specs/0100-session-safety-chrome.md`
  EXPECTED: output shows the live-buffer chrome contract, portal posture help, 0087 completion cross-reference, and the still-open profile safety evaluation track.

- [ ] T4 — Run the full compatibility and repository gates
  FILE:     whole repo
  CHANGE:   Run the Emacs UI matrix followed by required repository checks and build; inspect the final diff for scope drift.
  VERIFY:   `make test-emacs-ui-matrix && make check && make build`
  EXPECTED: command exits 0; raw Emacs and all available Evil/Doom slots pass, then all Go/ERT/denylist/sync gates and the signed-binary build pass.

## Execution notes

Use TDD for T1 and T2: add behavior assertions, run the task VERIFY to observe an assertion failure caused by missing behavior, implement the minimum change, then rerun to green. Execute tasks in order because T2 consumes the shared posture helper from T1. Commit each task only after its VERIFY matches EXPECTED; stop on the first failed verification.
