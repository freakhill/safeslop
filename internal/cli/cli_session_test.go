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

	"github.com/freakhill/safeslop/internal/engine/policy"
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

// trustFixtureForTest isolates the trust store into a temp HOME and host-approves the safeslop.cue
// at ws, so `session create --profile` passes the specs/0072 F1 trust gate — mirroring an operator
// running `safeslop trust` before launching from the Emacs client. HOME must be set before the create
// call so both the approval here and the gate inside runRootForTest read the same isolated store.
func trustFixtureForTest(t *testing.T, ws string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // trust store -> {home}/.config/safeslop/trust.json, off the real one
	if err := enforceTrust(filepath.Join(ws, "safeslop.cue"), true); err != nil {
		t.Fatalf("approve fixture policy: %v", err)
	}
}

func parseEnvelopeForTest(t *testing.T, out string) jsoncontract.Envelope {
	t.Helper()
	env, err := jsoncontract.Unmarshal([]byte(out))
	if err != nil {
		t.Fatalf("parse envelope %q: %v", out, err)
	}
	return env
}

func seedSessionStageDirForTest(t *testing.T, sess engsession.Session) string {
	t.Helper()
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		t.Fatalf("session stage dir: %v", err)
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		t.Fatalf("mkdir stage dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "secrets.env"), []byte("TEST_ONLY=1\n"), 0o600); err != nil {
		t.Fatalf("seed stage dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stageDir) })
	return stageDir
}

func assertStageDirRemovedForTest(t *testing.T, stageDir string) {
	t.Helper()
	if _, err := os.Stat(stageDir); !os.IsNotExist(err) {
		t.Fatalf("stage dir %q still exists (stat err = %v)", stageDir, err)
	}
}

// TestSessionCreateGoldenMatchesEmittedEnvelope pins ok-session-create.golden.json
// to the exact envelope `session create` emits for a freshly created session, so
// the fixture cannot drift from reality (the daemon-shaped nested session{} +
// socket fiction is gone; the flat sessionData shape used by status/list/stop is
// the one source of truth) — specs/0050 PR5.
func TestSessionCreateGoldenMatchesEmittedEnvelope(t *testing.T) {
	sess := engsession.Session{
		ID:          "sess-0123456789abcdef01234567",
		Agent:       "claude",
		Workspace:   "/workspace/project",
		Environment: "host",
		Network:     "deny",
		Status:      engsession.StatusCreated,
		CreatedAt:   time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC),
	}
	got, err := jsoncontract.Marshal(jsoncontract.OK(sessionData(sess)))
	if err != nil {
		t.Fatalf("marshal emitted envelope: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "jsoncontract", "testdata", "ok-session-create.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("ok-session-create.golden.json drifted from the emitted envelope\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSessionCreateEmitsContractAndPersistsSafeDefaults(t *testing.T) {
	ws := t.TempDir()
	state := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", state)

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
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
	if got := env.Data["environment"]; got != "host" {
		t.Fatalf("environment = %#v, want host", got)
	}
	if _, err := os.Stat(filepath.Join(state, "sessions", id+".json")); err != nil {
		t.Fatalf("session state not persisted: %v", err)
	}
}

func TestSessionCreateAcceptsClaudeCodeAlias(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude-code", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
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

func TestSessionCreateFromProfileResolvesRecipeMetadata(t *testing.T) {
	ws := t.TempDir()
	state := t.TempDir()
	project := filepath.Join(ws, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("mkdir project workspace: %v", err)
	}
	t.Setenv("SAFESLOP_STATE_DIR", state)
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {
			agent: "pi"
			environment: "container"
			network: "deny"
			workspace: "project"
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatalf("write safeslop.cue: %v", err)
	}

	trustFixtureForTest(t, ws)
	out, err := runRootForTest(t, ws, "session", "create", "--profile", "review", "--output", "json")
	if err != nil {
		t.Fatalf("session create --profile: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["profile"]; got != "review" {
		t.Fatalf("profile = %#v, want review", got)
	}
	if got := env.Data["agent"]; got != "pi" {
		t.Fatalf("agent = %#v, want pi", got)
	}
	if got := env.Data["environment"]; got != "container" {
		t.Fatalf("environment = %#v, want container", got)
	}
	if got := env.Data["network"]; got != "deny" {
		t.Fatalf("network = %#v, want deny", got)
	}
	wantWorkspace, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("canonicalize workspace: %v", err)
	}
	if got := env.Data["workspace"]; got != wantWorkspace {
		t.Fatalf("workspace = %#v, want %q", got, wantWorkspace)
	}
	recipeID, ok := env.Data["recipeID"].(string)
	if !ok || len(recipeID) != 12 {
		t.Fatalf("recipeID = %#v, want 12-char string", env.Data["recipeID"])
	}
	image, ok := env.Data["image"].(string)
	if !ok || !strings.HasSuffix(image, ":"+recipeID) {
		t.Fatalf("image = %#v, want tag ending in recipeID %q", env.Data["image"], recipeID)
	}
	resolved, ok := env.Data["resolved"].(map[string]any)
	if !ok {
		t.Fatalf("resolved metadata missing: %#v", env.Data["resolved"])
	}
	idsAny, ok := resolved["identitySet"].([]any)
	if !ok {
		t.Fatalf("resolved.identitySet missing: %#v", resolved)
	}
	ids := make([]string, 0, len(idsAny))
	for _, id := range idsAny {
		ids = append(ids, id.(string))
	}
	if got, want := strings.Join(ids, ","), "node,pi"; got != want {
		t.Fatalf("resolved.identitySet = %s, want %s", got, want)
	}

	id := env.Data["session_id"].(string)
	storedBytes, err := os.ReadFile(filepath.Join(state, "sessions", id+".json"))
	if err != nil {
		t.Fatalf("read stored session: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(storedBytes, &stored); err != nil {
		t.Fatalf("decode stored session: %v", err)
	}
	if stored["profile"] != "review" || stored["recipeID"] != recipeID || stored["image"] != image {
		t.Fatalf("stored profile recipe metadata = %#v", stored)
	}

	storedSession, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("load stored session: %v", err)
	}
	runProfile, sperr := sessionProfile(storedSession)
	if sperr != nil {
		t.Fatalf("sessionProfile: %v", sperr)
	}
	if !runProfile.BareAgent || strings.Join(runProfile.Packages, ",") != "node,pi" {
		t.Fatalf("sessionProfile packages = %v bare=%v, want exact resolved package identity", runProfile.Packages, runProfile.BareAgent)
	}
	if rerunResolved, err := policy.Resolve(runProfile); err != nil || strings.Join(rerunResolved.IdentitySet, ",") != "node,pi" {
		t.Fatalf("sessionProfile re-resolve = %+v err=%v, want node,pi only", rerunResolved, err)
	}

	out, err = runRootForTest(t, ws, "session", "status", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("session status: %v\nout=%s", err, out)
	}
	statusEnv := parseEnvelopeForTest(t, out)
	if !statusEnv.OK || statusEnv.Data["profile"] != "review" || statusEnv.Data["recipeID"] != recipeID || statusEnv.Data["image"] != image {
		t.Fatalf("status metadata did not round-trip: %+v", statusEnv)
	}
}

func TestSessionCreateFromProfileAcceptsShellAgent(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		dev: {
			agent: "shell"
			environment: "host"
			network: "deny"
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatalf("write safeslop.cue: %v", err)
	}

	trustFixtureForTest(t, ws)
	out, err := runRootForTest(t, ws, "session", "create", "--profile", "dev", "--output", "json")
	if err != nil {
		t.Fatalf("session create --profile shell: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK || env.Data["profile"] != "dev" || env.Data["agent"] != "shell" {
		t.Fatalf("shell profile create envelope = %+v", env)
	}
}

func TestSessionCreateRejectsUnsupportedAgentAsContract(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "shell", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err == nil {
		t.Fatalf("unsupported agent unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != jsoncontract.CodeAgentUnsupported {
		t.Fatalf("wrong error envelope: %+v", env)
	}
}

// fish/zsh are first-class launchable agents (specs/0055 W0): a session create
// that the old pi/claude-only allowlist would have rejected must now succeed.
func TestSessionCreateAcceptsFishAgent(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "fish", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("fish agent create failed: %v (%s)", err, out)
	}
	if id, _ := parseEnvelopeForTest(t, out).Data["session_id"].(string); id == "" {
		t.Fatalf("fish agent create returned no session_id: %s", out)
	}
}

func TestSessionStatusJSONLEmitsSingleLineContract(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "pi", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
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
	oldRevoke, oldKill, oldAlive := sessionRevokeCredentials, sessionKillProcess, sessionProcessAlive
	sessionRevokeCredentials = func(_ engsession.Session) error { order = append(order, "revoke"); return nil }
	sessionKillProcess = func(_ int) error { order = append(order, "kill"); return nil }
	sessionProcessAlive = func(engsession.Session) bool { return true }
	defer func() {
		sessionRevokeCredentials, sessionKillProcess, sessionProcessAlive = oldRevoke, oldKill, oldAlive
	}()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
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

func TestSessionStopWipesStageDirWithoutRevoke(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldKill := sessionKillProcess
	sessionKillProcess = func(int) error { return nil }
	defer func() { sessionKillProcess = oldKill }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunning(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stageDir := seedSessionStageDirForTest(t, sess)

	out, err = runRootForTest(t, ws, "session", "stop", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("stop: %v\nout=%s", err, out)
	}
	assertStageDirRemovedForTest(t, stageDir)
}

func TestSessionStopSkipsKillForStaleDetachedProcess(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldAlive, oldKill := sessionProcessAlive, sessionKillProcess
	sessionProcessAlive = func(engsession.Session) bool { return false } // supervisor PID is gone/stale before stop
	killed := false
	sessionKillProcess = func(int) error { killed = true; return nil }
	defer func() { sessionProcessAlive, sessionKillProcess = oldAlive, oldKill }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunningDetached(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark detached: %v", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stageDir := seedSessionStageDirForTest(t, sess)

	out, err = runRootForTest(t, ws, "session", "stop", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("stop: %v\nout=%s", err, out)
	}
	if killed {
		t.Fatal("stop signalled a stale/reconciled detached PID")
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK || env.Data["status"] != "stopped" {
		t.Fatalf("stop envelope = %+v, want stopped", env)
	}
	assertStageDirRemovedForTest(t, stageDir)
}

func TestSessionStatusReportsReconciledState(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldAlive := sessionProcessAlive
	sessionProcessAlive = func(engsession.Session) bool { return false } // run wrapper is gone
	defer func() { sessionProcessAlive = oldAlive }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
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

func TestSessionStatusReconcileWipesStageDir(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldAlive := sessionProcessAlive
	sessionProcessAlive = func(engsession.Session) bool { return false }
	defer func() { sessionProcessAlive = oldAlive }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunning(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stageDir := seedSessionStageDirForTest(t, sess)

	out, err = runRootForTest(t, ws, "session", "status", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("status: %v\nout=%s", err, out)
	}
	assertStageDirRemovedForTest(t, stageDir)
}

func TestSessionListReconcileWipesStageDir(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldAlive := sessionProcessAlive
	sessionProcessAlive = func(engsession.Session) bool { return false }
	defer func() { sessionProcessAlive = oldAlive }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunning(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stageDir := seedSessionStageDirForTest(t, sess)

	out, err = runRootForTest(t, ws, "session", "list", "--output", "json")
	if err != nil {
		t.Fatalf("list: %v\nout=%s", err, out)
	}
	assertStageDirRemovedForTest(t, stageDir)
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
	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
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
	sess, err := store.Create("shell", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
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

// TestSessionCreateEnvironmentOverride proves that --environment and --network flags
// override the profile defaults and are persisted in the session record so that
// `session run` launches under the requested boundary.
func TestSessionCreateEnvironmentOverride(t *testing.T) {
	ws := t.TempDir()
	state := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", state)

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "container", "--network", "allow",
	)
	if err != nil {
		t.Fatalf("session create: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["environment"]; got != "container" {
		t.Fatalf("environment = %#v, want container", got)
	}
	if got := env.Data["network"]; got != "allow" {
		t.Fatalf("network = %#v, want allow", got)
	}

	// Verify the override is also persisted in the stored session record.
	id := env.Data["session_id"].(string)
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Environment != "container" {
		t.Fatalf("persisted environment = %q, want container", sess.Environment)
	}
	if sess.Network != "allow" {
		t.Fatalf("persisted network = %q, want allow", sess.Network)
	}
}

// TestSessionCreateEnvironmentOnlyOverride proves that supplying only --environment
// overrides the environment while leaving network at the default ("deny").
func TestSessionCreateEnvironmentOnlyOverride(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "host", "--trust-host",
	)
	if err != nil {
		t.Fatalf("session create: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["environment"]; got != "host" {
		t.Fatalf("environment = %#v, want host", got)
	}
	if got := env.Data["network"]; got != "deny" {
		t.Fatalf("network = %#v, want deny (default unchanged)", got)
	}
}

// TestSessionCreateRejectsInvalidEnvironment proves that an unrecognised
// --environment value is rejected with a CodeInvalidArgument contract error.
func TestSessionCreateRejectsInvalidEnvironment(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "bogus",
	)
	if err == nil {
		t.Fatalf("invalid --environment unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 {
		t.Fatalf("expected error envelope, got: %+v", env)
	}
	if env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("error code = %q, want %q", env.Errors[0].Code, jsoncontract.CodeInvalidArgument)
	}
}

// TestSessionCreateRequiresEnvironment proves that omitting --environment is a
// CodeInvalidArgument contract error (specs/0053: environment is required — there
// is no default after the sandbox tier was removed).
func TestSessionCreateRequiresEnvironment(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
	)
	if err == nil {
		t.Fatalf("missing --environment unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 {
		t.Fatalf("expected error envelope, got: %+v", env)
	}
	if env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("error code = %q, want %q", env.Errors[0].Code, jsoncontract.CodeInvalidArgument)
	}
}

// TestSessionCreateRejectsSandboxEnvironment proves the removed sandbox tier is
// rejected like any other unknown environment (specs/0053).
func TestSessionCreateRejectsSandboxEnvironment(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--workspace", ws, "--output", "json",
		"--environment", "sandbox",
	)
	if err == nil {
		t.Fatalf("--environment sandbox unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 || env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument error envelope, got: %+v", env)
	}
}

// TestSessionCreateRejectsInvalidNetwork proves that an unrecognised --network
// value is rejected with a CodeInvalidArgument contract error.
func TestSessionCreateRejectsInvalidNetwork(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create",
		"--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json",
		"--network", "open",
	)
	if err == nil {
		t.Fatalf("invalid --network unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 {
		t.Fatalf("expected error envelope, got: %+v", env)
	}
	if env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("error code = %q, want %q", env.Errors[0].Code, jsoncontract.CodeInvalidArgument)
	}
}

// TestSessionRemoveDeletesStoppedRecordAndRevokes proves `session rm` deletes a
// stopped session's record (clearing a portal "corpse") and revokes any still-live
// staged credentials first, so a removal can never orphan secrets.
func TestSessionRemoveDeletesStoppedRecordAndRevokes(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	revoked := 0
	oldRevoke := sessionRevokeCredentials
	sessionRevokeCredentials = func(_ engsession.Session) error { revoked++; return nil }
	defer func() { sessionRevokeCredentials = oldRevoke }()

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().Finish(id, 1, "boom", nowForTest(t)); err != nil {
		t.Fatalf("finish: %v", err)
	}

	out, err = runRootForTest(t, ws, "session", "rm", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("rm not ok: %+v", env)
	}
	removed, _ := env.Data["removed"].([]any)
	if len(removed) != 1 || removed[0].(string) != id {
		t.Fatalf("rm removed = %v, want [%s]", env.Data["removed"], id)
	}
	if revoked != 1 {
		t.Fatalf("rm revoked %d times, want 1", revoked)
	}
	if _, err := sessionStore().Get(id); !errors.Is(err, engsession.ErrNotFound) {
		t.Fatalf("record still present after rm: %v", err)
	}
}

// TestSessionRemoveRefusesRunning proves `session rm` refuses a running session
// (you must stop it first) with SESSION_ALREADY_RUNNING and leaves the record.
func TestSessionRemoveRefusesRunning(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().MarkRunning(id, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	out, _ = runRootForTest(t, ws, "session", "rm", "--session-id", id, "--output", "json")
	env := parseEnvelopeForTest(t, out)
	if env.OK {
		t.Fatalf("rm of a running session must fail: %+v", env)
	}
	if code := string(env.Errors[0].Code); code != string(jsoncontract.CodeSessionAlreadyRunning) {
		t.Fatalf("rm error code = %q, want SESSION_ALREADY_RUNNING", code)
	}
	if _, err := sessionStore().Get(id); err != nil {
		t.Fatalf("running record wrongly deleted: %v", err)
	}
}

func TestSessionRemoveNotFound(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	out, _ := runRootForTest(t, ws, "session", "rm", "--session-id", "sess-missing", "--output", "json")
	env := parseEnvelopeForTest(t, out)
	if env.OK || string(env.Errors[0].Code) != string(jsoncontract.CodeSessionNotFound) {
		t.Fatalf("rm of missing session = %+v, want SESSION_NOT_FOUND", env)
	}
}

func TestSessionRemoveWipesStageDir(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().Finish(id, 1, "boom", nowForTest(t)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stageDir := seedSessionStageDirForTest(t, sess)

	out, err = runRootForTest(t, ws, "session", "rm", "--session-id", id, "--output", "json")
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	assertStageDirRemovedForTest(t, stageDir)
}

// TestSessionPruneRemovesStoppedIncludingCrashed proves `session prune` clears
// every stopped session in one call — including a crashed session (still marked
// running but whose process is gone) via the reconcile pass — while leaving
// created and live-running sessions untouched.
func TestSessionPruneRemovesStoppedIncludingCrashed(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	oldAlive := sessionProcessAlive
	// A recorded PID of 4242 is our crashed session; everything else is "alive".
	sessionProcessAlive = func(sess engsession.Session) bool { return sess.PID != 4242 }
	defer func() { sessionProcessAlive = oldAlive }()

	mk := func(agent string) string {
		out, err := runRootForTest(t, ws, "session", "create", "--agent", agent, "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
		if err != nil {
			t.Fatalf("create %s: %v\n%s", agent, err, out)
		}
		return parseEnvelopeForTest(t, out).Data["session_id"].(string)
	}
	stopped := mk("claude")
	crashed := mk("pi")
	created := mk("fish")
	if _, err := sessionStore().Finish(stopped, 0, "", nowForTest(t)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := sessionStore().MarkRunning(crashed, 4242, nowForTest(t)); err != nil {
		t.Fatalf("mark running crashed: %v", err)
	}

	out, err := runRootForTest(t, ws, "session", "prune", "--output", "json")
	if err != nil {
		t.Fatalf("prune: %v\n%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("prune not ok: %+v", env)
	}
	removed, _ := env.Data["removed"].([]any)
	got := map[string]bool{}
	for _, r := range removed {
		got[r.(string)] = true
	}
	if !got[stopped] || !got[crashed] || got[created] {
		t.Fatalf("prune removed = %v, want {stopped,crashed} not created", env.Data["removed"])
	}
	if _, err := sessionStore().Get(created); err != nil {
		t.Fatalf("created session wrongly pruned: %v", err)
	}
}

func TestSessionPruneWipesStageDir(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	id := parseEnvelopeForTest(t, out).Data["session_id"].(string)
	if _, err := sessionStore().Finish(id, 1, "boom", nowForTest(t)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	sess, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stageDir := seedSessionStageDirForTest(t, sess)

	out, err = runRootForTest(t, ws, "session", "prune", "--output", "json")
	if err != nil {
		t.Fatalf("prune: %v\n%s", err, out)
	}
	assertStageDirRemovedForTest(t, stageDir)
}

func TestSessionRmAndPruneRegistered(t *testing.T) {
	sessionCmd := cmdSession()
	names := map[string]bool{}
	for _, c := range sessionCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"rm", "rename", "prune"} {
		if !names[want] {
			t.Fatalf("session %q subcommand not registered; have %v", want, names)
		}
	}
}

// createSessionForRename creates a minimal host session and returns its id, so
// the rename tests can exercise the CLI surface without reaching into the store.
func createSessionForRename(t *testing.T, ws string) string {
	t.Helper()
	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--output", "json")
	if err != nil {
		t.Fatalf("create session: %v\nout=%s", err, out)
	}
	return parseEnvelopeForTest(t, out).Data["session_id"].(string)
}

// TestSessionCreateAppliesName proves `session create --name` validates and
// persists the display name and surfaces it in the OK envelope (specs/0065 S2).
func TestSessionCreateAppliesName(t *testing.T) {
	ws := t.TempDir()
	state := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", state)

	out, err := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--name", "Foo", "--output", "json")
	if err != nil {
		t.Fatalf("session create --name: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["name"]; got != "Foo" {
		t.Fatalf("name = %#v, want Foo", got)
	}
	id := env.Data["session_id"].(string)
	stored, err := sessionStore().Get(id)
	if err != nil {
		t.Fatalf("load stored session: %v", err)
	}
	if stored.Name != "Foo" {
		t.Fatalf("stored name = %q, want Foo", stored.Name)
	}
}

// TestSessionCreateRejectsControlName proves a name carrying a control character
// (a newline, which would also break the one-envelope-per-line protocol) is
// rejected with CodeInvalidArgument before any record is written.
func TestSessionCreateRejectsControlName(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())

	out, _ := runRootForTest(t, ws, "session", "create", "--agent", "claude", "--environment", "host", "--trust-host", "--workspace", ws, "--name", "a\nb", "--output", "json")
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 || env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument error envelope, got: %+v", env)
	}
}

// TestSessionCreateProfileWithNameNotRejected proves --name is combinable with
// --profile (it is not part of the profile-exclusivity guard) and is applied.
func TestSessionCreateProfileWithNameNotRejected(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	project := filepath.Join(ws, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("mkdir project workspace: %v", err)
	}
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {
			agent: "pi"
			environment: "container"
			network: "deny"
			workspace: "project"
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatalf("write safeslop.cue: %v", err)
	}

	trustFixtureForTest(t, ws)
	out, err := runRootForTest(t, ws, "session", "create", "--profile", "review", "--name", "Foo", "--output", "json")
	if err != nil {
		t.Fatalf("session create --profile --name: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("create returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["profile"]; got != "review" {
		t.Fatalf("profile = %#v, want review", got)
	}
	if got := env.Data["name"]; got != "Foo" {
		t.Fatalf("name = %#v, want Foo", got)
	}
}

// TestSessionRenameSetsName proves the happy path: rename returns an OK envelope
// carrying the new name.
func TestSessionRenameSetsName(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	id := createSessionForRename(t, ws)

	out, err := runRootForTest(t, ws, "session", "rename", "--session-id", id, "--name", "Renamed", "--output", "json")
	if err != nil {
		t.Fatalf("session rename: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("rename returned error envelope: %+v", env.Errors)
	}
	if got := env.Data["name"]; got != "Renamed" {
		t.Fatalf("name = %#v, want Renamed", got)
	}
	if got := env.Data["session_id"]; got != id {
		t.Fatalf("session_id = %#v, want %q", got, id)
	}
}

// TestSessionRenameClearsName proves an empty --name clears the label: the OK
// envelope omits "name" (sessionData surfaces it only when non-empty).
func TestSessionRenameClearsName(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	id := createSessionForRename(t, ws)
	if _, err := runRootForTest(t, ws, "session", "rename", "--session-id", id, "--name", "Temp", "--output", "json"); err != nil {
		t.Fatalf("seed name: %v", err)
	}

	out, err := runRootForTest(t, ws, "session", "rename", "--session-id", id, "--name", "", "--output", "json")
	if err != nil {
		t.Fatalf("session rename clear: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("rename clear returned error envelope: %+v", env.Errors)
	}
	if _, present := env.Data["name"]; present {
		t.Fatalf("name should be absent after clear, got: %#v", env.Data["name"])
	}
}

// TestSessionRenameNotFound proves an unknown id maps to SESSION_NOT_FOUND.
func TestSessionRenameNotFound(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	out, _ := runRootForTest(t, ws, "session", "rename", "--session-id", "sess-missing", "--name", "x", "--output", "json")
	env := parseEnvelopeForTest(t, out)
	if env.OK || string(env.Errors[0].Code) != string(jsoncontract.CodeSessionNotFound) {
		t.Fatalf("rename of missing session = %+v, want SESSION_NOT_FOUND", env)
	}
}

// TestSessionRenameRejectsControlName proves a name with a disallowed bidi
// override is rejected with CodeInvalidArgument. U+202E (RLO) is the
// Trojan-Source character that could make a stopped session render as running.
func TestSessionRenameRejectsControlName(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	id := createSessionForRename(t, ws)

	out, _ := runRootForTest(t, ws, "session", "rename", "--session-id", id, "--name", "a\u202eb", "--output", "json")
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 || env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument error envelope, got: %+v", env)
	}
}

// TestSessionRenameRequiresOutputJSON proves rename without --output json is a
// usage error (matching its sibling session commands), not a contract envelope.
func TestSessionRenameRequiresOutputJSON(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	id := createSessionForRename(t, ws)

	out, err := runRootForTest(t, ws, "session", "rename", "--session-id", id, "--name", "x")
	if err == nil {
		t.Fatalf("rename without --output json must be a usage error; out=%s", out)
	}
}
