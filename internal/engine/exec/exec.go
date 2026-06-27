// Package exec launches child processes for the safeslop engine.
//
// Two launch paths exist, matching the two interactive scenarios from the
// design (specs/0001, §6.2 — the ctty/#1 risk):
//
//   - RunInTerminal: the direct host launch (claude / shell in the user's own
//     terminal). The child inherits the real stdio, so it owns the controlling
//     terminal directly — the same way git/npm spawn an editor or pager. No PTY
//     and no tcsetpgrp gymnastics are needed for this path.
//   - RunInPTY: the wrapped / container (`docker exec`) interactive path, where
//     we sit between the user and the child. Here we allocate a pseudo-terminal,
//     proxy stdin/stdout, put the local terminal in raw mode, and forward
//     SIGWINCH so the child sees window-size changes.
//
// The PTY path is the one with real subtlety, so it is the one covered by tests.
package exec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// LaunchSpec describes a child process to launch.
type LaunchSpec struct {
	Argv   []string  // command + args; Argv[0] is the program
	Dir    string    // working directory; "" means inherit
	Env    []string  // environment; nil means inherit the parent's os.Environ()
	Stdin  io.Reader // nil means os.Stdin
	Stdout io.Writer // nil means os.Stdout
	Stderr io.Writer // nil means os.Stderr

	// ControllingTTY makes the child a session leader and acquires its stdin (an
	// *os.File PTY slave, child fd 0) as its controlling terminal. The detached
	// supervisor host path sets it so the agent gets real controlling-terminal
	// semantics (/dev/tty, terminal-generated signals, SIGHUP-on-hangup) on the PTY
	// the supervisor owns (specs/0051). It must NOT be set on the coupled path,
	// where the child shares the user's real terminal and a TIOCSCTTY would steal
	// it. When set, Stdin must be an *os.File (the PTY slave).
	ControllingTTY bool
}

// ErrNoArgv is returned when a LaunchSpec has no command to run.
var ErrNoArgv = errors.New("exec: empty Argv")

// RunInTerminal runs the child with inherited stdio (the direct host launch).
// In the coupled path the child shares the parent's controlling terminal; because
// the parent blocks in Wait, the child runs in the foreground and reads/writes the
// real tty. With spec.ControllingTTY (the detached supervisor) the child instead
// becomes a session leader and acquires its PTY-slave stdin as a fresh controlling
// terminal. Returns the child's exit code (or a non-zero code if it never started).
func RunInTerminal(ctx context.Context, spec LaunchSpec) (int, error) {
	if len(spec.Argv) == 0 {
		return 1, ErrNoArgv
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = envOrInherit(spec.Env)
	cmd.Stdin = readerOr(spec.Stdin, os.Stdin)
	cmd.Stdout = writerOr(spec.Stdout, os.Stdout)
	cmd.Stderr = writerOr(spec.Stderr, os.Stderr)
	if spec.ControllingTTY {
		// Setsid puts the child in a new session (so it can own a controlling tty);
		// Setctty + Ctty:0 makes its PTY-slave stdin that controlling terminal. On
		// ctx-cancel exec kills the session leader, and the kernel then hangs up the
		// terminal — SIGHUP to the foreground group — so the agent's subtree is torn
		// down too (the same teardown RunInPTY relies on).
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	}
	err := cmd.Run()
	return exitCode(err), err
}

// RunInPTY runs the child under a pseudo-terminal and proxies I/O between it and
// spec.Stdin/Stdout. When the local stdin is itself a terminal it is switched to
// raw mode for the duration and window-size changes are forwarded via SIGWINCH.
// Returns the child's exit code.
func RunInPTY(ctx context.Context, spec LaunchSpec) (int, error) {
	if len(spec.Argv) == 0 {
		return 1, ErrNoArgv
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = envOrInherit(spec.Env)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return 1, err
	}
	defer func() { _ = ptmx.Close() }()

	in := readerOr(spec.Stdin, os.Stdin)
	out := writerOr(spec.Stdout, os.Stdout)

	// If the local stdin is a real terminal, mirror window size + raw mode.
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for range winch {
				_ = pty.InheritSize(f, ptmx)
			}
		}()
		winch <- syscall.SIGWINCH // set the initial size

		oldState, err := term.MakeRaw(int(f.Fd()))
		if err == nil {
			defer func() { _ = term.Restore(int(f.Fd()), oldState) }()
		}
	}

	// stdin -> child. Best-effort; the copy ends when the child exits.
	go func() { _, _ = io.Copy(ptmx, in) }()

	// child -> stdout in the background so we can join it after Wait. Reading the
	// master after the child exits yields EOF (darwin) or EIO (linux); both are
	// expected and not surfaced as errors.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(out, ptmx)
	}()

	waitErr := cmd.Wait()
	_ = ptmx.Close() // unblock the reader if it is still draining
	wg.Wait()
	return exitCode(waitErr), waitErr
}

func envOrInherit(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}

func readerOr(r io.Reader, def io.Reader) io.Reader {
	if r == nil {
		return def
	}
	return r
}

func writerOr(w io.Writer, def io.Writer) io.Writer {
	if w == nil {
		return def
	}
	return w
}

// exitCode extracts the process exit code from an error returned by Run/Wait.
// nil error -> 0; *exec.ExitError -> the real code; anything else -> 1.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}
