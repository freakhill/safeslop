package exec

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunInTerminalCapturesOutputAndExitCode(t *testing.T) {
	var out bytes.Buffer
	code, err := RunInTerminal(context.Background(), LaunchSpec{
		Argv:   []string{"/bin/sh", "-c", "echo hello-stdout; exit 0"},
		Stdout: &out,
		Stderr: &out,
	})
	if err != nil {
		t.Fatalf("RunInTerminal error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "hello-stdout") {
		t.Fatalf("output %q does not contain hello-stdout", out.String())
	}
}

func TestRunInTerminalPropagatesNonZeroExit(t *testing.T) {
	code, _ := RunInTerminal(context.Background(), LaunchSpec{
		Argv:   []string{"/bin/sh", "-c", "exit 7"},
		Stdout: &bytes.Buffer{},
	})
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestRunInTerminalEmptyArgv(t *testing.T) {
	if _, err := RunInTerminal(context.Background(), LaunchSpec{}); err == nil {
		t.Fatal("expected error for empty Argv")
	}
}

// TestRunInPTYInteractive proves the PTY launch path: a child running under a
// pseudo-terminal can read what we write to it and we capture what it prints.
// This is the de-risking spike for the wrapped/container interactive launch.
func TestRunInPTYInteractive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	code, err := RunInPTY(ctx, LaunchSpec{
		// Echo a banner, read one line from the pty, echo it back, exit.
		Argv:   []string{"/bin/sh", "-c", "echo BANNER; read line; echo GOT=$line"},
		Stdin:  strings.NewReader("world\n"),
		Stdout: &out,
	})
	if err != nil {
		t.Fatalf("RunInPTY error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "BANNER") {
		t.Fatalf("pty output %q missing BANNER", got)
	}
	if !strings.Contains(got, "GOT=world") {
		t.Fatalf("pty output %q missing GOT=world (stdin was not delivered through the pty)", got)
	}
}

// TestRunInPTYCancelTearsDownProcessGroup proves that cancelling the context
// kills the child's whole process group, not just the direct child. pty.Start
// makes the shell a session leader, so the backgrounded `sleep` is a grandchild
// in that group; a direct-child-only kill would orphan it, a group teardown
// takes it down too. This is the mechanism `session stop` relies on to avoid
// leaving an agent (or its boundary) running after teardown (specs/0050 PR2).
func TestRunInPTYCancelTearsDownProcessGroup(t *testing.T) {
	dir := t.TempDir()
	grandpid := dir + "/grandpid"
	script := "sleep 60 & echo $! > " + grandpid + "; wait"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = RunInPTY(ctx, LaunchSpec{Argv: []string{"/bin/sh", "-c", script}, Stdin: strings.NewReader("")})
		close(done)
	}()

	pid := waitForPidFile(t, grandpid)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunInPTY did not return after cancel")
	}
	requireProcessDies(t, pid, 5*time.Second)
}

func waitForPidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid file %s never appeared", path)
	return 0
}

func requireProcessDies(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, syscall.Signal(0)) != nil {
			return // ESRCH: gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still alive %s after group teardown", pid, within)
}

func TestRunInPTYExitCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, _ := RunInPTY(ctx, LaunchSpec{
		Argv:  []string{"/bin/sh", "-c", "exit 3"},
		Stdin: strings.NewReader(""),
	})
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}
