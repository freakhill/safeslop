package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

func runRootForTest(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldwd)

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	jsonOut = false
	cmd := newRoot()
	cmd.SetArgs(args)
	err = cmd.Execute()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String(), err
}

func nowForTest(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
}

func parseEnvelopeForTest(t *testing.T, out string) jsoncontract.Envelope {
	t.Helper()
	env, err := jsoncontract.Unmarshal([]byte(out))
	if err != nil {
		t.Fatalf("parse envelope %q: %v", out, err)
	}
	return env
}

func TestSessionCreateEmitsContractAndPersistsSafeDefaults(t *testing.T) {
	ws := t.TempDir()
	state := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", state)

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("session create: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	id, ok := env.Data["session_id"].(string)
	if !ok || id == "" {
		t.Fatalf("session_id missing from data: %#v", env.Data)
	}
	if got := env.Data["agent"]; got != "claude" {
		t.Fatalf("agent = %#v", got)
	}
	if got := env.Data["workspace"]; got != ws {
		t.Fatalf("workspace = %#v, want %q", got, ws)
	}
	if got := env.Data["network"]; got != "deny" {
		t.Fatalf("network default = %#v, want deny", got)
	}
	if got := env.Data["environment"]; got != "sandbox" {
		t.Fatalf("environment default = %#v, want sandbox", got)
	}
	if _, err := os.Stat(filepath.Join(state, "sessions", id+".json")); err != nil {
		t.Fatalf("session state not persisted: %v", err)
	}
}

func TestSessionCreateAcceptsClaudeCodeAlias(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude-code", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("session create claude-code: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["agent"]; got != "claude" {
		t.Fatalf("agent = %#v, want canonical claude", got)
	}
}

func TestSessionCreateRejectsUnsupportedAgentAsContract(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "shell", "--workspace", ws, "--output", "json")
	if err == nil {
		t.Fatalf("unsupported agent unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != jsoncontract.CodeAgentUnsupported {
		t.Fatalf("wrong error envelope: %+v", env)
	}
}

func TestSessionStatusJSONLEmitsSingleLineContract(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "pi", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)

	out, err = runRootForTest(t, ws, "session", "status", "--session-id", id, "--output", "jsonl")
	if err != nil {
		t.Fatalf("status: %v\nout=%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("jsonl should be one line, got %d lines: %q", len(lines), out)
	}
	env := parseEnvelopeForTest(t, lines[0])
	if !env.OK || env.Data["session_id"] != id || env.Data["status"] != "created" {
		t.Fatalf("wrong status envelope: %+v", env)
	}
}

func TestSessionStopRevokesBeforeKillAndIsIdempotent(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	var order []string
	oldRevoke, oldKill := sessionRevokeCredentials, sessionKillProcess
	sessionRevokeCredentials = func(_ engsession.Session) error { order = append(order, "revoke"); return nil }
	sessionKillProcess = func(_ int) error { order = append(order, "kill"); return nil }
	defer func() { sessionRevokeCredentials, sessionKillProcess = oldRevoke, oldKill }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunning(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	out, err = runRootForTest(t, ws, "session", "stop", "--session-id", id, "--revoke-credentials", "--output", "json")
	if err != nil {
		t.Fatalf("stop: %v\nout=%s", err, out)
	}
	if got, want := strings.Join(order, ","), "revoke,kill"; got != want {
		t.Fatalf("stop order = %s, want %s", got, want)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK || env.Data["status"] != "stopped" || env.Data["credentials_revoked"] != true {
		t.Fatalf("wrong stop envelope: %+v", env)
	}

	order = nil
	out, err = runRootForTest(t, ws, "session", "stop", "--session-id", id, "--revoke-credentials", "--output", "json")
	if err != nil {
		t.Fatalf("second stop should be idempotent: %v\nout=%s", err, out)
	}
	if len(order) != 0 {
		t.Fatalf("idempotent stop should not revoke/kill again, got %v", order)
	}
}

func TestSessionStatusReportsReconciledState(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldAlive := sessionProcessAlive
	sessionProcessAlive = func(int) bool { return false } // run wrapper is gone
	defer func() { sessionProcessAlive = oldAlive }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunning(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	out, err = runRootForTest(t, ws, "session", "status", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("status: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK || env.Data["status"] != "stopped" {
		t.Fatalf("status not reconciled to stopped: %+v", env.Data)
	}
	if _, ok := env.Data["last_error"].(string); !ok {
		t.Fatalf("expected last_error on a reconciled session: %+v", env.Data)
	}
}

func TestSessionStatusNotFoundUsesContractCode(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	out, err := runRootForTest(t, ws, "session", "status", "--session-id", "missing", "--output", "json")
	if err == nil {
		t.Fatalf("missing status unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || env.Errors[0].Code != jsoncontract.CodeSessionNotFound {
		t.Fatalf("wrong missing envelope: %+v", env)
	}
}

func TestSessionContractOutputDoesNotLeakSecretRefs(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "super-secret-value")
	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(out, "super-secret-value") || strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Fatalf("session output leaked secret-ish data: %s", out)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json: %v", err)
	}
}

// newHostShellSessionForTest persists a host-tier session whose agent is a host
// shell pointed at a non-existent $SHELL. This keeps `session run` hermetic in
// both phases of the PTY_UNAVAILABLE TDD: the guard under test must short-circuit
// *before* any launch (so the bogus shell is never execed), and if the guard were
// missing the launch would fail fast and cmdSessionRun would return that error —
// never reaching its os.Exit(code) on the success path, which would otherwise
// tear down the test binary. No live agent ever runs.
func newHostShellSessionForTest(t *testing.T, ws string) string {
	t.Helper()
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "no-such-shell"))
	store := sessionStore()
	sess, err := store.Create("shell", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess.Environment = "host"
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return sess.ID
}

// TestSessionRunEmitsPTYUnavailableWhenNoTTY proves that `session run` invoked
// without a usable controlling terminal (runRootForTest replaces os.Stdout with a
// pipe, so neither stdin nor stdout is a tty) emits the PTY_UNAVAILABLE contract
// envelope byte-for-byte and exits non-zero (specs/0050 PR4). The interactive run
// path is undriveable without a tty for every boundary, so the honest response is
// the JSONL status fallback advertised in the envelope details.
func TestSessionRunEmitsPTYUnavailableWhenNoTTY(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	id := newHostShellSessionForTest(t, ws)

	out, err := runRootForTest(t, ws, "session", "run", "--session-id", id)
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("session run without a usable PTY: err = %v, want errOutputEmitted; out=%q", err, out)
	}
	golden, gerr := os.ReadFile(filepath.Join("..", "jsoncontract", "testdata", "error-pty-unavailable.golden.json"))
	if gerr != nil {
		t.Fatalf("read golden: %v", gerr)
	}
	if out != string(golden) {
		t.Fatalf("PTY_UNAVAILABLE envelope mismatch\n--- got ---\n%s\n--- want ---\n%s", out, golden)
	}
}

// TestSessionRunDoesNotMarkRunningOnPTYUnavailable proves the PTY_UNAVAILABLE
// short-circuit happens *before* MarkRunning: a session that could never start
// must not be left recorded as running (or carrying the wrapper PID), so the
// liveness/reconcile machinery and `session stop` are not handed a phantom
// (specs/0050 PR4).
func TestSessionRunDoesNotMarkRunningOnPTYUnavailable(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	id := newHostShellSessionForTest(t, ws)

	if _, err := runRootForTest(t, ws, "session", "run", "--session-id", id); !errors.Is(err, errOutputEmitted) {
		t.Fatalf("session run without a usable PTY: err = %v, want errOutputEmitted", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Status != engsession.StatusCreated {
		t.Fatalf("session status = %q, want %q (MarkRunning must not run on PTY_UNAVAILABLE)", sess.Status, engsession.StatusCreated)
	}
	if sess.PID != 0 {
		t.Fatalf("session PID = %d, want 0 (run must not record a PID on PTY_UNAVAILABLE)", sess.PID)
	}
	if !sess.StartedAt.IsZero() {
		t.Fatalf("session StartedAt = %v, want zero (run must not stamp a start on PTY_UNAVAILABLE)", sess.StartedAt)
	}
}
