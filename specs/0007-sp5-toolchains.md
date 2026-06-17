# SP5 — toolchains: a `toolchain:` concept (mise | nix | none) across all environments

**Goal:** Add a `toolchain:` field to a slop.cue profile — orthogonal to `environment` — that
either provisions a **pinned tool environment for the agent** (wrapper: `mise exec -- <agent>` /
`nix develop -c <agent>`) or **launches a mise task / nix flake app** as the run target
(`toolchain.run`). Composes with all four environments (host, sandbox, container, vm).

**Architecture:** The elegant core: a pure `toolchain.Wrap(kind, run, agentArgv) → argv` runs
once in `cmdRun` *before* the environment switch, so **every** environment automatically launches
the wrapped argv — orthogonality falls out for free. The remaining work is *enablement*: making
`mise`/`nix` actually present in each environment (host: already there; sandbox: allow the tool
stores in the seatbelt; container: bake `mise` into the image; vm: provision in the guest). The
schema and Go struct gain a `#Toolchain`; `slop doctor` already lists `mise`/`nix`.

**Tech stack:** Go 1.26 (pure `Wrap` + `os/exec` enablement), the embedded CUE schema, the SP3
container image + SP4 vm path. `mise` (a static binary) and `nix` (flakes) are the toolchains; no
new Go deps.

**File structure:**
- `internal/engine/policy/schema/schema.cue` (modify) — add `#Toolchain` + `toolchain?` on `#Profile`.
- `internal/engine/policy/policy.go` (modify) — add `Toolchain` struct + `Profile.Toolchain`.
- `internal/engine/toolchain/toolchain.go` (create) — pure `Wrap` + `Available`; no I/O.
- `internal/engine/toolchain/toolchain_test.go` (create) — hermetic `Wrap`/`Available` tests.
- `internal/cli/cli.go` (modify) — wrap argv in `cmdRun`; dry-run shows the toolchain; pass kind to `vm.Launch`.
- `internal/engine/sandbox/sandbox.go` (modify) — allow-read the mise/nix tool stores so the wrapped argv runs under seatbelt.
- `internal/engine/vm/launch.go` + `vm.go` (modify) — provision mise/nix in the guest when the toolchain needs it.
- `library/layer/container/Dockerfile.agent.tools` (modify) + re-sync — bake `mise` into the agent-tools image.
- `internal/engine/policy/policy_test.go`, `internal/engine/sandbox/sandbox_test.go` (modify) — schema + seatbelt tests.
- `specs/0001-go-rewrite-design.md` (modify) — flip SP5 to complete.

---

## Key design decisions

1. **Schema: `toolchain` is an object `{kind, run?}`, optional (absent = none).**
   `kind: "mise" | "nix" | "none"`; `run?: string` is a mise task name (mise) or a nix app ref
   like `".#app"` (nix). When `run` is set, slop launches it **instead of** the agent; when
   absent, the agent is **wrapped** so the pinned toolchain is on PATH.

2. **Wrap once, before the environment switch.** `cmdRun` computes the agent argv, then (if a
   toolchain is set) replaces it with `toolchain.Wrap(...)`. The wrapped argv flows unchanged into
   `sandbox.Launch` / `RunInTerminal` / `container.Launch` / `vm.Launch`. No per-environment Wrap
   logic — the orthogonality the design wants is structural.

3. **Enablement matrix (honest about what ships where).** mise (a single static binary) ships
   everywhere; nix (needs a writable `/nix` store) ships everywhere it can:

   | env | mise | nix | how |
   |---|---|---|---|
   | host | ✅ | ✅ | host's own `mise`/`nix` on PATH |
   | sandbox | ✅ | ✅ | seatbelt allow-reads `/nix` + `~/.local/share/mise` + the bins |
   | container | ✅ | ⏭ deferred | `mise` baked into the tools image; **nix-in-container is deferred** — the agent container is `read_only:true` and nix needs a writable `/nix` store, a direct conflict (a nix-store volume + relaxed hardening is a follow-up) |
   | vm | ✅ | ✅ | provisioned in the guest on launch (mise via `mise.run`, nix via the Determinate installer); the VM is a full writable macOS |

   So `toolchain: {kind: "nix"}` + `environment: "container"` fails fast with a clear message
   pointing at the deferral; every other (kind × env) combination works.

4. **Pinning = the safe-install story (the "nyx" point).** Toolchains are only as safe as their
   pins: `mise` reads `mise.toml` (pin tool versions), `nix` reads `flake.nix`+`flake.lock`
   (pinned inputs). SP5 does **not** invent a new pin format — it documents that a toolchain
   profile expects a pinned `mise.toml` / committed `flake.lock` in the workspace, and the
   provisioning installers are themselves version-pinned (Task 6). A `slop doctor` note flags a
   `nix` toolchain workspace with no `flake.lock`.

**Before you start:** `git checkout -b sp5-toolchains` (we're on `main`; never commit SP5 there).
The hermetic core (Tasks 1–4) is fully CI-verifiable; the container image (Task 5) and vm
provisioning (Task 6) are **env-gated** (image build / Tart on Apple Silicon) — verified by a
manual smoke, not CI, exactly as SP3/SP4.

---

### Task 1: schema `#Toolchain` + Go `Profile.Toolchain`

**Files:** Modify `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy.go`; Test `internal/engine/policy/policy_test.go`.

- [ ] **Step 1: Add `#Toolchain` to the schema** — after the `#Credentials` block (before `#Profile`):
```cue
// A pinned toolchain layered onto any environment (SP5), orthogonal to `environment`.
//   kind: which provider provisions tools — mise (version manager + task runner) or nix
//         (flakes; pinned inputs = the safe-install story).
//   run:  optional — a mise task name (kind=mise) or a nix app ref like ".#app" (kind=nix)
//         to launch INSTEAD of the profile's agent. Absent => the agent is wrapped so the
//         pinned toolchain is on PATH.
#Toolchain: {
	kind: "mise" | "nix" | "none"
	run?: string
}
```
Then add the field to `#Profile` (after `credentials?`):
```cue
	// Optional pinned toolchain, provisioned into the chosen environment (SP5).
	toolchain?: #Toolchain
```

- [ ] **Step 2: Add the Go struct** to `internal/engine/policy/policy.go` — after the `Credentials` type:
```go
// Toolchain layers a pinned tool environment onto any environment (SP5). When Run is set,
// slop launches that mise task / nix app instead of the agent; otherwise the agent is wrapped.
type Toolchain struct {
	Kind string `json:"kind"`
	Run  string `json:"run,omitempty"`
}
```
Then add to `Profile` (after `Credentials *Credentials ...`):
```go
	// Toolchain provisions a pinned tool environment, orthogonal to Environment (SP5).
	Toolchain *Toolchain `json:"toolchain,omitempty"`
```

- [ ] **Step 3: Failing test** (`internal/engine/policy/policy_test.go`) — a valid toolchain decodes; a bad kind is rejected. Mirror the existing table tests in that file (read it first for the `writeTemp`/load helper it already uses; the snippet below assumes a `loadCUE(t, src)` style helper — adapt to the actual helper name):
```go
func TestToolchainDecodes(t *testing.T) {
	cfg := mustLoad(t, `version: 1
profiles: dev: {agent: "claude", toolchain: {kind: "mise", run: "build"}}`)
	tc := cfg.Profiles["dev"].Toolchain
	if tc == nil || tc.Kind != "mise" || tc.Run != "build" {
		t.Fatalf("toolchain decoded wrong: %+v", tc)
	}
}

func TestToolchainRejectsBadKind(t *testing.T) {
	if _, err := load(t, `version: 1
profiles: dev: {agent: "claude", toolchain: {kind: "cargo"}}`); err == nil {
		t.Fatal("expected validation error for kind \"cargo\"")
	}
}
```
> Read `policy_test.go` first and use its real load helpers (`mustLoad`/`load` are placeholders
> for whatever it defines); the assertions above are the contract.

- [ ] **Step 4: Run** — `go test ./internal/engine/policy/ -run Toolchain -v` → PASS.
- [ ] **Step 5: Commit**
```bash
git add internal/engine/policy/schema/schema.cue internal/engine/policy/policy.go internal/engine/policy/policy_test.go
git commit -m "sp5: schema #Toolchain (mise|nix|none + run) + Profile.Toolchain"
```

---

### Task 2: the `toolchain` package — pure `Wrap` + `Available`

**Files:** Create `internal/engine/toolchain/toolchain.go`, `internal/engine/toolchain/toolchain_test.go`.

- [ ] **Step 1: Write `toolchain.go`**
```go
// Package toolchain layers a pinned tool environment (mise/nix) onto any slop environment.
// Wrap is pure: it transforms the agent argv so the toolchain is provisioned, or replaces it
// with a mise task / nix app. Enabling mise/nix inside each environment is the caller's job.
package toolchain

import "os/exec"

// Wrap transforms agentArgv per the toolchain. With run set, it returns the mise task / nix app
// (the agent is not launched). Without run, it wraps the agent so the pinned toolchain is on
// PATH. kind "none"/"" is a passthrough (returns agentArgv unchanged).
func Wrap(kind, run string, agentArgv []string) []string {
	switch kind {
	case "mise":
		if run != "" {
			return []string{"mise", "run", run}
		}
		return append([]string{"mise", "exec", "--"}, agentArgv...)
	case "nix":
		if run != "" {
			return []string{"nix", "run", run}
		}
		return append([]string{"nix", "develop", "-c"}, agentArgv...)
	default:
		return agentArgv
	}
}

// Wraps reports whether kind is a real toolchain (mise/nix) that Wrap will transform.
func Wraps(kind string) bool { return kind == "mise" || kind == "nix" }

// Available reports whether the kind's CLI is on the host PATH (for slop doctor / host runs).
func Available(kind string) bool {
	if !Wraps(kind) {
		return false
	}
	_, err := exec.LookPath(kind)
	return err == nil
}
```

- [ ] **Step 2: Tests** (`toolchain_test.go`)
```go
package toolchain

import (
	"strings"
	"testing"
)

func TestWrap(t *testing.T) {
	agent := []string{"claude"}
	cases := []struct {
		kind, run string
		want      string
	}{
		{"none", "", "claude"},
		{"", "", "claude"},
		{"mise", "", "mise exec -- claude"},
		{"mise", "build", "mise run build"},
		{"nix", "", "nix develop -c claude"},
		{"nix", ".#app", "nix run .#app"},
	}
	for _, c := range cases {
		got := strings.Join(Wrap(c.kind, c.run, agent), " ")
		if got != c.want {
			t.Errorf("Wrap(%q,%q)=%q want %q", c.kind, c.run, got, c.want)
		}
	}
}

func TestWrapPreservesAgentArgs(t *testing.T) {
	got := strings.Join(Wrap("mise", "", []string{"claude", "--flag"}), " ")
	if got != "mise exec -- claude --flag" {
		t.Fatalf("got %q", got)
	}
}

func TestWraps(t *testing.T) {
	if !Wraps("mise") || !Wraps("nix") || Wraps("none") || Wraps("") {
		t.Fatal("Wraps wrong")
	}
}
```

- [ ] **Step 3: Run** — `go test ./internal/engine/toolchain/ -v` → PASS.
- [ ] **Step 4: Commit**
```bash
git add internal/engine/toolchain/toolchain.go internal/engine/toolchain/toolchain_test.go
git commit -m "sp5: toolchain package — pure Wrap (mise/nix, wrapper + run target) + Available"
```

---

### Task 3: wire `cmdRun` (host toolchains work end-to-end)

**Files:** Modify `internal/cli/cli.go`; Test `internal/cli/cli_test.go`.

- [ ] **Step 1: Wrap the argv** in `cmdRun`'s `RunE`, right after `argv, err := agentArgv(prof)` (and its error check):
```go
		if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
			argv = toolchain.Wrap(prof.Toolchain.Kind, prof.Toolchain.Run, argv)
		}
```
Add the import `"github.com/freakhill/agentic_tactical_boots/internal/engine/toolchain"`. Because
this runs before both the dry-run block and `runProfile`, **every** environment + the dry-run
output get the wrapped argv for free.

- [ ] **Step 2: Surface the toolchain in dry-run** — in the non-JSON dry-run branch, after the
  `argv:` line, add:
```go
				if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
					fmt.Printf("  toolchain: %s", prof.Toolchain.Kind)
					if prof.Toolchain.Run != "" {
						fmt.Printf(" run=%s", prof.Toolchain.Run)
					}
					fmt.Println()
				}
```
(The JSON dry-run already includes the wrapped `argv`; optionally add `out["toolchain"] = prof.Toolchain` near the other `out[...]` assignments.)

- [ ] **Step 3: Test** (`internal/cli/cli_test.go`) — a mise toolchain wraps the argv that
  `cmdRun` would launch. The cleanest hermetic assertion is on `toolchain.Wrap` composition (Task
  2) plus a dry-run golden; add:
```go
func TestRunDryRunShowsToolchain(t *testing.T) {
	// Build a profile with a toolchain and confirm Wrap produces the launch argv runProfile sees.
	got := toolchain.Wrap("mise", "", []string{"claude"})
	if got[0] != "mise" || got[len(got)-1] != "claude" {
		t.Fatalf("toolchain wrap not applied: %v", got)
	}
}
```
> A fuller end-to-end dry-run test (capturing stdout of `cmdRun --dry-run`) is welcome if
> `cli_test.go` already has a stdout-capture helper; otherwise the Wrap composition test above
> guards the contract and the manual smoke covers the print.

- [ ] **Step 4: Run + smoke** — `go test ./internal/cli/ ./internal/engine/toolchain/ -v` → PASS;
  `make build`; with `mise` on PATH, a profile `{agent: "shell", environment: "host", toolchain:
  {kind: "mise"}}` → `slop run` launches `mise exec -- $SHELL` (tools on PATH). `slop run … --dry-run`
  prints the `toolchain:` line.
- [ ] **Step 5: Commit**
```bash
git add internal/cli/cli.go internal/cli/cli_test.go
git commit -m "sp5: wrap agent argv with the toolchain in slop run (host works) + dry-run shows it"
```

---

### Task 4: sandbox enablement — allow the tool stores under seatbelt

**Files:** Modify `internal/engine/sandbox/sandbox.go`; Test `internal/engine/sandbox/sandbox_test.go`.

- [ ] **Step 1: Add tool-store read paths** to `sandbox.go`. After `tempPaths`, add:
```go
// toolchainReadPaths are read-allowed so a mise/nix toolchain wrapper (mise exec / nix develop)
// can resolve its store + binaries under the seatbelt. Read-only; harmless when no toolchain is
// used. Home-relative paths are resolved at profile-render time.
func toolchainReadPaths() []string {
	paths := []string{"/nix", "/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".local", "share", "mise"),
			filepath.Join(home, ".local", "state", "mise"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	return paths
}
```
(Add `"path/filepath"` if not already imported — it is.)

- [ ] **Step 2: Emit them in `Profile`** — after the `systemReadPaths` loop, add:
```go
	for _, p := range toolchainReadPaths() {
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, escape(p)))
	}
```

- [ ] **Step 3: Test** (`internal/engine/sandbox/sandbox_test.go`)
```go
func TestProfileAllowsToolchainStores(t *testing.T) {
	p := Profile("/tmp/ws", "deny")
	for _, want := range []string{`(subpath "/nix")`, "mise"} {
		if !strings.Contains(p, want) {
			t.Fatalf("seatbelt profile missing toolchain read %q:\n%s", want, p)
		}
	}
}
```
(Ensure `"strings"` is imported in the test file.)

- [ ] **Step 4: Run** — `go test ./internal/engine/sandbox/ -v` → PASS (incl. the existing darwin
  workspace-write/deny tests). `make check`.
- [ ] **Step 5: Commit**
```bash
git add internal/engine/sandbox/sandbox.go internal/engine/sandbox/sandbox_test.go
git commit -m "sp5: sandbox seatbelt allow-reads the mise/nix tool stores (toolchain under sandbox)"
```

---

### Task 5: container enablement — bake `mise` into the tools image (nix deferred)

**Files:** Modify `library/layer/container/Dockerfile.agent.tools`; re-sync `internal/engine/container/assets/Dockerfile.agent.tools`; Modify `internal/engine/container/launch.go` (fast-fail nix-in-container); Test `internal/engine/container/container_test.go`.

- [ ] **Step 1: Read the canonical Dockerfile** — `internal/engine/container/` is synced FROM
  `library/layer/container/` (the drift gate), so edit the **canonical** copy:
```bash
sed -n '1,60p' library/layer/container/Dockerfile.agent.tools
```
Identify the final `RUN` layer / where tools are installed.

- [ ] **Step 2: Add a pinned `mise` install** to `library/layer/container/Dockerfile.agent.tools`
  (mise is a single static binary; pin the version — replace `<VER>` with the current mise release
  and add it to `agent-tools.env.example` if that file pins tool versions, to keep `slop-pinning`
  happy):
```dockerfile
# mise — pinned toolchain provisioner for `toolchain: {kind: "mise"}` (SP5).
ARG MISE_VERSION=2025.x.x
RUN curl -fsSL "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-x64" \
      -o /usr/local/bin/mise && chmod +x /usr/local/bin/mise && mise --version
```
> nix is **intentionally not** installed: the agent container runs `read_only:true` and nix needs
> a writable `/nix` store — a follow-up (nix-store volume + relaxed hardening). Document it here.

- [ ] **Step 3: Re-sync the embedded copy + verify the drift gate** —
```bash
make sync-container-assets && make check-assets && grep -c mise internal/engine/container/assets/Dockerfile.agent.tools
```
Expected: drift gate passes; `mise` present in the synced copy.

- [ ] **Step 4: Fast-fail nix-in-container** — in `internal/engine/container/launch.go`'s `Launch`,
  add a guard near the top (it needs the toolchain kind; pass it through — extend `Launch`'s
  signature with a `toolchainKind string` param and thread it from `runProfile`, OR detect
  `spec.Argv[0] == "nix"`). The minimal, signature-free version:
```go
	if len(spec.Argv) > 0 && spec.Argv[0] == "nix" {
		return 1, fmt.Errorf("toolchain:nix is not supported in environment:container yet (read-only container vs writable /nix store); use environment:vm or host, or toolchain:mise")
	}
```

- [ ] **Step 5: Test** (`container_test.go`) — assert the embedded tools image carries mise, and
  the nix-in-container guard message exists:
```go
func TestToolsImageHasMise(t *testing.T) {
	b, err := readAsset("Dockerfile.agent.tools")
	if err != nil || !strings.Contains(string(b), "mise") {
		t.Fatalf("tools image missing mise: %v", err)
	}
}
```

- [ ] **Step 6: Run + commit** — `go test ./internal/engine/container/`; `make check-assets`.
```bash
git add library/layer/container/Dockerfile.agent.tools internal/engine/container/assets/Dockerfile.agent.tools internal/engine/container/launch.go internal/engine/container/container_test.go
git commit -m "sp5: bake pinned mise into the tools image; fast-fail nix-in-container (deferred)"
```
> **Env-gated:** the actual image build (`mise` reachable in-container) is verified by the SP3
> Docker smoke with a `toolchain: {kind: "mise"}` profile, not CI.

---

### Task 6: vm enablement — provision mise/nix in the guest

**Files:** Modify `internal/engine/vm/launch.go`, `internal/engine/vm/vm.go`, `internal/cli/cli.go`.

- [ ] **Step 1: Thread the toolchain kind to `vm.Launch`** — add a `toolchainKind string` param:
```go
func Launch(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (int, error) {
```
and in `internal/cli/cli.go`'s `runProfile`, update the `vm` case:
```go
	case "vm":
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		return vm.Launch(ctx, argv, prof.Network, secretEnv, stageDir, name, tk)
```

- [ ] **Step 2: Provision the toolchain in the VM** before launching the agent — in `vm.Launch`,
  after SSH is up (after `CloneAndBoot` returns `ip`, before the agent `RunInTerminal`):
```go
	if err := provisionToolchain(ctx, ip, toolchainKind); err != nil {
		return 1, err
	}
```
and add to `vm.go` (pinned, idempotent installers):
```go
// provisionToolchain installs mise/nix into the running VM if the toolchain needs it (idempotent:
// skips when the CLI is already present). Pinned installers — the VM is a full writable macOS.
func provisionToolchain(ctx context.Context, ip, kind string) error {
	var script string
	switch kind {
	case "mise":
		script = "command -v mise >/dev/null 2>&1 || curl -fsSL https://mise.run | sh"
	case "nix":
		script = "command -v nix >/dev/null 2>&1 || curl --proto '=https' --tlsv1.2 -sSf -L " +
			"https://install.determinate.systems/nix | sh -s -- install --no-confirm"
	default:
		return nil
	}
	if err := osCommand(ctx, sshArgv(ip, false, "zsh", "-lc", script)).Run(); err != nil {
		return fmt.Errorf("provision %s toolchain in vm: %w", kind, err)
	}
	return nil
}
```
> Pin the installer versions deliberately before relying on this in anger (the `mise.run` /
> Determinate installers fetch latest by default) — record the pin in the commit. For now the
> guard is functional; pinning is the Task 7 follow-through.

- [ ] **Step 3: Build** — `go build ./...`; `go vet ./...`; `go test ./...` → green (no VM needed;
  `provisionToolchain` only runs under a live VM). `gofmt -w internal/`.
- [ ] **Step 4: Commit**
```bash
git add internal/engine/vm/launch.go internal/engine/vm/vm.go internal/cli/cli.go
git commit -m "sp5: provision mise/nix in the VM guest when the profile's toolchain needs it"
```
> **Env-gated:** real provisioning needs Tart on Apple Silicon (SP4 smoke), not CI.

---

### Task 7: pinning note, full gate, roadmap record, PR

**Files:** Modify `specs/0001-go-rewrite-design.md`; (optionally) `internal/cli/cli.go` doctor note.

- [ ] **Step 1: Doctor flake.lock note (optional, nice)** — in `cmdDoctor`, after the tool lines,
  if a `nix` flake without a lock is the common footgun, a generic note is enough; mise/nix
  presence is already reported via the `tools` list. Skip if it complicates the JSON shape.
- [ ] **Step 2: Full Go gate** — `make check` (check-assets + vet + fmtcheck + `go test ./...`) and
  `make build`. `./slop doctor` shows `mise`/`nix`; `./slop validate` accepts a `toolchain:` profile.
- [ ] **Step 3: Four fish gates** — `fish -n scripts/*.fish`; `fish tests/run.fish`;
  `fish scripts/slop-sync-help.fish check`; `fish scripts/slop-pinning.fish`. All pass. **Pinning
  watch:** the new `MISE_VERSION` pin lives in the canonical `library/` Dockerfile (scanned by
  `slop-pinning`); ensure it is an exact version, not `latest`.
- [ ] **Step 4: Flip SP5 to complete** in `specs/0001-go-rewrite-design.md` §11:
```
SP0–SP5 are **complete** (SP5 = `specs/0007`, the `toolchain:` concept — mise/nix across all
environments, nix-in-container deferred). **SP6** (terminal TUI) is the next artifact.
```
- [ ] **Step 5: Commit + push + PR**
```bash
git add specs/0001-go-rewrite-design.md internal/cli/cli.go
git commit -m "sp5: record toolchains complete in the roadmap"
git push -u origin sp5-toolchains
gh pr create --title "SP5: toolchains — mise/nix toolchain: concept across all environments" \
  --body "Adds toolchain: {kind, run?} orthogonal to environment. Wrap-once core; per-env enablement (host/sandbox/container-mise/vm). nix-in-container deferred. See specs/0007."
```

---

## Verification (what "done" means)

- `make check` green: vet + gofmt + `go test ./...`, including the hermetic core —
  `Wrap` table (none/mise/nix × wrapper/run), `Wraps`, schema decode + bad-kind rejection, the
  seatbelt tool-store reads, the tools-image-has-mise asset check, the nix-in-container guard.
  **No Docker/VM/network in CI.**
- Four fish gates green; the `mise` version pin is exact (not `latest`).
- Manual smokes (env-gated): `toolchain:{kind:"mise"}` on host wraps `mise exec`; on container the
  built image runs `mise` (SP3 smoke); on vm provisions + runs (SP4 smoke); `toolchain:{kind:"nix"}`
  + `environment:"container"` fails fast with the deferral message.

## Deliberately deferred (not in SP5)

- **nix-in-container** — needs a writable `/nix` store volume + relaxed container hardening (or a
  nix-in-docker base); the `read_only` agent container conflicts with it. Fails fast for now.
- **Pinning the toolchain installers** (`mise.run` / Determinate) to exact versions/digests — Task 6
  ships the functional path; deliberate version pinning is the immediate follow-up (and the SP8
  safe-install machinery's job).
- **A slop-managed `mise.toml`/`flake.nix` scaffold** — SP5 consumes the workspace's existing
  pinned config; generating one is out of scope.
- **`toolchain` provisioning for `environment: host` beyond PATH** (e.g. installing mise/nix on the
  host if absent) — host runs assume the user already has the toolchain CLI.
