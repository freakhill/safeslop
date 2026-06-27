# 0052 — Emacs cockpit parity

Restore the operator surfaces the SwiftUI cockpit had that the specs/0049
Emacs pivot dropped, now built honestly in Emacs on the daemonless CLI + the
specs/0075 async substrate.

## Status

Planned. Implements the W3 features tracked as #3, #5, #6, #7. Companion W2
task #4-Emacs (prompt for `--environment`/`--network` in `safeslop-session-new`)
is **separate** and not covered here, though it shares the portal's `Env`/`Net`
columns.

## Why

The SwiftUI cockpit (specs/0014, specs/0029) shipped an Installs tab, a
Create/Edit (profile CRUD) tab, per-profile isolation-tier chrome, and an
obvious "launch this" affordance. The Emacs pivot (specs/0049) deliberately
shed the Swift app and its gRPC control plane and rebuilt the operator view as
the `*safeslop portal*` dashboard plus discrete `C-c s` commands. Four parity
gaps remain:

- **#3** No obvious open/access affordance after creating a session. `n`
  creates and refreshes the portal, but never offers to open the new session;
  `safeslop-session-new` outside the portal just prints a JSON envelope.
- **#5** The portal colours only the **Status** cell. The `Env` column
  (host/sandbox/container/vm) is plain text, so the honest isolation-tier
  signal the Swift cockpit rendered as chrome is missing.
- **#6** No install/update surface. The engine exists (`internal/engine/install`,
  `internal/engine/uninstall`; CLI `install status|plan|apply|rollback`) but is
  unsurfaced in Emacs.
- **#7** No profile (policy) CRUD surface. Emacs has only
  `safeslop-policy-check-file` (validate). The engine has `policy.Presets()`
  and `internal/engine/policy`, but no list/create/edit/delete view.

## Current state (verified against source)

- `emacs/safeslop.el` — core: `safeslop--call-json-async` (the async substrate,
  specs/0075), `safeslop-doctor`, `safeslop-policy-check-file`,
  `safeslop--show-envelope-buffer`, and `safeslop-command-map` bound under
  `C-c s` (`P` portal, `d` doctor, `p` validate, `n` new, `a` attach, `l` list,
  `t` status, `s` stop, `r` reattach, `b` switch-buffer, `L` debug, `e` error,
  `?` help).
- `emacs/safeslop-portal.el` — `tabulated-list` dashboard; columns
  `Session|Agent|Env|Net|Status|PID|Age|Workspace`; `safeslop-portal--status-face`
  colours Status; auto-refresh timer (specs/0070); keymap
  `RET/o i k n g a d L ? q` + `R` reattach.
- `emacs/safeslop-session.el` — `safeslop-session-new` (prompts Agent +
  Workspace only), attach/reattach/list/status/stop, all async.
- `emacs/safeslop-doom.el` — Evil/Doom bindings mirror the portal + output maps.
- Engine source of truth for tiers: `policy.EnvTier(env)` →
  `host:"none"`, `sandbox:"mistake-guard"`, `container:"egress-allowlisted"`,
  `vm:"adversary-grade"`, each with an honest one-line `note`
  (`internal/engine/policy/policy.go:224`). `doctor --json` already emits these
  at `data.tiers.<env>.{tier,note}` via `doctorTiers()`.
- JSON contract is **not uniform**: `validate`/`session *` emit the
  `{schema_version,ok,data,warnings,errors}` envelope via `emitContract`, but
  `install status|plan|apply` and top-level `list` emit **raw** `json.Marshal`
  / `emitJSON`. The async substrate's parser (`safeslop-contract-*`) expects the
  envelope. This must be reconciled before #6/#7 can consume them cleanly.
- 6 presets exist (`internal/engine/policy/presets/*.cue`) exposed by
  `policy.Presets() []Preset{Name,Description,CUE}` — but **no CLI** lists them.

## Pinned design decisions

### D1 — Topology: three portal surfaces, one navigation model

The Emacs analogue of the Swift cockpit's tabs is **three sibling dashboard
buffers**, each its own major mode, sharing one navigation convention — not a
literal GUI tab-bar (kept dependency-free, idiomatic):

| Surface | Buffer | Mode | Feature |
|---|---|---|---|
| Sessions | `*safeslop portal*` (exists) | `safeslop-portal-mode` | existing + #5 + #3 |
| Install | `*safeslop install*` (new) | `safeslop-install-mode` | #6 |
| Profiles | `*safeslop profiles*` (new) | `safeslop-profiles-mode` | #7 |

Each surface renders a **textual tab strip** as its first legend line —
`Sessions │ Install │ Profiles` with the active label in `mode-line-emphasis`
(bold) and the others faced `link` — so the "which tab am I on" signal survives
grayscale (honest, colour-redundant). Shared navigation keys, identical in all
three modes: `P`→Sessions, `I`→Install, `F`→Profiles, `[`/`]`→prev/next. Each
surface keeps `g` refresh, `L` debug, `?` describe-mode, `q` quit. Global
additions: `C-c s I` and `C-c s F`.

### D2 — Keymap conventions

- Navigation keys (`P I F [ ]`) are **shared** across the three modes via a
  common `safeslop-surface-mode-map` parent keymap (new, in a small shared file
  `emacs/safeslop-surface.el`) that the three mode-maps inherit with
  `set-keymap-parent`. Action keys are per-surface.
- No existing portal binding changes. New keys only.
- Doom/Evil: every new mode + its action keys get an `evil-define-key 'normal`
  block in `safeslop-doom.el`, mirroring the existing portal block.

### D3 — Colour-by-isolation-tier (#5), honest and colour-redundant

- Colour the **existing `Env` text cell**, never replacing the text — so colour
  is redundant to the always-present label (satisfies specs/0031 non-colour
  danger channel and specs/0032 show-unrestricted-axes: the word carries the
  meaning, colour reinforces).
- Four themeable faces define a red→green danger ramp by isolation strength
  (most dangerous → safest), mirroring `policy.EnvTier` ordering
  `host < sandbox < container < vm`:
  `safeslop-tier-host` (red/error), `safeslop-tier-sandbox` (yellow/warning),
  `safeslop-tier-container` (green), `safeslop-tier-vm` (bold green).
- The honest tier **note** (the EnvTier caveat) is shown as the cell's
  `help-echo` (tooltip / `mouse-1` echo). Source of truth stays the engine:
  a constant `safeslop-portal--env-tiers` mirrors EnvTier's four
  `(env . (tier . note))` rows with a `;; keep in sync with policy.EnvTier`
  pointer, and a test asserts the mirror covers exactly host/sandbox/container/vm.
  (Enriching the note live from `doctor`'s `data.tiers` is a noted follow-up,
  deferred to avoid coupling portal render to a second async call.)
- Add a one-line tier legend under the shortcut legend so an operator can read
  the ramp without hovering.

### D4 — Engine = source of truth; make the JSON contract uniform

#6 and #7 must consume enveloped JSON through the async substrate. Rather than
teach Emacs to parse two shapes, add an **enveloped `--output json`** path to the
commands that lack one, leaving the legacy raw `--json`/`emitJSON` output intact
for back-compat:

- `install status|plan|apply` gain `--output json` emitting
  `emitContract(jsoncontract.OK(<existing payload as data>))`.
- A new `profile` command group exposes policy data enveloped:
  `profile list --output json` (data = profiles) and
  `profile presets --output json` (data = `policy.Presets()`).

The engine remains the single source of truth; Emacs never re-derives risk or
tier facts.

### D5 — Profile CRUD conservatism: CUE bytes are truth (specs/0029)

v1 of #7 does **not** machine-rewrite CUE blocks (fragile, guard-corrupting).
It offers:

- **list** — `profile list --output json` into a `tabulated-list`.
- **edit** — `RET`/`e` opens the active `safeslop.cue` via `find-file`, with a
  `validate`-on-save buffer-local hook surfacing lint into the echo area.
- **new-from-preset** — `n` pick a preset (`profile presets`), write its CUE to a
  chosen path, open it for editing, then `validate`.
- **validate** — `v` runs `safeslop-policy-check-file` on the active file.
- **delete** — `d` is **guided, not automated**: open the file at the profile
  block and prompt the operator to remove it, then `validate`. Automated
  block-level deletion is an explicit follow-up (see Out of scope).

## Constraints / off-limits

- **Async only.** Every new engine call goes through
  `safeslop--call-json-async`. Never add a synchronous `call-process` on the
  main thread (specs/0075). `safeslop--call-json` stays for parse-path tests only.
- **Pivot denylist.** `ci/pivot-denylist.sh` fails the build on the words
  `cockpit`, `grpc`, `proto`, `swift`, `vscode`, `Package.swift`, etc. anywhere
  outside `specs/**` and `STATUS.md`. **New Emacs/Go source and comments must not
  contain "cockpit"** — say "portal", "surface", or "dashboard". (This spec file
  is under `specs/` and is exempt.)
- **Do not modify** the async substrate (`safeslop--call-json-async`,
  `safeslop--finish-envelope`), the contract accessors (`safeslop-contract-*`),
  the existing portal columns/auto-refresh, or the `C-c s` bindings that already
  exist. New code is additive.
- **Honest labels.** Tier colour reinforces, never replaces, the text label. Do
  not invent tier names — use `policy.EnvTier`'s exact strings.
- **Hermetic tests.** Fake the CLI (the existing `safeslop-test--with-fake-cli`
  / fixture-envelope helpers); no real `install apply`, no network, no real CUE
  files outside `testdata`.

## Tasks

Ordered by dependency. Engine envelope tasks (E*) unblock the Emacs surfaces.
Within Emacs, the shared topology (M0) precedes the surfaces that depend on it.

### Engine — uniform enveloped JSON

- [ ] **E1. Enveloped `--output json` for `install status|plan|apply`**
  FILE: `internal/cli/cli.go` (the `cmdInstall` subcommands ~L961–1086),
        `internal/cli/cli_install_test.go` (new)
  CHANGE: Add an `--output` string flag (values `json`) to each of `install
        status`, `install plan`, `install apply` mirroring the session commands.
        When `--output json`, emit `emitContract(jsoncontract.OK(data))` where
        `data` is the existing payload struct (`install.Status`, the plan
        actions, the apply event stream → for `apply`, accumulate events into a
        `data.events` array and emit one terminal envelope, or emit `emitContractLine`
        per event for `jsonl`). Leave the legacy `--json` raw path untouched.
  VERIFY: `go test ./internal/cli/ -run Install.*Envelope -v`
  EXPECTED: PASS; a test asserts the first stdout char is `{` and the parsed
        object has `schema_version`, `ok:true`, and `data` carrying the status struct.

- [ ] **E2. `profile list --output json` (enveloped)**
  FILE: `internal/cli/cli.go` (new `cmdProfile()` group + wire into root),
        `internal/cli/cli_profile_test.go` (new)
  CHANGE: Add a `profile` command group. `profile list [safeslop.cue] --output
        json` loads via `policy.Load`, then `emitContract(jsoncontract.OK(map{
        "path":path, "profiles":cfg.Profiles}))`. (Top-level `list` raw output
        stays for back-compat.)
  VERIFY: `go test ./internal/cli/ -run ProfileList -v`
  EXPECTED: PASS; enveloped output, `data.profiles` keyed by profile name with
        `agent/environment/network` fields.

- [ ] **E3. `profile presets --output json` (enveloped)**
  FILE: `internal/cli/cli.go` (add to `cmdProfile()`),
        `internal/cli/cli_profile_test.go`
  CHANGE: `profile presets --output json` → `emitContract(jsoncontract.OK(map{
        "presets": policy.Presets()}))` (each `{name,description,cue}`).
  VERIFY: `go test ./internal/cli/ -run ProfilePresets -v`
  EXPECTED: PASS; `data.presets` is a 6-element array including
        `claude-sandbox-offline` and `claude-vm-disposable` with non-empty `cue`.

### Emacs — shared topology (M0)

- [ ] **M0a. Shared surface keymap + tab strip helper**
  FILE: `emacs/safeslop-surface.el` (new), `emacs/safeslop.el` (`require` it)
  CHANGE: Define `safeslop-surface-mode-map` (parent keymap) binding `P`/`I`/`F`
        to `safeslop-portal`, `safeslop-install`, `safeslop-profiles`, and
        `[`/`]` to `safeslop-surface-prev`/`safeslop-surface-next` (cycle the
        three). Define `safeslop-surface--tab-strip (active)` returning the
        `Sessions │ Install │ Profiles` line (active label bold via
        `mode-line-emphasis`, others `link`) + trailing blank line. No I/O.
  VERIFY: `emacs --batch -L emacs -l emacs/safeslop-surface.el --eval '(princ (safeslop-surface--tab-strip (quote install)))'`
  EXPECTED: prints a line containing `Sessions`, `Install`, `Profiles`.

- [ ] **M0b. Portal inherits the shared map + renders the tab strip**
  FILE: `emacs/safeslop-portal.el`
  CHANGE: `(set-keymap-parent safeslop-portal-mode-map safeslop-surface-mode-map)`;
        in `safeslop-portal--render`, prepend `(safeslop-surface--tab-strip 'sessions)`
        above the existing shortcut legend. Adjust the post-render
        `forward-line` offset so point still lands on the first session row.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: existing portal tests still pass (0 unexpected).

### Emacs — #5 isolation-tier colour

- [ ] **M1a. Tier faces + EnvTier mirror constant**
  FILE: `emacs/safeslop-portal.el`
  CHANGE: `defface` `safeslop-tier-host` (inherits `error`), `-sandbox`
        (`warning`), `-container` (`success`), `-vm` (`success` + `:weight bold`).
        Define `safeslop-portal--env-tiers` const: an alist of the four
        `(env . (tier-label . note))` rows copied verbatim from
        `policy.EnvTier` with a `;; keep in sync with internal/engine/policy/policy.go EnvTier`
        comment.
  VERIFY: `emacs --batch -L emacs -l emacs/safeslop-portal.el --eval '(princ (length safeslop-portal--env-tiers))'`
  EXPECTED: prints `4`.

- [ ] **M1b. Colour the Env cell + help-echo + tier legend**
  FILE: `emacs/safeslop-portal.el`
  CHANGE: Add `safeslop-portal--env-cell (env)` returning `env` propertized with
        the matching tier face and `help-echo` = the EnvTier note. Use it in
        `safeslop-portal--rows` for the `Env` column. Add
        `safeslop-portal--tier-legend` (host=red … vm=green) appended after the
        shortcut legend in `--render`.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: PASS incl. a new test asserting `safeslop-portal--env-cell` returns
        distinct faces for `host` vs `vm` and a non-empty `help-echo`.

### Emacs — #3 post-create open affordance

- [ ] **M2a. Offer to open a just-created session**
  FILE: `emacs/safeslop-session.el`
  CHANGE: In `safeslop-session-new`'s callback, when the envelope is ok and
        carries `data.session_id`, after showing the envelope prompt
        `(y-or-n-p "Open session <id> now? ")`; on yes call
        `safeslop-session-attach` with that id. Guard so the test `callback`
        path (non-interactive) does not prompt — only prompt when called
        interactively (check `(called-interactively-p 'any)` captured at call time,
        or gate on a new optional `open` arg defaulting to interactive).
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: PASS incl. a test that a fixture create envelope with
        `data.session_id` drives the open path (fake attach recorded) without
        a real prompt.

- [ ] **M2b. Portal `n` lands point on the new session**
  FILE: `emacs/safeslop-portal.el`
  CHANGE: `safeslop-portal-new` — after `safeslop-portal-refresh`, move point to
        the row whose id equals the newly created `session_id` (thread it out of
        `safeslop-session-new` via its callback) so the freshly created session
        is selected and obviously openable with `RET`.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: PASS; existing portal tests green.

### Emacs — #6 install/update surface

- [ ] **M3. `safeslop-install.el` — install/update dashboard**
  FILE: `emacs/safeslop-install.el` (new), `emacs/safeslop.el` (`require`),
        `emacs/test/safeslop-test.el`
  CHANGE: `safeslop-install-mode` (`tabulated-list`, parent map
        `safeslop-surface-mode-map`) listing tools from `install status --output
        json` (columns `Tool|Kind|Current|Desired|State`), all via
        `safeslop--call-json-async`. Keys: `g` refresh, `p` plan (show pending in
        an envelope buffer), `x` apply (after `yes-or-no-p`, async), `D`
        dry-run (`apply --dry-run --output json`), `b` rollback the tool at point
        (`install rollback <tool>`, confirm). Render the tab strip
        `(safeslop-surface--tab-strip 'install)` + a shortcut legend. No "cockpit"
        token anywhere (denylist).
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: PASS incl. a test that a fixture `install status` envelope renders N
        tool rows and that `x` is wired to `install apply` argv.

### Emacs — #7 profile CRUD surface

- [ ] **M4. `safeslop-profiles.el` — policy CRUD dashboard**
  FILE: `emacs/safeslop-profiles.el` (new), `emacs/safeslop.el` (`require`),
        `emacs/test/safeslop-test.el`
  CHANGE: `safeslop-profiles-mode` (`tabulated-list`, parent map) listing
        profiles from `profile list --output json` (columns
        `Profile|Agent|Env|Net`, Env coloured via the shared
        `safeslop-portal--env-cell`). Keys: `g` refresh, `RET`/`e` edit
        (`find-file` the active `safeslop.cue`; add a buffer-local
        `after-save-hook` running `validate`), `n` new-from-preset (read a preset
        from `profile presets --output json`, write its `cue` to a
        `read-file-name` path, open + validate), `v` validate, `d` guided delete
        (open file + message instructing block removal, then validate). Tab strip
        `(safeslop-surface--tab-strip 'profiles)` + legend. Async throughout.
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: PASS incl. tests: fixture `profile list` envelope renders profile
        rows with coloured Env; `n` reads `profile presets` and offers the 6 names.

### Glue, Doom, docs, gate

- [ ] **M5. Global `C-c s` + Doom/Evil bindings for the new surfaces**
  FILE: `emacs/safeslop.el` (`safeslop-command-map`: `I`→`safeslop-install`,
        `F`→`safeslop-profiles`; update `safeslop-help` string),
        `emacs/safeslop-doom.el` (autoloads + `evil-define-key 'normal` blocks for
        `safeslop-install-mode-map` and `safeslop-profiles-mode-map`, mirroring the
        portal block; bind `P/I/F/[/]` there too)
  VERIFY: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -f ert-run-tests-batch-and-exit 2>&1 | tail -3`
  EXPECTED: PASS incl. the existing keymap test extended to assert `I` and `F`
        resolve in `safeslop-command-map`.

- [ ] **M6. Docs: STATUS.md + README Emacs section**
  FILE: `STATUS.md`, `README.md` (Emacs usage section)
  CHANGE: Document the three surfaces, the `P/I/F/[/]` navigation, the
        tier-colour legend, and the install/profiles keys. (STATUS.md is denylist-
        exempt; README is **not** — do not write "cockpit" in README.)
  VERIFY: `ci/pivot-denylist.sh && echo OK`
  EXPECTED: prints `OK` (no forbidden token introduced).

- [ ] **M7. Full gate**
  FILE: —
  CHANGE: none.
  VERIFY: `make check && make build`
  EXPECTED: both succeed — asset sync, pivot denylist, `go vet`, `gofmt`,
        `go test ./...`, and the Emacs ERT suite all green.

## Verification summary

- Engine: `go test ./internal/cli/ -run 'Install.*Envelope|Profile' -v` green.
- Emacs: `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -f ert-run-tests-batch-and-exit` → 0 unexpected.
- Denylist: `ci/pivot-denylist.sh` clean (no "cockpit"/"grpc"/… in new source).
- Whole gate: `make check && make build`.
- Manual smoke (after `make install-emacs` + reload): `C-c s P` then `I`/`F`/`[`/`]`
  cycle the three surfaces; portal `Env` cells are tier-coloured with hover notes;
  `n` in the portal selects the new session and offers to open it.

## Out of scope / follow-ups

- #4-Emacs (environment/network prompt in `safeslop-session-new`) — separate W2 task.
- Automated CUE block-level profile **delete**/edit (machine-rewriting guards) —
  deferred; v1 delete is guided-manual.
- Live tier-note enrichment from `doctor`'s `data.tiers` instead of the Emacs
  mirror constant.
- A visual `tab-line`/`tab-bar` strip (vs the textual one) — polish only.
- Secrets/break-glass arbiter view (Swift cockpit S4/arbiter pane) — not a W3
  parity item; track separately if wanted.
