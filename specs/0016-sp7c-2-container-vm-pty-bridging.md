# SP7c-2 — container + vm PTY bridging for the embedded cockpit Implementation Plan

**Goal:** Extend the SP7c-1 session control plane so `environment: container` and `environment: vm` profiles run as embedded-cockpit sessions — the agent's terminal bridged through `docker compose run` / `ssh -t` onto the engine's host-side PTY, with teardown on session close.

**Architecture:** The SP7c-1 `control.Session` already owns a host PTY (`pty.Start`) and the `Attach` pump streams it to the app. SP7c-1 wired **host** (direct argv) and **sandbox** (`sandbox.WrapArgv` + temp-profile cleanup) into `resolveSession`. SP7c-2 adds the remaining two environments by the **same split** that `sandbox.WrapArgv` introduced: factor the side-effecting provisioning out of `container.Launch` / `vm.Launch` into a shared, in-package `provision` helper, leave `Launch` behaviour byte-for-byte identical (its existing tests guard this), and add a `PrepareSession(...) (argv, cleanup, err)` that returns the interactive argv to run on the Session PTY plus a `cleanup` closure that does the cockpit teardown (compose `down` + stage wipe for container; VM `Destroy` + stage wipe for vm). `resolveSession` calls these for the `container`/`vm` cases and returns a `control.SessionSpec{Argv, Dir, OnClose: cleanup}`. No new RPCs, no `.proto` change — the engine runs `docker compose run` / `ssh -t` on the PTY exactly as today's `RunInPTY` / `RunInTerminal` paths do, so docker/ssh bridge their own remote PTY to the slave and propagate `SIGWINCH` from `pty.Setsize` on the master.

**Tech stack:** Go, the existing `container`/`vm`/`exec`/`control` engine packages, `creack/pty` (already a dep). No new dependencies. No `.proto` / `make proto` change.

**Scope:** container + vm only — the last two environments for the cockpit. **Secrets/creds staging stays deferred**, exactly as in SP7c-1: cockpit sessions pass `secretEnv = nil` and run with the inherited host env. A stage dir is still provisioned because container/vm *require* one (compose.yml, entrypoint, `secrets.env`, scp source). Full secrets parity across all four environments remains its own future unit.

**Base branch:** new feature branch `sp7c-2-container-vm-pty` off `main` (SP7c-1 merged in PR #21, `main` @ `823f8f4`). **Never push `main`.**

**File structure:**
- `internal/engine/container/launch.go` (modify) — extract `provision`; `Launch` calls it; add `PrepareSession`.
- `internal/engine/container/launch_test.go` (modify) — add the `PrepareSession`-unavailable test.
- `internal/engine/vm/launch.go` (modify) — extract `provision`; `Launch` calls it; add `PrepareSession`.
- `internal/engine/vm/launch_test.go` (modify) — add the `PrepareSession`-unavailable test.
- `internal/cli/cli.go` (modify) — `resolveSession` gains `container` + `vm` cases (per-session unique stage dir; vm clone name derived from it).
- `internal/cli/cli_resolve_test.go` (create) — `resolveSession` maps host/sandbox to specs (no infra needed) and errors cleanly for container/vm when docker/tart are absent.

---

## Design decisions (read before executing)

1. **Mirror SP7c-1's secrets posture.** Cockpit container/vm sessions launch with `secretEnv = nil` (inherited host env). This keeps "full secrets/creds staging parity with `slop run`" a single coherent future unit spanning all four environments, rather than wiring it piecemeal here. `writeSecretsEnv(stageDir, nil)` is a no-op (returns `"", nil`), so the stack still provisions cleanly.

2. **Split provisioning from running; keep `Launch` identical.** The teardown semantics differ between `slop run` and the cockpit (`slop run` leaves the squid sidecar up for reuse and lets `slop down` stop it; the cockpit tears the stack down on session close). So `Launch` and `PrepareSession` **share the provisioning** (`provision`) but **own their teardown separately**. `Launch`'s observable behaviour is unchanged — its existing tests are the regression guard.

3. **Per-session unique stage dir.** `resolveSession` does not know the session id (it runs inside `OpenSession` *before* `Manager.Open` assigns one). To keep the SP7c-1 N-concurrent-sessions guarantee, it creates a unique stage dir with `os.MkdirTemp(<ws>/.slop/runtime, "cockpit-*")`; for vm, the dir's basename is reused as the VM clone name so two concurrent same-profile VM sessions don't collide on `tart` names. Cost: two concurrent same-profile **container** sessions get independent compose projects (hence separate squid sidecars) rather than sharing one — acceptable for v1, noted as a future optimization.

4. **No `.proto` / RPC change, no live docker/tart in CI.** Like the existing `container`/`vm` suites, the gate tests cover the deterministic surfaces (unavailable-rejection, error propagation, the host/sandbox resolver path). The real `docker compose run` / `ssh -t` PTY round-trip is verified **manually** on a host that has docker/tart (Task 4) — CI has neither, and the repo deliberately keeps no docker/tart-dependent tests in the gate.

---

### Task 1: container — `provision` + `PrepareSession`

**Files:**
- Modify: `internal/engine/container/launch.go:47-93`
- Test: `internal/engine/container/launch_test.go`

- [ ] **Step 1: Write the failing test.** Append to `internal/engine/container/launch_test.go`:

```go
func TestPrepareSessionRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, cleanup, err := PrepareSession(context.Background(), []string{"fish"}, t.TempDir(), "deny", nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error when docker unavailable")
	}
	if cleanup == nil {
		t.Fatal("cleanup must never be nil")
	}
	cleanup() // must be safe to call on the error path
}
```

- [ ] **Step 2: Run it, verify it fails.**

```bash
go test ./internal/engine/container/ -run TestPrepareSession -v
```
Expected: FAIL — `PrepareSession` undefined.

- [ ] **Step 3: Refactor `launch.go`.** Replace the existing `Launch` function (lines 47-93, the doc comment through the closing brace) with the extracted `provision`, a slimmed `Launch`, and the new `PrepareSession`:

```go
// provision materializes the per-run runtime dir and starts the compose stack, returning the
// interactive argv that runs the agent (`docker compose run --rm agent <argv>`) plus the compose
// file path (for teardown). Shared by Launch (slop run) and PrepareSession (the embedded cockpit).
// secretEnv is written to secrets.env and sourced by the entrypoint; SP7c-2 cockpit sessions pass
// nil (inherited-host-env parity with SP7c-1; full staging is a separate deferred unit).
func provision(ctx context.Context, agentArgv []string, workspace, network string, secretEnv []string, stageDir string) (argv []string, composeFile string, err error) {
	if !Available() {
		return nil, "", fmt.Errorf("container environment requires docker + docker compose v2 (run: slop doctor)")
	}
	if len(agentArgv) == 0 {
		return nil, "", exec.ErrNoArgv
	}
	if agentArgv[0] == "nix" {
		return nil, "", fmt.Errorf("toolchain:nix is not supported in environment:container yet (read-only container vs writable /nix store); use environment:vm or host, or toolchain:mise")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, "", err
	}
	_ = withRepoLock(workspace, func() error { return Reconcile(ctx, workspace, time.Hour) })
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return nil, "", err
	}
	_, npmErr := os.Stat(filepath.Join(stageDir, ".npmrc"))
	_, kubeErr := os.Stat(filepath.Join(stageDir, "kubeconfig"))
	_, sshErr := os.Stat(filepath.Join(stageDir, ".ssh", "id"))
	p := composeParams{
		RuntimeDir: stageDir,
		Workspace:  workspace,
		StageDir:   stageDir,
		Term:       os.Getenv("TERM"),
		NpmConfig:  npmErr == nil,
		Kubeconfig: kubeErr == nil,
		SshKey:     sshErr == nil,
	}
	composeFile, err = materializeRun(p, network == "allow")
	if err != nil {
		return nil, "", err
	}
	if err := Up(ctx, stageDir, composeFile); err != nil {
		return nil, "", err
	}
	return composeRunArgv(composeFile, agentArgv), composeFile, nil
}

// Launch runs spec.Argv in the agent container. secretEnv (the resolved profile secrets) is
// written to secrets.env and sourced by the entrypoint — never passed via -e, so it stays out
// of host `ps` and `docker inspect`. stageDir is the host .slop/runtime/<profile> dir (already
// holds .npmrc when pnpm creds were staged); it is bind-mounted ro at /slop/runtime and wiped
// on exit by the caller. The agent runs interactively through a PTY (design §6.2).
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, secretEnv []string, stageDir string) (int, error) {
	argv, _, err := provision(ctx, spec.Argv, workspace, network, secretEnv, stageDir)
	if err != nil {
		return 1, err
	}
	return exec.RunInPTY(ctx, exec.LaunchSpec{Argv: argv})
}

// PrepareSession provisions the agent container for an embedded-cockpit session (SP7c-2): it
// returns the interactive argv to run on the engine's PTY plus a cleanup that tears the stack
// down (compose down + stageDir wipe) when the session closes. Cockpit sessions pass secretEnv
// nil (inherited-host-env parity with SP7c-1). cleanup is always non-nil and safe to call once.
func PrepareSession(ctx context.Context, agentArgv []string, workspace, network string, secretEnv []string, stageDir string) (argv []string, cleanup func(), err error) {
	argv, composeFile, err := provision(ctx, agentArgv, workspace, network, secretEnv, stageDir)
	if err != nil {
		return nil, func() {}, err
	}
	return argv, func() {
		_ = Down(context.Background(), composeFile)
		_ = os.RemoveAll(stageDir)
	}, nil
}
```

> No import changes: `context`, `fmt`, `os`, `path/filepath`, `time`, and `internal/engine/exec` are already imported by `launch.go`. `Down` lives in `container.go` (same package).

- [ ] **Step 4: Run it, verify it passes** (new test + the unchanged `Launch` regression test).

```bash
go test ./internal/engine/container/ -v
```
Expected: PASS — `TestPrepareSessionRejectsWhenUnavailable` and `TestLaunchRejectsWhenUnavailable` both green.

- [ ] **Step 5: Commit.**

```bash
gofmt -w internal/engine/container/launch.go internal/engine/container/launch_test.go
git add internal/engine/container/launch.go internal/engine/container/launch_test.go
git commit -m "feat(container): PrepareSession for cockpit (provision + teardown), Launch unchanged"
```

---

### Task 2: vm — `provision` + `PrepareSession`

**Files:**
- Modify: `internal/engine/vm/launch.go:11-55`
- Test: `internal/engine/vm/launch_test.go`

- [ ] **Step 1: Write the failing test.** Append to `internal/engine/vm/launch_test.go`:

```go
func TestPrepareSessionRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, cleanup, err := PrepareSession(context.Background(), []string{"zsh"}, "allow", nil, t.TempDir(), "p", "")
	if err == nil {
		t.Fatal("expected error when tart unavailable")
	}
	if cleanup == nil {
		t.Fatal("cleanup must never be nil")
	}
	cleanup() // must be safe to call on the error path
}
```

> Add `"context"` to the test file's imports (it currently imports only `os`, `path/filepath`, `testing`).

- [ ] **Step 2: Run it, verify it fails.**

```bash
go test ./internal/engine/vm/ -run TestPrepareSession -v
```
Expected: FAIL — `PrepareSession` undefined.

- [ ] **Step 3: Refactor `launch.go`.** Replace the existing `Launch` function (lines 11-55, the doc comment through the closing brace) with the extracted `provision`, a slimmed `Launch`, and the new `PrepareSession`:

```go
// provision boots a disposable session VM, provisions its toolchain, scp's the staged dir in,
// and returns the `ssh -t` argv that runs the agent remotely (sourcing secrets.env over ssh).
// Shared by Launch (slop run) and PrepareSession (the embedded cockpit). On any failure after the
// VM boots, provision destroys it so no VM leaks; on success the caller owns teardown (Destroy +
// stage wipe). network "deny" requires SLOP_VM_PROXY_URL; "allow" is full VM network.
func provision(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (argv []string, err error) {
	if !Available() {
		return nil, fmt.Errorf("vm environment requires tart (Apple-Silicon macOS) — run: slop doctor")
	}
	if len(agentArgv) == 0 {
		return nil, exec.ErrNoArgv
	}
	proxyURL := ""
	if network == "deny" {
		proxyURL = os.Getenv("SLOP_VM_PROXY_URL")
		if proxyURL == "" {
			return nil, fmt.Errorf("network:%q needs SLOP_VM_PROXY_URL (a squid/proxy URL); set it or use network:\"allow\"", network)
		}
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return nil, err
	}
	if err := EnsureBase(ctx); err != nil {
		return nil, err
	}
	_ = Reconcile(ctx, profile) // reclaim an orphaned session from a prior crash
	ip, err := CloneAndBoot(ctx, profile)
	if err != nil {
		return nil, err
	}
	if err := provisionToolchain(ctx, ip, toolchainKind); err != nil {
		_ = Destroy(context.Background(), profile)
		return nil, err
	}
	if err := runScp(ctx, ip, stageDir, "~/.slop-runtime"); err != nil {
		_ = Destroy(context.Background(), profile)
		return nil, err
	}
	remote := remoteAgentCmd(agentArgv, proxyURL)
	return sshArgv(ip, true, "zsh", "-lc", remote), nil
}

// Launch clones+boots a disposable session VM, copies the staged dir in, runs the agent over
// ssh -t (sourcing secrets remotely), and destroys the VM on exit. secretEnv (resolved profile
// secrets) is written to secrets.env in stageDir; the whole stageDir is scp'd to ~/.slop-runtime.
// network "deny" requires SLOP_VM_PROXY_URL (advisory egress); "allow" is full VM network.
func Launch(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (int, error) {
	argv, err := provision(ctx, agentArgv, network, secretEnv, stageDir, profile, toolchainKind)
	if err != nil {
		return 1, err
	}
	defer func() { _ = Destroy(context.Background(), profile) }() // disposable: always tear down
	return exec.RunInTerminal(ctx, exec.LaunchSpec{Argv: argv})
}

// PrepareSession provisions a disposable VM for an embedded-cockpit session (SP7c-2): it returns
// the `ssh -t` argv to run on the engine's PTY plus a cleanup that destroys the VM and wipes the
// stage when the session closes. Cockpit sessions pass secretEnv nil (inherited-host-env parity
// with SP7c-1). cleanup is always non-nil and safe to call once.
func PrepareSession(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (argv []string, cleanup func(), err error) {
	argv, err = provision(ctx, agentArgv, network, secretEnv, stageDir, profile, toolchainKind)
	if err != nil {
		return nil, func() {}, err
	}
	return argv, func() {
		_ = Destroy(context.Background(), profile)
		_ = os.RemoveAll(stageDir)
	}, nil
}
```

> No import changes: `context`, `fmt`, `os`, `path/filepath`, and `internal/engine/exec` are already imported by `launch.go`. `EnsureBase`, `Reconcile`, `CloneAndBoot`, `Destroy` live in `vm.go`; `sshArgv`/`scpArgv`/`remoteAgentCmd`, `runScp`, `writeSecretsEnv`, `provisionToolchain` are same-package helpers.

- [ ] **Step 4: Run it, verify it passes** (new test + the unchanged `Launch`/secrets regression tests).

```bash
go test ./internal/engine/vm/ -v
```
Expected: PASS — `TestPrepareSessionRejectsWhenUnavailable`, `TestLaunchRejectsWhenUnavailable`, `TestLaunchDenyNeedsProxyURL`, `TestWriteSecretsEnvEscapesAndIs0600` all green.

- [ ] **Step 5: Commit.**

```bash
gofmt -w internal/engine/vm/launch.go internal/engine/vm/launch_test.go
git add internal/engine/vm/launch.go internal/engine/vm/launch_test.go
git commit -m "feat(vm): PrepareSession for cockpit (provision + teardown), Launch unchanged"
```

---

### Task 3: wire container + vm into `resolveSession`

**Files:**
- Modify: `internal/cli/cli.go` (the `resolveSession` `switch prof.Environment` block — currently `host`, `sandbox`/`""`, and a `default` that rejects container/vm)
- Test: `internal/cli/cli_resolve_test.go` (create)

- [ ] **Step 1: Write the failing test.** Create `internal/cli/cli_resolve_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/sandbox"
)

const resolverCue = `package slop
slop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny"}
		s: {agent: "claude", environment: "sandbox", network: "deny"}
		c: {agent: "claude", environment: "container", network: "deny"}
		v: {agent: "claude", environment: "vm", network: "allow"}
	}
}
`

func writeResolverCue(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveSessionHostAndSandbox(t *testing.T) {
	path := writeResolverCue(t)

	h, err := resolveSession("h", path)
	if err != nil {
		t.Fatalf("host resolve: %v", err)
	}
	if len(h.Argv) == 0 || h.Argv[0] != "claude" {
		t.Fatalf("host argv = %v, want it to start with claude", h.Argv)
	}
	if h.OnClose != nil {
		t.Fatal("host session needs no cleanup")
	}

	s, err := resolveSession("s", path)
	if err != nil {
		t.Fatalf("sandbox resolve: %v", err)
	}
	if len(s.Argv) == 0 || s.Argv[0] != sandbox.SandboxExecPath {
		t.Fatalf("sandbox argv = %v, want it to start with %s", s.Argv, sandbox.SandboxExecPath)
	}
	if s.OnClose == nil {
		t.Fatal("sandbox session must carry a cleanup (temp profile removal)")
	}
	s.OnClose() // must not panic
}

func TestResolveSessionContainerVMErrorWhenToolingAbsent(t *testing.T) {
	path := writeResolverCue(t)
	t.Chdir(t.TempDir())  // any cockpit-* stage dir lands under a throwaway cwd, not the repo
	t.Setenv("PATH", "") // docker + tart unavailable

	// The error must come from the real provisioning path (PrepareSession -> "docker"/"tart"
	// unavailable), not the pre-SP7c-2 "is SP7c-2" sentinel — that's what makes this fail first.
	if _, err := resolveSession("c", path); err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("container resolve must reach PrepareSession and fail on docker availability, got %v", err)
	}
	if _, err := resolveSession("v", path); err == nil || !strings.Contains(err.Error(), "tart") {
		t.Fatalf("vm resolve must reach PrepareSession and fail on tart availability, got %v", err)
	}
}
```

- [ ] **Step 2: Run it, verify it fails.**

```bash
go test ./internal/cli/ -run TestResolveSession -v
```
Expected: FAIL — `TestResolveSessionContainerVMErrorWhenToolingAbsent` fails because the current `default` case rejects container/vm with the "...is SP7c-2" sentinel, whose message contains neither "docker" nor "tart"; the substring assertions only pass once the real `PrepareSession` provisioning path is wired. (`TestResolveSessionHostAndSandbox` already passes against the current code — it pins the SP7c-1 host/sandbox paths against regression.)

- [ ] **Step 3: Add the container + vm cases.** In `internal/cli/cli.go`, replace the `default` arm of `resolveSession`'s `switch prof.Environment` (the one returning the `"embedded cockpit supports environment host/sandbox in SP7c-1; %q is SP7c-2"` error) with the two real cases plus a tightened default:

```go
	case "container":
		base := filepath.Join(ws, ".slop", "runtime")
		if err := os.MkdirAll(base, 0o700); err != nil {
			return control.SessionSpec{}, err
		}
		stageDir, err := os.MkdirTemp(base, "cockpit-*")
		if err != nil {
			return control.SessionSpec{}, err
		}
		cargv, cleanup, err := container.PrepareSession(context.Background(), argv, ws, prof.Network, nil, stageDir)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: cargv, Dir: ws, OnClose: cleanup}, nil
	case "vm":
		base := filepath.Join(ws, ".slop", "runtime")
		if err := os.MkdirAll(base, 0o700); err != nil {
			return control.SessionSpec{}, err
		}
		stageDir, err := os.MkdirTemp(base, "cockpit-*")
		if err != nil {
			return control.SessionSpec{}, err
		}
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		// stageDir basename is the per-session VM clone name, so concurrent same-profile
		// sessions don't collide on tart names (SP7c-1 N-session guarantee).
		vargv, cleanup, err := vm.PrepareSession(context.Background(), argv, prof.Network, nil, stageDir, filepath.Base(stageDir), tk)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: vargv, Dir: ws, OnClose: cleanup}, nil
	default:
		return control.SessionSpec{}, fmt.Errorf("unknown environment %q", prof.Environment)
	}
```

> No import changes: `context`, `os`, `path/filepath`, `fmt`, `container`, `vm`, and `control` are all already imported by `cli.go`. Update the `resolveSession` doc comment's "host/sandbox only (SP7c-1); container/vm follow (SP7c-2)" line to "all four environments (host/sandbox direct; container/vm provisioned + torn down on close)".

- [ ] **Step 4: Run it, verify it passes.**

```bash
go test ./internal/cli/ -run TestResolveSession -v
go build ./...
```
Expected: PASS for both resolver tests; build green.

- [ ] **Step 5: Commit.**

```bash
gofmt -w internal/cli/cli.go internal/cli/cli_resolve_test.go
git add internal/cli/cli.go internal/cli/cli_resolve_test.go
git commit -m "feat(cli): resolve container + vm profiles to cockpit sessions (PrepareSession + teardown)"
```

---

### Task 4: full verification + manual round-trip + PR

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
go test ./internal/engine/control/... -race
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green. (No `.proto` changed, so `make proto` is not needed; the committed stubs are unchanged.)

- [ ] **Step 2: Manual PTY round-trip** (only where docker / tart are installed — CI has neither, so this is a human step, not an automated gate). For container, with a `slop.cue` declaring a `container` profile in the cwd:

```bash
./slop serve &                      # start the control plane on ~/.slop/s.sock
# From a gRPC client (or the SwiftUI cockpit): OpenSession{profile:"<container-profile>"} ->
#   Attach{attach_session_id}; type `tty` + Enter; confirm a /dev/pts device echoes back
#   (proves the container TTY is bridged), resize the window and run `stty size` (proves
#   SIGWINCH propagates), then CloseSession and confirm `docker ps` shows the agent gone.
kill %1
```
Record the observed result (pass/fail + what you saw) in the PR description. If docker/tart are unavailable on this host, say so explicitly rather than claiming a pass.

- [ ] **Step 3: Push + PR.**

```bash
git push -u origin sp7c-2-container-vm-pty
gh pr create --base main --title "SP7c-2: container + vm PTY bridging for the embedded cockpit" --body "$(cat <<'EOF'
## Summary
Extends the SP7c-1 session control plane to the last two environments: `container` and `vm` profiles now run as embedded-cockpit sessions, their terminal bridged through `docker compose run` / `ssh -t` onto the engine's host-side PTY, with teardown on session close. No `.proto` / RPC change.

- `container.PrepareSession` / `vm.PrepareSession`: provisioning split out of `Launch` into a shared in-package `provision`; `Launch` behaviour unchanged (existing tests guard it). `PrepareSession` returns the interactive argv + a cleanup closure (compose `down` + stage wipe / VM `Destroy` + stage wipe).
- cli: `resolveSession` gains `container` + `vm` cases — per-session unique stage dir (`os.MkdirTemp`), vm clone name derived from it so concurrent same-profile sessions don't collide.
- The agent argv runs on the SP7c-1 `Session` PTY exactly as today's `RunInPTY` / `RunInTerminal` paths, so docker/ssh bridge their own remote PTY and `pty.Setsize` resize propagates via `SIGWINCH`.

## Deferred (unchanged from SP7c-1)
Full secrets/creds staging parity with `slop run` (cockpit sessions use the inherited host env across all four environments); scrollback/reconnect; the SwiftUI app.

## Test
`make check` + `make build` green; `go test ./internal/engine/control/... -race` clean; container/vm `PrepareSession` unavailable-rejection + the unchanged `Launch` regression tests; cli resolver maps host/sandbox to specs and errors cleanly for container/vm when docker/tart are absent; four fish gates green. Live docker/tart PTY round-trip: see manual-verification note above.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` green; `go test ./internal/engine/control/... -race` clean; four fish gates green.
- `container.Launch` / `vm.Launch` behaviour is byte-for-byte unchanged (existing tests pass untouched).
- `container.PrepareSession` / `vm.PrepareSession` return a runnable argv + a non-nil cleanup; cleanup is safe to call once and removes the stage dir (and stops the container / destroys the VM).
- `resolveSession` maps `host`/`sandbox`/`container`/`vm` profiles to a `control.SessionSpec`; container/vm error cleanly (and leave no stage dir behind) when docker/tart are absent.
- Manual: a `container` (and, where tart is available, a `vm`) cockpit session round-trips terminal I/O through the PTY and tears down on close.

## Deliberately deferred (not here)

- **Full secrets/creds staging parity** with `slop run` for cockpit sessions (all four environments still use the inherited host env).
- **Sharing one squid sidecar** across concurrent same-profile container sessions (v1 gives each its own compose project).
- **Scrollback / reconnect-after-drop** (engine streams live bytes only, per specs/0014 §10).
- **The SwiftUI app** (SwiftTerm + WindowGroup + chrome) — jojo's Xcode track, against the committed `.proto`.
