# GUI shell-environment reconstruction (cross-model research)

**Date:** 2026-06-21
**Method:** ayo — host (Opus) + Gemini 3.1 Pro (ai-router, ZDR) + GLM-5.1 + Kimi K2.7, blind lanes,
identical brief, compiled + triaged here.
**Problem:** launched from Finder/launchd, `SafeSlop.app` gets a minimal environment (PATH≈
`/usr/bin:/bin`, often no `$SHELL`), so the engine can't find brew, git, mise, uv/cargo, the agents,
or version-manager shims — tool **detection** (`internal/engine/tools`) and agent **launch** both break
outside a terminal. The bundled-engine fix made the app *start* the engine; this is about the engine
(and detection) then *finding everything else*.

---

## Headline (load-bearing, unanimous)

1. **Capture, don't parse.** Spawn the user's real login+interactive shell once and read the env it
   builds — never regex `~/.zshrc`. Dotfiles have conditional logic, prompt hooks, and version-manager
   `eval`s that only a real shell run resolves.
2. **Two environments, one firewall.** Keep a `host_discovery_env` (rich, reconstructed — used only for
   `LookPath` + resolving/launching binaries on the host) strictly separate from the `sandbox_env` (the
   existing `childEnv` allowlist scrub that crosses into the agent). The rich env **never** crosses the
   sandbox boundary. Reconstruction fixes discovery; the scrub still protects the agent. This is the
   whole security story — get it wrong and the credential scrub collapses.

---

## Triaged lessons  ( [C]=cross-model consensus  [U]=single-lane )

### A. Capture mechanism — HIGH
- **[C] Spawn `$SHELL -ilc` with marker-delimited env output.** `-l` loads profile, `-i` satisfies the
  `[[ $- == *i* ]]` guards that gate nvm/mise in `.zshrc`/`.bashrc`. Wrap the dump in a random UUID
  marker (`echo $UUID; env; echo $UUID`) and parse only between markers — users put `neofetch`/MOTD in
  rc files. → S_launch.
- **[C] Resolve the shell via `dscl . -read /Users/$USER UserShell`,** not `$SHELL` (absent under
  Finder) and never hardcode zsh/bash. Handle **fish** specially (lists, no POSIX `export -p`; capture
  per-var with `printf`). → S_launch.
- **[C] Defensive execution: `stdin=/dev/null`, `TERM=dumb`, `CI=1`/`NONINTERACTIVE=1`, suppress
  `PS1`/`PROMPT_COMMAND`/`precmd`, and a hard ~2–3 s timeout** with a baseline fallback. Configs block
  on `ssh-add`/`gpg`/nvm/update-checks; the cockpit must never freeze on a misconfigured profile. → S_launch.
- **[U] `env -0` is a GNU-ism — absent on macOS BSD userland.** Use `printf` per-var or marker-delimited
  `env`. → S_launch.

### B. Fallbacks when capture fails — HIGH
- **[C] `/usr/libexec/path_helper -s`** (reads `/etc/paths` + `/etc/paths.d/*`, runs **no user code**) —
  a safe, deterministic baseline that catches Docker/Postgres.app/etc. → S_detect.
- **[C] Hardcode the Homebrew prefixes** `/opt/homebrew/bin` (Apple Silicon) + `/usr/local/bin` (Intel)
  + common user dirs (`~/.local/bin`, `~/.cargo/bin`) as a zero-latency floor — covers ~95% even if the
  shell run fails entirely. → S_detect.
- **[C] Probe version-manager shim dirs** (`~/.local/share/mise/shims`, `~/.asdf/shims`) and
  `xcode-select -p`'s toolchain. (Bare `git` in `/usr/bin` triggers a hidden CLT-install dialog that
  silently blocks a spawn — resolve the real toolchain.) → S_detect/S_spawn.

### C. The security firewall — HIGH (do not skip)
- **[C] Maintain two env maps; the rich host env is for discovery/launch only — the sandbox gets the
  scrubbed allowlist.** Captured login env contains `AWS_*`, `ANTHROPIC_API_KEY`, `GITHUB_TOKEN`,
  `SSH_AUTH_SOCK`; passing it into the sandbox to "fix detection" defeats `childEnv`. → S_env/S_security.
- **[C] Sanitize the captured env before use:** strip `DYLD_INSERT_LIBRARIES`/`LD_PRELOAD` (host RCE
  vector if the host app inherits them), drop multiline/NUL/control-char values, and **reject PATH
  entries that aren't absolute, are world-writable, or contain `..`** (a poisoned `.zshrc` prepending
  `/tmp/evil` would make the host run malware). For a security tool this is mandatory. → S_security.
- **[C] Apply only to `exec.Cmd.Env` of the specific child — never `os.Setenv`/`launchctl setenv`.**
  Global mutation leaks the rich env into every child (incl. sandbox helpers) and across apps; runtime
  `launchctl setenv` doesn't even work for Finder-launched apps. → S_security/S_launch.
- **[U] Consider running the capture itself in a throwaway minimal sandbox** (sourcing untrusted rc is
  code execution on the host). MEDIUM — nice hardening; the sanitize+timeout covers the common case. → S_security.

### D. Shims, per-project context — HIGH/MEDIUM
- **[C] `cd` into the target workspace before capturing,** so `direnv`/`mise` per-project shims activate
  (project-pinned tool versions). A global login env misses local `node_modules/.bin`, `.mise.toml`. → S_detect.
- **CONTRADICTION (flag, decide per tier): discover-as-shim vs spawn-as-resolved.** Kimi: keep the shim
  in PATH (resolving to the real binary bypasses the version manager's env). Gemini: resolve the real
  binary (`mise which X`) for the *sandboxed* spawn, since the sandbox blocks the shim's host-shell
  hooks. **Resolution:** host/sandbox tier → keep shims in the discovery PATH and ensure the shim dir +
  `~/.config` are readable (the auto-deny list already excludes those); container/vm → tools live inside
  the guest image, so host shims are irrelevant. So: shim for discovery; for the seatbelt spawn, prefer
  the shim but fall back to `mise which` resolution if the shim fails. MEDIUM — revisit when toolchains land.

### E. Caching + transparency — MEDIUM
- **[C] Cache the reconstructed env in memory for the session; invalidate on shell-config `mtime`
  change** (or an explicit re-detect). Shell startup is hundreds of ms–seconds; don't pay it per
  Installs-tab click — but a tool installed mid-session must still appear. Never persist to disk
  (stale `/opt/homebrew` after a brew uninstall → phantom tools). → S_detect.
- **[U] Show provenance in the Installs tab** ("found via zsh / path_helper / fallback") so a missing
  tool is debuggable instead of mysterious. MEDIUM. → S_detect.

---

## Actionables (numbered → safeslop surface)

1. **New Go package `internal/engine/hostenv`** (engine-side, so it fixes *every* launch mode, not just
   the app): `Reconstruct() (Env, error)` →
   - detect "GUI-minimal" env (no `$SHELL`, PATH lacks brew prefix);
   - resolve shell via `dscl`; run `<shell> -ilc '<uuid>; env; <uuid>'` with `stdin=/dev/null`,
     `TERM=dumb`, `CI=1`, `PS1=`/`PROMPT_COMMAND=`, 3 s timeout;
   - fish branch; marker-delimited parse;
   - fallbacks: `path_helper -s`, hardcoded brew/user/shim dirs, `xcode-select -p`;
   - sanitize (drop `DYLD_*`/`LD_PRELOAD`/multiline/NUL; reject non-absolute/world-writable/`..` PATH
     entries);
   - cache in memory, invalidate on rc `mtime`.
2. **`tools.DetectAll` uses the reconstructed PATH** for `LookPath` + `brew` (S_detect) — fixes the
   Installs tab in the bundled app.
3. **Host/sandbox spawn uses `host_discovery_env` to RESOLVE the binary, then `childEnv` (unchanged)
   for what crosses in** (S_spawn/S_env). Keep the two-map firewall explicit.
4. **Optional `cd workspace` before capture** for per-project shims (S_detect) — wire when toolchains matter.
5. **Installs-tab provenance** label (S_detect) — cheap debuggability.
6. **Never `os.Setenv`/`launchctl setenv`;** scope to `exec.Cmd.Env`. Add a test asserting the sandbox
   env is the scrubbed set even when `hostenv` is rich (the firewall regression guard).

## Net

Build an engine-side `hostenv` that reconstructs the user's shell environment by *running their shell*
(defensively, with fallbacks and sanitization), use it **only** for host-side discovery + binary
resolution, and keep the existing `childEnv` allowlist as the sole gate into the sandbox. The hard part
isn't the capture (well-trodden by VS Code/Emacs) — it's the discipline of the two-environment firewall,
which for a security tool is the difference between "finds your tools" and "leaks your credentials."

## Method footer
Families: host (Opus 4.8, synthesizer) · Gemini 3.1 Pro (ai-router ZDR, 20 lessons) · GLM-5.1 (~14) ·
Kimi K2.7 (~16). Blind lanes; compiled by host. ZDR/subscription routes only; no anthropic/* or
moonshotai/* via OpenRouter.
