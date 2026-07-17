package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container"
	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestInvocationIdentityIsRandom128BitKey(t *testing.T) {
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^run-[0-9a-f]{32}$`)
	for range 128 {
		id, err := newInvocationID()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("invocation id = %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate invocation id %q", id)
		}
		seen[id] = true
	}
}

func TestConcurrentDirectRunInvocationStagesAreDistinct(t *testing.T) {
	ws := t.TempDir()
	prof := policy.Profile{Agent: "shell", Environment: "container", Network: "deny", Workspace: ws}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.detectRuntime = func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) { return runtimepkg.HostDockerEngine{}, nil }
	reaped := make(chan string, 2)
	d.reapDirectInvocation = func(_ runtimepkg.Engine, id string) error { reaped <- id; return nil }
	entered := make(chan string, 2)
	release := make(chan struct{})
	d.launchContainer = func(_ context.Context, _ runtimepkg.Engine, _ engexec.LaunchSpec, _, _ string, _, _ []string, stageDir string, _ []string, _ *policy.Projection, _ ...container.SessionGrant) (int, error) {
		entered <- stageDir
		<-release
		return 0, nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, err := runDirectProfileWithDeps(d, "direct-concurrent-fixture", prof, argv, ws)
			errs <- err
		}()
	}
	first, second := <-entered, <-entered
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("direct run: %v", err)
		}
	}
	if first == second {
		t.Fatalf("concurrent direct runs shared stage %q", first)
	}
	close(reaped)
	wantReaped := map[string]bool{filepath.Base(first): true, filepath.Base(second): true}
	for id := range reaped {
		if !wantReaped[id] {
			t.Fatalf("cleanup used invocation %q outside stages %v", id, wantReaped)
		}
		delete(wantReaped, id)
	}
	if len(wantReaped) != 0 {
		t.Fatalf("direct invocation cleanup missing: %v", wantReaped)
	}
}

func TestDirectLaunchUsesOneApprovedEngineForLaunchAndReap(t *testing.T) {
	ws := t.TempDir()
	prof := policy.Profile{Agent: "shell", Environment: "container", Network: "deny", Workspace: ws}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	probes := 0
	d.detectRuntime = func(network runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) {
		probes++
		if network != runtimepkg.PolicyDeny {
			t.Fatalf("runtime policy = %v, want deny gate", network)
		}
		return runtimepkg.PodmanEngine{}, nil
	}
	var launched, reaped runtimepkg.Engine
	d.launchContainer = func(_ context.Context, eng runtimepkg.Engine, _ engexec.LaunchSpec, _, _ string, _, _ []string, _ string, _ []string, _ *policy.Projection, _ ...container.SessionGrant) (int, error) {
		launched = eng
		return 0, nil
	}
	d.reapDirectInvocation = func(eng runtimepkg.Engine, _ string) error {
		reaped = eng
		return nil
	}

	if _, err := runDirectProfileWithDeps(d, "direct-engine", prof, argv, ws); err != nil {
		t.Fatalf("direct launch: %v", err)
	}
	if probes != 1 {
		t.Fatalf("runtime probes = %d, want exactly one", probes)
	}
	if launched == nil || reaped == nil || launched != reaped || launched.Name() != "podman" {
		t.Fatalf("launch/reap engines = %v/%v, want one detected podman engine", launched, reaped)
	}
}

func TestDirectInvocationReapFailureRetainsOnlyCleanupMarker(t *testing.T) {
	ws := t.TempDir()
	prof := policy.Profile{Agent: "shell", Environment: "container", Network: "deny", Workspace: ws}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.detectRuntime = func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) { return runtimepkg.HostDockerEngine{}, nil }
	var stage string
	d.launchContainer = func(_ context.Context, _ runtimepkg.Engine, _ engexec.LaunchSpec, _, _ string, _, _ []string, stageDir string, _ []string, _ *policy.Projection, _ ...container.SessionGrant) (int, error) {
		stage = stageDir
		return 0, nil
	}
	d.reapDirectInvocation = func(runtimepkg.Engine, string) error { return errors.New("injected reap failure") }
	t.Cleanup(func() { _ = os.RemoveAll(stage) })

	if _, err := runDirectProfileWithDeps(d, "direct-reap-failure", prof, argv, ws); err == nil {
		t.Fatal("direct run succeeded despite unproven boundary cleanup")
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatalf("read retained stage: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != ".safeslop-stage" {
		t.Fatalf("reap failure retained bearer/config files: %v", entries)
	}
}

// TestRunProfileCtxTeardownOnCancel proves that cancelling the run context (what
// the SIGTERM handler in runProfile does, and what `session stop` triggers)
// tears the agent down: the child process is killed and runProfileCtx returns,
// so its deferred teardown (stage wipe, credential revoke, and for container
// the boundary teardown) runs instead of being skipped by an abrupt signal
// death (specs/0050 PR2, gap #2). Hermetic: host env + a stub shell agent.
func TestRunProfileCtxTeardownOnCancel(t *testing.T) {
	ws := t.TempDir()
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "sleeper")
	pidFile := filepath.Join(stubDir, "agent.pid")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nexec sleep 60\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("SHELL", stub)

	prof := policy.Profile{Agent: "shell", Environment: "host", Network: "deny", Workspace: ws}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatalf("argv: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		code, _ := runProfileCtx(ctx, "session-teardown", prof, argv, ws)
		done <- code
	}()

	pid := waitForAgentPID(t, pidFile)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runProfileCtx did not return after cancel")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, syscall.Signal(0)) != nil {
			return // agent gone — teardown killed it
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent process %d still alive after cancel", pid)
}

// TestRunProfileCtxExitCodeFidelity locks the D4 contract's exit-code half: the
// agent's exit code propagates verbatim through the boundary launcher and back
// out of runProfileCtx, for every code (0 / 1 / 42), on the boundary that is
// hermetically launchable (host via a real stub agent). Container exit-code
// propagation rides exec.RunInPTY/RunInTerminal (exec-layer tested) plus
// docker, which forwards the inner code — not unit-tested here.
func TestRunProfileCtxExitCodeFidelity(t *testing.T) {
	for _, code := range []int{0, 1, 42} {
		for _, env := range []string{"host"} {
			t.Run(fmt.Sprintf("%s-%d", env, code), func(t *testing.T) {
				ws := t.TempDir()
				stub := filepath.Join(ws, "exiter")
				if err := os.WriteFile(stub, []byte(fmt.Sprintf("#!/bin/sh\nexit %d\n", code)), 0o755); err != nil {
					t.Fatalf("write stub: %v", err)
				}
				t.Setenv("SHELL", stub)

				prof := policy.Profile{Agent: "shell", Environment: env, Network: "deny", Workspace: ws}
				argv, err := agentArgv(prof)
				if err != nil {
					t.Fatalf("argv: %v", err)
				}
				got, _ := runProfileCtx(context.Background(), "session-exit", prof, argv, ws)
				if got != code {
					t.Fatalf("env=%s exit code = %d, want %d", env, got, code)
				}
			})
		}
	}
}

// TestRunProfileCtxContainerForwardsSupervisorPTY proves the detached container
// path threads the supervisor's PTY through to container.Launch: runProfileCtx's
// container branch must copy rio's stdin/stdout/stderr into the LaunchSpec (so the
// container's tty bridges to the supervisor socket for attach). Hermetic via the
// containerLaunch seam — no docker. The coupled case (no rio) is the zero value,
// which leaves the spec stdio nil (container.Launch then keeps RunInPTY).
func TestRunProfileCtxContainerForwardsSupervisorPTY(t *testing.T) {
	ws := t.TempDir()
	prof := policy.Profile{Agent: "shell", Environment: "container", Network: "deny", Workspace: ws}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatalf("argv: %v", err)
	}

	var gotSpec engexec.LaunchSpec
	d := defaultDependencies()
	d.detectRuntime = func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) { return runtimepkg.HostDockerEngine{}, nil }
	d.launchContainer = func(_ context.Context, _ runtimepkg.Engine, spec engexec.LaunchSpec, _, _ string, _, _ []string, _ string, _ []string, _ *policy.Projection, _ ...container.SessionGrant) (int, error) {
		gotSpec = spec
		return 0, nil
	}

	stdin := strings.NewReader("")
	var stdout, stderr bytes.Buffer
	rio := runIO{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}
	if _, err := runProfileCtxWithDeps(d, context.Background(), "session-ctr", prof, argv, ws, "", rio); err != nil {
		t.Fatalf("runProfileCtx: %v", err)
	}
	if gotSpec.Stdin != stdin {
		t.Fatalf("container spec.Stdin = %v, want the supervisor PTY reader", gotSpec.Stdin)
	}
	if gotSpec.Stdout != &stdout || gotSpec.Stderr != &stderr {
		t.Fatal("container spec did not forward the supervisor stdout/stderr")
	}
}

func TestRunProfilePiOAuthIsFileOnlyAndWiped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, ".pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	const access = "ACCESS_CANARY"
	auth := fmt.Sprintf(`{"openai-codex":{"type":"oauth","access":%q,"refresh":"REFRESH_SENTINEL","expires":%d}}`, access, time.Now().Add(time.Hour).UnixMilli())
	hostAuth := filepath.Join(agentDir, "auth.json")
	if err := os.WriteFile(hostAuth, []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}

	prof := policy.Profile{Agent: "pi", Environment: "container", Network: "deny", Workspace: ".", Credentials: &policy.Credentials{
		Pi: &policy.PiCreds{Provider: "openai-codex", Model: "gpt-5.6-luna"},
	}}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatal(err)
	}
	var seenStage string
	d := defaultDependencies()
	d.detectRuntime = func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) { return runtimepkg.HostDockerEngine{}, nil }
	d.launchContainer = func(_ context.Context, _ runtimepkg.Engine, spec engexec.LaunchSpec, _, _ string, _ []string, secretEnv []string, stageDir string, _ []string, _ *policy.Projection, _ ...container.SessionGrant) (int, error) {
		seenStage = stageDir
		body, err := os.ReadFile(filepath.Join(stageDir, "pi", "openai-codex", "auth.json"))
		if err != nil {
			t.Fatalf("read staged Pi auth at launch: %v", err)
		}
		if !strings.Contains(string(body), access) || strings.Contains(string(body), "REFRESH_SENTINEL") {
			t.Fatalf("launch-stage Pi auth is not access-only: %s", body)
		}
		leakSurface := strings.Join(append(append([]string{}, spec.Argv...), secretEnv...), "\n")
		if strings.Contains(leakSurface, access) || strings.Contains(leakSurface, hostAuth) {
			t.Fatalf("Pi OAuth leaked to argv/env: %s", leakSurface)
		}
		return 0, nil
	}

	if code, err := runProfileCtxWithDeps(d, context.Background(), "pi-oauth-file-only", prof, argv, t.TempDir(), ""); err != nil || code != 0 {
		t.Fatalf("runProfileCtx = %d, %v", code, err)
	}
	if seenStage == "" {
		t.Fatal("container launch did not observe a stage")
	}
	if _, err := os.Stat(seenStage); !os.IsNotExist(err) {
		t.Fatalf("Pi OAuth stage survived run teardown: %v", err)
	}
	if body, err := os.ReadFile(hostAuth); err != nil || string(body) != auth {
		t.Fatalf("host Pi auth changed: %q err=%v", body, err)
	}
}

func waitForAgentPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent pid file %s never appeared", path)
	return 0
}
