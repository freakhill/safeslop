# 0063 — Emacs UI ergonomics follow-ups (F1–F11)

SCOPE: implement the ergonomics findings deferred by specs/0062 — F1..F10 —
plus one new finding from operator use (2026-07-02):

- F11: the session detail view and profile inspect view print `Environment:`
  as plain text; the isolation-tier colour/help-echo channel (specs/0031)
  exists only on the dashboard table cells and legends. Detail views must
  reuse `safeslop-surface--env-cell` / `--net-cell` so the danger ramp is
  present everywhere an environment is named.

The F2 cross-surface key redesign was delegated to the agent (jojo:
"2. your choice", 2026-07-02) and is fixed by the scheme below.

OFF-LIMITS:
- No weakening of isolation/network defaults, prompts for world-changing
  actions, or honest labels.
- Never bind global `C-c s D`; the `C-c s` command map itself is unchanged.
- Public buffer names, command names, and CLI argv stay identical; only
  keybindings, prompts, legends, sort order, and rendering change. Moved
  keys keep their command names (`safeslop-portal-stop' stays the command;
  only its key moves), so user config keeps working.
- Session NAMING (create --name / rename) is specs/0065 (engine+CLI+UI),
  not smuggled in here (specs/0064 is the pi container $HOME fix). F7's
  annotation function is written so a `name` field, when the record gains
  one, is displayed automatically.

WORKTREE: `.worktrees/feat/emacs-ergonomics-followups/`
BRANCH: `feat/emacs-ergonomics-followups`

## Key scheme (resolves F1 + F2)

Principles, in priority order:

1. One meaning per key: across the three TAB-cycled surfaces a semantic key
   either performs the same verb class everywhere or is bound on exactly one
   surface. No key may change risk class between surfaces (the F2 hazard:
   `x` = cleanup on Sessions but LAUNCH on Profiles).
2. Evil normal state never shadows core motions (evil-collection
   convention): `j k g n f a` are left unbound in the Evil tables so
   j/k/gg/G//, n/N, f, a work; refresh is `gr`, portal auto-refresh toggle
   is `ga`. Raw keymaps keep `g` (special-mode convention) and `a`.
   Operator keys that are Evil edits-but-not-motions (x s c i o r v u D R A)
   are fair game in these read-only buffers, per evil-collection practice.
3. Risk classes keep distinct affordances: world-changing verbs (r R s D)
   confirm with `yes-or-no-p` + a danger summary; record cleanup (x X)
   downgrades to `y-or-n-p` (F6); read-only verbs (RET i v p g) never
   confirm.

| key   | Sessions                                | Install                 | Profiles              |
|-------|-----------------------------------------|-------------------------|-----------------------|
| RET/o | state-aware open (run branch confirms, F4) | —                    | inspect (RET)         |
| i     | details                                 | —                       | inspect               |
| r     | run created session, confirmed (new)    | apply (was `x`)         | launch (was `x`)      |
| R     | run detached (was `D`)                  | —                       | —                     |
| A     | reattach (was `R`)                      | —                       | —                     |
| s     | stop/revoke (was `k`)                   | —                       | —                     |
| x / X | remove / prune (now `y-or-n-p`)         | unbound (was apply)     | unbound (was launch)  |
| D     | unbound (was detach)                    | unbound (was dry-run)   | delete (unchanged)    |
| v     | —                                       | dry-run (was `D`)       | validate (unchanged)  |
| u     | —                                       | rollback (was `b`)      | —                     |
| c     | new session (was `n`)                   | —                       | create (was `n`)      |
| C     | —                                       | —                       | clone (was `c`)       |
| ^     | jump to backing profile (was `f`)       | —                       | —                     |
| p     | —                                       | plan (unchanged)        | —                     |
| g     | refresh — raw only; Evil `gr`           | same                    | same                  |
| a     | auto-refresh toggle — raw only; Evil `ga` | —                     | —                     |

Mnemonics: r/R = run coupled/detached, A = attach, s = stop, v = verify
(validate/dry-run share the read-only-check class), u = undo (rollback),
c/C = create/clone, ^ = up to the parent profile (dired convention).

Detail/inspect buffers: `g` (Evil `gr`) now REFRESHES the same faced view
(F5); RET in a profile-inspect buffer keeps meaning "back to list". The
session detail keymap gains `r`/`R`/`A`/`s` consistent with the portal.

## Per-finding design notes

- F3 (portal sort): rank rows running < created < detached-running (same
  rank as running) < stopped < failed/exited/error/cancelled < unknown;
  tie-break `string<` on session id for a deterministic, testable order.
  Implemented as a pure `safeslop-portal--status-rank` + sort predicate.
- F4 (run confirm): one shared `safeslop-session--confirm-run` takes
  (context agent environment network) and shows the same
  `safeslop-profiles--danger-summary` text (move that helper to
  safeslop-surface as `safeslop-surface--danger-summary`; profiles keeps an
  alias). Called by: portal RET run-branch, portal `r`, profiles launch.
  `C-c s a` (explicit attach command) is unchanged.
- F5 (refresh semantics): session-detail buffers get their own mode map +
  `safeslop-session-detail-refresh` that re-fetches `session status` and
  re-renders via `safeslop-session--detail-format` (not the raw envelope
  dump). Profile inspect `g` re-fetches `profile show` and re-renders the
  inspect view; "back" stays on RET.
- F6: `safeslop-portal-remove` / `-prune` use `y-or-n-p`. Stop, detach,
  install-apply, delete, and all launch confirms stay `yes-or-no-p`.
- F7: `safeslop-session--read-id` builds candidates from the full session
  alists and installs an annotation function showing
  `agent · status · workspace` (and any `name` field when present, for
  specs/0065). Pure candidate/annotation builders are unit-tested.
- F8: `safeslop-switch-to-session-buffer` offers ALL live safeslop buffers
  (portal/install/profiles surfaces, output/result buffers, session
  terminals, detail/inspect, progress, debug) via completing-read, most
  recent first; still binds `C-c s b`. `safeslop-help` echo line is
  regenerated from the real command map so it cannot drift again.
- F9: `safeslop--call-json-async` always attaches a private stderr buffer
  when the caller passed none; on failure (nonzero status or non-JSON
  stdout) the first stderr line is folded into the error envelope message
  and the debug log (`stderr=` field); the buffer is killed either way.
  The progress-runner path (explicit stderr buffer) is unchanged.
- F10: `make test-emacs` gains a byte-compile gate: batch-byte-compile all
  emacs/*.el, failing on ERRORS (warnings stay advisory — warning sets
  differ across the local floor vs CI-pinned Emacs); .elc artifacts are
  written to a temp dir so the tree stays clean. `SAFESLOP_ELISP_WERROR=1`
  escalates warnings for local hardening runs.
- F11: `safeslop-session--detail-format` renders Environment via
  `safeslop-surface--env-cell` and Network via `--net-cell`;
  `safeslop-profiles--inspect-format` does the same for its Environment/
  Network lines. Text labels stay; colour + help-echo become redundant
  reinforcement (specs/0031).

## Tasks

- [ ] Task 1 — Key tables: portal/install/profiles mode maps, detail/inspect
  maps, the two Doom Evil tables (shared + per-mode; add gr/ga, drop
  motion-shadowing keys), all `--key-hints` legends, docstrings, and the
  empty-state hints (`n` -> `c`). Update every ERT test asserting old keys,
  legends, or prompts; add binding regression tests for the new scheme and
  an Evil-table test asserting j/k/g/n/f/a are absent.
  VERIFY: `make test-emacs`
- [ ] Task 2 — Behaviour: F4 shared confirm, F5 refreshes, F11 tier cells,
  F3 sort, F6 prompt weights. New tests: status-rank order, run-confirm
  invoked on the portal run path (fixture-driven), detail refresh renders
  faced view, env cell present in detail/inspect output.
  VERIFY: `make test-emacs`
- [ ] Task 3 — Plumbing: F7 annotated completion, F8 buffer switcher +
  regenerated help, F9 stderr separation (+ debug log field), F10 Makefile
  gate. Tests for candidates/annotations, switcher listing, stderr folding
  (fake CLI script fixture).
  VERIFY: `make test-emacs && make build`
- [ ] Task 4 — Docs sync: emacs/README.md key tables (with an Evil column
  where raw/Evil differ: g/gr, a/ga), Portal/Profiles/Install sections,
  Doom section; README.md + skills/ grep for stale key references.
  VERIFY: `make check && make build`

EXPECTED END STATE: full ERT suite green (104 existing, some rewritten, plus
new), Go untouched, one comprehensive commit on the branch, merged --no-ff
into main after jojo's review of the scheme in practice.
