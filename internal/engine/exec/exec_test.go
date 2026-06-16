package exec

import (
	"bytes"
	"context"
	"strings"
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
