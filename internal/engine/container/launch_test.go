package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestLaunchRejectsWhenUnavailable(t *testing.T) {
	orig := detectRuntime
	t.Cleanup(func() { detectRuntime = orig })
	detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		return nil, errors.New("runtime unavailable")
	}
	_, err := Launch(context.Background(), exec.LaunchSpec{Argv: []string{"fish"}}, t.TempDir(), "deny", nil, nil, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error when docker unavailable")
	}
}

// TestComposeAllowlistUnionsAndDedupes locks specs/0046: the per-run allowlist is base ∪ extra,
// order-preserving, de-duplicated, with empty/whitespace entries dropped; nil extra == base.
func TestComposeAllowlistUnionsAndDedupes(t *testing.T) {
	base := []byte(".github.com\n.anthropic.com\n")
	out := string(composeAllowlist(base, []string{".pi.dev", ".anthropic.com", "  ", ".deepseek.com"}))
	for _, want := range []string{".github.com", ".anthropic.com", ".pi.dev", ".deepseek.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("composed allowlist missing %q:\n%s", want, out)
		}
	}
	if c := strings.Count(out, ".anthropic.com"); c != 1 {
		t.Errorf(".anthropic.com must appear once (dedupe), got %d:\n%s", c, out)
	}
	if strings.Contains(out, "  ") {
		t.Errorf("whitespace entry must be dropped:\n%q", out)
	}
	if got := strings.TrimSpace(string(composeAllowlist(base, nil))); got != strings.TrimSpace(string(base)) {
		t.Errorf("nil extra must equal base, got %q", got)
	}
}

// TestMaterializeRunScopesEgressPerAgent is the end-to-end proof (specs/0046, no engine needed):
// a pi run's materialized allowlist carries pi's providers + the base; a claude run's does NOT
// reach pi's providers (only the shared base).
func TestMaterializeRunScopesEgressPerAgent(t *testing.T) {
	piDir := t.TempDir()
	if _, err := materializeRun(composeParams{RuntimeDir: piDir, StageDir: piDir, Workspace: t.TempDir(), Egress: policy.AgentEgress("pi")}, false); err != nil {
		t.Fatal(err)
	}
	pi, err := os.ReadFile(filepath.Join(piDir, "allowlist.domains"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".deepseek.com", ".pi.dev", ".anthropic.com"} {
		if !strings.Contains(string(pi), want) {
			t.Errorf("pi run allowlist must carry %q (pi providers + base):\n%s", want, pi)
		}
	}

	clDir := t.TempDir()
	if _, err := materializeRun(composeParams{RuntimeDir: clDir, StageDir: clDir, Workspace: t.TempDir(), Egress: policy.AgentEgress("claude")}, false); err != nil {
		t.Fatal(err)
	}
	cl, err := os.ReadFile(filepath.Join(clDir, "allowlist.domains"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cl), ".deepseek.com") {
		t.Errorf("claude run must NOT reach pi's providers (per-agent scoping broken):\n%s", cl)
	}
	if !strings.Contains(string(cl), ".anthropic.com") {
		t.Errorf("claude run must still carry the shared base providers:\n%s", cl)
	}
}

// TestProvisionThreadsProjectionIntoCompose pins the descriptor-snapshot launch contract: compose
// mounts a private stage snapshot, never the live source under $HOME. No docker is invoked.
func TestMaterializeRunPiOAuthSentinelStaysInAuthFile(t *testing.T) {
	stage := t.TempDir()
	providerDir := filepath.Join(stage, "pi", "openai-codex")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const access = "ACCESS_SENTINEL_ONLY_IN_AUTH"
	authPath := filepath.Join(providerDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openai-codex":{"type":"api_key","key":"`+access+`"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeRun(composeParams{RuntimeDir: stage, StageDir: stage, Workspace: t.TempDir()}, false); err != nil {
		t.Fatal(err)
	}
	forbidden := []string{access, "REFRESH_SENTINEL", "OTHER_PROVIDER_SENTINEL", "/home/private/.pi/agent/auth.json"}
	if err := filepath.Walk(stage, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || path == authPath {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, sentinel := range forbidden {
			if strings.Contains(string(body), sentinel) {
				t.Errorf("Pi OAuth sentinel %q leaked into %s", sentinel, filepath.Base(path))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionThreadsProjectionIntoCompose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# zsh"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := detectRuntime
	t.Cleanup(func() { detectRuntime = orig })
	detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		return newFakeEngine(t, nil), nil
	}
	stageDir, ws := t.TempDir(), t.TempDir()
	proj := &policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: "~/.zshrc", Label: "zsh"}}}
	_, composeFile, _, err := provision(context.Background(), "sess-test", []string{"fish"}, ws, "deny", nil, nil, stageDir, nil, proj)
	if err != nil {
		t.Fatalf("provision with projection failed: %v", err)
	}
	yml, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(stageDir, "projection-snapshots", "000000")
	canonicalSnapshot, err := filepath.EvalSymlinks(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yml), `source: "`+canonicalSnapshot+`"`) || !strings.Contains(string(yml), `target: "/safeslop/projected/0"`) {
		t.Errorf("provision must mount the private snapshot read-only:\n%s", yml)
	}
	if strings.Contains(string(yml), filepath.Join(home, ".zshrc")) {
		t.Errorf("provision mounted the live host source:\n%s", yml)
	}
	if got, err := os.ReadFile(snapshot); err != nil || string(got) != "# zsh" {
		t.Errorf("snapshot bytes = %q, err=%v", got, err)
	}
	if _, err := os.ReadFile(filepath.Join(stageDir, "projection.json")); err != nil {
		t.Errorf("provision must write projection.json: %v", err)
	}
	tsv, err := os.ReadFile(filepath.Join(stageDir, "projection.tsv"))
	if err != nil {
		t.Errorf("provision must write projection.tsv: %v", err)
	} else if !strings.Contains(string(tsv), "/safeslop/projected/0\t/home/agent/.zshrc") {
		t.Errorf("projection.tsv must map the staging path to the home target:\n%s", tsv)
	}
}

// TestProvisionFailsClosedOnProjectionLawViolation pins specs/0096: a resolver-law violation
// (e.g. a credential-dir source) fails closed at provision time — the agent never launches with
// a half-resolved/illegal projection.
func TestProvisionFailsClosedOnProjectionLawViolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("x"), 0o644)
	orig := detectRuntime
	t.Cleanup(func() { detectRuntime = orig })
	detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		return newFakeEngine(t, nil), nil
	}
	proj := &policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: "~/.ssh/config"}}}
	_, _, _, err := provision(context.Background(), "sess-test", []string{"fish"}, t.TempDir(), "deny", nil, nil, t.TempDir(), nil, proj)
	if err == nil || !strings.Contains(err.Error(), "resolve host projection") {
		t.Fatalf("provision must fail closed on a credential-dir projection source, got: %v", err)
	}
}
