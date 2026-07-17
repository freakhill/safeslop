package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func acceptHostConsentForTest(t *testing.T, d *dependencies) {
	t.Helper()
	d.hostLaunchConsent = func(string, policy.Profile, io.Reader, io.Writer) error { return nil }
}

func TestHostLaunchConsentAcceptsMatchedAnswers(t *testing.T) {
	stmts := []policy.ConsentStatement{
		{Text: "This agent can read and write every file your account can.", Expected: true, TierOrigin: "host"},
		{Text: "Files outside the project are invisible to this agent.", Expected: false, TierOrigin: "container"},
	}
	var out bytes.Buffer

	err := confirmHostLaunchConsentRows("dev", policy.Profile{Environment: "host"}, []string{"/Volumes/Data"}, stmts, strings.NewReader("yes\nno\n"), &out)
	if err != nil {
		t.Fatalf("matched consent answers were rejected: %v\nout=%s", err, out.String())
	}
	for _, want := range []string{"no isolation", "1 other mounted volume", "full host network", "host launch consent passed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("consent output missing %q:\n%s", want, out.String())
		}
	}
}

func TestHostLaunchConsentRejectsWrongAnswer(t *testing.T) {
	stmts := []policy.ConsentStatement{
		{Text: "This agent can use your logged-in credentials.", Expected: true, TierOrigin: "host"},
		{Text: "Network access is limited to an approved allow-list.", Expected: false, TierOrigin: "container"},
	}
	var out bytes.Buffer

	err := confirmHostLaunchConsentRows("dev", policy.Profile{Environment: "host"}, nil, stmts, strings.NewReader("yes\nyes\n"), &out)
	if err == nil {
		t.Fatalf("wrong consent answer unexpectedly passed; out=%s", out.String())
	}
	if !strings.Contains(err.Error(), "host launch consent failed") {
		t.Fatalf("wrong-answer error = %v, want host launch consent failure", err)
	}
}

func TestCmdRunHostInvokesConsentGate(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	writeConsentGatePolicy(t, ws)

	consentErr := errors.New("consent sentinel")
	calls := 0
	d := defaultDependencies()
	d.hostLaunchConsent = func(name string, prof policy.Profile, in io.Reader, out io.Writer) error {
		calls++
		if name != "dev" || prof.Environment != "host" {
			t.Fatalf("gate called with name=%q environment=%q, want dev/host", name, prof.Environment)
		}
		return consentErr
	}

	_, err := runRootForTestWithDeps(t, ws, d, "run", "--trust", "dev")
	if !errors.Is(err, consentErr) {
		t.Fatalf("run error = %v, want consent sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("host consent gate calls = %d, want 1", calls)
	}
}

func TestSessionRunHostInvokesConsentGate(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("fish", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	d := defaultDependencies()
	d.store = store
	d.hasInteractivePTY = func() bool { return true }
	consentErr := errors.New("consent sentinel")
	calls := 0
	d.hostLaunchConsent = func(name string, prof policy.Profile, in io.Reader, out io.Writer) error {
		calls++
		if name != "session-"+sess.ID || prof.Environment != "host" {
			t.Fatalf("gate called with name=%q environment=%q, want session id/host", name, prof.Environment)
		}
		return consentErr
	}

	_, err = runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID)
	if !errors.Is(err, consentErr) {
		t.Fatalf("session run error = %v, want consent sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("host consent gate calls = %d, want 1", calls)
	}
}

func TestSessionRunHostDetachInvokesConsentBeforeSupervisor(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("fish", "host", ws, nowForTest(t))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	d := defaultDependencies()
	d.store = store
	consentErr := errors.New("consent sentinel")
	calls := 0
	d.hostLaunchConsent = func(name string, prof policy.Profile, in io.Reader, out io.Writer) error {
		calls++
		return consentErr
	}

	supervisorCalls := 0
	d.launchSupervisor = func(id string) (launchedSupervisor, error) {
		supervisorCalls++
		return launchedSupervisor{}, errors.New("supervisor must not launch before consent passes")
	}

	_, err = runRootForTestWithDeps(t, ws, d, "session", "run", "--session-id", sess.ID, "--detach")
	if !errors.Is(err, consentErr) {
		t.Fatalf("session run --detach error = %v, want consent sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("host consent gate calls = %d, want 1", calls)
	}
	if supervisorCalls != 0 {
		t.Fatalf("supervisor launched before consent passed (%d calls)", supervisorCalls)
	}
}

func writeConsentGatePolicy(t *testing.T, ws string) {
	t.Helper()
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		dev: {
			agent: "fish"
			environment: "host"
			network: "deny"
			workspace: "."
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatalf("write safeslop.cue: %v", err)
	}
}
