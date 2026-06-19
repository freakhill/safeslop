# Rename the Go CLI surface `slop` → `safeslop` Implementation Plan

**Goal:** Rename the user-facing surface of the Go binary from `slop` to `safeslop` — the command, the `slop.cue` config filename, the `~/.slop`/`.slop` state + socket, and the tool's own `SLOP_*` env vars — so it doesn't collide with other planned `slop`-named tools.

**Architecture:** A surgical, mechanical rename across the Go engine + Makefile + README + the `.proto`. Each task renames one *surface* (command, config filename, state/socket, env vars, the gRPC wire package, docs) and ends with the full test suite green — the existing tests are the regression net. The gRPC wire package `slop.control.v1` → `safeslop.control.v1` is renamed in the `.proto` and the stubs **regenerated** (`make proto`) rather than hand-edited — nothing is deployed yet, so the future SwiftUI app generates against the right package from the start. A few `slop` tokens are **deliberately left alone**: the Go module path `github.com/freakhill/safeslop` (already `safeslop`), the strangler-stack `scripts/slop-*.fish` + `library/` orchestrator (per the rename decision: legacy fish is left), and arbitrary `env:`-test-fixture variable names that aren't the tool's interface. The generated `internal/engine/control/pb/*.pb.go` is excluded from every hand-`sed` (it is regenerated from the renamed `.proto`, never edited in place).

**Tech stack:** Go, Make, fish gates. No `.proto`/stub regen, no new deps.

**Base branch:** new feature branch `rename-slop-to-safeslop` off `main` (SP7c-3 merged, `main` @ `bd8719b`). **Never push `main`.**

**File structure (what changes):**
- `internal/engine/control/control.proto` (modify) — `package slop.control.v1;` → `package safeslop.control.v1;`.
- `internal/engine/control/pb/control.pb.go` + `control_grpc.pb.go` (regenerated, committed) — new wire package via `make proto`.
- `cmd/slop/` → `cmd/safeslop/` (git mv) — the binary package; `main.go` doc comment.
- `Makefile` (modify) — `BINARY`, `PKG`.
- `internal/cli/cli.go` (modify) — cobra `Use: "slop"`; `slop.cue` filename + help/errors; `.slop` runtime/socket paths.
- `internal/engine/control/serve.go` + `server_test.go` (modify) — `~/.slop/s.sock` → `~/.safeslop/s.sock`.
- `internal/engine/container/{container,lock,launch}.go` + tests (modify) — `.slop/`, `.slop-stage`.
- `internal/engine/vm/{launch,ssh}.go` + tests (modify) — `~/.slop-runtime`, `SLOP_VM_*`.
- `internal/engine/launch/launch.go` + `launch_test.go` (modify) — `SLOP_SESSION`/`SLOP_CWD`.
- `internal/engine/policy/{policy.go,policy_test.go}` (modify) — `slop.cue` references.
- `internal/engine/userconfig/userconfig.go` (modify) — one `policy.slop.cue` comment.
- `internal/cli/*_test.go` (modify) — `slop.cue` fixture filenames; `.slop` path asserts.
- `README.md` (modify) — the 11 Go-command `slop …` refs → `safeslop …` (leave fish-script refs).

---

## Scope: rename vs leave (read before executing)

**Rename (`slop` → `safeslop`):**
1. **Command / binary:** `cmd/slop` dir, `Makefile` `BINARY`/`PKG`, cobra root `Use`, `main.go` comment.
2. **Config filename:** `slop.cue` → `safeslop.cue` (the file the binary searches for + all help/errors + the in-memory policy overlay name + Go test fixtures).
3. **State + socket:** `~/.slop/s.sock` → `~/.safeslop/s.sock`; `<repo>/.slop/` → `<repo>/.safeslop/`; the `.slop-stage` marker → `.safeslop-stage`; the VM guest path `~/.slop-runtime` → `~/.safeslop-runtime`.
4. **The tool's own env vars:** `SLOP_SESSION`, `SLOP_CWD`, `SLOP_VM_PROXY_URL`, `SLOP_VM_SSH_KEY` → `SAFESLOP_*` (+ the tests that assert them).
5. **The gRPC wire package:** `slop.control.v1` → `safeslop.control.v1` — edited in `control.proto` and regenerated (`make proto`), so the committed stubs carry the new service paths (`/safeslop.control.v1.Control/…`). The Go code uses the `pb` *Go* package (unaffected); only the wire/proto identifier changes.

**Leave (with rationale):**
- **`github.com/freakhill/safeslop`** — the module path is already `safeslop`; a bare `slop`→`safeslop` sed would corrupt it to `safesafeslop`. Every sed below is anchored to avoid it.
- **`internal/engine/control/pb/*.pb.go`** is excluded from every hand-`sed` — it is **regenerated** from the renamed `.proto` (Task 1), never edited in place. (Before that regen it still holds `.slop.control.v1`, which is exactly why the `.slop` sed in Task 3 excludes `/pb/`.)
- **`scripts/slop-*.fish`, the global `slop` fish launcher, `library/layer/policy/**`** — the strangler stack, explicitly left.
- **Arbitrary test-fixture env names** (`SLOP_TEST_SECRET`, `TEST_SLOP_SECRET`, `SLOP_IT_*`, `SLOP_A`/`SLOP_B`, `SLOP_TEST_NPM_TOKEN`) — these are `env:` *inputs* in tests, not the tool's env interface. Cosmetic; left to avoid churn.
- **`os.MkdirTemp`/`CreateTemp` prefixes** (`slop-down-*`, `slop-sb-*`, `slop-*.sb`) — invisible ephemeral temp names; left.
- **Comments naming legacy fish gates** (`slop-pinning`, `slop-sync-help`, `slop-isolate`) — those scripts keep their names.

> **TDD shape for a rename:** there is no new behaviour to test-first. Each task does the rename + updates the test fixtures it touches, then runs the suite, which must **stay green** — the existing tests are the regression guard. A task is done when the suite is green under the new names.

---

### Task 1: rename the command + binary

**Files:** `cmd/slop/` (→ `cmd/safeslop/`), `Makefile`, `internal/cli/cli.go`

- [ ] **Step 1: Move the binary package.**

```bash
git mv cmd/slop cmd/safeslop
```

- [ ] **Step 2: Fix the package doc comment.** In `cmd/safeslop/main.go`, change the first line:

```go
// Command safeslop is the single-binary entry point for the safeslop engine.
```

- [ ] **Step 3: Update the Makefile.** In `Makefile`, change:

```make
BINARY  := safeslop
PKG     := ./cmd/safeslop
```
(Line 1's `# slop — Go engine build` comment may also become `# safeslop — Go engine build`.)

- [ ] **Step 4: Rename the cobra command.** In `internal/cli/cli.go:52`:

```go
		Use:           "safeslop",
```

- [ ] **Step 5: Build + smoke the new command.**

```bash
make build
./safeslop --help | head -3
./safeslop --version
```
Expected: build green; help shows `safeslop`; version prints. (`go test ./...` will still pass — no test asserts the binary name.)

- [ ] **Step 6: Commit.**

```bash
git add Makefile cmd/ internal/cli/cli.go
git commit -m "rename(cli): slop -> safeslop command + binary (cmd/safeslop, BINARY)"
```

---

### Task 2: rename the config filename `slop.cue` → `safeslop.cue`

**Files:** `internal/cli/cli.go`, `internal/engine/policy/policy.go`, `internal/engine/userconfig/userconfig.go`, `internal/cli/cli_lint_test.go`, `internal/cli/cli_resolve_test.go`, `internal/engine/policy/policy_test.go`

- [ ] **Step 1: Sweep the `slop.cue` token across the Go tree, excluding the generated stubs.**

```bash
grep -rl 'slop\.cue' cmd internal --include='*.go' | grep -v '/pb/' \
  | xargs sed -i '' 's/slop\.cue/safeslop.cue/g'
```
This updates: `cli.go` (findConfig, `Use: "validate [safeslop.cue]"`, `list`, `Short`, error messages, the comment at ~433/606), `policy.go` (doc comments + the overlay key at ~125), `userconfig.go` (the `policy.safeslop.cue` comment), and the test fixtures in `cli_lint_test.go`, `cli_resolve_test.go`, `policy_test.go` (which write/read `safeslop.cue`). The `--include='*.go'` + `grep -v '/pb/'` keeps it off the module path and the generated proto.

- [ ] **Step 2: Verify the affected packages + check for stragglers.** (Run the sweep **once** — re-running would turn `safeslop.cue` into `safesafeslop.cue` since the new name contains the old token; the grep below confirms a clean single pass.)

```bash
grep -rn 'slop\.cue' cmd internal --include='*.go' | grep -v safeslop | grep -v '/pb/'   # expect: empty
go build ./...
go test ./internal/cli/ ./internal/engine/policy/ -v 2>&1 | tail -5
```
Expected: the grep is empty (every `slop.cue` is now `safeslop.cue`); build green; cli + policy tests PASS (findConfig now finds `safeslop.cue`; the fixtures write that name).

- [ ] **Step 3: Smoke `validate` end-to-end.**

```bash
mkdir -p /tmp/rn-check && printf 'package slop\nslop: {version: 1, profiles: {p: {agent: "claude", environment: "sandbox", network: "deny"}}}\n' > /tmp/rn-check/safeslop.cue
(cd /tmp/rn-check && /Users/jojo/workspace/safeslop/safeslop validate)
rm -rf /tmp/rn-check
```
Expected: `ok: …/safeslop.cue is valid`.

- [ ] **Step 4: Commit.**

```bash
git add cmd internal
git commit -m "rename(config): slop.cue -> safeslop.cue (config filename + help/errors + fixtures)"
```

---

### Task 3: rename the state dir + socket `.slop` → `.safeslop`

**Files:** `internal/cli/cli.go`, `internal/engine/control/serve.go` + `server_test.go`, `internal/engine/container/{container,lock,launch}.go` + `lock_test.go`, `internal/cli/{cli_test,cli_creds_test}.go`, `internal/engine/vm/{launch,ssh}.go` + `ssh_test.go`

- [ ] **Step 1: Sweep the `.slop` state tokens, excluding the generated stubs (which carry `.slop.control.v1`).** Order matters — do the longer tokens first so they aren't half-matched:

```bash
FILES=$(grep -rl '\.slop' cmd internal --include='*.go' | grep -v '/pb/')
echo "$FILES" | xargs sed -i '' \
  -e 's/\.slop-runtime/.safeslop-runtime/g' \
  -e 's/\.slop-stage/.safeslop-stage/g' \
  -e 's#\.slop/#.safeslop/#g' \
  -e 's#"\.slop"#".safeslop"#g'
```
The hyphen tokens (`.slop-runtime`, `.slop-stage`) go first, then the general `.slop/` slash form covers every path + comment (`~/.slop/s.sock`, the test's `"/.slop/s.sock"` suffix, the `.slop/runtime`/`.slop/lock` comments), and `"\.slop"` covers the quoted `filepath.Join(..., ".slop", ...)` literals. Together this hits: the socket (`serve.go`, `cli.go` help, `server_test.go`), the runtime/lock dirs (`cli.go`, `container/{container,lock}.go`, `cli_test.go`/`cli_creds_test.go`/`lock_test.go`), the `.slop-stage` marker (`container/{launch,container}.go`, `lock_test.go`), and the VM guest path `~/.slop-runtime` (`vm/{launch,ssh}.go`, `ssh_test.go`). The patterns target a following `/`, `"`, or `-`, so the module path (`/safeslop`) and proto (`.slop.control.v1`, already excluded via `/pb/`) are never touched.

- [ ] **Step 2: Catch any remaining `.slop` literal (e.g. a bare `".slop"` already handled, or a `"/.slop/s.sock"` suffix in a test assert).**

```bash
grep -rn '\.slop[^.]' cmd internal --include='*.go' | grep -v '/pb/' | grep -v 'safeslop'
```
Expected: empty (every `.slop` state token is now `.safeslop`). If a line remains (e.g. `server_test.go`'s `HasSuffix(p, "/.slop/s.sock")`), Edit it by hand to `/.safeslop/s.sock`.

- [ ] **Step 3: Verify the affected packages with `-race` on control.**

```bash
go build ./...
go test ./internal/cli/ ./internal/engine/control/... ./internal/engine/container/ ./internal/engine/vm/ -race 2>&1 | tail -6
```
Expected: all PASS (socket now `~/.safeslop/s.sock`; container lock/runtime + marker under `.safeslop`; vm scp target `~/.safeslop-runtime`).

- [ ] **Step 4: Commit.**

```bash
git add cmd internal
git commit -m "rename(state): .slop -> .safeslop (socket, runtime dir, lock, stage marker, vm guest path)"
```

---

### Task 4: rename the tool's env vars `SLOP_*` → `SAFESLOP_*`

**Files:** `internal/engine/launch/launch.go` + `launch_test.go`, `internal/engine/vm/{launch,ssh}.go` + `launch_test.go`, `internal/cli/cli.go` (one comment)

- [ ] **Step 1: Rename the four tool-owned env vars (only these — not arbitrary test fixtures).**

```bash
grep -rl 'SLOP_SESSION\|SLOP_CWD\|SLOP_VM_PROXY_URL\|SLOP_VM_SSH_KEY' cmd internal --include='*.go' \
  | xargs sed -i '' \
  -e 's/SLOP_SESSION/SAFESLOP_SESSION/g' \
  -e 's/SLOP_CWD/SAFESLOP_CWD/g' \
  -e 's/SLOP_VM_PROXY_URL/SAFESLOP_VM_PROXY_URL/g' \
  -e 's/SLOP_VM_SSH_KEY/SAFESLOP_VM_SSH_KEY/g'
```
Covers: `launch.go` (`SLOP_SESSION`/`SLOP_CWD` exports) + its test (which asserts both literals), `vm/launch.go` + `vm/ssh.go` (`SLOP_VM_PROXY_URL`/`SLOP_VM_SSH_KEY`) + `vm/launch_test.go` (`t.Setenv("SLOP_VM_PROXY_URL", ...)`), and the `cli.go:436` comment mentioning `SLOP_SESSION`.

- [ ] **Step 2: Confirm no tool env var was missed and no fixture was over-renamed.**

```bash
grep -rn 'SLOP_SESSION\|SLOP_CWD\|SLOP_VM_' cmd internal --include='*.go'   # expect: empty
grep -rn 'SAFESLOP_' cmd internal --include='*.go' | wc -l                  # expect: the renamed refs
```

- [ ] **Step 3: Verify.**

```bash
go build ./...
go test ./internal/engine/launch/ ./internal/engine/vm/ 2>&1 | tail -4
```
Expected: PASS (`SAFESLOP_SESSION`/`SAFESLOP_CWD` in the launch adapter; vm reads `SAFESLOP_VM_PROXY_URL`).

- [ ] **Step 4: Commit.**

```bash
git add cmd internal
git commit -m "rename(env): SLOP_SESSION/CWD/VM_* -> SAFESLOP_* (tool env vars)"
```

---

### Task 5: README + remaining command references

**Files:** `README.md`

- [ ] **Step 1: Find the Go-command refs.** The README uses both the Go binary (`slop run`, `slop validate`, `slop list`, `slop serve`, `slop run review`) and the legacy fish scripts (`slop-gh-key`, `slop-isolate`, …). Rename **only the binary subcommands**, not the hyphenated fish-script names:

```bash
grep -nE 'slop (run|validate|list|serve|down|doctor|launch)\b' README.md
```

- [ ] **Step 2: Rename those occurrences.** This pattern matches `slop <subcommand>` (a space) and never `slop-<script>` (a hyphen) or `slop.cue` (a dot — and `slop.cue` was already renamed in README? no — README config refs are renamed here too):

```bash
sed -i '' -E 's/\bslop (run|validate|list|serve|down|doctor|launch)\b/safeslop \1/g; s/\bslop\.cue\b/safeslop.cue/g' README.md
```

- [ ] **Step 3: Eyeball the diff for false hits** (the `slop.cue` → `safeslop.cue` and `slop <sub>` → `safeslop <sub>` edits should be the only changes; fish-script names like `slop-gh-key` and prose like "the slop toolkit" are untouched):

```bash
git diff README.md | grep '^[-+]' | grep -i slop | head -40
```
Fix any unintended change by hand. Leave prose references to the project/toolkit name as the author intended.

- [ ] **Step 4: Run the docs/pinning gates.**

```bash
fish scripts/slop-sync-help.fish check   # fish-script <-> README drift (Go binary not covered, but confirm no flag)
fish scripts/slop-pinning.fish
```
Expected: both pass. (If `slop-sync-help` flags a fish-script section you didn't intend to touch, revert that hunk — it manages `scripts/*.fish` AUTOGEN blocks, which are out of scope here.)

- [ ] **Step 5: Commit.**

```bash
git add README.md
git commit -m "docs: safeslop command + safeslop.cue in README (leave legacy slop-*.fish names)"
```

---

### Task 6: rename the gRPC wire package `slop.control.v1` → `safeslop.control.v1`

**Files:** `internal/engine/control/control.proto`, `internal/engine/control/pb/control.pb.go` + `control_grpc.pb.go` (regenerated)

- [ ] **Step 1: Rename the proto package.** In `internal/engine/control/control.proto:2`:

```proto
package safeslop.control.v1;
```
(Leave `option go_package = "github.com/freakhill/safeslop/internal/engine/control/pb;pb";` — that's the module path + the Go `pb` package, both unchanged.)

- [ ] **Step 2: Regenerate the stubs.** (protoc + plugins are on `~/go/bin`; generated `*.pb.go` is committed — CI never runs protoc.)

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
make proto
grep -c 'safeslop.control.v1' internal/engine/control/pb/control.pb.go   # expect: > 0
grep -c '[^e]slop\.control\.v1' internal/engine/control/pb/control.pb.go # expect: 0 (no bare slop.control.v1 left)
```

- [ ] **Step 3: Verify the control plane still builds + round-trips.**

```bash
go build ./...
go test ./internal/engine/control/... -race 2>&1 | tail -3
```
Expected: PASS — the Go `pb` package identifiers are unchanged; only the wire/service-path string changed, and client+server share the regenerated stubs.

- [ ] **Step 4: Commit** (regenerated code included).

```bash
git add internal/engine/control/control.proto internal/engine/control/pb/
git commit -m "rename(proto): slop.control.v1 -> safeslop.control.v1 wire package (regenerated stubs)"
```

---

### Task 7: full verification + PR

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
go test ./internal/engine/control/... -race
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
./safeslop --help | head -3
```
Expected: all green; the binary is `safeslop`.

- [ ] **Step 2: Final sweep for stragglers** (any tool-surface `slop` token left, excluding the deliberately-kept module path + legacy fish):

```bash
# hand-written Go (pb/ is generated, verified in Task 6):
grep -rnE '"slop"|slop\.cue|\.slop[/"]|SLOP_(SESSION|CWD|VM_)' cmd internal --include='*.go' | grep -v '/pb/'
# the proto wire package is fully renamed, source + generated:
grep -rn 'slop\.control\.v1' internal/engine/control | grep -v safeslop
```
Expected: both empty. Anything that prints is a miss — fix + re-run the suite before the PR.

- [ ] **Step 3: Push + PR.**

```bash
git push -u origin rename-slop-to-safeslop
gh pr create --base main --title "Rename the Go CLI surface slop -> safeslop" --body "$(cat <<'EOF'
## Summary
Renames the Go binary's user-facing surface from \`slop\` to \`safeslop\` so it doesn't collide with other planned \`slop\`-named CLI tools.

- **Command / binary:** \`cmd/slop\` -> \`cmd/safeslop\`, \`BINARY := safeslop\`, cobra \`Use: "safeslop"\`.
- **Config:** \`slop.cue\` -> \`safeslop.cue\` (search path + help + errors + fixtures).
- **State / socket:** \`~/.slop/s.sock\` -> \`~/.safeslop/s.sock\`; \`<repo>/.slop/\` -> \`.safeslop/\`; \`.slop-stage\` marker; vm \`~/.slop-runtime\`.
- **Env vars:** \`SLOP_SESSION\`/\`SLOP_CWD\`/\`SLOP_VM_PROXY_URL\`/\`SLOP_VM_SSH_KEY\` -> \`SAFESLOP_*\`.
- **gRPC wire package:** \`slop.control.v1\` -> \`safeslop.control.v1\` (\`.proto\` + regenerated committed stubs) — done pre-deployment so the future app generates against it from the start.

## Deliberately left
The Go module path (already \`safeslop\`); the strangler-stack \`scripts/slop-*.fish\` + \`library/\` orchestrator; arbitrary \`env:\` test-fixture variable names.

## Test
\`make check\` + \`make build\` green; \`make proto\` regenerates cleanly; control \`-race\` clean; four fish gates green; \`safeslop --help\` / \`safeslop validate\` smoke-pass; final grep sweep clean.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` green; the binary is `safeslop`; `make proto` regenerates cleanly; `go test ./internal/engine/control/... -race` clean; four fish gates green.
- `safeslop validate`/`list` find `safeslop.cue`; `safeslop serve` binds `~/.safeslop/s.sock`.
- The gRPC wire package is `safeslop.control.v1` (proto + regenerated stubs); the straggler greps (Task 7 Step 2) are empty.
- The module path and the legacy `slop-*.fish` stack are untouched.

## Deliberately deferred (not here)

- **The legacy `scripts/slop-*.fish` stack + `library/` orchestrator** — being strangled by the Go binary; renamed only if/when they're not simply deleted.
