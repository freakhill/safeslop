# Host-helper exec hardening: Expansion → ayo → FLO decision

Date: 2026-07-05
Status: decision accepted for `specs/0075-host-helper-exec-hardening.md`

## Verdict

Safeslop-owned host helper execution must use a shared Go `hostexec` layer: resolve helper binaries through `hostenv.Reconstruct()`'s sanitized PATH, reject security-critical shadows fail-closed, execute absolute paths only (`cmd.Path` and `Args[0]` set to the resolved path), and pass minimal helper-specific envs. Diagnostics may report shadows; credential and runtime helpers must not warn-and-execute.

`specs/0035` is stale and is not revived as-is; only its existing `hostenv.Env.LookAll` primitive is reused.

## Laws honored

- The reconstructed host env is rich and is for host-side discovery/binary resolution only.
- Never hand `hostenv.Environ()` or `os.Environ()` wholesale to credential-bearing helper subprocesses.
- `SAFESLOP_CONTAINER_RUNTIME` remains a name override (`docker|podman|lima`): exact selected runtime or fail.
- Teardown keeps `PolicyAllow`; runtime hardening must not block cleanup due to deny-tier launch gates.
- Tests stay hermetic; no live 1Password/cloud/runtime calls.

## Prior-art lessons carried forward

- OpenSSH/Go path-security: execute absolute paths; bare-name fallback is not a security boundary.
- sudo env_reset/secure_path: setting PATH alone is not enough; build explicit envs.
- Git safe.directory / OpenSSH StrictModes / Kubernetes exec-plugin allowlists: security-critical ambiguity should fail closed, not warn.
- Nix/Bazel: resolve tool identity once and reuse it, rather than letting execution re-resolve against ambient PATH.

## Rejections / deferred

- No path override env vars in 0075; no current requirement forces them.
- No Swift/cockpit/UI resurrection from 0035.
- No host-agent binary shadow policy beyond the existing `resolveHostBinary` path.
- No runtime installers or dependency additions.

## Method

Expansion read current specs/source: `specs/0070-security-review.md`, `specs/0035-shadowed-binary-detection.md`, `internal/engine/hostenv`, `internal/engine/secrets`, `internal/engine/creds`, `internal/engine/container/runtime`, `internal/engine/container`, `internal/engine/toolchain`, `internal/cli`.

AYO lanes: Gemini + DeepSeek. FLO worker drafted the decision. FLO evaluator (DeepSeek) scores: security correctness 8.0, code fit 9.5, UX/operability 7.0, scope control 8.5, testability 5.5; weighted total 80.5/100. Forced fixes applied in `specs/0075`: concrete test matrix, actionable error UX, exact env allowlists, and explicit host-agent scope boundary.
