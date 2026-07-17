package cli

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

// TestRunDetachRecordsSupervisorPIDAndReturns proves `session run --detach`
// returns promptly (no os.Exit, no inherited tty needed), accepts only a
// supervisor-committed PID/start-token identity, leaves the session running, and
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

	supervisorPID := os.Getpid()
	supervisorToken, ok := engsession.ProcessStartToken(supervisorPID)
	if !ok {
		t.Fatal("current process has no start token")
	}
	var listener net.Listener
	d.launchSupervisor = func(sid string) (launchedSupervisor, error) {
		// Simulate the supervisor's exact readiness order: bind, secure, then
		// commit its own detached process identity.
		var listenErr error
		listener, listenErr = net.Listen("unix", store.SocketPath(sid))
		if listenErr != nil {
			return launchedSupervisor{}, listenErr
		}
		if err := os.Chmod(store.SocketPath(sid), 0o600); err != nil {
			return launchedSupervisor{}, err
		}
		if _, err := store.HandoffRunningDetached(sid, os.Getpid(), supervisorPID, d.now()); err != nil {
			return launchedSupervisor{}, err
		}
		return launchedSupervisor{PID: supervisorPID, ProcessToken: supervisorToken}, nil
	}
	defer func() {
		if listener != nil {
			_ = listener.Close()
		}
		_ = os.Remove(store.SocketPath(id))
	}()

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
	if got.ProcessToken != supervisorToken {
		t.Fatalf("recorded process token = %q, want captured supervisor token", got.ProcessToken)
	}
}

func TestRunDetachRejectsExistingRunningOwnerBeforeSpawn(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkRunning(sess.ID, os.Getpid(), nowForTest(t)); err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.store = store
	acceptHostConsentForTest(t, d)
	launches := 0
	d.launchSupervisor = func(string) (launchedSupervisor, error) {
		launches++
		return launchedSupervisor{}, errors.New("must not spawn")
	}

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID, "--detach")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("repeated detached run = %v, want emitted conflict; out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != jsoncontract.CodeSessionAlreadyRunning {
		t.Fatalf("repeated run envelope = %+v", env)
	}
	if launches != 0 {
		t.Fatalf("repeated run spawned %d supervisors", launches)
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != os.Getpid() || got.Status != engsession.StatusRunning {
		t.Fatalf("existing owner was overwritten: %+v", got)
	}
}

func TestCoupledSessionRunRefusesRunningAndStoppedRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop bool
		code jsoncontract.ErrorCode
	}{
		{name: "existing owner", code: jsoncontract.CodeSessionAlreadyRunning},
		{name: "terminal record", stop: true, code: jsoncontract.CodeSessionStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
			store := sessionStore()
			sess, err := store.Create("claude", "host", ws, nowForTest(t))
			if err != nil {
				t.Fatal(err)
			}
			if tc.stop {
				_, err = store.Finish(sess.ID, 0, "", nowForTest(t))
			} else {
				_, err = store.MarkRunning(sess.ID, os.Getpid(), nowForTest(t))
			}
			if err != nil {
				t.Fatal(err)
			}
			d := defaultDependencies()
			d.store = store

			out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID)
			if !errors.Is(err, errOutputEmitted) {
				t.Fatalf("repeated coupled run = %v, want emitted refusal; out=%s", err, out)
			}
			env := parseEnvelopeForTest(t, out)
			if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != tc.code {
				t.Fatalf("repeated coupled run envelope = %+v, want %s", env, tc.code)
			}
		})
	}
}

func TestConcurrentRunDetachSpawnsOnlyClaimWinner(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.store = store
	spawned := make(chan struct{})
	release := make(chan struct{})
	launches := 0
	d.launchSupervisor = func(string) (launchedSupervisor, error) {
		launches++
		close(spawned)
		<-release
		return launchedSupervisor{}, errors.New("winner spawn failed after claim test")
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- runDetachWithDeps(d, store, sess.ID) }()
	<-spawned

	secondErr := runDetachWithDeps(d, store, sess.ID)
	if !errors.Is(secondErr, errOutputEmitted) {
		t.Fatalf("losing concurrent run = %v, want emitted running conflict", secondErr)
	}
	if launches != 1 {
		t.Fatalf("concurrent runs spawned %d supervisors, want one", launches)
	}
	close(release)
	if firstErr := <-firstDone; !errors.Is(firstErr, errOutputEmitted) {
		t.Fatalf("winning launch failure = %v, want emitted failure", firstErr)
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != engsession.StatusCreated {
		t.Fatalf("failed winner did not release claim: %+v", got)
	}
}

func TestRunDetachLaunchFailureReleasesParentClaim(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.store = store
	acceptHostConsentForTest(t, d)
	d.launchSupervisor = func(string) (launchedSupervisor, error) {
		return launchedSupervisor{}, errors.New("injected spawn failure")
	}

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID, "--detach")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("spawn failure = %v, want emitted error; out=%s", err, out)
	}
	got, getErr := store.Get(sess.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status != engsession.StatusCreated || got.PID != 0 || got.ProcessToken != "" {
		t.Fatalf("failed spawn left launch claim: %+v", got)
	}
}

func TestRunDetachRejectsSocketBeforeSupervisorAcknowledgement(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.store = store
	acceptHostConsentForTest(t, d)
	d.detachReadyTimeout = 100 * time.Millisecond
	d.launchSupervisor = func(id string) (launchedSupervisor, error) {
		// Binding publishes the filesystem socket before the supervisor has
		// secured it and committed its running identity.
		if err := os.WriteFile(store.SocketPath(id), nil, 0o600); err != nil {
			return launchedSupervisor{}, err
		}
		return launchedSupervisor{PID: 4242, ProcessToken: "supervisor-token"}, nil
	}
	d.processAlive = func(identity engsession.Session) bool {
		return identity.PID == 4242 && identity.ProcessToken == "supervisor-token"
	}
	killed, waited := 0, 0
	d.killProcess = func(pid int) error { killed = pid; return nil }
	d.waitProcess = func(pid int, _ engsession.Session) error { waited = pid; return nil }

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID, "--detach")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("unacknowledged socket launch = %v, want readiness failure; out=%s", err, out)
	}
	if killed != -4242 || waited != -4242 {
		t.Fatalf("half-published supervisor cleanup = kill %d wait %d, want group -4242", killed, waited)
	}
	if _, statErr := os.Lstat(store.SocketPath(sess.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("half-published socket remains after timeout: %v", statErr)
	}
	got, getErr := store.Get(sess.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status == engsession.StatusRunning {
		t.Fatalf("unacknowledged socket published running state: %+v", got)
	}
}

func TestRunDetachTimeoutUsesVerifiedGroupAndFailsLoudly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		alive       bool
		signalErr   error
		waitErr     error
		wantStopped bool
		wantKill    bool
		wantWait    bool
	}{
		{name: "pid reused", wantStopped: true},
		{name: "signal failure", alive: true, signalErr: errors.New("signal failed"), wantKill: true},
		{name: "wait failure", alive: true, waitErr: errors.New("wait failed"), wantKill: true, wantWait: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
			store := sessionStore()
			sess, err := store.Create("claude", "host", ws, nowForTest(t))
			if err != nil {
				t.Fatal(err)
			}
			d := defaultDependencies()
			d.store = store
			acceptHostConsentForTest(t, d)
			d.detachReadyTimeout = 20 * time.Millisecond
			d.launchSupervisor = func(string) (launchedSupervisor, error) {
				return launchedSupervisor{PID: 4242, ProcessToken: "original-process-token"}, nil
			}
			d.processAlive = func(identity engsession.Session) bool {
				if identity.PID != 4242 || identity.ProcessToken != "original-process-token" {
					t.Fatalf("timeout identity = %+v", identity)
				}
				return tc.alive
			}
			killCalls, waitCalls := 0, 0
			d.killProcess = func(target int) error {
				killCalls++
				if target != -4242 {
					t.Fatalf("signal target = %d, want verified group -4242", target)
				}
				return tc.signalErr
			}
			d.waitProcess = func(target int, identity engsession.Session) error {
				waitCalls++
				if target != -4242 || identity.PID != 4242 || identity.ProcessToken != "original-process-token" {
					t.Fatalf("wait authority = target %d identity %+v", target, identity)
				}
				return tc.waitErr
			}

			out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID, "--detach")
			if !errors.Is(err, errOutputEmitted) {
				t.Fatalf("timeout error = %v, want emitted failure; out=%s", err, out)
			}
			if (killCalls > 0) != tc.wantKill || (waitCalls > 0) != tc.wantWait {
				t.Fatalf("cleanup calls = kill %d wait %d, want kill=%t wait=%t", killCalls, waitCalls, tc.wantKill, tc.wantWait)
			}
			got, getErr := store.Get(sess.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if (got.Status == engsession.StatusStopped) != tc.wantStopped {
				t.Fatalf("timeout status = %q, want stopped=%t", got.Status, tc.wantStopped)
			}
		})
	}
}

func TestRunDetachTimeoutFailsLoudlyWhenTerminalRecordCommitFails(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	sess, err := store.Create("claude", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.store = store
	acceptHostConsentForTest(t, d)
	d.detachReadyTimeout = 20 * time.Millisecond
	d.launchSupervisor = func(string) (launchedSupervisor, error) {
		return launchedSupervisor{PID: 4242, ProcessToken: "supervisor-token"}, nil
	}
	d.processAlive = func(engsession.Session) bool { return true }
	d.killProcess = func(int) error { return nil }
	d.waitProcess = func(int, engsession.Session) error {
		return os.WriteFile(filepath.Join(store.Dir, sess.ID+".json"), []byte("{broken\n"), 0o600)
	}

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID, "--detach")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("terminal commit failure = %v, want emitted error; out=%s", err, out)
	}
	if env := parseEnvelopeForTest(t, out); env.OK {
		t.Fatalf("terminal commit failure emitted success: %+v", env)
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

	d.launchSupervisor = func(string) (launchedSupervisor, error) {
		return launchedSupervisor{PID: 4242, ProcessToken: "supervisor-token"}, nil
	} // never binds a socket
	d.detachReadyTimeout = 200 * time.Millisecond
	d.processAlive = func(identity engsession.Session) bool {
		return identity.PID == 4242 && identity.ProcessToken == "supervisor-token"
	}
	killed, waited := 0, 0
	d.killProcess = func(pid int) error { killed = pid; return nil }
	d.waitProcess = func(pid int, _ engsession.Session) error { waited = pid; return nil }

	out, err := runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", id, "--detach")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("readiness timeout: err = %v, want errOutputEmitted; out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK {
		t.Fatalf("expected an error envelope on readiness timeout, got %+v", env)
	}
	if killed != -4242 || waited != -4242 {
		t.Fatalf("half-born supervisor cleanup = kill %d wait %d, want group -4242", killed, waited)
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
