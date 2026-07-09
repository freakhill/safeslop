# 0088 — Host trust and runtime preflight UI

Status: implemented
Date: 2026-07-09
Follows: `specs/0087-product-activation.md` track 1.

Follow-up: `specs/0093` narrows the Emacs runtime preflight so socket reattach
no longer runs the Docker-shadow launch preflight; coupled/detached runtime-start
actions still do.

SCOPE: fix the immediate UI dead ends reported after local install: Emacs ad-hoc host session creation must offer the explicit `--trust-host` acknowledgement, and container session launch from Emacs must preflight obvious shadowed runtime helpers before creating another confusing stopped session.

OFF-LIMITS: no policy schema changes; no weakening host-helper shadow refusal; no automatic trust; no persistent host trust; no runtime path chooser/override; no network model redesign; no credential UI redesign; no live Docker/OrbStack calls in tests.

WORKTREE: `.worktrees/0088-host-trust-runtime-preflight-ui/`

## Problem

Two activation paths fail as raw CLI errors from the Emacs UI:

1. Creating an ad-hoc host session sends `session create --environment host` without `--trust-host`, so the CLI correctly returns `TRUST_REQUIRED`, but the UI gives no acknowledgement path.
2. Running a container session can fail after launch with a shadowed Docker helper error. The refusal is security-correct, but the UI should detect and explain that preflight before trying to run the session.

## Contract

- Interactive Emacs ad-hoc host creation asks a high-friction yes/no acknowledgement and, only on yes, includes `--trust-host` in the `session create` argv.
- If an interactive host create still receives host `TRUST_REQUIRED` without a policy path, Emacs offers one retry with `--trust-host` instead of offering `safeslop trust` or dead-ending on the raw envelope.
- Noninteractive/test callers retain the old default unless they explicitly pass the host-trust flag.
- Container coupled-run and detached-run launches from Emacs preflight `safeslop doctor --json` for a shadowed `docker` helper and abort with an actionable message before launching the terminal/subprocess. Socket reattach was originally included here, but `specs/0093` removes that launch-only preflight from reattach because it uses an existing supervisor socket.
- The preflight is best-effort and value-free: if doctor itself fails or returns old JSON, launch continues and the CLI remains authoritative.
- Security stays fail-closed in the CLI; the UI only improves timing and explanation.

## Tasks

- [x] T1 — Plan product activation tracks
  FILE:     `specs/0087-product-activation.md`, `specs/0088-host-trust-runtime-preflight-ui.md`
  CHANGE:   Add the umbrella roadmap and this executable blocker spec.
  VERIFY:   `rg -n 'Product activation|0088|--trust-host|runtime preflight|shadowed|Credential connection|Network authority' specs/0087-product-activation.md specs/0088-host-trust-runtime-preflight-ui.md`
  EXPECTED: output shows 0087 tracks and 0088 host/runtime blocker contract.

- [x] T2 — Add host `--trust-host` acknowledgement in Emacs session creation
  FILE:     `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`
  CHANGE:   Extend `safeslop-session--create-args` with an optional trust-host flag that appends `--trust-host` for host ad-hoc sessions. In interactive `safeslop-session-new`, ask an explicit yes-or-no prompt when environment is `host`; abort on no. Add a host-TRUST_REQUIRED retry helper that detects ad-hoc host create failures without a policy `details.path`, prompts, retries the same args with `--trust-host`, and leaves profile policy trust behavior unchanged.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-session-.*host.*trust")'`
  EXPECTED: command exits 0; tests prove host create gets `--trust-host` only after acknowledgement, no acknowledgement aborts before CLI, and host `TRUST_REQUIRED` retries with `--trust-host` rather than `safeslop trust`.

- [x] T3 — Add Emacs container runtime preflight for shadowed Docker
  FILE:     `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`, `emacs/test/safeslop-contract-test.el`
  CHANGE:   Add pure helpers to parse `doctor --json` tool rows and detect a shadowed `docker` helper (`tools.docker.shadowed_paths`). Before `safeslop-session-attach` and detached run call into the CLI for a container session, fetch session status (already best-effort for terminal naming), run the doctor preflight, and abort with an actionable `user-error` if docker is shadowed. `specs/0093` removed this preflight from `safeslop-session-reattach` because it uses an existing supervisor socket rather than starting a runtime. Keep old/failed doctor JSON best-effort-pass. Update exact-argv tests for the added doctor preflight where necessary.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-session-.*\\(runtime\\|shadow\\|preflight\\)")'`
  EXPECTED: command exits 0; tests prove shadowed docker aborts before launch, a clean doctor permits launch, doctor failure permits launch, and the error message lists the selected/shadowed paths without secrets.

- [x] T4 — Docs and skill sync
  FILE:     `README.md`, `emacs/README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0087-product-activation.md`, `specs/0088-host-trust-runtime-preflight-ui.md`
  CHANGE:   Document the Emacs host acknowledgement and the runtime shadow preflight; mark 0087 track 1 and 0088 T1-T4 as implemented only after verification. Do not claim broader activation tracks are complete.
  VERIFY:   `rg -n 'trust-host|host.*acknowledg|runtime preflight|shadowed|0088|Product activation' README.md emacs/README.md skills/agent-sandbox-ops/SKILL.md specs/0087-product-activation.md specs/0088-host-trust-runtime-preflight-ui.md`
  EXPECTED: output shows the new UI behavior and the remaining activation tracks stay open.

- [x] T5 — Full verification
  FILE:     whole repo
  CHANGE:   Run repo gates after formatting/lint-sensitive edits.
  VERIFY:   `make check && make build`
  EXPECTED: command exits 0; Go tests, ERT suite, shell denylist gates, byte-compile, and build pass.

## Execution notes

Use TDD for T2/T3. Tests must not invoke live Docker/OrbStack, real 1Password, or live credentials. The CLI remains the source of truth: Emacs preflight is an early warning only, not a bypass or downgrade of host-helper hardening.
