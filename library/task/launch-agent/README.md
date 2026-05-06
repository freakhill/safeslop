# task/launch-agent/

Drop into Claude Code or OpenCode with the repo's bundled defaults applied.

## What it composes

- [`../../layer/policy/claude-code.settings.json`](../../layer/policy/claude-code.settings.json) — the Claude Code drop-in: sandbox enabled, deny-read on `~/.ssh`/`~/.aws`/etc., deny-write on `~/**`, allow-write on `./` + `./tmp` + `~/.config/claude-code`.
- [`../../layer/policy/opencode.restrictive.json`](../../layer/policy/opencode.restrictive.json) — strict OpenCode permission policy.

## Recipe

```fish
source scripts/slop-agents.fish

# One-time: write secure defaults to <repo>/.claude/settings.json + opencode.json
slop-agents seed all

# Drop into Claude Code (precedence: $cwd/.claude/settings.json → $repo_root/.claude/settings.json → user-level)
slop-agents claude

# Drop into OpenCode
slop-agents opencode
```

`seed` never overwrites an existing settings file; edit the resulting JSON to take control. Settings precedence is documented inline in [`scripts/slop-agents.fish`](../../../scripts/slop-agents.fish).

## Failure modes

- `claude` (or `opencode`) not on PATH → `slop-agents` exits with an `npm install -g …` hint.
- A pre-existing `.claude/settings.json` you didn't expect → `seed` says "already present, leaving as-is". Move it aside and re-run `seed` to start over from defaults.

## Cleanup

`seed` writes only into the target repo. There's no global state to clean up; just delete `<repo>/.claude/settings.json` and `<repo>/opencode.json` if you want to drop the seeded defaults.
