# 0028 — direct profile launch for hotkeys (skhd / Raycast / menu bar)

**Status: deferred / future.** Captured at jojo's request — not scheduled.

**Goal:** Launch a *specific* profile's sandboxed session with one command, from anywhere, so it can
be bound to a global hotkey (skhd), a Raycast/Alfred action, or a menu-bar item. E.g. ⌥⌘R →
"sandboxed Claude on my work repo," with no terminal, no `cd`, no clicking.

## Current state (the gap)

- `safeslop run <profile>` — runs the agent in the **current terminal**; needs a TTY and a cwd that
  contains the `safeslop.cue`. Not hotkey-friendly.
- `safeslop launch <profile>` — opens the user's terminal app running `safeslop run <profile>`
  (`launch.Command` + `AdapterArgv`). **But** `launchProfile` ignores its `configPath` and resolves
  the workspace from `os.Getwd()` (cli.go ~`ws, _ = os.Getwd()`), so from a hotkey (arbitrary cwd) it
  can't find the right policy. **This is the prerequisite to fix.**
- The cockpit — a launcher window you click; no deeplink to a specific profile.

A hotkey fires from no meaningful context, so the binding must **fully specify profile + repo**.

## Design options

1. **CLI `--config` (do first — small, unblocks skhd today).** Make `launch`/`run` accept an explicit
   policy dir: `safeslop launch <profile> --config <dir>` (or a positional dir). `launchProfile` must
   use it for both `findConfig` *and* the workspace, instead of `os.Getwd()`. Then:
   `skhd: alt + cmd - r : safeslop launch review --config ~/work/repo`.
2. **Cockpit URL scheme (nicer GUI deeplink; needs the signed .app).** Register `safeslop://` (Info.plist
   `CFBundleURLTypes`) so `open 'safeslop://run/review?config=/Users/jojo/work/repo'` opens the cockpit
   straight into a session window — reuse the existing `WindowGroup(id:"session", for: ProfileRef.self)`
   + `openWindow(value:)`. Raycast/Alfred/skhd can all `open` a URL. Requires the bundled, signed app.
3. **Named launcher registry (best ergonomics; more design).** `~/.config/safeslop/launchers.cue`
   maps a short name → `{dir, profile, surface: cli|cockpit}`, so a hotkey is just
   `safeslop launch @myreview`. Decouples the binding from absolute paths.
4. **Cockpit launch args.** `SafeSlopCockpit --profile X --config Y` opens straight into a session
   (for `open -a` style launches).

## Constraints

- **Trust is not bypassed.** A hotkey-launched untrusted/changed policy must still gate: the cockpit
  shows the capability trust sheet (specs/0024 + the cockpit trust flow); the CLI fails closed with
  the `safeslop trust` hint. No silent auto-trust from a binding.
- **Canonical paths.** Reuse `canonicalPolicyPath` so a `--config ~/x` and the resolved cwd map to one
  trust key (the /tmp vs /private/tmp bug, specs/research cockpit notes).
- Profile names are already constrained (`^[A-Za-z0-9._-]+$`); validate `--config` is an existing dir.

## Recommendation

Land **(1) CLI `--config`** first — a few lines, makes `safeslop launch <profile> --config <dir>`
work from skhd immediately. Add **(2) the `safeslop://` URL scheme** when the cockpit is a signed
`.app` (it pairs with the GUI work). Consider **(3)** the named registry later if absolute paths in
bindings get annoying.
