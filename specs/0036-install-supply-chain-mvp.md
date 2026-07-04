# Install Supply-Chain MVP Implementation Plan

**Goal:** Build the FLO-selected ship-first MVP (`specs/research/2026-06-21-install-supply-chain-flo.md`,
~91) — close the worst install/update gaps on machinery safeslop already has, ordered by
ROI/risk. Leads with the robustness cluster (item ②: "don't break people's computers" — a real
app-bricking bug), then kills `curl|sh` the clean way (item ①: pin the binary, skip the script), then
the consent gate, brew re-verify, and freshness warning.

**Architecture:** Pure additive changes on the existing fail-closed embedded-pin engine
(`internal/engine/install`), the tools catalog (`internal/engine/tools`), and the cockpit Installs tab.
No new infra; the maximal design's heavier pieces (Rekor proofs, advisory feed, N=3 journaled
generations, Tart-VM heavy path, full WARP cert-env) stay deferred per the FLO note.

**Off-limits:** do NOT touch the verification logic in `verify.go` (sha256 + minisign chain is correct),
the CUE policy engine, the cockpit launch/trust code, or the gRPC proto unless a task says so.

**Branch:** continue on `sp-cockpit-risk-legibility` (carries the FLO note); it's the post-merge
improvements branch.

**Item ① design fork (resolved):** the FLO "fetch-pin-the-curl|sh-script" has a problem — those scripts
(`astral.sh/uv/install.sh`, `mise.run`, `sh.rustup.rs`, …) are MUTABLE bootstrappers, so pinning a
script sha breaks on every vendor edit AND the script still downloads un-pinned binaries. The CLEAN
fork, chosen here, is **pin the actual binary release and skip the script** — extend the existing
verified Route A (sha256 + fail-closed) to cover the curl|sh tools that ship versioned binary releases
(uv, ripgrep, fd, bun, …). This reuses the whole existing verify→install engine and eliminates the
remote-code route entirely for those tools. The genuinely script-only tools (nix, rustup-bootstrap)
stay on a sandboxed-script path (Task 8, deferred — needs the squid-wrap design).

---

## Task 1 — Non-destructive `.app` upgrade (keep the prior version) [item ②a — LEAD]

- [ ] **Make `installApp` never delete the old app before the new one is staged + in place**
  FILE: `internal/engine/install/apply.go` (`installApp`, lines 191-212)
  CHANGE: Replace the destructive `_ = os.RemoveAll(dest)` + `os.Rename(app, dest)` sequence with a
  stage→backup→commit→rollback sequence. The old `.app` is moved to `<dest>.bak` (kept as the rollback
  copy), the new app is staged at `<dest>.new` (same dir → same filesystem → rename is atomic), then
  committed by renaming `.new`→`dest`; on a commit failure the backup is restored. Exact replacement for
  lines 199-205 (`dest := …` through the `os.Rename` block):

  ```go
  dest := filepath.Join(appDir, name+".app")
  staged := dest + ".new"
  _ = os.RemoveAll(staged)
  if err := os.Rename(app, staged); err != nil {
      if cerr := copyTree(app, staged); cerr != nil { // cross-device fallback
          return cerr
      }
  }
  // Keep the prior version for rollback instead of deleting it up front: a failed commit must never
  // leave the user with no app (the old destructive RemoveAll-then-rename lost the app on any failure).
  backup := dest + ".bak"
  _ = os.RemoveAll(backup)
  hadOld := false
  if _, err := os.Stat(dest); err == nil {
      if err := os.Rename(dest, backup); err != nil {
          return fmt.Errorf("back up existing %s.app: %w", name, err)
      }
      hadOld = true
  }
  if err := os.Rename(staged, dest); err != nil {
      if hadOld {
          _ = os.Rename(backup, dest) // roll back to the prior version
      }
      _ = os.RemoveAll(staged)
      return fmt.Errorf("commit %s.app: %w", name, err)
  }
  ```
  Leave the symlink block (lines 206-211) unchanged.
  VERIFY: `go test ./internal/engine/install/ -run TestInstallApp -v 2>&1 | tail -5`
  EXPECTED: existing app tests still PASS (the happy path is unchanged: new app ends up at `dest`).

- [ ] **Test: an upgrade preserves the prior version as `.bak` and never deletes-before-stage**
  FILE: `internal/engine/install/apply_test.go`
  CHANGE: Add a test that installs a fake `<name>.app` (a dir with `Contents/MacOS/<name>`), then
  "upgrades" it by calling `installApp` again with a new staged app, and asserts: (a) `dest` exists and
  holds the NEW content, (b) `<dest>.bak` exists and holds the OLD content. Use `t.TempDir()` for
  appDir/binDir and a small helper that builds a `<name>.app` tree under a srcRoot. (Mirror the existing
  apply_test.go style; if a helper to build an app tree already exists, reuse it.)
  VERIFY: `go test ./internal/engine/install/ -run TestInstallAppUpgradeKeepsBackup -v 2>&1 | tail -5`
  EXPECTED: PASS — both the new `dest` and the `.bak` old copy are present with the right contents.

- [ ] **Commit Task 1**
  FILE: —
  CHANGE: `git add internal/engine/install/apply.go internal/engine/install/apply_test.go && git commit`
  with message `fix(install): non-destructive .app upgrade — keep prior version, never RemoveAll-first`
  + the Co-Authored-By trailer.
  VERIFY: `git log --oneline -1`
  EXPECTED: the commit is the tip.

---

## Task 2 — Atomic binary install (stage→rename, keep prior) [item ②b]

- [ ] **Make `installBinary` atomic + non-destructive**
  FILE: `internal/engine/install/apply.go` (`installBinary`, lines 174-188)
  CHANGE: Today `copyFile(src, binDir/name)` overwrites in place (a reader mid-exec sees a torn file).
  Copy to `binDir/<name>.new` (same dir → atomic), back up an existing `binDir/<name>` to
  `binDir/<name>.bak`, then `os.Rename(<name>.new, <name>)`; on commit failure restore the backup.
  Mirror Task 1's stage→backup→commit→rollback shape. Keep the `findFile` source-resolution unchanged.
  VERIFY: `go test ./internal/engine/install/ -run TestInstallBinary -v 2>&1 | tail -5`
  EXPECTED: PASS — the binary lands at `binDir/<name>`, a prior copy is preserved at `<name>.bak`.

- [ ] **Test: binary upgrade keeps the prior copy + commits atomically**
  FILE: `internal/engine/install/apply_test.go`
  CHANGE: Add `TestInstallBinaryUpgradeKeepsBackup`: install a fake binary, upgrade it, assert the new
  bytes are at `binDir/<name>` and the old bytes at `binDir/<name>.bak`, both `0755`.
  VERIFY: `go test ./internal/engine/install/ -run TestInstallBinaryUpgradeKeepsBackup -v 2>&1 | tail -5`
  EXPECTED: PASS.

- [ ] **Commit Task 2** — `feat(install): atomic non-destructive binary install + kept prior version`.

---

## Task 3 — `safeslop install rollback <tool>` [item ②c]

- [ ] **Add a rollback that restores the kept `.bak` prior version**
  FILE: `internal/engine/install/apply.go` (new exported `Rollback(name string, dirs Dirs) error`) +
  `internal/cli/cli.go` (a `rollback` subcommand under the existing `install` command group)
  CHANGE: `Rollback` checks for `<AppDir>/<name>.app.bak` (or `<BinDir>/<name>.bak`), and if present
  swaps it back into place (rename current → `.failed`, rename `.bak` → current). Error clearly if no
  backup exists ("no prior version of %q to roll back to"). Wire a cobra `rollback <tool>` subcommand
  that calls it with `DefaultDirs()`.
  VERIFY: `go test ./internal/engine/install/ -run TestRollback -v 2>&1 | tail -5`
  EXPECTED: PASS — after an upgrade, `Rollback` restores the prior version to the live path.

- [ ] **Test + help-sync**
  FILE: `internal/engine/install/apply_test.go`, then run the help-sync gate
  CHANGE: `TestRollbackRestoresPriorVersion`. After adding the subcommand, if it has a `--help`, run
  `fish scripts/slop-sync-help.fish check` and reconcile README if it flags drift (or note the new
  command is engine-CLI-only and not in the README table).
  VERIFY: `go test ./internal/engine/install/ -run TestRollback && make check`
  EXPECTED: PASS; `make check` green.

- [ ] **Commit Task 3** — `feat(install): safeslop install rollback <tool> restores the kept prior version`.

---

## Task 4 — Kill `curl|sh` for uv: migrate to verified Route A [item ① — the clean fork]

- [ ] **Add a pinned binary release for uv to the embedded desired-state manifest**
  FILE: `internal/engine/install/desired.go` (`DesiredState()`)
  CHANGE: Add a `Pin` for `uv` (Kind `runtime`, Format `binary-tarball`) with the EXACT current
  darwin-arm64 release URL + sha256 from uv's published `SHASUMS256.txt`. Obtain the values:
  `curl -fsSL https://github.com/astral-sh/uv/releases/latest/download/uv-aarch64-apple-darwin.tar.gz` is
  the artifact; the sha is the matching line in
  `https://github.com/astral-sh/uv/releases/download/<ver>/SHASUMS256.txt` (uv also publishes a
  `.minisig` — wire `Pin.Sig` if its pubkey is authoritative, else sha256-only is the floor). Pin the
  exact version, never `latest` (the `slop-pinning` + `ValidateDesired` gates enforce this).
  VERIFY: `go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed -v 2>&1 | tail -5`
  EXPECTED: PASS — the new pin satisfies `ValidateDesired` (version pinned, 64-hex sha256 present).

- [ ] **Route uv through the verified installer, not the catalog `curl|sh`**
  FILE: `internal/engine/tools/tools.go` (the `uv` Catalog entry + `InstallArgv`/`InstallByName`)
  CHANGE: When a tool has a pinned binary in `DesiredState()`, `InstallByName` must install it via the
  embedded-pin path (`install.Plan` + `install.Apply` for that single tool) instead of returning the
  `Script` (`curl … | sh`) argv. Concretely: in `InstallByName`, before falling to the Script route,
  check if `install.DesiredState()` contains a pin for this tool; if so, run the verified install for it
  and return. Keep brew as the preferred route when present (it's already verified-ish); the Script
  route becomes the LAST resort only for tools with neither a brew formula nor an embedded pin.
  VERIFY: `go test ./internal/engine/tools/ -v 2>&1 | tail -8`
  EXPECTED: PASS — uv resolves to the verified-pin install route, not `/bin/sh -c "curl …"`.

- [ ] **Test: uv no longer resolves to a raw `curl|sh`**
  FILE: `internal/engine/tools/tools_test.go`
  CHANGE: `TestUvUsesPinnedBinaryNotCurlSh` — assert that for a missing uv, the chosen install route is
  the verified-pin path (or brew), and that `InstallArgv` never returns an argv beginning
  `/bin/sh -c curl` for uv.
  VERIFY: `go test ./internal/engine/tools/ -run TestUvUsesPinnedBinaryNotCurlSh -v 2>&1 | tail -5`
  EXPECTED: PASS.

- [ ] **Commit Task 4** — `feat(install): uv installs via verified pinned binary, eliminating its curl|sh route`.
  Then `make check` must be green before moving on.

---

## Remaining MVP backlog (tasks to detail when reached — each additive on the same spine)

These are real MVP items from the FLO note; they need a short design pass at execution time (flagged so
the plan does not pretend they are turnkey):

- **Task 5 — Migrate the rest of the curl|sh tools (ripgrep, fd, bun) to pinned Route A** — same shape as
  Task 4, one pin per tool. Mechanical once Task 4 establishes the pattern.
- **Task 6 — Sandboxed-script path for script-only tools (nix, rustup)** — fetch → sha256-pin → show →
  exec the LOCAL file under `sandbox.WrapArgv` with a squid egress allowlist; UNVERIFIED tools render in
  the cockpit danger channel + require consent, never silent-pipe. NEEDS: the script-pin source
  (vendor-at-sha vs pin-live-sha) decided, and the squid-wrap wiring for an installer subprocess.
- **Task 7 — Cockpit consent + preview gate before `InstallTool`** — a plan sheet (exact URL, VERIFIED
  sha+sig vs UNVERIFIED, the literal command, blast radius) + route-proportionate consent (verified-pin
  one click; unverified routes reuse the host-launch comprehension gate). UI (`InstallsTab`) + a
  preflight RPC. (Mirrors specs/0030's gate shape.)
- **Task 8 — Brew bottle re-verify** — pin the bottle URL+sha safeslop resolves and fail-closed on drift
  from the embedded expectation (single-source first).
- **Task 9 — Freshness-floor warning** — the binary carries its pin-set date; a stale pin surfaces
  "stale — update safeslop" in the gate (warn, not block).

---

## Verify before "done" (whole-MVP)

```
make check            # go vet + gofmt + go test ./...  (incl. install + tools)
make build            # static binary
fish scripts/slop-pinning.fish     # the new uv pin must not trip the latest-gate
fish scripts/slop-sync-help.fish check
```
