# 0062 — Emacs UI ergonomics review + modularity refactor

SCOPE:
- An ergonomics review of the Emacs operator UI (findings recorded below; most
  behavior changes deferred to explicit follow-ups).
- A behavior-preserving modularity refactor of the Emacs package: extract the
  CLI substrate and output rendering into their own modules, share one
  dashboard render engine across the three surfaces, and delete the
  duplication that accreted over specs/0052–0061.
- Two narrow behavior alignments (not new design, code catching up to the
  already-documented contract in `emacs/README.md`):
  1. Persistent in-buffer error banners + empty-state + loading hints on ALL
     three surfaces (today only Profiles has them; the shared helpers
     `safeslop-surface--empty-state` / `--loading` are dead code).
  2. Fix the `safeslop-surface--breadcrumb-title` anchor typo (`"\`/"` is the
     two-char regexp "backtick slash", so absolute paths are never excluded
     and output-buffer titles render as e.g. `validate /Users/x/safeslop.cue`).

OFF-LIMITS:
- No keybinding changes and no new commands: key ergonomics findings (Evil
  `k`-conflict, cross-surface `x`/`D` semantics, sort order, y-or-n-p
  downgrades) are recorded as follow-ups, not smuggled into this refactor.
- Do not weaken network/isolation defaults or host/container boundaries.
- No runtime dependencies outside the Go binary / existing Emacs package.
- Do not bind global `C-c s D` (existing tests keep it unbound).
- Public command names, keymaps, buffer names, and CLI argv stay identical.

WORKTREE: `.worktrees/refactor/emacs-ui-modularity/`
BRANCH: `refactor/emacs-ui-modularity`

## Ergonomics review — condensed findings

Fixed by this spec (all code-vs-docs drift or outright bugs, not redesign):

- E1 Persistent error banner promised by `emacs/README.md` ("Error output is
  persistent in-buffer text rather than only an echo-area flash") exists only
  on Profiles; Portal and Install flash the echo area and leave a bare table.
- E2 `safeslop-surface--empty-state` and `safeslop-surface--loading` are
  unused; Portal/Install show a blank buffer while the first fetch is in
  flight and an unexplained empty table when there are no rows.
- E3 Breadcrumb title anchor typo (see SCOPE) leaks absolute paths into
  output-buffer titles.
- E4 `when-let` is obsolete on the Emacs 31/32 line (two warnings at load).

Deferred follow-ups (behavior changes; each needs its own decision):

- F1 Evil normal-state `k` on the portal fires stop-session instead of
  moving up a row; `n`/`f`/`a`/`g` similarly shadow core Vim motions.
  evil-collection convention would keep j/k motions and put refresh on `gr`.
- F2 Cross-surface key semantics diverge: `x` = remove (Portal) / launch
  (Profiles) / apply (Install); `D` = detach (Portal) / delete (Profiles) /
  dry-run (Install). Muscle memory across TAB-cycled surfaces is unsafe-ish
  (all are confirmed, but the prompts differ wildly).
- F3 Portal sort is alphabetical by status string; a lifecycle rank
  (running, created, stopped, failed…) with a stable secondary key would put
  actionable rows first.
- F4 Running a *created* session (RET) has no isolation/network risk summary,
  while launching the same config from Profiles (`x`) does.
- F5 `g` in a session-detail buffer re-renders as the raw envelope dump,
  losing the faced detail view; `g` in a profile-inspect buffer means "back"
  instead of refresh.
- F6 Prompt weight: every row action uses full-word `yes-or-no-p`; `x`/`X`
  on already-stopped records could be `y-or-n-p` (keep full-word for stop /
  detach / install-apply).
- F7 Session-id completion (`C-c s a/s/r/t`) offers bare ids; annotate
  candidates with agent/status/workspace.
- F8 `safeslop-switch-to-session-buffer` (`C-c s b`) only knows the doctor /
  validate buffers; `safeslop-help` echo line drifted from the real map.
- F9 stderr of non-progress commands shares the stdout buffer; noisy stderr
  would break envelope parsing (route :stderr separately in the client).
- F10 Byte-compile gate in `make test-emacs` (kept out of this pass: warning
  sets differ across the local floor vs CI-pinned Emacs).

## Module design

Dependency graph after the refactor (strictly one direction; the only upward
references are key-bound entry commands, late-bound by symbol):

```
safeslop-contract.el   envelope parse/validate (unchanged)
safeslop-client.el     NEW: defgroup, safeslop-program, last-error, debug log,
                       sync/async CLI runners, error/finish envelope,
                       safe-rerun-p
safeslop-surface.el    shared nav (tab strip, cycling, view capture/restore)
                       + shared cells/faces (net + isolation tiers, legends)
                       + banners (error/empty/loading)
                       + NEW shared dashboard render engine
safeslop-output.el     NEW: envelope -> read-only buffer rendering,
                       safeslop-output-mode, safe `g` re-run
safeslop-session.el    session commands, terminal launch, detail view
safeslop-portal.el     Sessions dashboard (thin: rows + actions + timer)
safeslop-install.el    Install dashboard (thin)
safeslop-profiles.el   Profiles dashboard + CRUD (no longer requires portal)
safeslop.el            entry: requires, doctor/validate commands, command map
safeslop-doom.el       optional shim; Evil bindings become data-driven tables
```

Key moves:

- Isolation-tier faces/cells/legends move portal -> surface (this deletes the
  sideways `profiles -> portal` require). `safeslop-tier-host/container` face
  names are load-bearing (tests, user themes) and do not change; portal keeps
  `safeslop-portal--env-face/--env-cell/--tier-legend/--goto-id` as aliases.
- One `safeslop-surface-render` engine owns: capture views -> set entries ->
  print -> re-insert header -> error banner / empty state -> restore views /
  goto kept row / run THEN. The 0061 cursor-jump fix then lives in exactly
  one place instead of three hand-copied variants.
- `safeslop-portal--refresh-in-flight` generalizes to a buffer-local
  `safeslop-surface--refresh-in-flight` set by the engine for every surface;
  the portal timer keeps consulting it.
- `safeslop--call-json-async` gains an optional STDERR-BUFFER arg; the
  session progress runner becomes a thin wrapper instead of a second copy of
  the spawn/parse/error path.
- Doom Evil bindings: one shared-keys table + per-mode action tables, applied
  in a loop (the four hand-maintained copies drift today — that is how
  Install/Profiles briefly lost keys in 0060).

## Tasks

- [x] Task 1 — Extract `safeslop-client.el` and `safeslop-output.el`; slim
  `safeslop.el` to the entry module. Pure moves; function names unchanged;
  every `declare-function` backreference into safeslop.el replaced by a
  `require` where the direction allows it.
  VERIFY: `make test-emacs`

- [x] Task 2 — Surface engine + shared cells. Move tier cells/legends into
  surface; add `safeslop-surface--legend`, `--goto-first-row`, buffer-local
  in-flight flag, and `safeslop-surface-render`; fix E3 anchor + E4
  `when-let*`; port Portal/Install/Profiles onto the engine (E1/E2 banners
  come with it). Portal keeps compatibility aliases.
  VERIFY: `make test-emacs`

- [x] Task 3 — Session/doom cleanups: client stderr param + progress runner
  dedup; data-driven Evil binding tables.
  VERIFY: `make test-emacs`

- [x] Task 4 — Tests + docs sync. Mechanical test updates (in-flight var
  rename; portal error test now asserts the persistent banner). New tests:
  breadcrumb path exclusion, portal/install error banner, portal empty state,
  loading hint on first open. `emacs/README.md` gains the module map; banner
  text now true for all surfaces.
  VERIFY: `make check && make build`
  EXPECTED: full ERT suite green (100 existing + new), Go untouched and green.
