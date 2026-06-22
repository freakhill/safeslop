# 0041 — `safeslop uninstall`: implementation plan

**Status:** implementation plan. **Date:** 2026-06-22. Implements the seven actionables of the
research-derived design note **specs/0040-uninstall-clean-and-reinstall.md**. Sibling of the install
arc (specs/0036–0039), the consent + blast-radius gate (specs/0037), and the symmetric trust↔revoke
arc (specs/0033).

A fresh agent should be able to execute these tasks top-to-bottom without further context. Each task
names exact files/symbols and carries its own verification. **Refactor and behaviour tasks are kept
separate.** Run `make check` (= `check-assets proto-sync-check vet fmtcheck test`) after each phase.

## Load-bearing decisions (do not re-litigate — from specs/0040)

- The **receipt is the removal authority**, never `install.DesiredState()`. The manifest is fetch
  intent; the filesystem drifts (safeslop's own `claude` pin self-updates by design).
- **Two paths, two disciplines, asymmetric reversibility.** Path A = own-and-remove against the
  receipt, hash-verified, recoverable from trash. Path B = delegate to the tool's designated
  uninstaller, fail-closed on exit, verify the teardown, honest that it is irreversible.
- **Explicit non-features:** NO Docker Desktop uninstall under any path; NO `--force` that overrides
  the "not ours, untouched" boundary. (A `--yes` that only skips the *interactive prompt* for
  automation is allowed; it must NOT widen what gets removed.)

## Conventions confirmed against the live tree (so tasks stay exact)

- Host-side stores live at `~/.config/safeslop/` and mirror `internal/engine/trust/trust.go`:
  `storeVersion` const, `storeFile{Version, Entries}`, `DefaultPath()`, `Load` (missing file → empty,
  not error), persist with `0o700` dir + `0o600` file, **rewrite-not-mutate** for crash-safety.
- Install places Path A artifacts via `install.Apply` (`internal/engine/install/apply.go:95`) into
  `Dirs.BinDir` (`~/.local/bin`) and `Dirs.AppDir` (`~/Applications`); app bundles get a `BinDir/<name>`
  symlink. `DefaultDirs()` is `apply.go:44`. **No receipt is written today** — this plan adds it.
- `Pin` is `internal/engine/install/plan.go:32`. Provenance consts `ProvenanceVendor`/`ProvenanceTLS`
  are `plan.go:49`.
- Path B runs through `tools.installVerifiedInstaller` (`internal/engine/tools/tools.go:700`) using
  `install.FetchVerified` (`internal/engine/install/installer.go:18`). `VerifiedInstaller` is
  `tools.go:115`; the nix entry is `tools.go:185`, rustup `tools.go:207`.
- CLI commands register in `newRoot()` at `internal/cli/cli.go:65`; `cmdInstall()` (`cli.go:684`) is
  the structural template (subcommands `status`/`plan`/`apply`/`rollback`, an `--dry-run` flag, a
  `renderInstallPlanJSON` helper, plan rendering at `cli.go:730`). CLI tests:
  `internal/cli/cli_install_*_test.go`.
- The tart VM harness is `internal/engine/vm` (`Available()`, `EnsureBase`, `CloneAndBoot`, `Destroy`;
  `vm.go`). Tests skip with `if !Available() { t.Skip(...) }`. CI (`.woodpecker/go.yml`,
  darwin/arm64 local backend) runs `make check` then `make build`; no real-VM test runs in CI today.

## Scope / off-limits

- **Do NOT** touch the credential staging code (`internal/engine/creds`, `vm/ssh.go`), the policy
  compiler, the orchestrator, or any fish/Python under `scripts/`. This is Go-engine only.
- **Do NOT** modify `install.Apply`'s placement logic (the `commitStaged`/`.bak` flow) beyond adding
  a return value and the receipt-record call. Removal is a *new* package, not edits to install.
- **Do NOT** add an uninstall path for Docker, brew/cask-managed tools, or any non-receipted binary.
- New packages: `internal/engine/receipt`, `internal/engine/uninstall`. New CLI command in `cli.go`.

---

## Phase 0 — Foundations (no behaviour change to install/uninstall yet)

- [ ] **0.1 Add `SelfUpdating` to `install.Pin`** *(actionable 7)*
  FILE:     `internal/engine/install/plan.go`
  CHANGE:   Add field `SelfUpdating bool \`json:"self_updating,omitempty"\`` to the `Pin` struct
            (after `Sig`). Add a one-line comment: tools that overwrite their own binary after
            install (their on-disk hash diverges from the pin by design). `ValidateDesired`
            (`plan.go:61`) needs no change — an optional bool is always valid.
  VERIFY:   `go build ./... && go vet ./internal/engine/install/`
  EXPECTED: builds clean; `grep -n 'SelfUpdating' internal/engine/install/plan.go` shows the field.

- [ ] **0.2 Mark `claude` self-updating in the manifest** *(actionable 7)*
  FILE:     `internal/engine/install/desired.go`
  CHANGE:   In the `claude` Pin (the `FormatRawBinary` entry, ~line 91), add `SelfUpdating: true,`.
            The existing comment already says "The binary self-updates after install" — leave it.
            Do NOT set it on any other tool (bun/pnpm self-update too, but only mark what we've
            verified; leave a `// TODO(0041): verify bun/pnpm self-update churn` note next to bun).
  VERIFY:   `go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed`
  EXPECTED: PASS — manifest still fully pinned and fail-closed with the new flag set.

- [ ] **0.3 Create the receipt store package** *(actionable 1 — the core)*
  FILE:     `internal/engine/receipt/receipt.go` (new)
  CHANGE:   New package `receipt`, mirroring `trust.go`'s store shape exactly. Define:
            - `const storeVersion = 1`
            - `type File struct { Path string \`json:"path"\`; SHA256 string \`json:"sha256,omitempty"\`; Symlink bool \`json:"symlink,omitempty"\` }`
            - `type Entry struct { Tool string; Path string /* "A"|"B" */; Version string; Provenance string \`json:",omitempty"\`; SelfUpdating bool \`json:",omitempty"\`; Files []File \`json:",omitempty"\` /* Path A */; Uninstall []string \`json:",omitempty"\` /* Path B delegate argv */; InstallerVersion string \`json:",omitempty"\` }` (json tags snake_case, mirror trust style)
            - `type storeFile struct { Version int \`json:"version"\`; Entries map[string]Entry \`json:"entries"\`; Unmanaged map[string]string \`json:"unmanaged,omitempty"\` /* tool -> path, negative provenance */ }`
            - `type Store struct { path string; entries map[string]Entry; unmanaged map[string]string }`
            - `func DefaultPath() (string, error)` → `~/.config/safeslop/receipts.json` (copy trust's `DefaultPath` verbatim, change filename).
            - `func Load(path string) (*Store, error)` (missing file → empty store, not error).
            - `func (s *Store) Record(e Entry) error` — upsert by `e.Tool`, persist (0700 dir / 0600 file, MarshalIndent, rewrite-not-mutate).
            - `func (s *Store) Get(tool string) (Entry, bool)`; `func (s *Store) All() []Entry` (sorted by Tool for deterministic plans).
            - `func (s *Store) Remove(tool string) error` — delete entry, persist (no-op if absent → nil).
            - `func (s *Store) NoteUnmanaged(tool, path string) error` and `func (s *Store) Unmanaged() map[string]string`.
            Package doc comment: receipt is the removal authority; never reconstruct from DesiredState
            (cite specs/0040, specs/0041).
  VERIFY:   `go build ./internal/engine/receipt/`
  EXPECTED: compiles.

- [ ] **0.4 Receipt store round-trip + crash-safety test**
  FILE:     `internal/engine/receipt/receipt_test.go` (new)
  CHANGE:   Table tests using `t.TempDir()` for the store path: (a) `Load` of a missing file returns
            an empty, usable store; (b) `Record` then `Load` round-trips an Entry with `Files`,
            `Uninstall`, `SelfUpdating`; (c) `Record` of the same Tool upserts (not duplicates);
            (d) `Remove` of an absent tool is nil; `Remove` of a present tool drops it and persists;
            (e) the on-disk file is `0600` and the dir `0700` (stat the file); (f) `NoteUnmanaged`
            round-trips. Mirror `trust_test.go` style if it exists; else standard table tests.
  VERIFY:   `go test ./internal/engine/receipt/`
  EXPECTED: PASS.

- [ ] **0.5 Add a `codesign --verify` exec helper** *(actionable 4 dependency)*
  FILE:     `internal/engine/install/codesign.go` (new)
  CHANGE:   `func VerifyCodesign(ctx context.Context, path string) error` shelling out to
            `/usr/bin/codesign --verify --strict <path>` via `exec.CommandContext`; non-zero exit →
            wrapped error including combined output; if `/usr/bin/codesign` is absent (non-darwin /
            stripped), return a sentinel `ErrCodesignUnavailable` so callers can decide. This is a
            plain process exec — NOT the deferred Security.framework peer-auth in
            `control/peerauth.go`; do not conflate them. Comment that this is execution-time
            re-verification of a delegate uninstaller before running it with user privileges.
  VERIFY:   `go build ./internal/engine/install/ && go vet ./internal/engine/install/`
  EXPECTED: builds; `grep -n 'func VerifyCodesign' internal/engine/install/codesign.go` matches.

---

## Phase 1 — Write receipts at install time (behaviour: install now records)

- [ ] **1.1 Make `applyOne` report what it placed** *(refactor only — no behaviour change)*
  FILE:     `internal/engine/install/apply.go`
  CHANGE:   Change `applyOne` (the per-action installer) to return the placed artifact paths and the
            primary binary's sha256: e.g. `(placed []receipt.File, err error)` where each `File`
            carries `Path` + `SHA256` (binary) or `Symlink:true` (the `BinDir/<name>` app symlink).
            Compute the sha from the already-verified bytes/extracted file (do not re-read if avoidable).
            For app-tarball: emit the `AppDir/<name>.app` path (SHA of the inner Mach-O optional —
            leave SHA empty, it's a bundle) plus the symlink `File`. Adjust `installBinary`/
            `installRawBinary`/`installApp` to surface their dest paths. **This task changes only the
            return plumbing; the files placed on disk are byte-identical to before.**
  VERIFY:   `go test ./internal/engine/install/`
  EXPECTED: PASS — existing apply tests unchanged in behaviour.

- [ ] **1.2 `install.Apply` records a Path A receipt entry per applied action** *(actionable 1)*
  FILE:     `internal/engine/install/apply.go`
  CHANGE:   `Apply` (`apply.go:95`) gains a receipt store. Open `receipt.DefaultPath()` + `receipt.Load`
            once at the top (a load failure is fatal — fail-closed, since a missing-but-unwritable
            receipt would silently break future uninstall). After each successful `applyOne`, call
            `store.Record(receipt.Entry{Tool: a.Name, Path: "A", Version: a.Desired, Provenance: <from pin>, SelfUpdating: <from pin>, Files: placed})`. Look up the matching `Pin` for
            `Provenance`/`SelfUpdating` from the `Action` (extend `Action` in `plan.go` with
            `Provenance string` + `SelfUpdating bool` carried from the Pin in `Plan`, OR pass the
            `[]Pin` into Apply — prefer carrying on `Action`, smaller blast radius). Recording failure
            after a successful placement → return the error (the file is placed but unrecorded is a
            real inconsistency the user must see). Keep `emit` events as-is.
  VERIFY:   `go test ./internal/engine/install/`
  EXPECTED: PASS (add the assertion in 1.3).

- [ ] **1.3 Test: a Path A install writes a verifiable receipt**
  FILE:     `internal/engine/install/apply_receipt_test.go` (new)
  CHANGE:   Drive `Apply` with a fake `Fetcher` returning a tiny known artifact for one synthetic Pin,
            `Dirs` under `t.TempDir()`, and `HOME` pointed at a temp dir (so `receipt.DefaultPath`
            resolves into the sandbox — set via `t.Setenv("HOME", tmp)`). Assert: the binary landed in
            `BinDir`; `receipt.Load` shows one Entry with `Path=="A"`, the correct `Version`, one
            `File` whose `SHA256` equals the sha of the placed bytes, and `SelfUpdating` matching the
            Pin. Use the simplest installable `Format` (raw-binary) to avoid archive fixtures.
  VERIFY:   `go test ./internal/engine/install/ -run Receipt`
  EXPECTED: PASS.

- [ ] **1.4 Add the designated uninstaller to `VerifiedInstaller`** *(actionable 4 — data)*
  FILE:     `internal/engine/tools/tools.go`
  CHANGE:   Add fields to `VerifiedInstaller` (`tools.go:115`): `Uninstall []string` (the argv of the
            tool's designated uninstaller for the installed state) and an optional
            `UninstallVerify []string` (post-teardown probe argv whose non-empty stdout = "still
            present"). Populate per specs/0040 §"Contradiction resolved":
            - nix (`tools.go:185`): `Uninstall: []string{"/nix/nix-installer", "uninstall", "--no-confirm"}` (Determinate drops `/nix/nix-installer` for exactly this); `UninstallVerify: []string{"/usr/sbin/diskutil", "apfs", "list"}` (caller greps for the Nix volume).
            - rustup (`tools.go:207`): `Uninstall: []string{"rustup", "self", "uninstall", "-y"}` (the live self-managed uninstaller, per the Gemini lane); no APFS verify.
            Add a comment block citing the per-tool resolution. Update
            `TestCatalogInstallersAreFullyPinned` expectations if it enumerates installer fields.
  VERIFY:   `go test ./internal/engine/tools/`
  EXPECTED: PASS.

- [ ] **1.5 Path B install records a receipt entry + negative provenance** *(actionable 1, 5)*
  FILE:     `internal/engine/tools/tools.go`
  CHANGE:   In `installVerifiedInstaller` (`tools.go:700`), on the installer exiting 0, open the
            receipt store and `Record(receipt.Entry{Tool: t.Name, Path: "B", Version: t.Installer.Version, Provenance: t.Installer.Provenance, Uninstall: t.Installer.Uninstall, InstallerVersion: t.Installer.Version})`.
            Also, when the catalog detects a present-but-unmanaged tool during install flows (Docker
            via brew/cask, or any tool whose route is brew/cask), call `store.NoteUnmanaged(name, path)`
            so the audit trail exists at install time. `tools` already imports `install`; add the
            `receipt` import. Recording failure → surface as the install error (consistent with 1.2).
  VERIFY:   `go test ./internal/engine/tools/`
  EXPECTED: PASS (assertion added in 1.6).

- [ ] **1.6 Test: a Path B install records the delegate uninstaller**
  FILE:     `internal/engine/tools/tools_uninstall_test.go` (new)
  CHANGE:   Without actually running an installer, unit-test the record path: factor the
            receipt-recording in 1.5 into a small `recordVerifiedInstall(store, t Tool) error` and test
            it directly — assert the Entry has `Path=="B"`, `Uninstall` equal to the catalog's
            `t.Installer.Uninstall`, and `Version` set. `HOME` under `t.Setenv`. Also assert
            `NoteUnmanaged` records a docker-shaped path.
  VERIFY:   `go test ./internal/engine/tools/ -run Uninstall`
  EXPECTED: PASS.

---

## Phase 2 — The uninstall engine (new package, pure logic + macOS effects)

- [ ] **2.1 `uninstall.Plan` — receipt-driven, with untouched enumeration** *(actionable 2, 5)*
  FILE:     `internal/engine/uninstall/plan.go` (new)
  CHANGE:   New package `uninstall`. Define:
            - `type Kind int` → `RemovePathA`, `DelegatePathB`.
            - `type Item struct { Tool string; Kind Kind; Files []receipt.File; Delegate []string; Reversible bool; Version string }` (Reversible=true for Path A, false for Path B).
            - `type Plan struct { Items []Item; Untouched []Untouched }` where
              `type Untouched struct { Tool, Path, Reason string }`.
            - `func Build(store *receipt.Store, st install.State, tools []string) (Plan, error)`:
              for each requested tool (or all receipted if `tools` empty), read its Entry → Item.
              Then enumerate **Untouched**: every tool present in `st` (install.Status probe) that has
              NO receipt entry → `Untouched{Reason:"not installed by safeslop"}`, plus everything in
              `store.Unmanaged()`. Docker is always reported here, never as an Item.
            - `func (p Plan) Reversible() bool` / counts helpers for the consent copy.
  VERIFY:   `go build ./internal/engine/uninstall/`
  EXPECTED: compiles.

- [ ] **2.2 Test: plan classifies A vs B and lists untouched**
  FILE:     `internal/engine/uninstall/plan_test.go` (new)
  CHANGE:   Seed a temp receipt store with one Path A entry (uv) and one Path B entry (nix); build a
            fake `install.State` that also reports docker present. Assert: `Items` has uv
            (`RemovePathA`, Reversible) and nix (`DelegatePathB`, not Reversible, Delegate ==
            recorded argv); `Untouched` contains docker with a "not installed by safeslop" reason and
            does NOT appear in `Items`.
  VERIFY:   `go test ./internal/engine/uninstall/ -run Plan`
  EXPECTED: PASS.

- [ ] **2.3 Trash dir helper + move/rollback/prune** *(actionable 3 — the recoverable tier)*
  FILE:     `internal/engine/uninstall/trash.go` (new)
  CHANGE:   - `func TrashDir() (string, error)` → `~/.local/share/safeslop/trash` (same `$HOME` volume
              as `~/.local/bin`, so `os.Rename` is atomic — comment this constraint).
            - `func moveToTrash(paths []string, stamp string) (manifestPath string, err error)` —
              `mkdir trash/<stamp>/`, `os.Rename` each path under it preserving a relative layout, and
              write a small `manifest.json` mapping trashed→original for rollback. `stamp` is supplied
              by the caller (uses `time.Now().UTC().Format("20060102T150405Z")`).
            - `func Rollback(stamp string) error` — read newest (or named) trash manifest, rename each
              file back to its original path; refuse if the original path now exists (don't clobber).
            - `func Prune(ttl time.Duration) (int, error)` — delete trash stamps older than ttl.
  VERIFY:   `go test ./internal/engine/uninstall/ -run Trash` (test added in 2.4)
  EXPECTED: compiles; test green in 2.4.

- [ ] **2.4 Test: trash move + rollback round-trip, no-clobber**
  FILE:     `internal/engine/uninstall/trash_test.go` (new)
  CHANGE:   Under `t.Setenv("HOME", t.TempDir())`: create a fake binary, `moveToTrash` it, assert the
            original path is gone and a trash copy + manifest exist; `Rollback` restores it byte-for-
            byte; a second `Rollback` (original now present) returns an error and does not clobber;
            `Prune(0)` removes the stamp dir.
  VERIFY:   `go test ./internal/engine/uninstall/ -run Trash`
  EXPECTED: PASS.

- [ ] **2.5 Path A apply: verify-then-delete, ENOENT-tolerant, symlink-safe, atomic batch** *(actionable 3)*
  FILE:     `internal/engine/uninstall/apply_a.go` (new)
  CHANGE:   `func applyPathA(item Item, dirs install.Dirs, stamp string) (Result, error)`:
            1. **Pre-flight, no mutation:** for every `File` with a non-empty `SHA256`, recompute the
               on-disk sha. A mismatch → if the Entry is `SelfUpdating`, record a "self-updated
               (expected)" note and require explicit confirmation upstream (do not auto-delete); else
               **abort the whole item** with a diff (`hash mismatch: <path> placed=<a> ondisk=<b>`).
               A missing file (`os.IsNotExist`) is fine (`rm -f` semantics) — skip it, not an error.
            2. **Symlink safety:** if a receipted path is now a symlink pointing OUTSIDE `BinDir`/
               `AppDir` (resolve + check prefix), skip it with a warning — never follow it to delete
               an external (e.g. brew) target.
            3. **Atomic batch:** only after all checks pass for every file, `moveToTrash` the whole
               set in one stamp. Never `RemoveAll` a Dir; only the receipted paths. Untracked siblings
               are left untouched.
            4. On success, the caller removes the receipt entry (`store.Remove(tool)`).
            Return a `Result` listing trashed/skipped/aborted paths.
  VERIFY:   `go build ./internal/engine/uninstall/`
  EXPECTED: compiles; tested in 2.6.

- [ ] **2.6 Test: Path A apply honours hash-mismatch abort, ENOENT, external-symlink skip**
  FILE:     `internal/engine/uninstall/apply_a_test.go` (new)
  CHANGE:   Cases: (a) clean removal of a hash-matching file → trashed, gone from BinDir; (b)
            hash-mismatch on a non-self-updating tool → error, file NOT trashed (atomic abort, nothing
            moved); (c) hash-mismatch on a `SelfUpdating` tool → returns a "needs confirmation" signal
            (not a hard error), file untouched; (d) already-missing file → success, no error; (e) a
            receipted path replaced by a symlink to a file outside the prefix → skipped, target intact.
  VERIFY:   `go test ./internal/engine/uninstall/ -run PathA`
  EXPECTED: PASS.

- [ ] **2.7 Running-instance detection (warn-only)** *(actionable 3 — MEDIUM)*
  FILE:     `internal/engine/uninstall/running.go` (new)
  CHANGE:   `func runningInstances(binPath string) ([]int, error)` using `pgrep -f <binPath>` (or
            `lsof <binPath>`); returns pids. `applyPathA` calls it and adds a warning to the `Result`
            when a tool (esp. `tart`) is running; it does NOT block (no plist teardown exists for any
            Path A tool today — comment that, and that bootout would be added here if one ever does).
  VERIFY:   `go build ./internal/engine/uninstall/`
  EXPECTED: compiles; `grep -n 'func runningInstances' internal/engine/uninstall/running.go` matches.

- [ ] **2.8 Path B apply: re-verify, delegate, fail-closed, post-verify teardown** *(actionable 4)*
  FILE:     `internal/engine/uninstall/apply_b.go` (new)
  CHANGE:   `func applyPathB(ctx, item Item) (Result, error)`:
            1. Resolve the delegate binary (`item.Delegate[0]`). If it is an on-disk path
               (`/nix/nix-installer`), `install.VerifyCodesign(ctx, path)` before running; on
               `ErrCodesignUnavailable`, record that the check was skipped (don't fail-open silently —
               note it in the Result). For a PATH command (`rustup`), resolve via `exec.LookPath`.
            2. Run the delegate argv, streaming output. **Fail-closed:** non-zero exit → return error;
               the CLI must then halt the entire uninstall (do not proceed to other tools).
            3. **Post-verify teardown** (don't trust exit 0): if `UninstallVerify`/known probes are
               set, run them — grep `diskutil apfs list` for the Nix volume, `launchctl print system`
               for a nix daemon label, and read `/etc/synthetic.conf` for stale `nix` lines. Any
               residue → return a non-nil error describing what survived.
            4. On clean teardown, caller `store.Remove(tool)`.
            Never hand-roll `rm -rf /nix`, volume deletion, or `synthetic.conf` edits — only the
            delegate touches system state.
  VERIFY:   `go build ./internal/engine/uninstall/`
  EXPECTED: compiles; tested in 2.9.

- [ ] **2.9 Test: Path B fail-closed on non-zero exit + teardown verification (stubbed)**
  FILE:     `internal/engine/uninstall/apply_b_test.go` (new)
  CHANGE:   Inject the delegate runner + probe runner as function fields (or an interface) so the test
            substitutes fakes — do NOT run real nix/rustup. Cases: (a) delegate exits 0 and probes
            report clean → success, no error; (b) delegate exits non-zero → error returned, no
            `store.Remove`; (c) delegate exits 0 but a probe still reports the Nix volume present →
            error ("teardown incomplete"); (d) codesign unavailable → Result notes the skipped check,
            still proceeds.
  VERIFY:   `go test ./internal/engine/uninstall/ -run PathB`
  EXPECTED: PASS.

---

## Phase 3 — CLI surface (the symmetric, gated mirror of `install`)

- [ ] **3.1 `cmdUninstall` skeleton + `plan` subcommand (+`--json`)** *(actionable 2, 5)*
  FILE:     `internal/cli/cli.go`
  CHANGE:   Add `func cmdUninstall() *cobra.Command` mirroring `cmdInstall()` (`cli.go:684`). Register
            it in `newRoot()` (`cli.go:65`): `root.AddCommand(..., cmdInstall(), cmdUninstall())`.
            `uninstall plan [tool...]`: load `receipt.DefaultPath`, `install.Status`, build
            `uninstall.Build`, render: per-item lines (tool, A/B, reversible?, file count or delegate
            cmd) and a clearly separated **"Untouched (not installed by safeslop)"** block listing
            Docker etc. Add `--json` mirroring `renderInstallPlanJSON` — add a sibling
            `renderUninstallPlanJSON(home string)` so it's unit-testable.
  VERIFY:   `go build ./... && ./safeslop uninstall plan --help` *(after `make build`)*
  EXPECTED: help prints; `safeslop uninstall plan --json` on a machine with no receipt prints an empty
            plan (`{"items":[],...}`) and exits 0.

- [ ] **3.2 `uninstall apply` with typed-confirmation gate, `--dry-run`, `--yes`** *(actionable 2)*
  FILE:     `internal/cli/cli.go`
  CHANGE:   `uninstall apply [tool...]`: build the plan, print the itemized blast radius INCLUDING the
            reversibility asymmetry copy ("Path A → restorable from trash for 7 days; Path B (APFS
            volume / daemon) → irreversible") and the Untouched block. Then a **typed-confirmation
            gate** (CLI-side, since uninstall is destructive and CLI-driven, unlike install's
            cockpit-side gate): read a line from stdin and require it to equal `uninstall`; mismatch →
            abort with non-zero. `--dry-run` prints the plan and exits 0 without prompting or removing.
            `--yes` skips ONLY the interactive prompt (for CI/automation) — it must NOT change what is
            removed and must NOT touch the Untouched set. Execute Path A items first (recoverable),
            then Path B; on any Path B error, **halt** (do not continue). After each item's clean
            apply, `store.Remove(tool)`.
  VERIFY:   `printf 'no\n' | ./safeslop uninstall apply; echo "exit=$status"` (fish) *(after build, on
            a machine with at least one receipt — or assert the prompt+abort path)*
  EXPECTED: prints the plan, reads the declined input, aborts without removing anything, non-zero exit.

- [ ] **3.3 `--purge` second tier behind a second typed confirmation** *(actionable 2)*
  FILE:     `internal/cli/cli.go`
  CHANGE:   Add `--purge` to `uninstall apply`. Semantics (conservative — honour "never remove what
            you didn't install"): purge does NOT recursively `rm` arbitrary user dirs. It (a) for
            Path B, appends the delegate's own documented purge step if the catalog defines one (none
            do yet → purge == uninstall for nix/rustup, stated plainly), and (b) requires a SECOND
            typed confirmation (`type 'purge' to also remove ...`) that itemizes exactly the
            additional paths. With nothing extra to purge today, `--purge` prints "no additional
            user-data removal defined for these tools" and proceeds as a normal uninstall. Leaves the
            hook for future per-tool purge data.
  VERIFY:   `go test ./internal/cli/ -run Uninstall`
  EXPECTED: PASS (test added in 3.5).

- [ ] **3.4 `uninstall rollback` + `uninstall prune` subcommands** *(actionable 3)*
  FILE:     `internal/cli/cli.go`
  CHANGE:   `uninstall rollback [stamp]` → `uninstall.Rollback` (newest if no stamp); prints what was
            restored. `uninstall prune [--older-than 168h]` → `uninstall.Prune` (default 7d TTL);
            prints count reclaimed. Both refuse to clobber existing files (delegated to the engine).
  VERIFY:   `go build ./... && ./safeslop uninstall rollback --help && ./safeslop uninstall prune --help`
  EXPECTED: both help texts print; exit 0.

- [ ] **3.5 CLI tests: plan JSON shape, gate declines, untouched present**
  FILE:     `internal/cli/cli_uninstall_test.go` (new)
  CHANGE:   Mirror `cli_install_plan_test.go`: (a) `renderUninstallPlanJSON` with `HOME` at a temp dir
            seeded with a 2-entry receipt → valid JSON with an `items` key of length 2 and an
            `untouched` key; (b) empty-receipt machine → `items:[]`, exit-0; (c) a unit test of the
            confirmation comparison helper (declining input → abort, `uninstall` → proceed). Keep these
            hermetic — no real removal, drive the gate logic via the extracted helper, not a live apply.
  VERIFY:   `go test ./internal/cli/ -run Uninstall`
  EXPECTED: PASS.

---

## Phase 4 — Idempotent reinstall as a CI test *(actionable 6)*

- [ ] **4.1 `install→uninstall→install` integration test, gated** 
  FILE:     `internal/engine/vm/idempotency_integration_test.go` (new; first line `//go:build integration`)
  CHANGE:   Guard with `if !Available() { t.Skip("tart unavailable") }` AND the `integration` build tag
            (so `go test ./...` in normal CI never runs it). The test: `EnsureBase` → `CloneAndBoot` a
            clean darwin-arm64 guest → scp the built `safeslop` binary in → run `safeslop install apply`
            for one Path A tool (uv) and one Path B tool (nix) → `safeslop uninstall apply --yes` →
            assert, via ssh, the baseline: `which uv` empty, `launchctl print system | grep safeslop`
            empty, `diskutil apfs list | grep -i nix` empty, `/etc/synthetic.conf` has no nix line →
            `install apply` again succeeds (idempotent) → `Destroy`. Comment each assertion to the
            specs/0040 baseline list.
  VERIFY:   `go vet -tags integration ./internal/engine/vm/` (vet only — the test body needs real tart)
  EXPECTED: vets clean under the integration tag; is skipped/excluded by the default `go test ./...`.

- [ ] **4.2 Opt-in CI wiring + Makefile target** 
  FILE:     `Makefile`, `.woodpecker/go.yml`
  CHANGE:   Add a Makefile target `test-integration: ; go test -tags integration ./...` (NOT part of
            `check`). In `.woodpecker/go.yml`, add a SEPARATE, clearly-labelled step that runs
            `make test-integration` — since the agent is darwin/arm64 local-backend with tart, it can
            run for real; if tart is absent the test self-skips. Keep it out of `make check` so the
            fast gate stays fast. Add a one-line note to `CLAUDE.md`'s "Verify your changes" Go section
            that `make test-integration` exists and needs tart.
  VERIFY:   `make -n test-integration && grep -n 'test-integration' .woodpecker/go.yml`
  EXPECTED: the target expands to the tagged `go test`; the Woodpecker step references it.

---

## Phase 5 — Close-out (docs, gates, record)

- [ ] **5.1 README + `--help` sync for the new command**
  FILE:     `README.md` (and whatever `slop-sync-help` checks for the Go binary, if applicable)
  CHANGE:   Document `safeslop uninstall plan|apply|rollback|prune`, the A/B reversibility asymmetry,
            `--purge`/`--dry-run`/`--yes`, and the explicit non-features (no Docker, no `--force`).
  VERIFY:   `fish scripts/slop-sync-help.fish check`  *(skip if the Go binary isn't under its scope —
            then just `go build ./... && ./safeslop uninstall --help`)*
  EXPECTED: drift gate passes (or help renders cleanly).

- [ ] **5.2 Full gate green + pinning**
  FILE:     (repo-wide)
  CHANGE:   No code change — run the done-checklist.
  VERIFY:   `make check && make build && fish scripts/slop-pinning.fish`
  EXPECTED: all green; no `latest` introduced (the receipt/uninstall code pins nothing new).

- [ ] **5.3 Record the spec status + memory**
  FILE:     `specs/0041-uninstall-implementation.md` (this file)
  CHANGE:   Flip Status to "implemented" with the merge commit once landed. Save the load-bearing
            decisions (receipt = removal authority; A/B asymmetry; non-features) to project memory so
            they're not re-litigated.
  VERIFY:   `grep -n 'Status:' specs/0041-uninstall-implementation.md`
  EXPECTED: reflects the final state.

---

## Execution notes

- **Order matters across phases, not always within.** Phase 0 is independent and parallelizable.
  Phase 1 depends on 0.3 (receipt store). Phase 2 depends on 0.3/0.5 and Phase 1's Entry shape.
  Phase 3 depends on Phase 2. Phase 4 depends on Phase 3's `--yes`. Within a phase, tasks touching
  different new files are independent.
- **Atomic, scoped commits** — one logical task or a tight cluster per commit; never `git add -A`.
  Suggested commit boundaries: Phase 0 (foundations), Phase 1 (install records receipts), Phase 2
  (uninstall engine), Phase 3 (CLI), Phase 4 (CI idempotency), Phase 5 (docs/close-out).
- Hand this to **executing-plans** (sequential, since later phases consume earlier types) — not
  subagent-driven-development, because the phases share the receipt `Entry` type and aren't independent.
