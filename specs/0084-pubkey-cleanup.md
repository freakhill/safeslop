# 0084 — Remove Forgejo public key files immediately (0070 L4)

**Status:** implemented

SCOPE: Close `specs/0070` L4 by ensuring Forgejo/Gitea deploy-key staging removes each generated `*.pub` file immediately after reading it, before any network registration or later parse/revoke-info work can fail.

OFF-LIMITS: Do not change deploy-key title conventions, API request shape, revoke-info format, private-key staging paths/modes, host helper resolution, or GitHub/AWS credential behavior.

WORKTREE: `.worktrees/0084-pubkey-cleanup/`

Design: keep Forgejo deploy-key staging exactly as-is except for the public-key file lifetime. Add a tiny helper around `os.ReadFile(keyPath + ".pub")` that removes the `.pub` file right after a successful read and treats a failed cleanup as a staging error. `stageForgejoMulti` then uses the in-memory public key for API registration; failures after the read leave only the private key plus normal staged metadata, never the generated public-key file.

- [x] Add red Forgejo cleanup regression
  FILE:     `internal/engine/creds/forgejo_test.go`
  CHANGE:   Add a hermetic Forgejo staging test where `ssh-keygen` creates `id_<slug>.pub`, the fake Forgejo key-registration endpoint fails after the public key is read, and the test asserts the `.pub` file is already absent when `StageForgejo` returns the registration error.
  VERIFY:   `go test ./internal/engine/creds -run 'Forgejo.*Pub|StageForgejo.*Failure' -v`
  EXPECTED: Fails on current code because the `.pub` file is removed only after successful registration/parsing.

- [x] Remove `.pub` immediately after read
  FILE:     `internal/engine/creds/multirepo.go`
  CHANGE:   Add a small `readAndRemovePublicKey` helper and call it from `stageForgejoMulti` before `forgejoDo`; remove the later post-registration `os.Remove`.
  VERIFY:   `go test ./internal/engine/creds -run Forgejo -v`
  EXPECTED: Forgejo staging/revoke tests pass, including failure cleanup.

- [x] Update specs status
  FILE:     `specs/0070-security-review.md`, `specs/0084-pubkey-cleanup.md`
  CHANGE:   Mark L4 implemented and set this spec status to implemented after the tests pass.
  VERIFY:   `rg -n 'L4|0084|pub' specs/0070-security-review.md specs/0084-pubkey-cleanup.md`
  EXPECTED: Security-review and spec status mention the implemented L4 cleanup.

- [x] Run final verification
  FILE:     repository root
  CHANGE:   No behavior changes; run required gates from the worktree.
  VERIFY:   `make check && make build`
  EXPECTED: Both commands exit 0.
