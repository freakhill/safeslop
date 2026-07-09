# 0093 — UI runtime access and compose toggle key

Status: complete
Date: 2026-07-09

SCOPE: fix two operator regressions after the Emacs UI install: reattaching to an already-detached container session must not be blocked by a shadowed Docker preflight that the attach socket path does not need, and the Profiles compose buffer must stop using `SPC` as its checkbox toggle key because Evil/Doom leader handling makes it unreliable and hostile to muscle memory.

OFF-LIMITS: do not weaken CLI host-helper shadow refusal, do not introduce a runtime path chooser/override, do not make Emacs bypass a real runtime launch failure, do not read private Emacs config, and do not change isolation/network defaults. Runtime-start actions still preflight/report a shadowed Docker helper before spawning; only socket reattach stops doing a launch-only preflight.

WORKTREE: `.worktrees/0093-ui-runtime-keys/`

## Reproduction

On this host, the installed CLI reports shadowed Docker helpers:

```text
$ ~/.local/bin/safeslop doctor --json
...
"docker": {
  "path": "/opt/homebrew/bin/docker",
  "present": false,
  "shadowed_paths": ["/usr/local/bin/docker", "/Users/jojo/.orbstack/bin/docker"]
}
```

Current Emacs code runs this shadow preflight through `safeslop-session--launch-term`, so both `session run` and `session attach` are blocked for container records. That is too broad: `session attach --session-id` rejoins an existing detached supervisor over its socket and does not select or execute a container runtime. The CLI remains authoritative for socket failures.

The compose buffer still documents and binds `SPC` for toggling rows even after the prior Evil table workaround. The operator explicitly rejected this key because Evil/Doom shadows it in practice. The toggle should move to a non-leader key and `SPC` should not be advertised or bound by safeslop.

## Tasks

- [x] T1 — Narrow Emacs runtime preflight to runtime-start paths
  FILE: `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`, `emacs/test/safeslop-contract-test.el`, docs
  CHANGE: Make `safeslop-session-attach` / coupled `session run` and detached `session run --detach` keep the shadowed-Docker preflight. Make `safeslop-session-reattach` fetch status for naming/header only and then invoke `session attach` without doctor preflight, so existing detached sessions remain accessible even when the local Docker helper set is shadowed. Keep stop/rm/prune behavior unchanged.
  VERIFY: targeted Emacs runtime-preflight ERT.

- [x] T2 — Replace compose `SPC` toggle with `RET`
  FILE: `emacs/safeslop-profiles.el`, `emacs/safeslop-doom.el`, `emacs/test/*.el`, docs/specs
  CHANGE: Bind `RET` to `safeslop-profiles-compose-toggle` in raw and Evil normal state; remove safeslop's compose `SPC` binding and documentation. Update the UI probe to assert `RET` in raw/Evil slots and to reject a safeslop-owned `SPC` binding.
  VERIFY: `make test-emacs-ui-matrix` and `make test-emacs`.

- [x] T3 — Docs and install verification
  FILE: `README.md`, `emacs/README.md`, `skills/agent-sandbox-ops/SKILL.md`, this spec
  CHANGE: Document `RET` as the compose toggle and clarify that Docker-shadow preflight applies to runtime-start actions, not socket reattach. Mark complete after gates and install pass.
  VERIFY: `git diff --check && make check && make build && make install`.
