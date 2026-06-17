# safeslop — local skills

Repo-versioned skills for **safeslop** live here and can be installed into `~/.claude/skills`.

## Contributor expectations

Before updating any skill, read:

1. `CONTRIBUTING.md`
2. `agents.md`
3. `scripts/CONVENTIONS.md`

When script behavior changes, keep skills, docs, and tests in sync in the same change:

- `README.md`
- affected `skills/*/SKILL.md`
- this file when install/usage guidance changes
- `tests/test_*.fish` for any changed argv handling, flags, or error paths
- `scripts/_py/*.py` if the script's Python helper contract changes (Python work is uv-managed; never reintroduce bare `python3`)

Install:

```fish
scripts/slop-skills-install.fish
```

Replace existing:

```fish
scripts/slop-skills-install.fish --force
```

Dry run:

```fish
scripts/slop-skills-install.fish --dry-run
```

Install fish tool shims (stow preferred, direct fallback):

```fish
scripts/slop-install.fish install
```

The installer stows into `~/.local` (so shared `~/.local` setups coexist), installs fish vendor config/completions under `~/.local/share/fish`, and prints a `fish_add_path ~/.local/bin` hint when `PATH` is missing that entry. Re-running `install` is safe and idempotent — the cleanup phase will not follow tree-folded directory symlinks back into the repo's stow source.
