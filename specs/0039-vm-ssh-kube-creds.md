# `ssh` + `kube` credentials under `environment: vm` (specs/0010, 0011 deferral, resolved)

**Context:** specs/0010 (`kube`) and specs/0011 (`ssh`) shipped the credential providers for
`host`/`sandbox`/`container`, but explicitly **deferred** `environment: vm` behind a guard error.
The stated reason (specs/0010): *"single-quoted `secrets.env` + unknown guest `$HOME` make a correct
in-VM path out of scope for v1."* This slice removes that guard for the `safeslop run` path by wiring
the already-staged credential files into the VM guest.

## Why it was actually a small slice

The staging half was already environment-agnostic and the transport already existed:

1. `stageProfile` calls `StageSSH`/`StageKube` regardless of environment — they mint the ephemeral
   deploy key / short-lived bearer token on the **host** and write `stageDir/.ssh/{id,known_hosts}`
   and `stageDir/kubeconfig` (both `0600`). The kubeconfig embeds the token inline
   (`renderKubeconfig`), so it is **portable** — no cloud CLI needed in the guest.
2. `vm.provision` already `scp -r`'s the **entire** `stageDir` to `~/.safeslop-runtime` in the guest.
   So `.ssh/id`, `known_hosts`, and `kubeconfig` already arrive in the VM.

The only missing piece was the guest-side environment: `remoteAgentCmd` sourced `secrets.env` but
never exported `GIT_SSH_COMMAND` / `KUBECONFIG` — the container path sets these in the compose env
(`compose.yml.tmpl`), the VM path set neither.

## The two deferral blockers, resolved

- **Unknown guest `$HOME`** → use `~`, not an assumed absolute path. `ssh` expands `~` in `-i` and
  `UserKnownHostsFile` itself, and `zsh` expands `~` in the `KUBECONFIG=` assignment — so the guest's
  real home is resolved at run time without safeslop knowing it.
- **`secrets.env` quoting** → orthogonal. ssh/kube creds are delivered as their **own files** under
  `~/.safeslop-runtime`, not as `secrets.env` entries, so the single-quote escaping never touches them.

## Implemented

- `remoteAgentCmd(agentArgv, proxyURL, hasSSHKey, hasKubeconfig)` exports, before sourcing secrets and
  `exec`:
  - `GIT_SSH_COMMAND` — the **same** option string as `compose.yml.tmpl` (pinned `known_hosts`,
    `IdentitiesOnly=yes`, `IdentityAgent=none`, `StrictHostKeyChecking=yes`), pointed at
    `~/.safeslop-runtime/.ssh/{id,known_hosts}`.
  - `KUBECONFIG=~/.safeslop-runtime/kubeconfig`.
- `scp -r` does not reliably preserve the host's `0600` on the private key and `ssh` refuses an
  over-permissive key, so the guest command re-tightens (`chmod 700 .ssh`, `chmod 600 .ssh/id`) before
  use.
- `vm.provision` detects the staged files (`os.Stat` of `.ssh/id` / `kubeconfig`) exactly like
  `container/launch.go`, and flips the matching export.
- Removed the `environment:"vm"` guard in `runProfile` (`internal/cli/cli.go`).
- Tests: `vm` package unit tests for the new exports (`TestRemoteAgentCmdExportsSSHKey`,
  `TestRemoteAgentCmdExportsKubeconfig`, and a no-creds negative case). The obsolete cli-level guard
  tests were removed (the guard is intentionally gone; delivery is covered in the `vm` package).

## Scope / deferred

- **`safeslop run` only.** The embedded-cockpit path (`PrepareSession`, `cli.go` `sessionSpec`) still
  blocks ssh (deploy key is scoped to the *workspace* git origin, which `safeslop serve`'s cwd is not)
  and vm+kube. Cockpit sessions don't stage credentials at all yet ("a separate deferred unit"), so
  lifting those blocks without a staging story would start a session with no creds — left untouched.
- **`network: deny` + ssh-to-GitHub.** A deny-tier VM has only the advisory HTTP(S) proxy, no direct
  route, so git-over-ssh needs `network: allow` — the same property the container path has. Not changed
  here.
- **On-exit revoke.** `RevokeSSH` is best-effort and runs host-side against `stageDir/.ssh/revoke-info`
  (unchanged); the VM is destroyed on exit and the stage is wiped, so the decay-first guarantee holds.
