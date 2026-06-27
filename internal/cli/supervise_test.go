package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/sandbox"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/session/wire"
)

// newSupervisedStubSession persists a host-tier session whose agent is a stub
// shell script (the same SHELL seam the runProfile tests use), so Supervise runs
// it hermetically with no real agent and no network.
func newSupervisedStubSession(t *testing.T, script string) (engsession.Store, string, string) {
	return newSupervisedStubSessionIn(t, script, shortStateDir(t), "host")
}

// newSupervisedStubSessionIn is newSupervisedStubSession with an explicit state
// dir (so a test can force the sun_path-overflow relocation branch with a long
// one) and isolation environment (so a test can drive the sandbox launch path).
func newSupervisedStubSessionIn(t *testing.T, script, stateDir, env string) (engsession.Store, string, string) {
	t.Helper()
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", stateDir)
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
	sess.Environment = env
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return store, sess.ID, ws
}

// shortStateDir returns a state dir under a short base. The per-session unix
// socket lives at <state>/sessions/<id>.sock, and a unix socket path must fit in
// sun_path (104 bytes on macOS); t.TempDir() under /var/folders/... is already
// too long once the 43-char "sessions/sess-<24hex>.sock" suffix is appended.
// Store.SocketPath now relocates such overflowing paths, so this is no longer
// required for correctness; tests use it to keep the socket at its natural
// in-state-dir path (the common, default-state-dir branch).
func shortStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ss")
	if err != nil {
		t.Fatalf("short state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// longStateDir returns a state dir long enough that <state>/sessions/<id>.sock
// overflows sun_path, forcing Store.SocketPath's relocation branch.
func longStateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), strings.Repeat("x", 90))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("long state dir: %v", err)
	}
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

	conn := dialSocketForTest(t, store.SocketPath(id))
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
	sockPath := store.SocketPath(id)
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

// TestSuperviseAndAttachUnderOverflowingStateDir proves the sun_path guard end to
// end. With a $SAFESLOP_STATE_DIR long enough that <Dir>/<id>.sock would overflow
// sun_path — net.Listen("unix", …) on the natural path fails with "invalid
// argument" — the supervisor still binds (SocketPath relocates the socket to a
// short runtime dir) and a client attaches, drives stdin, and gets the agent's
// output and exit code (specs/0051 sun_path hardening).
func TestSuperviseAndAttachUnderOverflowingStateDir(t *testing.T) {
	store, id, _ := newSupervisedStubSessionIn(t, "#!/bin/sh\nread x\nprintf 'MARKER\\n'\nexit 42\n", longStateDir(t), "host")

	natural := filepath.Join(store.Dir, id+".sock")
	if len(natural) <= 103 {
		t.Fatalf("test misconfigured: natural socket path len = %d, want > 103 to exercise relocation", len(natural))
	}
	sock := store.SocketPath(id)
	if len(sock) > 103 || strings.HasPrefix(sock, store.Dir) {
		t.Fatalf("SocketPath did not relocate the overflowing path: %q (len %d, under dir %q)", sock, len(sock), store.Dir)
	}
	t.Cleanup(func() { _ = os.Remove(sock) }) // teardown removes it; belt-and-suspenders since it lives outside t.TempDir

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = Supervise(ctx, store, id, time.Now) }()

	if !waitForFile(sock, 5*time.Second) {
		t.Fatalf("supervisor never bound the relocated socket %q", sock)
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

// TestSuperviseGivesHostAgentAControllingTerminal proves the detached host
// supervisor launches the agent with the PTY it owns as the agent's controlling
// terminal: the agent can open /dev/tty. Under the daemon there is no inherited
// terminal, so without the Setctty wiring the agent would have none. The probe's
// output is captured via the JSONL tee (specs/0051 host Setctty).
func TestSuperviseGivesHostAgentAControllingTerminal(t *testing.T) {
	store, id, _ := newSupervisedStubSession(t, "#!/bin/sh\nif : </dev/tty 2>/dev/null; then printf 'CTTY=yes\\n'; else printf 'CTTY=no\\n'; fi\nexit 0\n")
	jsonlPath := filepath.Join(store.Dir, id+".jsonl")

	code, err := Supervise(context.Background(), store, id, time.Now)
	if err != nil || code != 0 {
		t.Fatalf("Supervise: code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if jsonlContains(data, "CTTY=no") {
		t.Fatalf("agent reported no controlling terminal under the supervisor:\n%s", data)
	}
	if !jsonlContains(data, "CTTY=yes") {
		t.Fatalf("agent controlling-terminal probe never captured:\n%s", data)
	}
}

// TestSuperviseGivesSandboxAgentAControllingTerminal is the sandbox sibling of the
// host controlling-terminal test: a detached SANDBOX session launches the agent
// (under sandbox-exec) with the supervisor PTY as its controlling terminal, so the
// sandboxed agent can open /dev/tty. The /dev read and tty ioctls are already
// permitted by the Seatbelt profile; this proves runProfileCtx's sandbox branch
// requests the controlling terminal too (specs/0051 sandbox Setctty).
func TestSuperviseGivesSandboxAgentAControllingTerminal(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	store, id, _ := newSupervisedStubSessionIn(t, "#!/bin/sh\nif : </dev/tty 2>/dev/null; then printf 'CTTY=yes\\n'; else printf 'CTTY=no\\n'; fi\nexit 0\n", shortStateDir(t), "sandbox")
	jsonlPath := filepath.Join(store.Dir, id+".jsonl")

	code, err := Supervise(context.Background(), store, id, time.Now)
	if err != nil || code != 0 {
		t.Fatalf("Supervise: code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if jsonlContains(data, "CTTY=no") {
		t.Fatalf("sandboxed agent reported no controlling terminal under the supervisor:\n%s", data)
	}
	if !jsonlContains(data, "CTTY=yes") {
		t.Fatalf("sandboxed agent controlling-terminal probe never captured:\n%s", data)
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
