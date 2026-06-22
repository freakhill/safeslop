# CLAUDE.md

Per-repo guidance for Claude Code working in `safeslop`.

## Read before editing

This repo already has detailed contracts. Skim them, don't duplicate them:

- `AGENTS.md` — agent operating contract and done-checklist.
- `scripts/CONVENTIONS.md` — required `--help` shape, AUTOGEN markers,
  `here` shortcuts, comment style, parameter naming, safety defaults.
- `CONTRIBUTING.md` — contributor expectations.
- `README.md` — user-facing reference; the source of truth for examples.
- `specs/` — the Go rewrite: `0001` (architecture design) and `0002`+
  (per-sub-project plans/records). The repo is migrating from this fish/Python
  stack to a single signed Go binary; read `specs/0001` before engine work.

The conventions file in particular tells you the exact help-text layout
every script must follow and how `slop-sync-help` keeps `--help` in sync
with `README.md`. Read it before adding a new script or changing one.

## Stack at a glance

This repo is mid-migration (strangler) from a fish + Python(uv) toolkit to a
single signed **Go binary** (`safeslop`). Both stacks are live; new engine work goes
into Go (see `specs/0001`). The fish/Python notes below still govern the existing
`scripts/` stack until each piece is ported.

- **Go** is the engine + CLI: `cmd/safeslop` (thin cobra entry) over
  `internal/engine/*` — `policy` (embedded CUE via `cuelang.org/go`, **no external
  `cue`**), `exec` (ctty/PTY launch), `sandbox` (sandbox-exec boundary), plus
  `secrets`/`creds` as they land. Build with `make build`; gate with `make check`.
- **Fish** for every CLI script in `scripts/*.fish` (sourced as conf.d
  modules, no `+x` bit, no shebang reliance).
- **Python via `uv`** for helpers in `scripts/_py/*.py`. PEP-723 inline
  metadata pins versions; fish wrappers call `uv run --script <file>`.
  Never reintroduce bare `python3 -c '...'`.
- **CUE** drives `slop-isolate`'s policy compiler (presets in
  `library/layer/policy/presets/`, fixtures in `library/layer/policy/fixtures/`).
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

For the Go engine, also run:

```bash
make check          # go vet + gofmt + go test ./...  (CI: .github/workflows/go.yml, macOS)
make build          # static CGO_ENABLED=0 binary -> ./safeslop
```

`make test-integration` runs the `integration`-tagged tests (today: the
`install→uninstall→install` idempotency proof on a real tart VM, specs/0041).
It is NOT part of `make check` — it boots a VM and does real network installs,
needs `tart` on the host (self-skips otherwise), and is wired as a manual/cron
Woodpecker pipeline (`.woodpecker/integration.yml`), never on push/PR.

CI runs all four fish gates plus the Go workflow. The **active CI is Forgejo
Woodpecker** (`.woodpecker/*.yml`) while GitHub is paused (see *Development
happens on Forgejo* below); the `.github/workflows/` mirror is kept for the
eventual release. Woodpecker runs natively on a **darwin/arm64** agent (local
backend, so steps run on the host — no Docker images, host needs Go/make/fish/uv),
so CI mirrors the local `make check` / `fish tests/run.fish` and the `sandbox-exec`
launch tests run for real. The Textual launcher has two diagnostic entry points worth knowing:

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

## Development happens on Forgejo (GitHub paused)

Active development is on the **Forgejo** remote
(`forgejo` → `ssh://git@forgejojo.lucyjojo.me:2222/jojo/safeslop.git`) with
**Woodpecker CI** (`.woodpecker/`). GitHub (`origin`) is paused until release —
don't push there or open GitHub PRs for now.

- Push feature branches to `forgejo`; open PRs in the Forgejo web UI.
- `forgejo`'s `main` is the source of truth; `origin` (GitHub) is a stale mirror
  to be re-synced at release time. The `.github/workflows/` files are kept (not
  deleted) so GitHub CI can resume for the release.
- The sandbox blocks `git push origin main` (GitHub); pushing to `forgejo` is the
  normal path now. Still don't push `main` directly unless asked — branch + PR, or
  hand back commits sitting locally.

## Where the load-bearing code lives

- `scripts/_py/slop_tui.py` — Textual app, action model, audit, mount
  check, ctty helper. ~1500 lines; touch carefully.
- `scripts/_py/slop_orchestrator.py` — the `slop.cue` runtime. PEP-723
  but pure stdlib (no Textual). Reads the user's `./slop.cue`,
  resolves it against the bundled CUE schema in
  `library/layer/policy/`, and dispatches to the right launcher per
  `environment` (host / container / vm). Composes existing scripts
  via `_fish_run` / `_fish_exec` rather than reimplementing them.
  ~900 lines.
- `scripts/slop.fish` — thin wrapper around the Textual app + the
  orchestrator. Pre-flight: if `./slop.cue` exists in $cwd or any
  parent, defer to the orchestrator instead of starting the TUI.
  Also routes `slop run|validate|list|down` directly to the
  orchestrator. Layered TLS fallback for the first-run textual
  install. Don't put logic here that belongs in either Python module.
- `scripts/slop-isolate.fish` + `scripts/_py/isolation.py` — CUE
  policy compiler. The `here` shortcut convention originated here.
- `scripts/slop-gh-key.fish` / `slop-forgejo-key.fish` —
  credential lifecycle. Repo-uniqueness tests
  in `tests/test_slop_*.fish` document the two layers' scoping.
  The orchestrator captures `here create-pair` stdout to extract the
  just-issued key ids so on-exit revoke can target them by id rather
  than waiting for the TTL.
- `scripts/slop-agents.fish` — host-side launchers for Claude Code /
  OpenCode with bundled defaults; `seed` is non-clobbering and the
  fixtures are the slop-isolate compile output. The orchestrator's
  `host` environment defers here.

## slop.cue orchestrator (Phases C–G)

Drop a `slop.cue` at the root of any repo and `slop` reads it,
provisions the right credentials + image + proxy, drops the user
into the agent, runs cleanup hooks. Schema lives at
`library/layer/policy/schema/schema.cue` (`#Slop` / `#Profile` /
`#Agent` / `#Environment` / `#Credentials` / `#OnExitHook` /
`#ImageSpec`). Sample at
`library/layer/policy/samples/slop/slop.cue`.

Anatomy of a `slop run review` against a container profile with
ephemeral GitHub creds + tailored image extras:

1. CUE evaluation: copy user's slop.cue into a tempdir under
   `library/layer/policy/.runtime/`, run `cue export --out json .`,
   parse. The runtime tempdir is auto-cleaned via
   `tempfile.TemporaryDirectory`.
2. State load: `<repo>/.slop/state.json`. Per-repo, gitignored.
3. Provision: `slop-gh-key here create-pair`. Capture stdout, regex
   out the two `id: <num>` lines, store in
   `state.credentials.github.key_ids`.
4. Stage credentials: `_stage_credentials` filters host's `~/.ssh/`
   to *only* `llm_agent_<host>_*` files this profile created (never
   the user's permanent `id_ed25519` etc.) and copies them into
   `<repo>/.slop/runtime/<profile>/.ssh/` with a fresh SSH config.
   Github's `HostName` is hardcoded `github.com`; forgejo's is
   parsed from the user's existing `~/.ssh/config` marker block.
5. Resolve image: if `image.extra-{apt,pip,npm}` declared, content-
   hash the spec, generate `Dockerfile.tailored` under the runtime
   dir, `docker build -t local/agent-sandbox-tools:slop-<hash>` if
   not already cached locally. Same hash → cached build hit.
6. Start the stack: `slop-agent-sandbox-tools up` (idempotent
   build of base + tools + squid sidecar).
7. Launch: `docker compose -f <main> -f <override> run --rm
   agent-tools <agent>`. The override is per-profile; sets
   `image:` to the tailored tag (when any) and bind-mounts the
   staged `.ssh/` read-only at `/root/.ssh/`.
8. On agent exit: run on-exit hooks. `snapshot-state` is hoisted to
   run *first* regardless of declaration order so destructive hooks
   can't destroy evidence. `revoke-credentials` uses the captured
   key_ids per family (falls back to `here cleanup` for older
   state). `stop-container` → `slop-agent-sandbox-tools down`.
   `stop-proxy` → `slop-isolate proxy stop`. `destroy-vm` →
   `slop-brew-vm destroy`.
9. Wipe `<repo>/.slop/runtime/<profile>/` regardless of hooks —
   leaving private keys staged on disk after the run defeats the
   point of "ephemeral".

VM dispatch (`environment: vm`) is the same shape but uses
`slop-brew-vm copy-in <stage> ~/.ssh` instead of a docker bind mount.

When extending: the existing test_slop_orchestrator.fish (~73
assertions) covers schema validation, the parser, the staging
filter (the `id_ed25519` decoy test is the canonical isolation
guarantee), the per-id revoke flow, the dry-run announcements, the
TUI's dynamic profile menu, and the snapshot-state hoist. Mirror
that style for any new on-exit hook.

`slop-pinning` scans every `*.cue` under the repo for `:latest"`,
`@latest"`, `==latest` — so adding an `image.base: ".../foo:latest"`
or a `extra-pip: ["ruff"]` (no `==`) into a slop.cue trips the gate.
This complements the existing pinning checks against the four
agent-tools build-config files.
