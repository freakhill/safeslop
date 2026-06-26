package cli

import (
	"bytes"
	"encoding/json"
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
