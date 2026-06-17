# Script Conventions

This folder uses a single operational style to keep scripts easy to read, modify, and debug by hand.

## Required interface

- Every user-facing script supports `--help` and `help`.
- Help output includes: `Usage`, key commands/options, and at least one short safety note.
- Prefer subcommand style over positional ambiguity (for example: `tool run ...`, `tool list ...`).

### Enriched help structure

Every script's help follows this layout, in this order:

```
<tool> — <one-line description>

Description:
  <2-4 lines: what it does, default safety stance, when to use it>

Usage:
  <subcommand synopsis lines>

Options:
  --flag value    <one-line meaning, including default>

Examples (synced from README → '<exact heading text>'):
  <step caption>
    <command 1>
    <command 2>
  <next step caption>
    ...

Notes:
  - <one bullet per practice-safe-slop reminder>
  - Full reference: README.md → '<exact heading text>'.
```

Implementation pattern in fish:

- One `__<tool>_help` function prints help to stdout.
- One `__<tool>_help_to_stderr` function calls the above with `1>&2`.
- Every error path prints a one-line `Error: ...` message to stderr, then a
  blank line, then `__<tool>_help_to_stderr`. No script should leave the user
  with just a single-line "Usage: ..." error.
- `help` / `--help` / `-h` and the no-args invocation all print the full help.

### AUTOGEN markers (README is the single source of truth for examples)

The Examples block is generated from the README so the two cannot drift:

```fish
function __<tool>_examples
    # BEGIN AUTOGEN: examples section="<exact README heading text>"
    echo '...auto-rewritten by scripts/slop-sync-help.fish...'
    # END AUTOGEN: examples
end
```

The section value matches a Markdown heading after stripping `#` and
backticks. Within the matched section, every fenced ` ```fish ` code block is
extracted and the immediately preceding `1. ...:` or `- ...` line becomes a
caption. Run `scripts/slop-sync-help.fish sync` after editing README.
CI runs `... check` and fails PRs that drift.

### Repo-aware `here` shortcuts

For tools that take `--repo` (or analogous "which repo am I touching" flags),
add a `here <subcommand>` family that infers the value from the cwd's git
state. Reference implementation: `slop-gh-key here` in
`scripts/slop-gh-key.fish`. Conventions:

- `here` is sugar; it pre-pends inferred flags to argv and falls through to
  the normal subcommand dispatcher so behavior stays identical to explicit
  invocations.
- Inference must read from `$ATB_USER_PWD` first and fall back to `$PWD`,
  because the bin-shim dispatcher cds into the repo root.
- A failed inference must print the underlying CLI flag the user can supply,
  not just "could not infer".
- Common `here` subcommands: `create-pair`, `list`, `revoke <id>`, `cleanup`,
  `revoke-all`. Map convenience names like `cleanup` to the underlying
  `revoke-expired --yes` so users do not have to remember every flag.

### Per-tool TUI subcommand

For tools with non-trivial workflows, add a `tui` subcommand that opens an
interactive launcher. Conventions:

- Soft-deps on `gum`. If gum is missing, print install hints
  (`brew install gum`, charmbracelet URL) and a CLI fallback pointer.
- Every action prints `Equivalent CLI: <cmd>` BEFORE running so the TUI is
  teachable, not a black box.
- Destructive actions confirm with `gum confirm --default=false`.
- Reuses `here` shortcuts under the hood when applicable.

The global launcher `slop` (separate script) is hard-dep on gum and
delegates to per-tool TUIs where they exist.

## Comment best practices

- Add a short top-of-file block describing:
  - purpose
  - key safety/model assumptions
  - official documentation links
- Add function-level comments only when behavior is non-obvious or safety-critical.
- Explain **why** a pattern exists, not what obvious shell syntax does.
- Keep comments stable: avoid version-pinned claims unless necessary.

## Safety defaults

- Default network policy should be `strict-egress` for untrusted execution paths.
- Keep deny-by-default egress and explicit domain allowlists.
- For VM workflows, prefer explicit file transfer (`copy-in`/`copy-out`) over broad host mounts.
- For key lifecycle workflows, prefer short-lived credentials and clear revocation paths.

### Host-side temporary state

Never write host-side state to a fixed path under `/tmp` (e.g.
`/tmp/<tool>-<session>.log`). On multi-user systems that is a
symlink-attack target and lets one user clobber or read another's data.

Use one of:

- `mktemp` / `mktemp -d` — atomic, unique, respects `$TMPDIR` (per-user
  on macOS).
- A per-user state directory under `$XDG_STATE_HOME` (fall back to
  `$HOME/.local/state`), created with `mkdir -p` followed by
  `chmod 700`. See `__brew_vm_state_dir` in `slop-brew-vm.fish` for the
  reference helper.

Guest-side paths inside disposable VMs/containers (e.g. `/tmp/llm-share`
inside a Tart VM) are exempt — those filesystems are single-user and
ephemeral by construction.

## Python helpers via uv

When a fish script needs Python, place the logic in `scripts/_py/<name>.py`
with PEP-723 inline metadata pinning the interpreter, and call it as
`uv run --script "$HELPER_PY" <subcommand> ...` from the fish wrapper.
List `uv` (not `python3`) in the wrapper's `__require_tools` check.

Path discovery: compute the helper path with
`set -g <NAME>_PY (path resolve (dirname (status filename)))"/_py/<name>.py"`
at source time. Do **not** use the `(cd (dirname (status filename)); pwd)`
pattern — fish's command substitution is not a subshell, so the `cd`
silently changes the caller's working directory.

## Parameter naming standards

Use these names consistently where applicable:

- `--name`: human-readable label/session
- `--id`: object identifier (key id, identity id)
- `--repo`: `owner/repo`
- `--access`: `ro|rw`
- `--ttl`: duration (`30m`, `24h`, `7d`)
- `--yes`: non-interactive confirmation
- `--force`: overwrite/bootstrap replacement
- `--network-policy`: `strict-egress|proxy-only|off`

If a domain requires custom identifiers (for example `--rid`), keep aliases where possible and document clearly in help.

## Validation checklist before merge

1. `fish -n scripts/*.fish`
2. `fish tests/run.fish`
3. Run each script's help path (`<script> --help` or equivalent)
4. For network-related scripts, run at least one allow and one block verification path
5. Confirm docs and `tests/test_<script>.fish` reference any new commands/options
