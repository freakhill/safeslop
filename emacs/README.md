# safeslop Emacs package

Raw Emacs frontend for safeslop.  Doom support is optional and lives in
`safeslop-doom.el`; core `safeslop.el` does not depend on Doom APIs.  The package
parses safeslop's versioned JSON envelope via `safeslop-contract.el`, opens
interactive session runs through built-in `make-term`/`term-mode`, and falls
back to a read-only `compilation-mode` JSONL monitor for session status.  Its
ERT tests consume Go's canonical `internal/jsoncontract/testdata/*.golden.json`
fixtures directly.  When Doom/Evil is present, output buffers enter Evil normal
state and get normal-state bindings for refresh/error/quit actions.

## Operator UI navigation

The Emacs package is a small operator UI with three surfaces: **Sessions** (`P`),
**Install** (`I`), and **Profiles** (`F`).  The tab strip at the top of every
surface shows each surface's direct switch key next to its name
(`P Sessions │ I Install │ F Profiles   TAB/[] cycle surface`), and every label
and key in the strip is clickable with the mouse — so switching surface is never a
guess.  In any operator UI surface and most result buffers, the shared keys are:

| key | action |
|---|---|
| `P` / `I` / `F` | switch directly to Sessions / Install / Profiles |
| `TAB` / `S-TAB` | cycle next / previous surface |
| `[` / `]` | cycle previous / next surface |
| `g` | refresh this view (result buffers only rerun read-only commands) |
| `d` | doctor |
| `E` | show last error |
| `L` | debug log |
| `?` | describe-mode help |
| `q` | quit window |

Error output is persistent in-buffer text rather than only an echo-area flash;
when a command fails, the banner points to `g` retry, `d` doctor, `E` last error,
and `L` debug.  Environment and network cells keep their text labels and add
colour/help-echo as redundant safety hints: `host` is no isolation, `container` is
contained, `deny` is default-deny egress, and `allow` is open egress.

## Portal

`M-x safeslop` (alias of `safeslop-portal`, also `C-c s P`) opens the **Sessions**
portal: a `tabulated-list` dashboard of every session — id, agent, environment,
network, status, PID, age, recipe/image, workspace — that you act on in place:

| key | action |
|---|---|
| `RET` / `o` | state-aware open: run created sessions, reattach detached sessions, focus live coupled sessions, or show details for stopped sessions |
| `D` | start detached after a staged-credential lifetime warning |
| `R` | reattach only when a detached supervisor socket exists |
| `i` | details buffer (lifecycle, credentials, last error, next action) |
| `k` | stop, revoke credentials, and tear down the boundary |
| `x` | remove one stopped/created session record from the list |
| `X` | prune — remove every stopped session at once |
| `n` | new session |
| `f` | jump to the backing profile when present |
| `g` | refresh now |
| `a` | pause/resume auto-refresh |

A session that has exited stays listed as `stopped` so you can read its exit code
and last error; `x` clears one such record and `X` clears them all in one call, so
the portal never fills up with dead-session corpses.  `x`/`X` refuse a running
session (stop it first with `k`); the CLI (`session rm` / `session prune`) revokes
any still-live staged credentials before deleting a record, and `prune` also
reconciles a crashed session (marked running but whose process is gone) to
`stopped` and sweeps it in the same pass.

Detached sessions are explicit because they survive the Emacs buffer and keep
staged credentials until stop/revoke.  Coupled run remains the default.

While the portal is displayed, it **auto-refreshes** every
`safeslop-portal-refresh-interval` seconds (default 5; set to nil to disable).
Each in-place redraw preserves every showing window's scroll position and keeps
point on the same session — an automatic or manual refresh never scrolls the
window or jumps the cursor to the top (so the row action keys always act on the
row you are looking at).  A tick is skipped while a prompt is open, while you have
keystrokes pending, or while a previous fetch is still in flight, so refreshes
never fight your input or pile up.  The header shows whether polling is on or
paused; polling only runs `safeslop session list`, never an agent action.  Debug
lines from polling are labelled `event=poll`.

## Profiles

`M-x safeslop-profiles` (`C-c s F`) opens the **Profiles** surface for the active
`safeslop.cue`.  Rows show profile, agent, environment, and network with the same
safety legends as the portal.

| key | action |
|---|---|
| `RET` / `i` | inspect resolved packages, egress, recipe/image/base |
| `x` | launch a session from the selected profile after an isolation/network summary |
| `e` | edit the profile in `safeslop.cue` |
| `n` | create a profile with structured prompts |
| `c` | clone the selected profile |
| `v` | validate the backing `safeslop.cue` |
| `D` | guided delete (manual CUE edit anchored at the block) |
| `g` | refresh |

The intended flow is profile → inspect → launch → portal.  Inspect buffers are no
longer dead ends: `x`, `e`, `c`, and `g` act from the detail view too.

## Debug buffer

`M-x safeslop-debug-log` (`C-c s L`) opens `*safeslop debug*`, a redacted client
diagnostics log: each CLI invocation and its result is one timestamped line
(`event=call argv=… / event=result status=0 ok=t`).  Only allowlisted, non-secret
fields are written.  Toggle with `safeslop-debug-log-enabled`.  safeslop is a
self-contained CLI, so commands run as direct subprocesses — it is daemonless,
and no daemon is ever started.

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

Default key prefix: `C-c s` (global), and `safeslop-doom-bind-leader` puts the
same command map under `SPC o s` on Doom's leader.  That deliberately overrides
Doom's `:os macos` "send to application" prefix on `SPC o s` (slopmaxx sits at
`SPC o m`); rebind `safeslop-doom-bind-leader` if you want the macOS prefix back.
Session creation offers `claude`, `claude-code`, and `pi`; `claude-code` is an
alias for the canonical `claude` engine agent.
