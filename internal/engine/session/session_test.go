package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testNow() time.Time { return time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC) }

// TestSocketPathFitsSunPath pins the sun_path-overflow guard (specs/0051): a
// per-session socket path must fit the platform sun_path cap (104 bytes on macOS),
// so when the natural <Dir>/<id>.sock would overflow, SocketPath relocates it to a
// short runtime dir. A short Dir keeps the natural path; the mapping is always
// deterministic and per-id distinct so supervisor, attach client, and the
// reconcile sweep agree without persisting it.
func TestSocketPathFitsSunPath(t *testing.T) {
	short := NewStore("/tmp/ss")
	if got, want := short.SocketPath("sess-abcd"), "/tmp/ss/sess-abcd.sock"; got != want {
		t.Fatalf("short-dir SocketPath = %q, want the natural %q", got, want)
	}

	long := NewStore(filepath.Join(t.TempDir(), strings.Repeat("x", 90), "sessions"))
	id := "sess-0123456789abcdef01234567"
	p := long.SocketPath(id)
	if len(p) > 103 {
		t.Fatalf("relocated SocketPath len = %d (%q), want <= 103 to fit sun_path", len(p), p)
	}
	if strings.HasPrefix(p, long.Dir) {
		t.Fatalf("overflowing SocketPath %q was not relocated out of the long dir %q", p, long.Dir)
	}
	if again := long.SocketPath(id); again != p {
		t.Fatalf("SocketPath not deterministic: %q then %q", p, again)
	}
	if other := long.SocketPath("sess-ffffffffffffffffffffffff"); other == p {
		t.Fatalf("distinct ids collided on the same relocated socket path %q", p)
	}
}

func TestStopSignalsSupervisorGroupAndRemovesSocket(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunningDetached(sess.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark detached: %v", err)
	}
	sock := store.SocketPath(sess.ID)
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("seed socket: %v", err)
	}

	var killedWith int
	killer := func(target int) error { killedWith = target; return nil }
	if _, err := store.Stop(sess.ID, false, testNow(), func(Session) error { return nil }, killer, nil); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if killedWith != -4242 {
		t.Fatalf("kill target = %d, want -4242 (the supervisor's process group)", killedWith)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket not removed on stop (stat err = %v)", err)
	}
}

func TestStopCoupledSignalsBarePID(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunning(sess.ID, 4242, testNow()); err != nil { // coupled, not detached
		t.Fatalf("mark running: %v", err)
	}
	var killedWith int
	if _, err := store.Stop(sess.ID, false, testNow(), func(Session) error { return nil },
		func(target int) error { killedWith = target; return nil }, nil); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if killedWith != 4242 {
		t.Fatalf("coupled kill target = %d, want bare 4242 (no group negation)", killedWith)
	}
}

func TestReconcileRemovesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunningDetached(sess.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark detached: %v", err)
	}
	sock := store.SocketPath(sess.ID)
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("seed socket: %v", err)
	}

	got, err := store.GetReconciled(sess.ID, testNow(), func(int) bool { return false }) // supervisor dead
	if err != nil {
		t.Fatalf("get reconciled: %v", err)
	}
	if got.Status != StatusStopped {
		t.Fatalf("status = %q, want stopped", got.Status)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("stale socket not swept on reconcile (stat err = %v)", err)
	}
}

func TestStoreStopRevokesBeforeKillAndIsIdempotent(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunning(sess.ID, 12345, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	var order []string
	revoke := func(Session) error { order = append(order, "revoke"); return nil }
	kill := func(int) error { order = append(order, "kill"); return nil }
	reap := func(Session) error { order = append(order, "reap"); return nil }
	stopped, err := store.Stop(sess.ID, true, testNow(), revoke, kill, reap)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got, want := strings.Join(order, ","), "revoke,kill,reap"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if stopped.Status != StatusStopped || !stopped.CredentialsRevoked {
		t.Fatalf("wrong stopped state: %+v", stopped)
	}

	order = nil
	if _, err := store.Stop(sess.ID, true, testNow(), revoke, kill, reap); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("second stop should be no-op, got %v", order)
	}
}

func TestCreateDefaultsBackendSystem(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", "container", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sess.Backend != "system" {
		t.Fatalf("backend = %q, want system", sess.Backend)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Backend != "system" {
		t.Fatalf("stored backend = %q, want system", stored.Backend)
	}
}

func TestReconcileMarksDeadRunningSessionStopped(t *testing.T) {
	sess := Session{ID: "sess-x", Status: StatusRunning, PID: 4242}
	got, changed := reconcile(sess, testNow(), func(int) bool { return false })
	if !changed {
		t.Fatalf("dead running session should be reconciled")
	}
	if got.Status != StatusStopped {
		t.Fatalf("status = %q, want %q", got.Status, StatusStopped)
	}
	if got.PID != 0 {
		t.Fatalf("pid = %d, want 0", got.PID)
	}
	if got.LastError == "" {
		t.Fatalf("expected last_error explaining the dead transition")
	}
	if got.StoppedAt.IsZero() {
		t.Fatalf("expected stopped_at to be set")
	}
}

func TestReconcileLeavesLiveSessionRunning(t *testing.T) {
	sess := Session{ID: "sess-x", Status: StatusRunning, PID: 4242}
	got, changed := reconcile(sess, testNow(), func(int) bool { return true })
	if changed {
		t.Fatalf("live running session should not be reconciled")
	}
	if got.Status != StatusRunning || got.PID != 4242 {
		t.Fatalf("live session mutated: %+v", got)
	}
}

func TestReconcileIsIdempotentOnStopped(t *testing.T) {
	for _, st := range []string{StatusStopped, StatusCreated} {
		sess := Session{ID: "sess-x", Status: st, PID: 0}
		got, changed := reconcile(sess, testNow(), func(int) bool { return false })
		if changed {
			t.Fatalf("%s session should not be reconciled", st)
		}
		if got.Status != st {
			t.Fatalf("status changed from %q to %q", st, got.Status)
		}
	}
}

func TestGetReconciledPersistsDeadTransition(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunning(sess.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	got, err := store.GetReconciled(sess.ID, testNow(), func(int) bool { return false })
	if err != nil {
		t.Fatalf("get reconciled: %v", err)
	}
	if got.Status != StatusStopped {
		t.Fatalf("reconciled status = %q, want %q", got.Status, StatusStopped)
	}

	// The correction is persisted, so a plain Get sees stopped and we never
	// reconcile (or revoke) the same dead session twice.
	again, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if again.Status != StatusStopped || again.PID != 0 {
		t.Fatalf("dead transition not persisted: %+v", again)
	}
}

func TestListReconciledCorrectsDeadSessions(t *testing.T) {
	store := NewStore(t.TempDir())
	dead, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create dead: %v", err)
	}
	if _, err := store.MarkRunning(dead.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	created, err := store.Create("pi", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create created: %v", err)
	}

	sessions, err := store.ListReconciled(testNow(), func(int) bool { return false })
	if err != nil {
		t.Fatalf("list reconciled: %v", err)
	}
	byID := map[string]Session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	if byID[dead.ID].Status != StatusStopped {
		t.Fatalf("dead session not reconciled: %+v", byID[dead.ID])
	}
	if byID[created.ID].Status != StatusCreated {
		t.Fatalf("created session wrongly reconciled: %+v", byID[created.ID])
	}
}

func TestStoreStopCanRevokeAlreadyStoppedUnrevokedSession(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("pi", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Finish(sess.ID, 0, "", testNow()); err != nil {
		t.Fatalf("finish: %v", err)
	}

	var order []string
	stopped, err := store.Stop(sess.ID, true, testNow(), func(Session) error {
		order = append(order, "revoke")
		return nil
	}, func(int) error {
		order = append(order, "kill")
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got, want := strings.Join(order, ","), "revoke"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if !stopped.CredentialsRevoked {
		t.Fatalf("credentials not marked revoked: %+v", stopped)
	}
}

func TestRemoveDeletesNonRunningRecordAndRevokesLiveCredentials(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Finish(sess.ID, 1, "boom", testNow()); err != nil {
		t.Fatalf("finish: %v", err)
	}

	var revoked, reaped bool
	removed, err := store.Remove(sess.ID,
		func(Session) error { revoked = true; return nil },
		func(Session) error { reaped = true; return nil })
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.ID != sess.ID {
		t.Fatalf("removed wrong session: %+v", removed)
	}
	if !revoked {
		t.Fatal("Remove must revoke still-live credentials before deleting the record")
	}
	if !reaped {
		t.Fatal("Remove must reap any residual boundary")
	}
	if _, err := store.Get(sess.ID); err != ErrNotFound {
		t.Fatalf("record still present after remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sess.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("session file not deleted: %v", err)
	}
}

func TestRemoveSkipsRevokeWhenAlreadyRevoked(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Finish(sess.ID, 0, "", testNow()); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// Simulate a session already stopped with credentials revoked.
	if _, err := store.Stop(sess.ID, true, testNow(),
		func(Session) error { return nil }, func(int) error { return nil }, nil); err != nil {
		t.Fatalf("stop: %v", err)
	}

	revokeCalls := 0
	if _, err := store.Remove(sess.ID, func(Session) error { revokeCalls++; return nil }); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if revokeCalls != 0 {
		t.Fatalf("Remove revoked %d times for an already-revoked session, want 0", revokeCalls)
	}
}

func TestRemoveRefusesRunningSession(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", "host", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunning(sess.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if _, err := store.Remove(sess.ID, func(Session) error { return nil }); err != ErrSessionRunning {
		t.Fatalf("Remove of a running session = %v, want ErrSessionRunning", err)
	}
	if _, err := store.Get(sess.ID); err != nil {
		t.Fatalf("running session record wrongly deleted: %v", err)
	}
}

func TestRemoveNotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Remove("sess-missing", nil); err != ErrNotFound {
		t.Fatalf("Remove of missing session = %v, want ErrNotFound", err)
	}
}

func TestPruneStoppedRemovesOnlyStoppedSessions(t *testing.T) {
	store := NewStore(t.TempDir())
	stopped1, _ := store.Create("claude", "host", t.TempDir(), testNow())
	stopped2, _ := store.Create("pi", "host", t.TempDir(), testNow())
	created, _ := store.Create("fish", "host", t.TempDir(), testNow())
	running, _ := store.Create("zsh", "host", t.TempDir(), testNow())
	for _, id := range []string{stopped1.ID, stopped2.ID} {
		if _, err := store.Finish(id, 0, "", testNow()); err != nil {
			t.Fatalf("finish %s: %v", id, err)
		}
	}
	if _, err := store.MarkRunning(running.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	revokeCalls := 0
	removed, err := store.PruneStopped(func(Session) error { revokeCalls++; return nil })
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("pruned %v, want the 2 stopped sessions", removed)
	}
	for _, id := range removed {
		if _, err := store.Get(id); err != ErrNotFound {
			t.Fatalf("pruned session %s still present: %v", id, err)
		}
	}
	if _, err := store.Get(created.ID); err != nil {
		t.Fatalf("created session wrongly pruned: %v", err)
	}
	if _, err := store.Get(running.ID); err != nil {
		t.Fatalf("running session wrongly pruned: %v", err)
	}
}

func TestPruneStoppedNoopWhenNoneStopped(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Create("claude", "host", t.TempDir(), testNow()); err != nil {
		t.Fatalf("create: %v", err)
	}
	removed, err := store.PruneStopped(func(Session) error { return nil })
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("pruned %v, want none", removed)
	}
}
