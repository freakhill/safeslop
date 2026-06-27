package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
)

// TestRunProfileCtxTeardownOnCancel proves that cancelling the run context (what
// the SIGTERM handler in runProfile does, and what `session stop` triggers)
// tears the agent down: the child process is killed and runProfileCtx returns,
// so its deferred teardown (stage wipe, credential revoke, and for vm/container
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
// out of runProfileCtx, for every code (0 / 1 / 42), on the boundaries that are
// hermetically launchable (host + sandbox via a real stub agent). Container/VM
// exit-code propagation rides exec.RunInPTY/RunInTerminal (exec-layer tested)
// plus docker/ssh, which forward the inner code — not unit-tested here.
func TestRunProfileCtxExitCodeFidelity(t *testing.T) {
	for _, code := range []int{0, 1, 42} {
		for _, env := range []string{"host", "sandbox"} {
			t.Run(fmt.Sprintf("%s-%d", env, code), func(t *testing.T) {
				if env == "sandbox" && !sandbox.Available() {
					t.Skip("sandbox-exec unavailable")
				}
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
