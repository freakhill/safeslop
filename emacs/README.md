# safeslop Emacs package

Raw Emacs frontend for safeslop.  Doom support is optional and lives in
`safeslop-doom.el`; core `safeslop.el` does not depend on Doom APIs.  The package
parses safeslop's versioned JSON envelope via `safeslop-contract.el`, opens
interactive session runs through built-in `make-term`/`term-mode`, and falls
back to a read-only `compilation-mode` JSONL monitor for session status.  Its
ERT tests consume Go's canonical `internal/jsoncontract/testdata/*.golden.json`
fixtures directly.  When Doom/Evil is present, output buffers enter Evil normal
state and get normal-state bindings for refresh/error/quit actions.

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

When a command runs and no socket is present, `safeslop-autostart-daemon` tries to
start a daemon.  Current safeslop builds may not ship a daemon yet; in that case
autostart is a no-op until one of these points at an executable:

- `safeslop-daemon-program`
- `$SAFESLOP_DAEMON_BIN`
- `safeslopd` on `exec-path`
- `safeslop-mcp` on `exec-path`

Manual start from Emacs: `M-x safeslop-daemon-start` or `C-c s D`.
