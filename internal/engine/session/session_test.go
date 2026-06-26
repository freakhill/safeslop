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
