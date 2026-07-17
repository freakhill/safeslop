# safeslop Emacs package

Raw Emacs frontend for safeslop.  Doom support is optional and lives in
`safeslop-doom.el`; core `safeslop.el` does not depend on Doom APIs.  The package
parses safeslop's versioned JSON envelope via `safeslop-contract.el`, opens
interactive session runs through built-in `make-term`/`term-mode`, and falls
back to a read-only `compilation-mode` JSONL monitor for session status.  Its
ERT tests consume Go's canonical `internal/jsoncontract/testdata/*.golden.json`
fixtures directly.  When Doom/Evil is present, output buffers enter Evil normal
state and get normal-state bindings for refresh/error/quit actions.

## Module layout

One direction of `require`s, entry point at the bottom (specs/0062); every
dashboard redraw goes through the one `safeslop-surface-render` engine, so the
scroll/cursor preservation from specs/0061 lives in exactly one place:

| file | role |
|---|---|
| `safeslop-contract.el` | versioned JSON envelope parse/validate |
| `safeslop-client.el` | CLI subprocess substrate + redacted debug log |
| `safeslop-surface.el` | shared dashboard chrome, tier/net cells, render engine |
| `safeslop-output.el` | read-only envelope output buffers (`safeslop-output-mode`) |
| `safeslop-session-terminal.el` | session shard: PTY launch, JSONL monitor, failure/safety chrome |
| `safeslop-egress.el` | session shard: progressive egress command construction + passive review UI |
| `safeslop-session.el` | session front: commands, terminal attach, detail view (owns the `safeslop-session--*` internals split across the two shards above) |
| `safeslop-portal.el` | Sessions dashboard |
| `safeslop-profile-evaluation.el` | profile shard: versioned safety-evaluation validate/render |
| `safeslop-profile-compose.el` | profile shard: interactive profile composition UI |
| `safeslop-profiles.el` | profile front: Profiles dashboard + CUE-backed CRUD |
| `safeslop-credentials.el` | Credentials surface: value-free posture + account-link status |
| `safeslop.el` | entry point: top-level commands + `C-c s` command map |
| `safeslop-doom.el` | optional Doom/Evil shim (data-driven binding tables) |

The session and profile fronts are decomposed into private feature shards: each
shard keeps the unchanged `safeslop-session--*` / `safeslop-profiles--*` internal
symbols and is required only by its front, which is the single public entry.  The
require graph stays one-directional, so no shard reaches upward into a layer that
requires it.

## Operator UI navigation

The Emacs package is a small operator UI with three surfaces: **Sessions** (`P`),
**Profiles** (`F`), and **Credentials** (`K`).  The tab strip at the top of every
surface shows each surface's direct switch key next to its name
(`P Sessions │ F Profiles │ K Credentials   TAB/[] cycle surface`), and every label
and key in the strip is clickable with the mouse — so switching surface is never a
guess.  In any operator UI surface and most result buffers, the shared keys are:

| key | action |
|---|---|
| `P` / `F` / `K` | switch directly to Sessions / Profiles / Credentials |
| `TAB` / `S-TAB` | cycle next / previous surface |
| `[` / `]` | cycle previous / next surface |
| `g` (Evil: `gr`) | refresh this view (result buffers rerun read-only commands; detail/inspect views re-render faced, never a raw dump) |
| `d` | doctor |
| `E` | show last error |
| `L` | debug log |
| `?` | describe-mode help |
| `q` | quit window |

Every dashboard keeps its state legible in the buffer itself rather than only as
an echo-area flash: a persistent error banner when a fetch fails (pointing to
`g` retry, `d` doctor, `E` last error, `L` debug), an empty-state hint when
there are no rows, and a loading hint while the first fetch is in flight — never
a silent blank table.  Environment and network cells keep their text labels and add
colour/help-echo as redundant safety hints: `host` is no isolation, `container` is
contained, `deny` is default-deny egress, and `allow` is open egress.

## Portal

`M-x safeslop` (alias of `safeslop-portal`, also `C-c s P`) opens the **Sessions**
portal: a `tabulated-list` dashboard of every session — id, agent, environment,
network, status, PID, age, recipe/image, workspace — that you act on in place:

| key | action |
|---|---|
| `RET` / `o` | state-aware open: run created sessions (after the same isolation/network confirm as a profile launch), reattach detached sessions, focus live coupled sessions, or show details for stopped sessions |
| `r` | run the created session at point coupled (same risk confirm) |
| `R` | start detached after a staged-credential lifetime warning |
| `A` | reattach only when a detached supervisor socket exists |
| `i` | details buffer (lifecycle, credentials, structured failure/action, next action; tier-coloured like the table) |
| `s` | stop, revoke credentials, and tear down the boundary |
| `x` | remove one stopped/created session record from the list |
| `X` | prune — remove every stopped session at once |
| `c` | new session |
| `N` | rename — set or clear the session's display label (empty input clears it) |
| `^` | jump to the backing profile when present |
| `g` (Evil: `gr`) | refresh now |
| `a` (Evil: `ga`) | pause/resume auto-refresh |

Rows are lifecycle-ordered — running, then created, then stopped, then failed —
so the actionable sessions sit at the top regardless of id or age.

A session can carry an optional human **display name** (specs/0065): set it at
create time with `safeslop session create … --name <name>`, or change/clear it
later with `N` in the portal (`safeslop session rename --session-id <id> --name
<name> --output json` under the hood — an empty name clears the label).  The name
is a pure label: it never replaces the `sess-<hex>` id as the addressing handle,
and rename is allowed in any status.  When set, it rides inside the Session cell
as a suffix (`sess-abcd… my-label`, truncated to the cell) and in the annotated
session pickers — never in its own column.

A session that has exited stays listed as `stopped`.  Failed rows include a
bounded, visible engine-owned reason in the Status column; `i` shows the full
structured summary, action, and stable code, with legacy `last_error` only as a
fallback.  If a coupled terminal exits during startup, Emacs fetches status once,
opens that durable detail view, refreshes a live portal, and emits one deduplicated
summary/action message.  Raw resolver paths, command output, and secret values are
not rendered.  `x` clears one stopped record and `X` clears them all, so the portal
never fills up with dead-session corpses.  `x`/`X` refuse a running session (stop
it first with `s`); the CLI (`session rm` / `session prune`) revokes any still-live
staged credentials before deleting a record, and `prune` also reconciles a crashed
session (marked running but whose process is gone) to `stopped` and sweeps it in
the same pass.

Detached sessions are explicit because they survive the Emacs buffer and keep
staged credentials until stop/revoke.  Coupled run remains the default.

For a `container` + `deny` session, the details buffer performs an asynchronous,
passive denied-destination count; it never focuses a buffer or changes authority
when proxy traffic arrives. Press `v` to open the operator review: it renders
only value-free FQDN:port/count/time/grantability rows. At a row, `a` explicitly
**Allows now** for this session, `k` **Keeps denied** without authority, and `A`
opens a separate hash-checked persistent-rule/CUE-delta review. Its `a` key is a
second, explicit write for future sessions only; after it succeeds, review and
re-trust the changed policy before creating a new session. The profile composer
labels container deny **Deny (progressive review)** as a workflow cue, never as
an authorization. No proxy event opens a prompt, focuses a review buffer, edits
CUE, or grants access.

Creating a new ad-hoc host session (`c` / `safeslop-session-new` with
environment `host`) asks an explicit yes/no host acknowledgement before passing
`--trust-host` to `safeslop session create`; a no answer aborts before the CLI is
called. If a host ad-hoc create still returns `TRUST_REQUIRED` with no policy
path, Emacs offers one retry with `--trust-host` rather than sending you to
policy trust.

Before `RET`/`o`, `r`, or `R` starts a container session, Emacs runs a
best-effort runtime preflight via `safeslop doctor --json`. A shadowed Docker
helper aborts the launch before the terminal/subprocess starts and lists the
selected/shadowed paths; old or failed doctor output is allowed through, leaving
the CLI as source of truth. `A` reattaches over an existing supervisor socket and
does not preflight Docker.

While the portal is displayed, it **auto-refreshes** every
`safeslop-portal-refresh-interval` seconds (default 5; set to nil to disable).
Each in-place redraw preserves every showing window's scroll position and keeps
point on the same session — an automatic or manual refresh never scrolls the
window or jumps the cursor to the top (so the row action keys always act on the
row you are looking at).  Portal row actions that mutate session state (`s`, `R`,
`x`, `X`) refresh the portal in place on success instead of popping a JSON result
buffer over the dashboard.  A tick is skipped while a prompt is open, while you
have keystrokes pending, or while a previous fetch is still in flight, so
refreshes never fight your input or pile up.  The header shows whether polling is
on or paused; polling only runs `safeslop session list`, never an agent action.
Debug lines from polling are labelled `event=poll`.

## Profiles

`M-x safeslop-profiles` (`C-c s F`) opens the **Profiles** surface for the active
`safeslop.cue`.  Rows show profile, agent, environment, and network with the same
safety legends as the portal. It also lists signed-binary `claude`, `fish`, `pi`,
and `zsh` defaults, marked `builtin` in Source; same-named project profiles win.
Builtin rows can be inspected/launched but cannot be edited, deleted, or cloned.

| key | action |
|---|---|
| `RET` / `i` | inspect resolved packages, egress, recipe/image/base |
| `r` | launch a session from the selected profile after an isolation/network summary |
| `e` | edit the profile in `safeslop.cue` |
| `c` | create a profile with structured prompts |
| `C` | clone the selected profile |
| `v` | validate the backing `safeslop.cue` |
| `D` | confirm, then delete through the validated CLI and refresh |
| `g` (Evil: `gr`) | refresh |

The intended flow is profile → inspect → launch → portal.  Inspect buffers are no
longer dead ends: `r`, `e`, and `C` act from the detail view too; `g` (Evil:
`gr`) re-fetches and re-renders the faced view, and `RET` returns to the list.
The compose buffer uses `RET` to edit its Name, Agent, Environment, Network, and
Workspace rows, and to toggle unlocked bundle/package rows; `?` shows row help,
`g` (Evil: `gr`) refreshes, `C-c C-c` previews/saves, and `q` cancels. `L` marks a
row that is included by its displayed source and cannot be partly toggled. Agent
changes recompute inherited default packages; compose remains creation-only, so it
cannot partially overwrite fields outside its UI. Toggle and refresh preserve the
logical row and scroll position in every showing window. When an agent has a catalog default, the `Automatic agent
bundle` row deliberately toggles the all-or-nothing automatic inclusion: disabling
it sends `--no-default-bundle`, keeps explicit selections, and can leave the agent
without its runtime so launch may fail. It does not change the isolation, network,
or workspace-only file boundary.

## Credentials

`M-x safeslop-credentials` (`C-c s K`) opens the **Credentials** surface. It
combines declared credential posture (`creds list --output json`) with
value-free account-link status (`creds status --output json`): account rows show
only forge, host/owner, non-secret GitHub App ids or Forgejo SSH port, probe
class, and TTL model — never token/key refs or values.

| key | action |
|---|---|
| `RET` / `i` | inspect one profile's credential posture (`creds show`) |
| `A` | link a GitHub App or Forgejo account after a value-free identity review |
| `U` | unlink a linked account (`host/owner`); profile declarations are unchanged |
| `R` | configure profile repos: existing provider/origin/read/write scopes are prefilled, then replaced after a before/after review |
| `X` | clear only a profile's GitHub/Forgejo scopes; account links and other providers remain |
| `e` | edit the CUE block directly |
| `g` (Evil: `gr`) | refresh account status and credential readiness |

These uppercase action keys work in raw Emacs and Evil normal state; lowercase
`a`/`u`/`p` remain raw-only compatibility aliases. First-time flow: create or
clone a project profile in `F`, then `A` link account → `R` assign origin or
explicit repositories → inspect/re-trust the changed policy → launch. `R` fetches
the project profile list independently of credential rows, so an empty profile is
selectable. It writes through `safeslop profile credentials set --output json`;
`X` uses `profile credentials clear`. Emacs never rewrites CUE itself. Setting one
forge clears only the opposite forge declaration and preserves other credential
providers. Failed `A`/`R` writes keep value-free drafts; return with `K`, then
press the same action to correct/retry. `U` warns that unchanged profile scopes
will fail staging until relinked or cleared with `X`. Live repo discovery remains deliberately deferred:
GitHub discovery would require a minted installation token and Forgejo discovery
would use the account-wide token, so the surface accepts origin inference and
manual `owner/repo` entries only.

Pi OAuth is inspection-only in the Emacs MVP: a row exposes only
`openai-codex/gpt-5.6-luna`, `access snapshot, short-lived`, provider-default
readiness/authority, and no ref, host path, token, account, or exact expiry. There
is no Pi OAuth mutation key. Use `e` to add the literal `credentials.pi` block to
a Pi/container/deny **project** profile, review and re-trust the exact policy
bytes, then create a new session. The fixed source accepts proven relative
same-HOME links and exact absolute same-HOME descendant links. HOME and every
reached directory must be current-user-owned with `mode & 0022 == 0` (`0755` is
valid); the ultimate leaf remains regular `0600`, single-link, bounded, and on the
same mount. The lexical lock, descriptor read, and fresh full proof remain
mandatory; outside/ambiguous links, writable ancestry, mount crossings, and races
fail closed. Launch requires more than 15 minutes of access lifetime; the snapshot
has no refresh or renewal. Review the denied
`chatgpt.com:443` observation from the Sessions surface and grant only that
session. Stop/remove wipes local stage/tmpfs copies but does not revoke the
upstream bearer.

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

Under Evil, dashboard keys follow evil-collection convention (specs/0063):
`j`/`k`, `gg`/`G`, `/`+`n`, `f`, and `a` stay pure motions/searches; refresh is
`gr`, and the portal auto-refresh toggle is `ga`. Credentials uses universal
uppercase `A/U/R/X` actions in both raw and Evil modes, while raw Emacs retains
lowercase compatibility aliases. The raw (non-Evil) keymaps otherwise keep
single-key actions such as `g` refresh and portal `a` auto-toggle.

## Local UI compatibility matrix

Run `make test-emacs-ui-matrix` after changing safeslop Emacs surfaces, Doom
integration, or Evil bindings.  The matrix runs raw Emacs, a tiny Doom `map!`
shim, real Evil when locally available, Doom-shim+Evil, and an opt-in personal
config slot.  The real Evil slots auto-detect local straight/elpaca build dirs;
if needed, provide colon-separated load paths explicitly:

```sh
SAFESLOP_EVIL_LOAD_PATH="$HOME/.emacs.d/.local/straight/build-31.0.50/compat:$HOME/.emacs.d/.local/straight/build-31.0.50/goto-chg:$HOME/.emacs.d/.local/straight/build-31.0.50/undo-fu:$HOME/.emacs.d/.local/straight/build-31.0.50/evil" \
  make test-emacs-ui-matrix
```

The personal slot is never part of `make check` and never reads config files on
its own.  To opt in, provide the batch Emacs command prefix that loads your
config; the runner appends the repository load path and probe arguments and
redacts the command in logs:

```sh
SAFESLOP_UI_PERSONAL_CMD='emacs --batch -l ~/.emacs.d/init.el' make test-emacs-ui-matrix
SAFESLOP_UI_REQUIRE_PERSONAL=1 SAFESLOP_UI_PERSONAL_CMD='emacs --batch -l ~/.emacs.d/init.el' make test-emacs-ui-matrix
```
