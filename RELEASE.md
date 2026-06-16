# Release Notes

## Unreleased — interactive launcher, repo-aware shortcuts, enriched help

This release reshapes the developer experience without changing the security
guarantees of the toolkit. Every user-facing script now ships with rich help,
the most common deploy-key flows have one-line repo-aware shortcuts, and a
new `gum`-based TUI gives a single discoverable entry point across every
tool. A small `/tmp` security risk in `slop-brew-vm` was also fixed in passing.

### Highlights

- **`slop` — global TUI launcher.** One menu-driven entry point that wraps
  every sandbox / VM / key tool in this repo. Hard-deps on
  [`gum`](https://github.com/charmbracelet/gum) (`brew install gum`). Every
  action prints its equivalent CLI before running so the TUI is a teaching
  layer, not a black box. Esc on any menu exits/returns. Help and `--version`
  work without `gum` installed so the gate is informative, not silent.
- **Per-tool TUIs (soft-dep on gum).** `slop-gh-key tui`, `slop-forgejo-key tui`,
  `slop-brew-vm tui`, `slop-agent-sandbox tui`,
  `slop-agent-sandbox-tools tui`. Each shows its current operating context
  (cwd, origin, network policy, deps) at the top.
- **Repo-aware `here` shortcuts.** Infer `--repo` (and `--instance`/`--rid`
  where relevant) from the cwd's git state:
  - `slop-gh-key here create-pair | list | revoke <id> | cleanup | revoke-all`
  - `slop-forgejo-key here create-pair | list | revoke <id> | cleanup | revoke-all`
    (host → instance profile lookup)
- **Enriched help pattern across all 16 user-facing scripts.** Every script
  now follows the same layout: tagline → Description → Usage → Options →
  Examples → Notes. Every error path prints the full help to stderr (no more
  one-line "Usage: ..." dead ends).
- **README → help sync.** A new `scripts/slop-sync-help.fish`
  generator extracts fish code blocks from named README sections and
  rewrites AUTOGEN-marked example blocks in fish scripts. CI gate
  (`.github/workflows/help-sync-check.yml`) fails PRs that drift.
- **`/tmp` security fix.** `slop-brew-vm` now writes the `tart run` boot log to a
  per-user state dir (`$XDG_STATE_HOME` or `~/.local/state/agentic-tactical-boots/brew-vm/`,
  mode 0700) instead of `/tmp/brew-vm-<fixed-name>.log`. Eliminates a
  predictable-path symlink-attack target on multi-user systems. Other `/tmp`
  references in the repo were audited and shown to be safe (atomic
  `mktemp`, container tmpfs mounts, JSON test fixtures, single-user
  disposable-VM guest paths).

### New / renamed commands

| Command                | Purpose                                                       |
|------------------------|---------------------------------------------------------------|
| `slop`                 | Global interactive launcher (hard-dep on `gum`).              |
| `<tool> tui`           | Per-tool launcher (soft-dep on `gum`); see scripts above.     |
| `<tool> here ...`      | Repo-aware shortcut; see `llm-*` and `here` examples.         |
| `slop-sync-help`| Maintenance tool (sync/check) for AUTOGEN example blocks.     |

### New conventions

The following conventions are codified in `scripts/CONVENTIONS.md`:

- **Enriched help structure** with a fixed section order; every script has
  `__<tool>_help` and `__<tool>_help_to_stderr`; every error path prints
  the full help.
- **AUTOGEN markers** in scripts, with the README as the single source of
  truth for examples. CI-gated.
- **Repo-aware `here` shortcuts** with rules for inference, fallback to
  `$ATB_USER_PWD` (set by the bin-shim dispatcher), and clear failure
  messages that name the underlying CLI flag.
- **Per-tool `tui` subcommand**: soft-dep on gum; every action prints its
  equivalent CLI before executing; destructive actions confirm with
  `gum confirm --default=false`.
- **Host-side `/tmp` policy**: never write host state to a fixed
  `/tmp/<name>` path; use `mktemp` or a per-user state dir.

### Compatibility

- All previous CLI invocations continue to work unchanged. `here`, `tui`, and
  `slop` are additive.
- `__<tool>_usage` aliases are retained where existing callers (notably
  `slop-sandboxctl`) still reference them, so cross-script dispatch keeps working
  during the transition.
- The bin-shim dispatcher now exports `ATB_USER_PWD` before `cd`-ing into
  the repo root. This is invisible to existing tools and only consumed by
  scripts that opt into the `here`-style inference pattern.

### Installation / upgrade

For users with the shims already installed, re-run the installer to pick up
the new `slop` binary and updated completions:

```fish
scripts/slop-install.fish install
```

For the global TUI:

```fish
brew install gum    # one-time, hard dependency for slop
slop                # menu-driven launcher across every tool in this repo
```

For the most common workflow (managing deploy keys for the current repo):

```fish
slop-gh-key here create-pair    # RO+RW pair, 24h, ssh-config installed
slop-gh-key here list
slop-gh-key here revoke <id>
slop-gh-key here cleanup        # revoke-expired --yes
slop-gh-key here revoke-all     # destructive; confirms
slop-gh-key tui                 # per-tool TUI; soft-deps on gum
```

### Tests

The fish test suite grew from 9 to 18 files. Total assertions now north of
250. CI runs the full suite plus the new help-sync drift check on every PR.
