package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestAttachBridgesIOAndPropagatesExitCode attaches to a real supervised stub
// session over its socket: the client drives the agent's stdin, reads the agent's
// output back, and exits with the agent's code from the X frame.
func TestAttachBridgesIOAndPropagatesExitCode(t *testing.T) {
	store, id, _ := newSupervisedStubSession(t, "#!/bin/sh\nread x\nprintf 'MARKER\\n'\nexit 42\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = Supervise(ctx, store, id, time.Now) }()

	if !waitForFile(filepath.Join(store.Dir, id+".sock"), 5*time.Second) {
		t.Fatal("supervisor socket never appeared")
	}

	var out bytes.Buffer
	code, err := attachSession(store, id, strings.NewReader("go\n"), &out, nil)
	if err != nil {
		t.Fatalf("attachSession: %v", err)
	}
	if code != 42 {
		t.Fatalf("exit code = %d, want 42 (from the X frame)", code)
	}
	if !strings.Contains(out.String(), "MARKER") {
		t.Fatalf("attach did not bridge the agent's output: %q", out.String())
	}
}

// TestAttachWithoutTTYEmitsPTYUnavailable proves `session attach` with no usable
// controlling terminal emits the PTY_UNAVAILABLE contract envelope byte-for-byte
// and makes no connect attempt (the guard runs before any dial), so the session
// need not even exist (specs/0050 PR4 guard, reused for attach).
func TestAttachWithoutTTYEmitsPTYUnavailable(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "attach", "--session-id", "sess-nonexistent")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("attach without a usable PTY: err = %v, want errOutputEmitted; out=%q", err, out)
	}
	golden, gerr := os.ReadFile(filepath.Join("..", "jsoncontract", "testdata", "error-pty-unavailable.golden.json"))
	if gerr != nil {
		t.Fatalf("read golden: %v", gerr)
	}
	if out != string(golden) {
		t.Fatalf("PTY_UNAVAILABLE envelope mismatch\n--- got ---\n%s\n--- want ---\n%s", out, golden)
	}
}

// TestAttachDoesNotMarkRunning is a guard that the no-tty attach path is purely a
// client: it must not create or mutate session state.
func TestAttachDoesNotMarkRunning(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("claude", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := runRootForTest(t, ws, "session", "attach", "--session-id", sess.ID); !errors.Is(err, errOutputEmitted) {
		t.Fatalf("expected PTY_UNAVAILABLE, got %v", err)
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != engsession.StatusCreated {
		t.Fatalf("attach mutated session status to %q; it must stay created", got.Status)
	}
}
