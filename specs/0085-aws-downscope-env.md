# 0085 — Pin AWS downscope subprocess env (0070 L5)

**Status:** implemented

SCOPE: Close `specs/0070` L5 by explicitly pinning and documenting that `assumeRoleDownscope` runs `aws sts assume-role` with the hostexec AWS allowlist plus only the freshly minted SSO base credentials, not the full host environment.

OFF-LIMITS: Do not change AWS credential delivery into agents, downscope argv/session-policy semantics, helper resolution policy, proxy/common hostexec allowlists, or any non-AWS credential provider.

WORKTREE: `.worktrees/0085-aws-downscope-env/`

Design: `specs/0075` already routed AWS helper execution through `hostCommand`/`hostexec.CredentialSpec`, so the production path uses `EnvAWS` instead of `os.Environ()`. This slice adds a focused regression that would fail if `assumeRoleDownscope` again inherited ambient AWS/profile/token/secrets env, tightens the code comment to name the allowlist contract, and marks L5 implemented.

- [x] Add AWS downscope env regression
  FILE:     `internal/engine/creds/aws_test.go`
  CHANGE:   Add a hermetic fake-`aws` test that sets ambient `AWS_PROFILE`, ambient AWS keys/session/role/web-identity vars, and unrelated secret env; during `sts assume-role`, the fake records its environment. Assert it sees only the explicit SSO base AWS key/secret/session injected by `assumeRoleDownscope` and does not see ambient authority variables.
  VERIFY:   `go test ./internal/engine/creds -run 'StageAWSDownscope.*Env' -v`
  EXPECTED: Passes on the current hostexec-based code; would fail if the downscope subprocess inherited `os.Environ()` or ambient AWS authority.

- [x] Clarify downscope env contract
  FILE:     `internal/engine/creds/aws.go`
  CHANGE:   Update the `assumeRoleDownscope` comment to state that hostexec supplies the minimal AWS helper allowlist and that only freshly minted SSO base credentials are injected.
  VERIFY:   `go test ./internal/engine/creds -run AWS -v`
  EXPECTED: AWS credential tests pass.

- [x] Update specs status
  FILE:     `specs/0070-security-review.md`, `specs/0085-aws-downscope-env.md`
  CHANGE:   Mark L5 implemented and set this spec status to implemented after the regression passes.
  VERIFY:   `rg -n 'L5|0085|assumeRoleDownscope|downscope' specs/0070-security-review.md specs/0085-aws-downscope-env.md`
  EXPECTED: Security-review and spec status mention the implemented L5 env allowlist.

- [x] Run final verification
  FILE:     repository root
  CHANGE:   No behavior changes beyond the regression/comment/spec updates; run required gates from the worktree.
  VERIFY:   `make check && make build`
  EXPECTED: Both commands exit 0.
