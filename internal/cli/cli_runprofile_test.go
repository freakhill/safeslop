package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
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
