package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

// TestSessionDataSocketPresentWhenRunningDetached: a running session whose
// per-session socket exists on disk advertises it (specs/0051 D5).
func TestSessionDataSocketPresentWhenRunningDetached(t *testing.T) {
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := "sess-aaaa0000"
	sock := filepath.Join(store.Dir, id+".sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("seed socket: %v", err)
	}
	sess := engsession.Session{ID: id, Status: engsession.StatusRunning, PID: 4242, Detached: true}
	if got := sessionData(sess)["socket"]; got != sock {
		t.Fatalf("socket = %v, want %q", got, sock)
	}
}

// TestSessionDataSocketAbsentWhenCoupled: the field is absent unless the session
// is running AND its socket file exists (specs/0051 D5), so a coupled run (no
// socket) and any non-running session never advertise one.
func TestSessionDataSocketAbsentWhenCoupled(t *testing.T) {
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	store := sessionStore()
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	running := engsession.Session{ID: "sess-bbbb0000", Status: engsession.StatusRunning, PID: 4242}
	if _, ok := sessionData(running)["socket"]; ok {
		t.Fatal("socket advertised for a running session with no socket file")
	}
	// A non-running session must never advertise a socket, even if a file lingers.
	_ = os.WriteFile(filepath.Join(store.Dir, "sess-cccc0000.sock"), nil, 0o600)
	created := engsession.Session{ID: "sess-cccc0000", Status: engsession.StatusCreated}
	if _, ok := sessionData(created)["socket"]; ok {
		t.Fatal("socket advertised for a non-running session")
	}
}

// TestDetachedGoldenMatchesEmittedEnvelope pins ok-session-detached.golden.json to
// the exact envelope a running detached session emits, so Go and Emacs parse the
// same fixture (specs/0051 D5). sessionSocket is stubbed to a fixed path so the
// golden is machine-independent.
func TestDetachedGoldenMatchesEmittedEnvelope(t *testing.T) {
	old := sessionSocket
	sessionSocket = func(sess engsession.Session) (string, bool) {
		return "/state/sessions/" + sess.ID + ".sock", true
	}
	defer func() { sessionSocket = old }()

	sess := engsession.Session{
		ID:          "sess-0123456789abcdef01234567",
		Agent:       "claude",
		Workspace:   "/workspace/project",
		Environment: "sandbox",
		Network:     "deny",
		Status:      engsession.StatusRunning,
		CreatedAt:   time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC),
		StartedAt:   time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC),
		PID:         4242,
		Detached:    true,
	}
	got, err := jsoncontract.Marshal(jsoncontract.OK(sessionData(sess)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "jsoncontract", "testdata", "ok-session-detached.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("ok-session-detached.golden.json drifted from the emitted envelope\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
