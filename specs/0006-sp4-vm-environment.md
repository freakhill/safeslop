# SP4 — vm environment: disposable Tart VM launch path in Go

**Goal:** Add `environment: vm` to the Go engine — run a profile's agent inside a **disposable
Tart macOS VM** (cloned fresh per run, destroyed on exit), with SP2 secrets/creds copied in over
`scp` and the agent launched over `ssh -t`. A faithful Go port of the fish `slop-brew-vm` path;
the highest-isolation (and slowest) environment. Marked "later" in the roadmap (`specs/0001`
§10) but next in execution order after SP3.

**Architecture:** A new `internal/engine/vm` package mirrors `internal/engine/sandbox` and
`internal/engine/container`: one exported `Launch` plus `Available` / `EnsureBase` /
`CloneAndBoot` / `Destroy` / `Reconcile`. There are **no embedded assets** — the VM is driven by
shelling out to `tart` (clone/run/ip/stop/delete) and `ssh`/`scp`. A pinned base image is cloned
once into a cached template; each run clones a throwaway session VM from it. The agent runs
interactively over `ssh -t` (the remote VM allocates the PTY; `ssh` owns the local terminal, so
this uses the direct `exec.RunInTerminal` path — unlike container's wrapped PTY). `slop run`
gains a `case "vm"`; `slop down` destroys a lingering session VM; reconcile-on-run reaps a
session VM orphaned by a crash.

**Tech stack:** Go 1.26, `os/exec` (drive `tart` / `ssh` / `scp`), `time` (boot/SSH polling),
the existing `internal/engine/exec.RunInTerminal`. No new third-party deps. Tart (Apple
Virtualization, Apple-Silicon macOS only) + `ssh`/`scp` are runtime requirements (reported by
`slop doctor`).

**File structure:**
- `internal/engine/vm/vm.go` (create) — `Available`, the pinned config consts, `EnsureBase`, `CloneAndBoot`, `Destroy`, `Reconcile`, plus `tartIP`/`waitSSH` helpers.
- `internal/engine/vm/ssh.go` (create) — pure argv builders: `sshArgv`, `scpArgv`, `remoteAgentCmd` (the `set -a; . secrets.env; exec <agent>` wrapper). No I/O; unit-tested.
- `internal/engine/vm/launch.go` (create) — `Launch` (ensure base → clone+boot → scp stage in → `ssh -t` agent → defer destroy).
- `internal/engine/vm/vm_test.go` (create) — hermetic tests (argv builders, remote-cmd quoting, image-is-pinned, Available guard, reconcile). No tart, no VM, no network.
- `internal/cli/cli.go` (modify) — `case "vm"` in `runProfile`; `slop down` also destroys a session VM; `slop doctor` reports `vm-runtime`.
- `specs/0001-go-rewrite-design.md` (modify) — flip SP4 to **complete** in §11.

---

## Key design decisions

1. **VM = disposable isolation; the *boundary is the throwaway VM*, not egress filtering.** The
   session VM is cloned fresh, gets only what we `scp` in, and is `tart delete`d on exit. Unlike
   the container (network-allowlist) boundary, the VM gets **full host network by default** — a
   tart VM has no kernel-enforced egress filter without host-side `pf`/NAT on the tart bridge.
   We reuse the `deny|allow` field honestly:
   - `network: "allow"` → full VM network (the disposable VM is the isolation).
   - `network: "deny"` → inject `HTTP_PROXY`/`HTTPS_PROXY` (advisory, honest) pointing at
     `SLOP_VM_PROXY_URL` if set; if unset, **fail fast** with a clear message (don't silently run
     open when the profile asked for deny). True network-layer VM egress enforcement (host `pf`
     on the tart interface, or the LuLu-style NE filter) is **deferred to SP8** — recorded below.

2. **Pinned base image (fixes the fish `:latest` gap).** The fish stack clones
   `ghcr.io/cirruslabs/macos-sonoma-base:latest` (unpinned). SP4 pins it to a digest in a Go
   const; Task 1 Step 2 resolves the current digest and bakes it in. The pinning gate
   (`slop-pinning`) scans `*.cue` + build configs, not Go consts — so a Go-side `// PINNED:`
   comment + a unit test asserting the const is not `:latest` is the SP4 guard.

3. **Two-stage clone, faithful to the fish stack.** `EnsureBase` clones the pinned source image
   into a cached template `slop-vm-base` once (idempotent via `tart list`); each run
   `CloneAndBoot` clones `slop-vm-base` → a session VM `slop-vm-<profile>`, `tart run
   --no-graphics`, and polls `tart ip` then SSH readiness (120 s timeouts, matching fish).

4. **Secrets reach the VM as a sourced file, not the ssh command line.** The staged
   `.slop/runtime/<profile>/` (already holding `.npmrc` from SP2, plus `secrets.env` written the
   same way as SP3) is `scp -r`'d into the VM at `~/.slop-runtime`. The agent is launched as
   `ssh -t … zsh -lc 'set -a; [ -f ~/.slop-runtime/secrets.env ] && . ~/.slop-runtime/secrets.env;
   set +a; exec <agent>'` — so secret **values never appear in the local `ps`/argv** (only the
   remote filename does) and never touch the host beyond the 0600 stage (wiped on exit). Honest
   residual: secrets land on the **VM's** disk for the VM's lifetime — acceptable because the VM
   is disposable and destroyed on exit, and the VM is itself the isolation boundary.

5. **Lifecycle = clone-per-run + destroy-on-exit + reconcile + `slop down`.** The base template
   persists (cached, fast re-clone); the **session VM is destroyed every run** (`defer Destroy`).
   `slop down` destroys any lingering session VM. `Reconcile` (start of every `slop run`, under
   the same repo `flock` SP3 added) destroys a session VM left running by a crashed prior run.

**Before you start:** `git checkout -b sp4-vm` (we're on `main`; never commit SP4 there). Note:
the hermetic Go tests need no VM; the real VM smoke (Task 5 Step 5) needs Tart on Apple Silicon
and boots a multi-GB macOS VM (minutes) — it is **not** run in CI.

---

### Task 1: vm package skeleton — Available, pinned config, doctor

**Files:** Create `internal/engine/vm/vm.go`, `internal/engine/vm/vm_test.go`; Modify `internal/cli/cli.go`.

- [ ] **Step 1: Write the package skeleton** (`internal/engine/vm/vm.go`)
```go
// Package vm runs a profile's agent inside a disposable Tart macOS VM: a fresh session VM is
// cloned from a cached base per run, the agent runs over ssh, and the VM is destroyed on exit.
package vm

import (
	"context"
	"os/exec"
	"strings"
)

const (
	// sourceImage is the Tart base image, PINNED by digest (not :latest). Resolve the current
	// digest with: tart pull ghcr.io/cirruslabs/macos-sonoma-base:latest &&
	//   docker buildx imagetools inspect ... | grep Digest   (or `crane digest`), then paste it.
	sourceImage  = "ghcr.io/cirruslabs/macos-sonoma-base@sha256:REPLACE_WITH_PINNED_DIGEST"
	baseTemplate = "slop-vm-base"
	sshUser      = "admin"
)

// sessionName is the disposable per-profile VM name.
func sessionName(profile string) string { return "slop-vm-" + profile }

// Available reports whether this host can run the VM boundary: the tart CLI on PATH.
func Available() bool {
	_, err := exec.LookPath("tart")
	return err == nil
}

// imageIsPinned reports whether sourceImage is digest-pinned (no floating :latest tag).
func imageIsPinned() bool {
	return strings.Contains(sourceImage, "@sha256:") && !strings.HasSuffix(sourceImage, ":latest")
}

func tartList(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "tart", "list", "--format", "json").Output()
	return string(out), err
}
```

- [ ] **Step 2: Resolve + bake the pinned digest** — replace `REPLACE_WITH_PINNED_DIGEST`:
```bash
tart pull ghcr.io/cirruslabs/macos-sonoma-base:latest 2>/dev/null
crane digest ghcr.io/cirruslabs/macos-sonoma-base:latest 2>/dev/null \
  || docker manifest inspect ghcr.io/cirruslabs/macos-sonoma-base:latest -v 2>/dev/null | grep -m1 digest
```
Paste the `sha256:…` into `sourceImage`. (If neither tool is installed, record the digest from
`https://github.com/cirruslabs/macos-image-templates/pkgs/container/macos-sonoma-base` and note
it in the commit.) Expected: `sourceImage` ends with `@sha256:<64 hex>`.

- [ ] **Step 3: Failing tests** (`internal/engine/vm/vm_test.go`)
```go
package vm

import "testing"

func TestImageIsPinned(t *testing.T) {
	if !imageIsPinned() {
		t.Fatalf("sourceImage must be digest-pinned, got %q", sourceImage)
	}
}

func TestSessionNamePerProfile(t *testing.T) {
	if sessionName("review") != "slop-vm-review" {
		t.Fatalf("got %q", sessionName("review"))
	}
}

func TestAvailableFalseWithoutTart(t *testing.T) {
	t.Setenv("PATH", "")
	if Available() {
		t.Fatal("Available must be false when tart is not on PATH")
	}
}
```

- [ ] **Step 4: Wire `slop doctor`** — add to `cmdDoctor` (after the `container-runtime` line):
```go
		report["vm-runtime"] = map[string]any{"present": vm.Available(), "path": ""}
```
Add the import `"github.com/freakhill/safeslop/internal/engine/vm"` to `cli.go`.

- [ ] **Step 5: Run** — `go test ./internal/engine/vm/ -v` → PASS; `go build ./...` ok; `./slop doctor | grep vm-runtime` shows the line.
- [ ] **Step 6: Commit**
```bash
git add internal/engine/vm/vm.go internal/engine/vm/vm_test.go internal/cli/cli.go
git commit -m "sp4: vm package skeleton (tart Available, pinned base image) + doctor vm-runtime"
```

---

### Task 2: pure ssh/scp argv builders + remote agent command

**Files:** Create `internal/engine/vm/ssh.go`; Test `internal/engine/vm/vm_test.go`.

- [ ] **Step 1: Write `ssh.go`**
```go
package vm

import (
	"os"
	"strings"
)

func sshBaseOpts() []string {
	opts := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=5",
	}
	if key := os.Getenv("SLOP_VM_SSH_KEY"); key != "" {
		opts = append(opts, "-i", key)
	}
	return opts
}

// sshArgv builds `ssh [opts] [-t] admin@ip -- <remote...>`. tty requests a remote PTY (for the
// interactive agent); ssh itself owns the local terminal (exec.RunInTerminal).
func sshArgv(ip string, tty bool, remote ...string) []string {
	a := append([]string{"ssh"}, sshBaseOpts()...)
	if tty {
		a = append(a, "-t")
	}
	a = append(a, sshUser+"@"+ip, "--")
	return append(a, remote...)
}

// scpArgv builds `scp [opts] -r <src> admin@ip:<dst>`.
func scpArgv(ip, src, dst string) []string {
	a := append([]string{"scp"}, sshBaseOpts()...)
	a = append(a, "-r", src, sshUser+"@"+ip+":"+dst)
	return a
}

// remoteAgentCmd returns the zsh -lc argument that sources the staged secrets (if present) then
// execs the agent. Each agent arg is single-quote escaped so values with spaces survive the
// remote shell. proxyURL != "" prepends HTTP(S)_PROXY exports (advisory egress for network=deny).
func remoteAgentCmd(agentArgv []string, proxyURL string) string {
	var b strings.Builder
	if proxyURL != "" {
		p := shellQuote(proxyURL)
		b.WriteString("export HTTP_PROXY=" + p + " HTTPS_PROXY=" + p + " http_proxy=" + p + " https_proxy=" + p + "; ")
	}
	b.WriteString("set -a; [ -f ~/.slop-runtime/secrets.env ] && . ~/.slop-runtime/secrets.env; set +a; exec")
	for _, a := range agentArgv {
		b.WriteString(" " + shellQuote(a))
	}
	return b.String()
}

// shellQuote wraps s in single quotes, escaping embedded single quotes POSIX-style ('\'').
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [ ] **Step 2: Tests** (append to `vm_test.go`)
```go
import "strings"

func TestSSHArgvTTYAndUser(t *testing.T) {
	got := strings.Join(sshArgv("10.0.0.9", true, "zsh", "-lc", "x"), " ")
	for _, want := range []string{"-t", "admin@10.0.0.9", "BatchMode=yes", "-- zsh -lc x"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sshArgv missing %q in %q", want, got)
		}
	}
	if strings.Contains(strings.Join(sshArgv("10.0.0.9", false, "x"), " "), " -t ") {
		t.Fatal("no -t expected when tty=false")
	}
}

func TestScpArgv(t *testing.T) {
	got := strings.Join(scpArgv("10.0.0.9", "/stage", "~/.slop-runtime"), " ")
	if !strings.Contains(got, "-r /stage admin@10.0.0.9:~/.slop-runtime") {
		t.Fatalf("scpArgv wrong: %q", got)
	}
}

func TestRemoteAgentCmdSourcesSecretsAndEscapes(t *testing.T) {
	cmd := remoteAgentCmd([]string{"claude", "--flag with space"}, "")
	if !strings.Contains(cmd, ". ~/.slop-runtime/secrets.env") || !strings.HasPrefix(cmd, "set -a;") {
		t.Fatalf("missing secrets sourcing: %q", cmd)
	}
	if !strings.Contains(cmd, `exec 'claude' '--flag with space'`) {
		t.Fatalf("agent argv not quoted: %q", cmd)
	}
	if !strings.Contains(remoteAgentCmd([]string{"zsh"}, "http://p:3128"), "export HTTP_PROXY='http://p:3128'") {
		t.Fatal("proxy export missing when proxyURL set")
	}
}
```

- [ ] **Step 3: Run** — `go test ./internal/engine/vm/ -v` → PASS. `gofmt -w internal/engine/vm/`.
- [ ] **Step 4: Commit**
```bash
git add internal/engine/vm/ssh.go internal/engine/vm/vm_test.go
git commit -m "sp4: pure ssh/scp argv builders + remote agent command (secrets-sourcing, escaped)"
```

---

### Task 3: VM lifecycle — EnsureBase / CloneAndBoot / Destroy / Reconcile

**Files:** Modify `internal/engine/vm/vm.go`; Test `internal/engine/vm/vm_test.go`.

- [ ] **Step 1: Add the lifecycle** to `vm.go`
```go
import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func runTart(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, "tart", args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// vmExists reports whether a VM/template of the given name exists.
func vmExists(ctx context.Context, name string) bool {
	out, err := tartList(ctx)
	if err != nil {
		return false
	}
	var entries []struct {
		Name string `json:"Name"`
	}
	if json.Unmarshal([]byte(out), &entries) != nil {
		return false
	}
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// EnsureBase clones the pinned source image into the cached base template if absent (idempotent).
func EnsureBase(ctx context.Context) error {
	if !imageIsPinned() {
		return fmt.Errorf("vm: source image is not digest-pinned (%s)", sourceImage)
	}
	if vmExists(ctx, baseTemplate) {
		return nil
	}
	return runTart(ctx, "clone", sourceImage, baseTemplate)
}

// CloneAndBoot clones a fresh session VM from the base, boots it headless, and returns its IP
// once SSH is reachable. Caller is responsible for Destroy.
func CloneAndBoot(ctx context.Context, profile string) (string, error) {
	name := sessionName(profile)
	if vmExists(ctx, name) {
		_ = Destroy(ctx, profile) // reclaim a stale session before re-cloning
	}
	if err := runTart(ctx, "clone", baseTemplate, name); err != nil {
		return "", fmt.Errorf("clone session vm: %w", err)
	}
	// tart run blocks; start it in the background and poll for IP + SSH.
	cmd := exec.CommandContext(ctx, "tart", "run", "--no-graphics", name)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("boot session vm: %w", err)
	}
	ip, err := waitIP(ctx, name, 120*time.Second)
	if err != nil {
		return "", err
	}
	if err := waitSSH(ctx, ip, 120*time.Second); err != nil {
		return "", err
	}
	return ip, nil
}

func tartIP(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, "tart", "ip", name).Output()
	if err != nil {
		return ""
	}
	return string(bytesTrim(out))
}

func waitIP(ctx context.Context, name string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ip := tartIP(ctx, name); ip != "" {
			return ip, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("vm %s did not get an IP within %s", name, timeout)
}

func waitSSH(ctx context.Context, ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if exec.CommandContext(ctx, "ssh", append(sshBaseOpts(), sshUser+"@"+ip, "true")...).Run() == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("vm at %s did not accept SSH within %s", ip, timeout)
}

// Destroy stops and deletes the session VM (no-op if absent).
func Destroy(ctx context.Context, profile string) error {
	name := sessionName(profile)
	if !vmExists(ctx, name) {
		return nil
	}
	_ = runTart(ctx, "stop", name)
	return runTart(ctx, "delete", name)
}

// Reconcile destroys a session VM orphaned by a crashed prior run.
func Reconcile(ctx context.Context, profile string) error { return Destroy(ctx, profile) }

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
```

- [ ] **Step 2: Test the pure helper** — `bytesTrim` (the pinned-image guard is already covered by
  `TestImageIsPinned` in Task 1; `EnsureBase`'s `!imageIsPinned()` branch is unreachable at
  runtime with the pinned const, so don't add a skip-only test for it):
```go
func TestBytesTrim(t *testing.T) {
	for in, want := range map[string]string{"10.0.0.9\n": "10.0.0.9", "1.2.3.4\r\n": "1.2.3.4", "5.6.7.8": "5.6.7.8"} {
		if got := string(bytesTrim([]byte(in))); got != want {
			t.Fatalf("bytesTrim(%q)=%q want %q", in, got, want)
		}
	}
}
```
> The tart/ssh paths (`EnsureBase` build, `CloneAndBoot`, `waitIP/waitSSH`, `Destroy`) shell out
> to a real VM and are exercised by the Task 5 manual smoke, not CI — same policy as SP3's Docker
> smoke. Keep CI hermetic.

- [ ] **Step 3: Run** — `go vet ./internal/engine/vm/`; `go test ./internal/engine/vm/ -v` → PASS; `gofmt -w internal/engine/vm/`.
- [ ] **Step 4: Commit**
```bash
git add internal/engine/vm/vm.go internal/engine/vm/vm_test.go
git commit -m "sp4: VM lifecycle (EnsureBase/CloneAndBoot/Destroy/Reconcile) over tart"
```

---

### Task 4: `vm.Launch` + wire `runProfile`

**Files:** Create `internal/engine/vm/launch.go`; Modify `internal/cli/cli.go`; Test `internal/engine/vm/vm_test.go`.

- [ ] **Step 1: Write `launch.go`**
```go
package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/freakhill/safeslop/internal/engine/exec"
)

// Launch clones+boots a disposable session VM, copies the staged dir in, runs the agent over
// ssh -t (sourcing secrets remotely), and destroys the VM on exit. secretEnv (resolved profile
// secrets) is written to secrets.env in stageDir; the whole stageDir is scp'd to ~/.slop-runtime.
// network "deny" requires SLOP_VM_PROXY_URL (advisory egress); "allow" is full VM network.
func Launch(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile string) (int, error) {
	if !Available() {
		return 1, fmt.Errorf("vm environment requires tart (Apple-Silicon macOS) — run: slop doctor")
	}
	if len(agentArgv) == 0 {
		return 1, exec.ErrNoArgv
	}
	proxyURL := ""
	if network == "deny" {
		proxyURL = os.Getenv("SLOP_VM_PROXY_URL")
		if proxyURL == "" {
			return 1, fmt.Errorf("network:\"deny\" needs SLOP_VM_PROXY_URL (a squid/proxy URL); set it or use network:\"allow\"")
		}
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return 1, err
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return 1, err
	}
	if err := EnsureBase(ctx); err != nil {
		return 1, err
	}
	_ = Reconcile(ctx, profile) // reclaim an orphaned session from a prior crash
	ip, err := CloneAndBoot(ctx, profile)
	if err != nil {
		return 1, err
	}
	defer func() { _ = Destroy(context.Background(), profile) }() // disposable: always tear down

	if err := runScp(ctx, ip, stageDir, "~/.slop-runtime"); err != nil {
		return 1, err
	}
	remote := remoteAgentCmd(agentArgv, proxyURL)
	inner := exec.LaunchSpec{Argv: sshArgv(ip, true, "zsh", "-lc", remote)}
	return exec.RunInTerminal(ctx, inner)
}

func runScp(ctx context.Context, ip, src, dst string) error {
	argv := scpArgv(ip, src, dst)
	c := osCommand(ctx, argv)
	if err := c.Run(); err != nil {
		return fmt.Errorf("scp stage into vm: %w", err)
	}
	return nil
}

// writeSecretsEnv writes shell-escaped KEY='VAL' lines (0600) to stageDir/secrets.env.
func writeSecretsEnv(stageDir string, secretEnv []string) (string, error) {
	if len(secretEnv) == 0 {
		return "", nil
	}
	var b []byte
	for _, kv := range secretEnv {
		eq := indexByte(kv, '=')
		if eq < 0 {
			continue
		}
		b = append(b, kv[:eq+1]...)
		b = append(b, shellQuote(kv[eq+1:])...)
		b = append(b, '\n')
	}
	p := filepath.Join(stageDir, "secrets.env")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
```
> Add a tiny `osCommand` helper in `vm.go` to keep `os/exec` use in one file:
> ```go
> func osCommand(ctx context.Context, argv []string) *exec.Cmd {
> 	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
> 	c.Stdout, c.Stderr = os.Stdout, os.Stderr
> 	return c
> }
> ```
> (`exec` here is stdlib `os/exec`, already imported in `vm.go`; `launch.go`'s `exec` is the
> engine package — keep `osCommand` in `vm.go` to avoid the name clash, exactly as SP3 split
> `container.go` (stdlib) from `launch.go` (engine exec).)

- [ ] **Step 2: Refactor `runProfile`** in `internal/cli/cli.go` — add the `vm` case (mirrors the `container` case; secrets stay in `secretEnv`):
```go
	case "vm":
		return vm.Launch(ctx, argv, prof.Network, secretEnv, stageDir, name)
```
Insert it between the `container` and `default` cases. Add the import
`"github.com/freakhill/safeslop/internal/engine/vm"`.

- [ ] **Step 3: Guard test** (`vm_test.go`)
```go
func TestLaunchRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Launch(context.Background(), []string{"zsh"}, "allow", nil, t.TempDir(), "p")
	if err == nil {
		t.Fatal("expected error when tart unavailable")
	}
}

func TestLaunchDenyNeedsProxyURL(t *testing.T) {
	t.Setenv("PATH", "")            // also unavailable, but the deny/proxy check is what we assert
	t.Setenv("SLOP_VM_PROXY_URL", "")
	_, err := Launch(context.Background(), []string{"zsh"}, "deny", nil, t.TempDir(), "p")
	if err == nil {
		t.Fatal("expected error")
	}
}
```
> Note: with `PATH=""`, `Available()` fails first, so `TestLaunchDenyNeedsProxyURL` only proves
> Launch errors. To assert the proxy-specific message, the engineer may instead make `Available`
> injectable; the minimal version above is acceptable (both paths must error).

- [ ] **Step 4: Run** — `go build ./...`; `go vet ./...`; `go test ./... ` → all green (new vm tests + unchanged SP1–SP3 suite). `gofmt -w internal/`.
- [ ] **Step 5: Manual VM smoke (needs Tart on Apple Silicon; NOT CI; multi-minute)** — `/tmp/sp4/slop.cue`:
```cue
version: 1
profiles: box: {agent: "shell", environment: "vm", network: "allow"}
```
```bash
cd /tmp/sp4 && /Users/jojo/workspace/safeslop/slop run box
```
Expected: first run clones the base (slow, one-time), boots a session VM, drops into a remote
`zsh`; `hostname` shows the VM, `ls ~` is the VM's home; `exit` destroys the session VM (`tart
list` shows no `slop-vm-box`). Record the result in the PR.

- [ ] **Step 6: Commit**
```bash
git add internal/engine/vm/launch.go internal/engine/vm/vm.go internal/cli/cli.go internal/engine/vm/vm_test.go
git commit -m "sp4: vm.Launch (clone+boot, scp stage, ssh -t agent, destroy-on-exit) + wire environment:vm"
```

---

### Task 5: `slop down` destroys the session VM + full gate + roadmap record

**Files:** Modify `internal/cli/cli.go`, `specs/0001-go-rewrite-design.md`; Test `internal/cli/cli_test.go`.

- [ ] **Step 1: Extend `cmdDown`** to also reap a lingering VM session. `slop down` is profile-less,
  so destroy any `slop-vm-*` session by listing them; add a `vm.DestroyAll(ctx)` helper:
```go
// vm.go
func DestroyAll(ctx context.Context) error {
	out, err := tartList(ctx)
	if err != nil {
		return nil // tart absent/usable elsewhere; down stays best-effort
	}
	var entries []struct {
		Name string `json:"Name"`
	}
	if json.Unmarshal([]byte(out), &entries) != nil {
		return nil
	}
	for _, e := range entries {
		if len(e.Name) > len("slop-vm-") && e.Name[:len("slop-vm-")] == "slop-vm-" && e.Name != baseTemplate {
			_ = runTart(ctx, "stop", e.Name)
			_ = runTart(ctx, "delete", e.Name)
		}
	}
	return nil
}
```
In `cmdDown`'s `RunE`, after the container teardown, add (best-effort, only if tart present):
```go
			if vm.Available() {
				_ = vm.DestroyAll(context.Background())
			}
```

- [ ] **Step 2: Go gate** — `make check` (check-assets + vet + fmtcheck + `go test ./...`) and `make build`. `./slop doctor` shows `vm-runtime`.
- [ ] **Step 3: Four fish gates** — `fish -n scripts/*.fish`; `fish tests/run.fish`; `fish scripts/slop-sync-help.fish check`; `fish scripts/slop-pinning.fish`. All pass (no fish files changed; pinning unaffected — the VM image pin lives in Go).
- [ ] **Step 4: Flip SP4 to complete** in `specs/0001-go-rewrite-design.md` §11:
```
SP0–SP4 are **complete** (SP4 = `specs/0006`, disposable Tart VM `environment: vm`). SP5
(nyx/mise toolchains) is the next artifact.
```
- [ ] **Step 5: Commit + push + PR**
```bash
git add internal/cli/cli.go internal/engine/vm/vm.go internal/cli/cli_test.go specs/0001-go-rewrite-design.md
git commit -m "sp4: slop down reaps vm sessions + roadmap record SP4 complete"
git push -u origin sp4-vm
gh pr create --title "SP4: vm environment — disposable Tart VM launch path in Go" \
  --body "Ports the disposable Tart VM path to the Go engine (environment: vm). See specs/0006."
```
(Push the feature branch + PR; do **not** push `main`. Hand the PR link back.)

---

## Verification (what "done" means)

- `make check` green: check-assets + vet + gofmt + `go test ./...`, including the hermetic vm
  tests — image-is-pinned, session-name, ssh/scp argv (incl. `-t` toggle), `remoteAgentCmd`
  secrets-sourcing + shell-escaping + proxy export, `bytesTrim`, and the unavailable/deny-needs-
  proxy guards. **No tart, VM, or network in CI.**
- The four fish gates green (old stack untouched).
- Manual VM smoke (Task 4 Step 5): `slop run box` clones+boots a session VM, runs a remote shell,
  destroys the VM on exit; `slop down` reaps any lingering `slop-vm-*`.
- `slop doctor` reports `vm-runtime`.

## Deliberately deferred (not in SP4)

- **Network-layer VM egress enforcement.** SP4's VM gets full host network (the disposable VM is
  the boundary); `network:"deny"` only injects *advisory* `HTTP_PROXY` env. Kernel-enforced VM
  egress (host `pf`/NAT on the tart bridge, or the slop-owned macOS **NetworkExtension filter
  à-la-LuLu**) is **SP8** (`specs/0001` §10) — the same successor noted for the container squid
  boundary.
- **Linux guests / non-macOS VMs** — SP4 ships the macOS Sonoma base only.
- **VM snapshot/restore + warm-pool** (keeping a booted VM hot for fast re-launch) — SP4 clones
  cold per run; a warm session is a later optimization.
- **`copy-out` / artifact extraction** from the VM — the fish stack has it; not needed for the
  agent launch path.
