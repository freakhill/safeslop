# Contributing

Thanks for contributing.

## First Read

Before editing code or docs, read:

1. `AGENTS.md`
2. `scripts/CONVENTIONS.md`
3. `README.md`
4. `CLAUDE.md` (if you're an LLM agent — repo-specific landmines and workflows)

## Script Standards

- Keep script UX consistent (`help`/`--help`, predictable subcommands, stable flags).
- Prefer safe defaults (for sandbox and network-sensitive workflows this means `strict-egress`).
- Add comments that explain **why**, not obvious shell syntax.
- Include official documentation links in script headers when behavior is non-obvious.

## Python via uv

Any Python invoked from this repo must run under `uv` so the interpreter and
dependencies are pinned and reproducible across machines.

- Helpers for fish scripts live in `scripts/_py/*.py` and start with PEP-723
  inline metadata (`requires-python`, `dependencies`).
- Fish wrappers call them as `uv run --script "$HELPER_PY" <subcommand> ...`.
- New Python work follows the same pattern. Do not introduce bare
  `python3 -c '...'` snippets or list `python3` as a required tool.

## Skills, Docs, and Tests Sync Policy

When changing any script under `scripts/` that affects behavior, flags, workflows, or defaults, update all relevant docs **and tests** in the same change:

- `README.md`
- Related skill files under `skills/*/SKILL.md`
- `skills/README.md` if install/use guidance changes
- `tests/test_<script>.fish` for changed/added subcommands, flags, or error paths
- `scripts/_py/<helper>.py` and `tests/test_py_helpers.fish` when the Python helper contract changes

Do not merge behavior changes where skills, docs, or tests are stale.

## Verification

Run at least:

```fish
fish -n scripts/*.fish
fish tests/run.fish
```

For command-surface changes, also verify help output paths still work.

## Network and File-Sharing Guardrails

- Keep deny-by-default egress and explicit allowlists.
- Do not broaden allowlist domains without rationale.
- For VM paths, prefer explicit `copy-in`/`copy-out`; avoid broad host mounts.
- Never introduce defaults that expose host credential directories.
