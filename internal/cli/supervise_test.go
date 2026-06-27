package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/session/wire"
)

// newSupervisedStubSession persists a host-tier session whose agent is a stub
// shell script (the same SHELL seam the runProfile tests use), so Supervise runs
// it hermetically with no real agent and no network.
func newSupervisedStubSession(t *testing.T, script string) (engsession.Store, string, string) {
	t.Helper()
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", shortStateDir(t))
	stub := filepath.Join(ws, "agent")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("SHELL", stub)
	store := sessionStore()
	sess, err := store.Create("shell", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess.Environment = "host"
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return store, sess.ID, ws
}

// shortStateDir returns a state dir under a short base. The per-session unix
// socket lives at <state>/sessions/<id>.sock, and a unix socket path must fit in
// sun_path (104 bytes on macOS); t.TempDir() under /var/folders/... is already
// too long once the 43-char "sessions/sess-<24hex>.sock" suffix is appended.
func shortStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ss")
	if err != nil {
		t.Fatalf("short state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func dialSocketForTest(t *testing.T, path string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket %s never became dialable", path)
	return nil
}

// TestSuperviseRunsAgentAndServesSocket proves the supervisor launches the agent
// on its PTY and serves that PTY over the per-session unix socket: a client
// connects, drives the agent's stdin, and reads the agent's output back, then the
// agent's exit code arrives as an X frame.
func TestSuperviseRunsAgentAndServesSocket(t *testing.T) {
	// The agent blocks on read until the client (now attached) sends input, so the
	// output it then prints can't be produced before the client connects — no race.
	store, id, _ := newSupervisedStubSession(t, "#!/bin/sh\nread x\nprintf 'MARKER\\n'\nexit 0\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type sret struct {
		code int
		err  error
	}
	done := make(chan sret, 1)
	go func() {
		c, e := Supervise(ctx, store, id, time.Now)
		done <- sret{c, e}
	}()

	conn := dialSocketForTest(t, filepath.Join(store.Dir, id+".sock"))
	defer conn.Close()

	if err := wire.Write(conn, wire.DataFrame([]byte("go\n"))); err != nil {
		t.Fatalf("send input frame: %v", err)
	}

	var out []byte
	var code int
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		f, err := wire.Read(conn)
		if err != nil {
			t.Fatalf("read frame: %v (output so far %q)", err, out)
		}
		switch f.Type {
		case wire.Data:
			out = append(out, f.Data...)
		case wire.Exit:
			code = f.Code
			goto exited
		}
	}
exited:
	if !bytes.Contains(out, []byte("MARKER")) {
		t.Fatalf("agent output not served over socket: %q", out)
	}
	if code != 0 {
		t.Fatalf("exit frame code = %d, want 0", code)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Supervise err: %v", r.err)
		}
		if r.code != 0 {
			t.Fatalf("Supervise returned %d, want 0", r.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Supervise did not return after agent exit")
	}
}

// TestSuperviseExitRunsTeardownAndRemovesSocket proves that when the agent exits,
// the supervisor's inherited teardown runs (stage dir gone), the socket is
// removed, and the session is Finished with the agent's real code.
func TestSuperviseExitRunsTeardownAndRemovesSocket(t *testing.T) {
	store, id, ws := newSupervisedStubSession(t, "#!/bin/sh\nexit 42\n")
	sockPath := filepath.Join(store.Dir, id+".sock")
	stageDir := filepath.Join(ws, ".safeslop", "runtime", "session-"+id)

	code, err := Supervise(context.Background(), store, id, time.Now)
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	if code != 42 {
		t.Fatalf("Supervise returned %d, want 42", code)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("socket not removed (stat err = %v)", err)
	}
	if _, err := os.Stat(stageDir); !os.IsNotExist(err) {
		t.Fatalf("stage dir not wiped (stat err = %v)", err)
	}
	sess, err := store.Get(id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Status != engsession.StatusStopped {
		t.Fatalf("status = %q, want stopped", sess.Status)
	}
	if sess.ExitCode == nil || *sess.ExitCode != 42 {
		t.Fatalf("recorded exit_code = %v, want 42", sess.ExitCode)
	}
}

// TestSuperviseRecordsSupervisorPIDAlive proves the recorded PID is the
// supervisor's own (it runs in-process here) and is alive while the agent runs.
func TestSuperviseRecordsSupervisorPIDAlive(t *testing.T) {
	store, id, _ := newSupervisedStubSession(t, "#!/bin/sh\nread x\n") // blocks until cancel
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		c, _ := Supervise(ctx, store, id, time.Now)
		done <- c
	}()

	var sess engsession.Session
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s, err := store.Get(id)
		if err == nil && s.Status == engsession.StatusRunning && s.PID != 0 {
			sess = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sess.Status != engsession.StatusRunning {
		t.Fatal("session never marked running")
	}
	if sess.PID != os.Getpid() {
		t.Fatalf("recorded PID = %d, want supervisor pid %d", sess.PID, os.Getpid())
	}
	if !engsession.ProcessAlive(sess.PID) {
		t.Fatalf("recorded supervisor PID %d is not alive", sess.PID)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Supervise did not return after cancel")
	}
}

// TestSuperviseTeesOutputToJSONL proves the supervisor drains the agent PTY and
// tees its output to the per-session JSONL log even with no client attached (so a
// detached agent never blocks on a full PTY before the first attach).
func TestSuperviseTeesOutputToJSONL(t *testing.T) {
	store, id, _ := newSupervisedStubSession(t, "#!/bin/sh\nprintf 'LOGGED\\n'\nexit 0\n")
	jsonlPath := filepath.Join(store.Dir, id+".jsonl")

	code, err := Supervise(context.Background(), store, id, time.Now)
	if err != nil || code != 0 {
		t.Fatalf("Supervise: code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if !jsonlContains(data, "LOGGED") {
		t.Fatalf("jsonl did not capture agent output:\n%s", data)
	}
}

func jsonlContains(data []byte, marker string) bool {
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Data string `json:"data"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		dec, err := base64.StdEncoding.DecodeString(rec.Data)
		if err != nil {
			continue
		}
		if bytes.Contains(dec, []byte(marker)) {
			return true
		}
	}
	return false
}
