# CLAUDE.md

Per-repo guidance for Claude Code working in `agentic_tactical_boots`.

## Read before editing

This repo already has detailed contracts. Skim them, don't duplicate them:

- `AGENTS.md` — agent operating contract and done-checklist.
- `scripts/CONVENTIONS.md` — required `--help` shape, AUTOGEN markers,
  `here` shortcuts, comment style, parameter naming, safety defaults.
- `CONTRIBUTING.md` — contributor expectations.
- `README.md` — user-facing reference; the source of truth for examples.

The conventions file in particular tells you the exact help-text layout
every script must follow and how `slop-sync-help` keeps `--help` in sync
with `README.md`. Read it before adding a new script or changing one.

## Stack at a glance

- **Fish** for every CLI script in `scripts/*.fish` (sourced as conf.d
  modules, no `+x` bit, no shebang reliance).
- **Python via `uv`** for helpers in `scripts/_py/*.py`. PEP-723 inline
  metadata pins versions; fish wrappers call `uv run --script <file>`.
  Never reintroduce bare `python3 -c '...'`.
- **CUE** drives `slop-isolate`'s policy compiler (presets in
  `library/isolation/presets/`, fixtures in `library/isolation/fixtures/`).
- **Textual** is the only Python runtime dep; lives in
  `scripts/_py/slop_tui.py` (the global `slop` launcher).
- **Tests** are fish-based in `tests/`. Run with `fish tests/run.fish`.

## Verify your changes

Always run before declaring done:

```fish
fish -n scripts/*.fish              # syntax
fish tests/run.fish                 # full suite (~22 files today)
fish scripts/slop-sync-help.fish check   # README ↔ --help drift gate
fish scripts/slop-pinning.fish      # no `latest` in pinned files
```

CI runs all four. The Textual launcher has two diagnostic entry points
worth knowing:

```fish
env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
    uv run --script --quiet scripts/_py/slop_tui.py --audit
# Walks every action in build_top_actions(), checks no leaf calls a
# legacy `slop-X tui` shellout, no leftover `{N}` placeholders, and
# every fish_sub verb is accepted by its target script's dispatcher.

env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
    uv run --script --quiet scripts/_py/slop_tui.py --mount-check
# Drives the App through Textual's headless run_test() driver. Catches
# mount-time crashes (e.g. reactive watchers firing before compose).
```

## Fish-specific landmines I keep stepping on

These are not documented elsewhere. When you write fish, remember:

- **`echo "a\tb"` prints LITERAL `\t`.** Fish's echo does not interpret
  escapes. Use `printf 'a\tb\n'` for a real tab. Search the repo for
  `printf 'id\\taccess` for the canonical pattern.
- **`string split "\t" --` does NOT split real tabs.** It splits on the
  literal two-character `\t` sequence. Use unquoted `\t`:
  `string split \t -- "$line"`.
- **`string trim` exits 1 when there is nothing to trim.** Combined with
  `string collect < file` (which strips one trailing newline), the exit
  code 1 propagates through `set -g foo (string trim ...)` and silently
  fails the enclosing function. Always end such functions with an
  explicit `return 0`. See the comment block in
  `__llm_gh_generate_key` (`scripts/slop-gh-key.fish`) for the bug
  shape — it cost a real-world "create + list shows nothing" outage.
- **`(cd ...; pwd)` is not a subshell in fish.** Command substitution
  runs in the *same* shell, so the `cd` mutates the caller's cwd. Use
  `(path resolve (dirname (status filename)))` for path discovery.
- **`set -l x (cmd)` propagates `cmd`'s exit status to `$status`.** This
  is the opposite of bash. Useful for chained checks; don't be fooled
  by reading `$status` *after* an enclosing builtin and assuming it's
  the builtin's status.

## Adding a new top-level Textual menu entry

`scripts/_py/slop_tui.py:build_top_actions()` is the registry. Pattern
to follow (mirror an existing factory like `build_gh_key_actions`):

1. Write a `build_<thing>_actions()` factory returning `list[Action]`.
   Use `fish_tool=` + `fish_sub=` for sourced-tool dispatch (preferred);
   `argv=` only for direct script invocations.
2. Add a top-level `Action` in `build_top_actions()` with `submenu=`
   pointing at the factory. Pick an unused single-letter `key` (taken
   today: `a g f r s d D b z i k v R`).
3. Run `--audit` — it will fail fast if any verb you pass isn't
   accepted by the target script's `case` / `if test "$cmd" = "..."`
   dispatch.
4. Run `--mount-check` — drives compose/mount under run_test() to
   catch widget-lookup races (the `init=False` reactive on
   `MenuScreen.filter_text` exists because of one).
5. Add a fish-side test in `tests/` if the new flow has a script-side
   regression surface. See `test_slop_agents.fish` for a recent shape.

## Subprocess + ctty: don't reintroduce `subprocess.call`

`run_subprocess` in `slop_tui.py` goes through `_spawn_with_ctty`, not
`subprocess.call`. The helper does the shell-style fork → setpgid →
tcsetpgrp dance so interactive children (zsh, vi, claude) can claim
terminal foreground without the macOS "can't set tty pgrp: operation
not permitted" failure. Static guard in `tests/test_slop.fish`. Don't
revert.

## TLS-intercepting proxies (`UnknownIssuer` from uv)

`scripts/slop.fish` ships a four-strategy fallback for first-run uv
installs: rustls defaults → `UV_NATIVE_TLS=1` → `SSL_CERT_FILE` →
`--allow-insecure-host` (gated on `SLOP_INSECURE_HOSTS=1`). If you
add a new uv-driven entry point and it fails on a corporate-WARP /
Zscaler / similar setup, mirror that strategy chain.

## Python memory rule (already in user memory)

All Python work goes through `uv` for isolated, repeatable runs. Don't
suggest installing packages globally with pip; PEP-723 metadata in the
script is the canonical place to add a dep.

## Don't push directly to `main`

The sandbox blocks `git push origin main`. Either branch + PR, or hand
back to the user with the commits sitting locally — a `! git push origin
main` from the prompt works for them.

## Where the load-bearing code lives

- `scripts/_py/slop_tui.py` — Textual app, action model, audit, mount
  check, ctty helper. ~1400 lines; touch carefully.
- `scripts/slop.fish` — thin wrapper around the Textual app, including
  the layered TLS fallback. Don't put logic here that belongs in the
  Python module.
- `scripts/slop-isolate.fish` + `scripts/_py/slop_isolate/` — CUE
  policy compiler. The `here` shortcut convention originated here.
- `scripts/slop-gh-key.fish` / `slop-forgejo-key.fish` /
  `slop-radicle.fish` — credential lifecycle. Repo-uniqueness tests
  in `tests/test_slop_*.fish` document the three layers' scoping.
- `scripts/slop-agents.fish` — host-side launchers for Claude Code /
  OpenCode with bundled defaults; `seed` is non-clobbering and the
  fixtures are the slop-isolate compile output.
