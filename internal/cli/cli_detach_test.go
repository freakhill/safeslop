package cli

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestRunDetachRecordsSupervisorPIDAndReturns proves `session run --detach`
// returns promptly (no os.Exit, no inherited tty needed), records the supervisor
// PID (the re-exec'd child, not this wrapper), leaves the session running, and
// emits the session envelope once the socket is up.
func TestRunDetachRecordsSupervisorPIDAndReturns(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	d := defaultDependencies()
	d.store = store
	acceptHostConsentForTest(t, d)
	id := sess.ID

	const supervisorPID = 4242
	d.launchSupervisor = func(sid string) (int, error) {
		// Simulate the detached supervisor becoming ready by binding its socket.
		f, ferr := os.Create(sessionStore().SocketPath(sid))
		if ferr == nil {
			_ = f.Close()
		}
		return supervisorPID, nil
	}

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", id, "--detach")
	if err != nil {
		t.Fatalf("run --detach: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("detach returned error envelope: %+v", env.Errors)
	}

	got, err := store.Get(id) // raw (no reconcile) so the fake PID isn't flipped to stopped
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Status != engsession.StatusRunning {
		t.Fatalf("status = %q, want running", got.Status)
	}
	if got.PID != supervisorPID {
		t.Fatalf("recorded PID = %d, want supervisor pid %d", got.PID, supervisorPID)
	}
	if got.PID == os.Getpid() {
		t.Fatalf("recorded PID is the wrapper (%d), not the supervisor", got.PID)
	}
}

// TestRunDetachWaitsForSocketBeforeSuccess proves the launcher waits for the
// supervisor's socket before reporting success: when it never appears, readiness
// times out, the half-born supervisor is killed, a contract error is emitted, and
// the session is NOT left running (no phantom).
func TestRunDetachWaitsForSocketBeforeSuccess(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	d := defaultDependencies()
	d.store = store
	acceptHostConsentForTest(t, d)
	id := sess.ID

	d.launchSupervisor = func(string) (int, error) { return 4242, nil } // never binds a socket
	d.detachReadyTimeout = 200 * time.Millisecond
	killed := 0
	d.killProcess = func(pid int) error { killed = pid; return nil }

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", id, "--detach")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("readiness timeout: err = %v, want errOutputEmitted; out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK {
		t.Fatalf("expected an error envelope on readiness timeout, got %+v", env)
	}
	if killed != 4242 {
		t.Fatalf("half-born supervisor not killed (killed=%d)", killed)
	}
	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Status == engsession.StatusRunning {
		t.Fatalf("session left running after readiness timeout: %+v", got)
	}
}

// TestSuperviseSubcommandHidden proves the re-exec target `session supervise` is
// registered but hidden from help (it is an internal daemon entry point).
func TestSuperviseSubcommandHidden(t *testing.T) {
	root := newRoot()
	var sessionCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "session" {
			sessionCmd = c
			break
		}
	}
	if sessionCmd == nil {
		t.Fatal("session command not registered")
	}
	var supervise *cobra.Command
	for _, c := range sessionCmd.Commands() {
		if c.Name() == "supervise" {
			supervise = c
			break
		}
	}
	if supervise == nil {
		t.Fatal("session supervise subcommand not registered")
	}
	if !supervise.Hidden {
		t.Fatal("session supervise must be Hidden (internal re-exec target)")
	}
}
