# 2026-07-16 — Fish container startup prior art

Status: lessons triaged

## Reproduced problem

Spec 0108 made the real builtin Fish projection succeed: the session reached `running`, eight physical regular completion files were snapshotted, and two terminal links were omitted. Fish then eagerly executed the copied host `config.fish`, which assumed macOS/Homebrew paths and unavailable tools (`swiftly`, `direnv`, `mise`, `opam`, `brew`). This is a startup portability failure, not a projection safety failure.

## High-pertinence lessons

1. **Do not infer portability for eager host startup code.** Fish automatically executes user `config.fish` and `conf.d/*.fish`. Arbitrary shell logic cannot be safely rewritten around missing tools, paths, side effects, or environment assumptions.
2. **Demand-loading is the useful narrow boundary.** Fish functions and completions are discovered by normal lookup when their command/completion is requested. They do not need host startup scripts to run first.
3. **Prefer omission to runtime suppression machinery.** `fish --no-config` suppresses user startup but also removes user function/completion search paths. Restoring them requires special init argv and displaces normal container-owned startup; omitting eager files is smaller.
4. **Container dotfiles should be explicit.** Dev Containers uses an explicitly configured dotfile repository/install command rather than silently copying arbitrary host startup state. Future eager container config should likewise be opt-in, not a builtin default.
5. **Do not compensate with authority.** Installing/shimming host commands, adding egress, or parsing shell code would widen the image/runtime boundary without establishing correctness.

## Triage

- **HIGH:** builtin Fish projects only optional `functions/*.fish` and `completions/*.fish`; normal `fish` argv remains.
- **HIGH:** `config.fish` and `conf.d/*.fish` are not resolved, copied, or represented as absent builtin candidates.
- **DEFERRED:** an explicit container-dotfiles/install capability, if a future user requirement justifies its security design.
- **REJECTED:** `--no-config` init bridge, command shims/package expansion, script rewriting, and docs-only host guards.

## Sources

- Fish command options: https://fishshell.com/docs/current/cmds/fish.html
- Fish startup/tutorial: https://fishshell.com/docs/current/tutorial.html#startup
- VS Code Dev Container dotfiles: https://code.visualstudio.com/docs/devcontainers/containers#_personalizing-with-dotfile-repositories

## Method

Two blind AYO lanes (DeepSeek and Gemini) converged on omission of eager startup inputs and retention of demand-loaded assets. Host probes confirmed Fish 3.6 `--no-config` suppresses user autoload paths and that a clean normal Fish can autoload projected function files without host startup scripts.
