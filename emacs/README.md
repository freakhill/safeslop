# safeslop Emacs package

Raw Emacs frontend for safeslop.  Doom support is optional and lives in
`safeslop-doom.el`; core `safeslop.el` does not depend on Doom APIs.  The package
parses safeslop's versioned JSON envelope via `safeslop-contract.el`, opens
interactive session runs through built-in `make-term`/`term-mode`, and falls
back to a read-only `compilation-mode` JSONL monitor for session status.  Its
ERT tests consume Go's canonical `internal/jsoncontract/testdata/*.golden.json`
fixtures directly.  When Doom/Evil is present, output buffers enter Evil normal
state and get normal-state bindings for refresh/error/quit actions.

## Portal

`M-x safeslop` (alias of `safeslop-portal`, also `C-c s P`) opens the **portal**: a
`tabulated-list` dashboard of every session — id, agent, environment, network,
status, workspace — that you act on in place:

| key | action |
|---|---|
| `RET` / `o` | open (run the agent in a term buffer) |
| `R` | reattach to a detached supervisor |
| `i` | status |
| `k` | stop (revoke credentials) |
| `n` | new session |
| `g` | refresh |
| `d` | doctor · `L` debug log · `q` quit |

Every command shows its result — `doctor`, `status`, `validate`, and the rest
render the envelope's full `data` payload (not just `ok:`), and `session list`
becomes the portal table.

## Debug buffer

`M-x safeslop-debug-log` (`C-c s L`) opens `*safeslop debug*`, a redacted client
diagnostics log: each CLI invocation and its result is one timestamped line
(`event=call argv=… / event=result status=0 ok=t`).  Only allowlisted, non-secret
fields are written.  Toggle with `safeslop-debug-log-enabled`.  safeslop is a
self-contained CLI, so commands run as direct subprocesses — no daemon round-trip
is attempted on the command path (see Daemon autostart below).

## Install from the repo

```sh
make install-emacs   # installs to ~/.local/share/safeslop/emacs
make install         # also installs ~/.local/bin/safeslop
```

## Raw Emacs

```elisp
(add-to-list 'load-path (expand-file-name "~/.local/share/safeslop/emacs"))
(require 'safeslop)
(safeslop-bind-default-keys) ; C-c s prefix
```

## Doom Emacs

Add to `~/.doom.d/config.el`:

```elisp
(let ((safeslop-dev-dir (expand-file-name "~/.local/share/safeslop/emacs"))
      (safeslop-bin (expand-file-name "~/.local/bin/safeslop")))
  (when (file-directory-p safeslop-dev-dir)
    (add-to-list 'load-path safeslop-dev-dir)
    (when (file-executable-p safeslop-bin)
      (setq safeslop-program safeslop-bin))
    (require 'safeslop-doom)
    (safeslop-bind-default-keys)
    (safeslop-doom-bind-leader)))
```

Default key prefix: `C-c s`. Session creation offers `claude`, `claude-code`,
and `pi`; `claude-code` is an alias for the canonical `claude` engine agent.

## Daemon autostart

The Emacs package mirrors slopmaxx's local developer shape:

- state dir: `~/Library/Application Support/safeslop/`
- socket: `~/Library/Application Support/safeslop/safeslop.sock`
- log: `~/Library/Application Support/safeslop/daemon.log`

safeslop is a self-contained CLI, so the Emacs commands no longer attempt a daemon
autostart on the command path.  `M-x safeslop-daemon-start` (`C-c s D`) remains for
explicitly launching a daemon should one ship later; it resolves a binary from one
of these:

- `safeslop-daemon-program`
- `$SAFESLOP_DAEMON_BIN`
- `safeslopd` on `exec-path`
- `safeslop-mcp` on `exec-path`

Manual start from Emacs: `M-x safeslop-daemon-start` or `C-c s D`.
