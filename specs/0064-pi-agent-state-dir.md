# 0064 — Writable ephemeral $HOME for container agents (pi startup crash)

BUG (jojo, 2026-07-02): running a `pi` profile session exits 1 within ~1.5s;
the session record keeps only `last_error: "exit status 1"`. Reproduced under
a PTY (`script -q … safeslop session run …`), the real failure is:

```
Error: ENOENT: no such file or directory,
  mkdir '/home/agent/.pi/agent/sessions/--workspace--'
    at getDefaultSessionDir (…/pi-coding-agent/dist/core/session-manager.js)
```

ROOT CAUSE: the agent service in `compose.yml.tmpl` runs with
`read_only: true` and tmpfs only on `/tmp` and `/var/tmp`. `/home/agent`
exists in the image (bash dotfiles only) but is read-only at runtime and has
no per-agent state trees. pi's session-store `mkdirSync` is non-recursive, so
the missing parent chain fails ENOENT at startup. Any agent that writes under
`$HOME` (claude's `~/.claude` as well) hits the same wall; the image itself is
fine (`pi 0.80.2`, `node v22.23.1` run when invoked directly).

FIX (runtime-only; no image rebuild — the entrypoint is staged per-run and the
compose file is materialized per-run):

1. `compose.yml.tmpl`: add `/home/agent` to the agent service tmpfs list,
   owned by the agent user: `uid=1000,gid=1000,mode=0700`. A bare tmpfs
   mounts root:root 0755 and only moves the crash from ENOENT to EACCES
   (validated live 2026-07-02). Ephemeral and in-memory: no host path is
   exposed, state is wiped on exit — consistent with the staged-credential
   wipe posture. It masks the image's baked shell dotfiles, which is
   acceptable (agents, not login shells).
2. `entrypoint.sh`: after sourcing secrets, pre-create the state trees agents
   assume exist (`~/.pi/agent/sessions`, `~/.claude`, `~/.config`, `~/.cache`,
   `~/.local/state`), resolving `$HOME` via passwd when docker leaves it
   unset.

NON-GOALS:
- Persisting agent state (pi conversation history) across session runs — that
  means a host mount, i.e. a file-sharing boundary change needing its own
  spec and consent story.
- Capturing agent stderr into the session record's `last_error` (the Emacs
  client side of this is specs/0063 F9; an engine-side capture is a separate
  follow-up).

WORKTREE: `.worktrees/fix/pi-agent-state-dir/`
BRANCH: `fix/pi-agent-state-dir`

## Tasks

- [ ] Task 1 — compose.yml.tmpl tmpfs `/home/agent` + entrypoint.sh state-tree
  pre-creation (both done with this spec's landing); `make sync-container-assets`
  so the mirrored copies match; extend `internal/engine/container` tests to
  assert the tmpfs line and the entrypoint mkdir.
  VERIFY: `go test ./internal/engine/container/...`
- [ ] Task 2 — Full gate + live verification.
  VERIFY: `make check && make build`, then
  `safeslop session create --profile pi` + `script -q /tmp/pi.txt safeslop
  session run --session-id …` reaches the pi UI instead of the ENOENT crash
  (network-deny profile: the agent may still refuse remote calls without an
  allowlisted key — the gate here is only that startup survives).
