# SP1 — Go engine foundation + headline launch path

**Goal:** Stand up the signed-Go-binary engine for `slop`: read `slop.cue` with an
embedded CUE engine, launch Claude Code / a shell under the first-class sandbox-exec
boundary, and ship a clean scriptable CLI. First sub-project of the rewrite after SP0.
Design: `specs/0001-go-rewrite-design.md` (§6–§8).

## What this delivers

A single Go binary `slop` (module `github.com/freakhill/agentic_tactical_boots`), structured
as engine library + thin CLI:

```
cmd/slop/main.go                       # thin entry → internal/cli
internal/cli/cli.go                    # cobra tree: validate / list / doctor / run, all --json
internal/engine/exec/                  # subprocess launch: RunInTerminal + RunInPTY (the ctty spike)
internal/engine/policy/                # embedded CUE (go:embed) load → validate → decode to structs
internal/engine/policy/schema/schema.cue
internal/engine/sandbox/               # Seatbelt (.sb) profile gen + sandbox-exec launch
examples/slop.cue                      # worked example
Makefile                               # build / check / dist / sign
scripts/sign-notarize.sh               # codesign + notarytool pipeline (maintainer-run)
.github/workflows/go.yml               # CI on macos-latest (vet, gofmt, build, test)
```

### The three load-bearing pieces (all verified)

1. **ctty/PTY spike** (`internal/engine/exec`) — the design's #1 risk. The direct host launch
   (`RunInTerminal`) inherits the real stdio so the child owns the controlling terminal; the
   wrapped/container path (`RunInPTY`) allocates a pseudo-terminal, proxies I/O, sets raw mode,
   and forwards SIGWINCH. The PTY path is fully tested (interactive read/write through the pty
   + exit-code propagation).
2. **Embedded CUE** (`internal/engine/policy`) — the central win. `//go:embed` the schema,
   unify it with the user's `slop.cue` in-process via `cuelang.org/go`, validate, decode to
   typed structs, and render errors with `cue/errors.Details`. **No external `cue` binary.**
3. **sandbox-exec launch** (`internal/engine/sandbox`) — Seatbelt profile ported faithfully
   from the proven `slop-macos-sandbox.fish` generator; `slop run` compiles the policy to a
   `.sb` and launches under `/usr/bin/sandbox-exec`. Workspace paths are canonicalized
   (`/var`→`/private/var`) so Seatbelt's resolved-path matching confines writes correctly.

### CLI surface

- `slop validate [slop.cue]` — validate against the embedded schema (walks up for `slop.cue`).
- `slop list [slop.cue]` — list profiles.
- `slop doctor` — report tool availability (git, docker, op, claude, opencode, tart, mise, nix)
  and the sandbox boundary; `--json` for the GUI.
- `slop run [profile] [--dry-run]` — launch the profile's agent under its environment
  (`sandbox` default, `host` direct; `--dry-run` prints the resolved launch + the `.sb`).
- `--json` on every command; `--version` from `-ldflags`.

## Verification (local, macOS arm64, Go 1.26)

- `go vet ./...` — clean
- `gofmt -l cmd internal` — empty
- `go build` — ok (static, `CGO_ENABLED=0`)
- `go test ./...` — **14 passed in 5 packages**, including the darwin sandbox tests that run a
  command, allow a workspace write, and **deny a write outside the workspace**.
- Binary smoke: `validate` / `list --json` / `doctor` / `run --dry-run` all behave; a broken
  config fails with a real CUE error.

CI (`.github/workflows/go.yml`) re-runs vet/gofmt/build/test on `macos-latest` so the
sandbox-exec tests execute on every PR.

## Deliberately deferred (not in this PR)

- **Signing execution** — `make sign` + `scripts/sign-notarize.sh` are written but not run;
  they need the Apple Developer identity (maintainer's gate).
- **Distribution format** — Homebrew tap / `.pkg` wrapper + stapling (specs/0001 §8) follow
  once the binary stabilizes.
- **Credential providers** (gh/forgejo/pnpm/1Password) — SP2.
- **container / vm environments** — SP3 / SP4 (`slop run` errors clearly for now).
- **CLAUDE.md / AGENTS.md / CONVENTIONS.md Go-stack rewrite** — deferred until SP0 (PR #1) and
  this PR both land, to avoid editing the same docs on two branches. Tracked as a follow-up.

## Build / run

```
make build          # -> ./slop
make check          # vet + gofmt + test (mirrors CI)
make dist           # cross-compile darwin arm64 + amd64 into dist/
make sign           # codesign + notarize (needs SLOP_SIGN_IDENTITY + SLOP_NOTARY_PROFILE)
./slop run review --dry-run
```
