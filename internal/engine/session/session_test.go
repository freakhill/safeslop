package session

import (
	"strings"
	"testing"
	"time"
)

func testNow() time.Time { return time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC) }

func TestStoreStopRevokesBeforeKillAndIsIdempotent(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("claude", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.MarkRunning(sess.ID, 12345, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	var order []string
	revoke := func(Session) error { order = append(order, "revoke"); return nil }
	kill := func(int) error { order = append(order, "kill"); return nil }
	stopped, err := store.Stop(sess.ID, true, testNow(), revoke, kill)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got, want := strings.Join(order, ","), "revoke,kill"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if stopped.Status != StatusStopped || !stopped.CredentialsRevoked {
		t.Fatalf("wrong stopped state: %+v", stopped)
	}

	order = nil
	if _, err := store.Stop(sess.ID, true, testNow(), revoke, kill); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("second stop should be no-op, got %v", order)
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
	sess, err := store.Create("claude", t.TempDir(), testNow())
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
	dead, err := store.Create("claude", t.TempDir(), testNow())
	if err != nil {
		t.Fatalf("create dead: %v", err)
	}
	if _, err := store.MarkRunning(dead.ID, 4242, testNow()); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	created, err := store.Create("pi", t.TempDir(), testNow())
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
	sess, err := store.Create("pi", t.TempDir(), testNow())
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
	})
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
