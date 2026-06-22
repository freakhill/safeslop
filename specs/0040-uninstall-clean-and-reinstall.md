# Clean uninstall + idempotent reinstall — research-derived design note (ayo)

**Status:** design note (research-derived; precedes an implementation plan). **Date:** 2026-06-22.
Sibling of the install arc (specs/0036–0039), the consent + blast-radius arc (specs/0037), and the
symmetric trust↔revoke arc (specs/0033). Produced by a cross-model `ayo` pass (method footer below).

## Headline — the load-bearing insights

1. **The receipt — not `DesiredState()` — is the removal authority.** Reconstructing "what to remove"
   from the manifest is unsafe: the manifest is *fetch intent*, the filesystem is *ground truth that
   drifts*. safeslop's own `claude` pin proves it — it self-updates after install (desired.go), so its
   on-disk hash diverges from the pinned hash *by design*. Write a safeslop-owned install **receipt**
   (paths + placed-sha256 + provenance + a `self_updating` flag + path-A/B + the delegate's own
   uninstaller pointer), stored *outside* any managed dir, and drive uninstall from it.

2. **Two install paths → two removal disciplines, with asymmetric reversibility.** Path A =
   own-and-remove against the receipt, hash-verified, recoverable from a trash dir. Path B = delegate
   to the tool's designated uninstaller, fail-closed on its exit code, *verify the teardown actually
   happened*, and tell the user plainly it is irreversible (a destroyed `/nix` APFS volume is gone).
   The consent gate must state this asymmetry, not fake symmetry.

3. **Yes, build it — and make `install→uninstall→install` an idempotency CI test, not a claimed
   property.** That closes the "so we can reinstall ourselves" loop, and safeslop already has the tart
   VM harness to run it on a clean darwin-arm64 guest.

## Is uninstall worth building?

Yes. Two motivations: (a) the dev/CI loop — today there is no way back to a clean state to re-test the
installer (`install plan` shows "OK" forever); (b) symmetric, user-facing removal — the mirror of the
install consent arc. It strengthens the honesty/symmetry story. But scope it honestly: it is a
fail-closed, receipt-driven, blast-radius-gated operation, **not** a "tidy my machine" tool.

## Triaged lessons

Grouped by theme. `[C]` = cross-model consensus (high confidence); `[U:x]` = unique to one lane
(higher novelty / risk). HIGH = act on it; MEDIUM = actionable but needs design; DEFERRED = noted, not
acted on.

### Receipts & provenance (the core)
- **HIGH [C, all 5]** — Write an install-time receipt and drive uninstall from it; never reconstruct
  from `DesiredState()`. *Evidence:* dpkg `/var/lib/dpkg/info/*.list`, Homebrew `INSTALL_RECEIPT.json`,
  nix `/nix/receipt.json` — all exist because install is non-deterministic / intent ≠ reality.
  *Surface:* S3. Constraint: receipt unreadable → fail closed, don't guess.
- **HIGH [C, all]** — Hash-verify before unlink; mismatch → halt/skip with a diff, never silent-delete.
  *Evidence:* dpkg md5sums/`debsums`; Homebrew has shipped regressions deleting user-edited files that
  collided with a formula path. *Surface:* S1, S6.
- **HIGH [U: Kimi/DeepSeek] (safeslop-specific)** — Self-updating Path A tools (`claude`, explicit in
  desired.go; possibly bun/pnpm) WILL mismatch their placed hash. Record a `self_updating` flag in the
  receipt; on mismatch for such a tool, do **not** silently delete and do **not** retroactively bless
  the new hash — surface "this self-updated (expected for claude), confirm removal." *Surface:* S1, S3,
  S6.
- **HIGH [C]** — Store the receipt OUTSIDE BinDir/AppDir (e.g. under the userconfig dir /
  `~/.config/safeslop/receipts`), so a partial uninstall or `rm -rf BinDir` can't orphan it.
  *Surface:* S3.
- **MEDIUM [U: GLM]** — Log *negative* provenance too ("Docker at /opt/homebrew/bin/docker — not ours,
  skipped") at install AND uninstall. Cheap audit trail for "why didn't you remove X." *Surface:* S3,
  S7.

### Path A: own-and-remove
- **HIGH [C]** — Never `RemoveAll` the BinDir/AppDir; delete only receipted paths; untracked siblings →
  leave + warn ("directory ownership ≠ inode ownership"). *Evidence:* Homebrew "we don't own everything
  under the prefix." *Surface:* S1, S7.
- **HIGH [C]** — `ENOENT` during removal = success (`rm -f` semantics); never error on already-gone
  files. *Evidence:* dpkg/MSI zombie "un-removable package" states from a pre-deleted file. *Surface:*
  S5.
- **HIGH [C]** — Never follow a symlink out of the prefix; if a receipted path is now an external
  symlink (e.g. to a brew binary), abort/skip — don't delete the target. *Surface:* S6, S7.
- **MEDIUM [U: GLM/Gemini/DeepSeek]** — Move to a trash dir (`~/.safeslop/trash/<ts>/`) with a TTL +
  `prune`, plus `uninstall --rollback`, instead of unlinking — Path A is the *recoverable* tier (MSI
  transactional-rollback precedent). *Surface:* S1, S5. Constraint: trash on same APFS volume for
  atomic `mv`.
- **MEDIUM [C]** — Stop running instances before removal (`launchctl bootout` the plist *then* remove;
  quit a `.app` via Launch Services; warn on a running plain binary) — esp. `tart`. macOS otherwise
  keeps a referenced inode + zombie. *Surface:* S1, S5.
- **MEDIUM [U: DeepSeek]** — Cross-receipt conflict check: don't delete a shared bin name still listed
  by another installed tool's receipt. *Surface:* S1, S6.
- **MEDIUM [C]** — Atomic batch: compute the full delete set + verify all hashes first; abort before
  deleting anything if any check fails (no half-removed environment). *Surface:* S1, S5, S6.

### Path B: delegate, verify, be honest
- **HIGH [C, all]** — Delegate to the tool's own uninstaller; never hand-roll teardown of `/nix`, the
  daemon, or `synthetic.conf`. *Evidence:* `nix-installer uninstall`, `rustup self uninstall`.
  *Surface:* S2.
- **HIGH [C]** — Fail-closed on the delegate's exit code; non-zero → halt the WHOLE uninstall, don't
  proceed to the next tool. *Surface:* S2, S4.
- **HIGH [C: GLM/DeepSeek]** — Don't trust exit 0 — VERIFY teardown (`launchctl print`,
  `diskutil apfs list`, grep `synthetic.conf`). nix-installer has shipped "successful" uninstalls that
  left stale `synthetic.conf`, breaking the next install. *Surface:* S2, S5.
- **HIGH [C]** — Preserve user data by default; split `uninstall` (keep ~/.cargo crates, channels) from
  `purge` (also remove user data), dpkg `remove`/`purge` precedent; `purge` behind a *second* itemized
  consent. *Surface:* S4, S5.
- **HIGH [C: GLM]** — Re-verify the delegate uninstaller (sha256 + `codesign --verify` at *execution*
  time, not just download) before running it with user/root privileges. *Surface:* S6, S2.
- **MEDIUM [C]** — Shell-rc: the delegate edits dotfiles and only it should remove them; safeslop must
  NOT grep-delete PATH lines it didn't write. If safeslop ever writes a dotfile block itself, use
  sentinel markers + a captured baseline + fail-on-tamper. (Verify whether safeslop edits dotfiles at
  all — likely not; the Path B tools do.) *Surface:* S6, S7.

### Consent & the unmanaged
- **HIGH [C, all]** — Consent = an itemized plan with counts by category (A: files to delete; B:
  delegate cmd + known blast radius; dotfiles touched; the NOT-touched list), with explicit *typed*
  confirmation — the symmetric mirror of the install gate. Reuse the existing plan/apply + consent
  machinery (specs/0037). *Surface:* S4.
- **HIGH [C: GLM/Kimi]** — State the reversibility asymmetry in the consent copy: Path A restorable
  from trash; Path B (APFS volume / daemon) irreversible. Don't pretend symmetric recoverability.
  *Surface:* S4, S5.
- **HIGH [C]** — Enumerate untouched tools (Docker brew/cask, hand-installed) in the plan — "Docker
  (brew-managed) — untouched" — so the user doesn't falsely believe the machine is fully clean.
  *Surface:* S4, S7.
- **HIGH [C: GLM] (explicit non-feature)** — No Docker Desktop uninstall under any path; no `--force`
  escape hatch overriding "not ours, untouched." Docker Desktop = a Virtualization.framework VM +
  system extension + launchd services + network extensions + cruft no script cleans correctly.
  *Surface:* S7.

### Idempotent reinstall
- **HIGH [U: GLM, + DeepSeek]** — Make `install→uninstall→install` a CI test on a clean darwin-arm64 VM,
  asserting: `which` pre == post; `launchctl print system | grep safeslop` empty; `diskutil apfs list`
  clean; `synthetic.conf` byte-identical to baseline; shell-rc files match baseline outside safeslop
  marker blocks. safeslop's own `internal/engine/vm` (tart) harness can run it. "If it's not asserted
  in CI, it's not a guarantee." *Surface:* S5.

### Deferred / low
- **DEFERRED [U: GLM]** — Namespacing the APFS volume / launchd label under a `safeslop-` prefix for
  reliable idempotency probes is *not available for Path B*: Determinate controls those names (volume
  "Nix Store", `org.nixos.nix-daemon`). Keep the grep-based probes; drop the renaming idea.
- **DEFERRED [U: DeepSeek/Kimi vs Kimi]** — `lsof`/in-use checks for Path B are racy and incomplete (a
  nix shell on another TTY is invisible) — prefer honest blast-radius disclosure over false safety. A
  soft "is it running" *warning* is fine for Path A only.

## Contradiction surfaced & resolved — which Path B uninstaller to run

- **Gemini:** use the tool's *live* self-managed uninstaller (`rustup self uninstall`), never a cached
  old installer (a v1 `rustup-init` can corrupt v2 state).
- **DeepSeek:** *re-fetch* the uninstaller from the pinned source and sha-verify it (trust symmetry).
- **Kimi:** *pin* the uninstaller to the exact install version (receipt-schema compatibility).

**Resolution (per-tool, captured in the receipt at install):** use the uninstaller the tool
*designates for the installed state*. For self-managing tools (rustup) that is the on-disk
`rustup self uninstall`. For receipt-driven installers (nix) it is the `nix-installer` build matching
the receipt — Determinate conveniently drops `/nix/nix-installer` for exactly this. Record the
uninstaller command + version in safeslop's receipt at install time; re-verify sha256 + codesign
before running. Not FLO-worthy — the per-tool split dissolves the contradiction.

## Actionables (numbered → surface / file)

1. **Install receipt store + writer** in `internal/engine/install`: on every `Apply`, record
   `{tool, paths, placed-sha256, provenance, self_updating, path A|B, delegate uninstaller cmd+version,
   dotfile-block baseline if any, negative-provenance notes}`. Store under the userconfig dir, not
   BinDir. → S3.
2. **`safeslop uninstall [tool...]`** with `plan`/`apply` symmetry mirroring `install`; reuse the
   consent / blast-radius gate (specs/0037). `--purge` as a second tier behind a second itemized
   consent. → S4.
3. **Path A apply:** receipt-driven delete with hash-verify, ENOENT-tolerant, external-symlink-safe,
   trash-dir move + `--rollback`/`prune`, running-process/plist stop, atomic-batch verify-then-delete.
   → S1, S5, S6.
4. **Path B apply:** invoke the receipted delegate uninstaller, re-verify sha + codesign, fail-closed
   on exit code, post-verify teardown (launchctl / diskutil / synthetic.conf). → S2, S5.
5. **Explicit non-touch:** Docker + any non-receipted PATH tool enumerated as untouched in the plan; no
   `--force`. → S7.
6. **CI idempotency test:** `install→uninstall→install` on a tart VM with the baseline assertions. → S5.
7. **Pin model:** add a `self_updating` flag to `install.Pin` (`claude` = true) so the receipt can carry
   it. → S3, S6.

## Net

A `safeslop uninstall` is worth building and fits the architecture cleanly: it is the receipt-driven,
fail-closed, consent-gated mirror of the install arc. The single highest-leverage decision is to
introduce an **install receipt** as the removal authority — the manifest is intent, not truth, and
safeslop's own self-updating `claude` proves the drift. Path A removes its own hash-verified artifacts
reversibly; Path B delegates to each tool's designated uninstaller, verifies the teardown, and is
honest that it is irreversible. Idempotent reinstall becomes a CI test on safeslop's own VM harness.
Explicit non-features (Docker Desktop, any `--force`) keep "never remove what you didn't install"
intact. Next step: a `/writing-plans` implementation plan keyed to the seven actionables.

## Method footer

Cross-model blind research (`ayo`): host (Anthropic, Opus 4.8) + Gemini 3.1 Pro (Google, via
ai-router/OpenRouter, ZDR) + Kimi K2.7 (Moonshot, flat-rate subscription) + GLM-5.1 (z.ai, flat-rate
subscription) + DeepSeek V4 Pro (OpenRouter `intel` profile, ZDR). Five independent families, none
seeing another's output; the orchestrator (host) synthesized + pertinence-triaged. No Fable lane
(standing policy; not OK'd for this run). All OpenRouter calls ZDR-enforced; `anthropic/`,
`moonshotai/`, `zai/` are never routed via OpenRouter.
