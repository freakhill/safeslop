# 0075 — Host-helper exec hardening (0070 H1)

Status: design ready, awaiting execution
Date: 2026-07-05
Follows: 0070 H1, 0035 (stale shadow-detection plan), 0066 (runtime detect semantics), 0072–0074

SCOPE: close 0070 H1 by making safeslop-owned host helper execution resolve through the sanitized `hostenv` PATH, fail closed on security-critical shadows, execute absolute paths only, and pass minimal helper-specific envs.
OFF-LIMITS: do not reopen 0072/0073/0074; do not revive the stale Swift/cockpit portions of 0035; do not add runtime installers or non-Go runtime dependencies; do not change trust gates, egress policy, or teardown `PolicyAllow` behavior; do not broaden into host-agent binary shadow policy beyond the existing `resolveHostBinary` path.
WORKTREE: `.worktrees/0075-host-helper-exec-hardening/` on branch `0075-host-helper-exec-hardening` for implementation.

## Problem

0070 H1 found that host-side helpers with credential or boundary authority are still spawned as bare names against the raw process `PATH` (`op`, `aws`, `gcloud`, `git`, `ssh-keygen`, `ssh-keyscan`, container CLIs, etc.). A poisoned early-PATH entry can therefore run as the user before any safeslop boundary exists. `hostenv.Env.LookPath` and `LookAll` already provide a sanitized resolution primitive; 0075 wires them into every safeslop-owned helper exec path.

## Decision (Expansion → ayo → FLO)

All safeslop-owned host helper execution must go through one shared Go helper layer that:

1. resolves helper binaries through `hostenv.Reconstruct()`'s sanitized PATH;
2. rejects security-critical shadows fail-closed (no warn-and-execute mode);
3. executes only absolute paths with direct argv (`exec.CommandContext` argv array, no shell);
4. sets `cmd.Path` and `cmd.Args[0]` to the resolved absolute path;
5. passes a minimal, explicit environment, never `hostenv.Environ()` or `os.Environ()` wholesale for credential-bearing helpers;
6. preflights profile-required helpers before writing staged credential artifacts where the requirements are known up front.

`specs/0035` is not executed verbatim: its Swift/cockpit target is stale, but the already-implemented `hostenv.Env.LookAll` primitive is reused.

## Helper policy

| Category | Helpers | Missing | Shadowed on sanitized PATH | Execution env |
|---|---|---|---|---|
| Credential-critical | `op`, `aws`, `gcloud`, `gke-gcloud-auth-plugin`, `git` for remote inference, `ssh-keygen`, `ssh-keyscan` | fail when required | fail | credential allowlist |
| Boundary-runtime | `docker`, `podman`, `lima` | auto-detect tries next; explicit runtime override fails | fail | runtime allowlist |
| Diagnostic/discovery | doctor rows, `toolchain.Available(mise/nix)` | report unavailable | report shadowed, do not execute shadowed helper | diagnostic allowlist only if probing |

`SAFESLOP_CONTAINER_RUNTIME` remains a **name** override (`docker|podman|lima`), not a path override: exact selected runtime or fail.

## Error UX contract

Host-helper errors must be actionable and value-free:

- missing: `host helper "op" not found on sanitized PATH; required for op:// secrets; install it or fix PATH`
- shadowed: `host helper "op" is shadowed on sanitized PATH: /opt/homebrew/bin/op, /usr/local/bin/op; safeslop refuses to choose for credential-bearing helpers; remove the duplicate or fix PATH order`
- rejected relative path: `host helper "foo/bar" must be an absolute path or bare name; relative paths are refused`

Never include secret values, command stderr, or raw helper output in these messages.

## Environment allowlists

Common env allowed for helper subprocesses: `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`, `TMP`, `TEMP`, `LANG`, `LC_ALL`, `LC_CTYPE`, `LC_MESSAGES`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `ALL_PROXY` and lowercase proxy variants.

Credential-specific additions:

- `op`: allow `OP_ACCOUNT`; deny `OP_SESSION*`, `OP_SERVICE_ACCOUNT_TOKEN`, and `OP_CONNECT_*`.
- `aws`: allow `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE`, `AWS_DEFAULT_REGION`, `AWS_REGION`, `AWS_CA_BUNDLE`; deny ambient `AWS_PROFILE`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE` unless the call site injects freshly minted values explicitly (e.g. `assumeRoleDownscope`).
- `gcloud`/GKE: allow `CLOUDSDK_CONFIG`; deny `GOOGLE_APPLICATION_CREDENTIALS` as ambient authority.
- `git`/`ssh-keygen`/`ssh-keyscan`: deny ambient `GIT_*` and `SSH_*` unless a call site explicitly constructs a value.

Runtime env additions: `DOCKER_HOST`, `DOCKER_CONTEXT`, `DOCKER_CONFIG`, `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH`, `DOCKER_BUILDKIT`, `COMPOSE_DOCKER_CLI_BUILD`, `CONTAINER_HOST`, `CONTAINERS_CONF`, `REGISTRY_AUTH_FILE`, `XDG_RUNTIME_DIR`, `LIMA_HOME`. Do not include cloud, 1Password, GitHub, Anthropic, or staged credential variables in runtime env.

## Execution plan

- [x] Add `internal/engine/hostexec` resolver/command layer.
  FILE:     `internal/engine/hostexec/hostexec.go`, `internal/engine/hostexec/hostexec_test.go`
  CHANGE:   Add a `Resolver` over an injectable `LookupEnv` (`PATH()`, `Get`, `LookPath`, `LookAll`) plus helper specs/classes. Implement `Resolve`, `Inspect`, `Preflight`, `CommandContext`, and env builders. Resolution rules: bare name uses `LookAll` and fails on zero or >1 executable matches for executable helpers; absolute path must be executable; relative slash paths are refused. `CommandContext` must set absolute `cmd.Path`, absolute `cmd.Args[0]`, direct argv only, and the selected allowlisted env.
  VERIFY:   `go test ./internal/engine/hostexec -v`
  EXPECTED: PASS tests for: missing helper; shadowed helper; absolute helper accepted; relative slash rejected; diagnostic inspect reports shadow without executing; `cmd.Path`/`Args[0]` are absolute; credential env excludes `OP_SESSION`, ambient AWS keys, `GIT_*`, `SSH_*`; runtime env excludes cloud/op/token vars.

- [ ] Route `op` availability/sign-in/read through `hostexec`.
  FILE:     `internal/engine/secrets/secrets.go`, `internal/engine/secrets/secrets_test.go`, affected credentials-inspection tests if needed.
  CHANGE:   Replace `osexec.LookPath("op")` and bare `exec.CommandContext(ctx, "op", ...)` with `hostexec` resolution/commands. Keep errors generic and value-free; do not wrap helper stderr into user-visible errors. Add a test seam for a fake resolver so tests stay hermetic.
  VERIFY:   `go test ./internal/engine/secrets ./internal/engine/creds -run 'Op|Secret|Inspect' -v`
  EXPECTED: PASS; missing/shadowed `op` produces actionable host-helper errors; fake `op read --no-newline` still resolves refs; no test shells live 1Password.

- [ ] Route credential helpers through `hostexec`.
  FILE:     `internal/engine/creds/ssh.go`, `internal/engine/creds/aws.go`, `internal/engine/creds/gcp.go`, `internal/engine/creds/kube.go`, tests beside each.
  CHANGE:   Update `runSSHCmd`, `StageAWS`, `assumeRoleDownscope`, `StageGCP`, and `runKubeCmd` to resolve helpers via `hostexec` and use credential envs. `assumeRoleDownscope` must stop appending to `os.Environ()` and instead inject only the freshly minted base AWS creds on top of the credential allowlist. Preserve direct argv and existing hermetic fake-binary seams.
  VERIFY:   `go test ./internal/engine/creds -run 'AWS|GCP|Kube|SSH|Forgejo|Github|Pnpm' -v`
  EXPECTED: PASS; tests prove helper argv is unchanged except argv[0] absolute; missing/shadowed helpers fail before command execution; downscope env excludes ambient AWS vars except explicit staged values.

- [ ] Preflight required helpers before staging credential artifacts.
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_stage_test.go`, optional helper file under `internal/engine/creds/` for required-helper derivation.
  CHANGE:   Before `stageProfile` writes `.npmrc`, kubeconfig, git config, SSH keys, or token files, compute the helpers known from the profile/account refs and call `hostexec.Preflight`. Include `op` for any `op://` in secrets, pnpm tokens, GitHub App key refs, Forgejo token refs; `aws` for AWS/EKS; `gcloud` and `gke-gcloud-auth-plugin` for GCP/GKE; `git` for omitted repo inference; `ssh-keygen`/`ssh-keyscan` for Forgejo deploy keys. Dynamic helpers that can only be known after a safe read still fail closed at their call sites.
  VERIFY:   `go test ./internal/cli -run 'StageProfile|HostHelper|Preflight' -v`
  EXPECTED: PASS; a missing/shadowed required helper returns before any stage file is created; profiles using only `env:` refs do not require `op`; declared forge repos avoid the `git remote` helper when no inference is needed.

- [ ] Cache absolute container-runtime CLI paths from detect to execution.
  FILE:     `internal/engine/container/runtime/runtime.go`, `internal/engine/container/runtime/engine.go`, `internal/engine/container/runtime/*_test.go`, `internal/engine/container/container.go`
  CHANGE:   Make production `Detect` use `hostexec`/`hostenv` lookup instead of raw `exec.LookPath`. Return engines carrying resolved absolute CLI paths (`docker`, `podman`, or `lima`) so `Engine.Argv` and `Engine.Command` cannot drift from detection. `defaultRunner` must execute the already-resolved path used for probes. Auto-detect keeps docker → podman → lima; missing/probe-failed candidates try next; a shadowed candidate is a hard error; explicit `SAFESLOP_CONTAINER_RUNTIME` missing/shadowed/probe-failed is fatal. `container.Available` uses the same path.
  VERIFY:   `go test ./internal/engine/container/runtime ./internal/engine/container -run 'Detect|Available|Engine|Runtime' -v`
  EXPECTED: PASS; tests assert `Engine.Argv()[0]` is absolute; `Engine.Command().Path` is absolute; shadowed docker errors instead of selecting podman silently; explicit override still uses exactly the selected runtime or fails; teardown callers still use `PolicyAllow`.

- [ ] Move diagnostics and toolchain probes onto sanitized inspection.
  FILE:     `internal/cli/cli.go`, `internal/engine/toolchain/toolchain.go`, affected tests.
  CHANGE:   `doctorReport` and `toolchain.Available` must use `hostexec.Inspect`/sanitized lookup. Diagnostics may report shadowed paths but must not execute a shadowed security-critical helper. Preserve JSON shape unless a new `shadowed_paths` field is explicitly added and tested.
  VERIFY:   `go test ./internal/cli ./internal/engine/toolchain -run 'Doctor|Toolchain|Available' -v`
  EXPECTED: PASS; doctor paths come from sanitized resolution; shadowed helper state is visible or conservatively marked unavailable; no raw `exec.LookPath` remains for listed helpers.

- [ ] Add a regression guard for bare host-helper execs.
  FILE:     `ci/host-helper-exec-denylist.sh`, `Makefile`, tests if the repo has shell-script gates.
  CHANGE:   Add a `make check` gate that rejects new raw `exec.LookPath` / `exec.CommandContext` uses for the listed helper names outside `hostexec`, `hostenv` internals, and tests. Keep allowlist narrow so generic agent execution (`internal/engine/exec`) and test helper processes are not false positives.
  VERIFY:   `ci/host-helper-exec-denylist.sh && make check`
  EXPECTED: PASS; intentionally adding `exec.CommandContext(ctx, "op", ...)` outside allowed files fails the gate locally.

- [ ] Docs/skills sync and final verification.
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, this spec.
  CHANGE:   Document that host helper CLIs are resolved via safeslop's sanitized host PATH and shadowed security-critical helpers fail closed; keep command UX examples unchanged. Mark this spec complete only after real verification.
  VERIFY:   `make check && make build`
  EXPECTED: PASS; Go tests, Emacs ERT, shell gates, and build all pass.

## Method

Expansion read: `specs/0070-security-review.md`, `specs/0035-shadowed-binary-detection.md`, current `hostenv`, `secrets`, `creds`, `container/runtime`, `container`, `toolchain`, and CLI source surfaces. AYO lanes: Gemini + DeepSeek prior-art passes (sudo secure_path/env_reset, OpenSSH absolute paths/StrictModes, Git safe.directory, Go path-security/execabs, Nix/Bazel hermetic resolution, Kubernetes exec-plugin allowlist lessons). FLO: one worker drafted the policy; one DeepSeek evaluator scored it 80.5/100. Forced fixes applied here: concrete tests/verification, actionable error messages, exact env allowlists, and explicit scope note for host-agent binary policy.
