package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestLaunchRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Launch(context.Background(), exec.LaunchSpec{Argv: []string{"fish"}}, t.TempDir(), "deny", nil, nil, t.TempDir(), nil)
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
	if _, err := materializeRun(composeParams{RuntimeDir: piDir, StageDir: piDir, Workspace: "/", Egress: policy.AgentEgress("pi")}, false); err != nil {
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
	if _, err := materializeRun(composeParams{RuntimeDir: clDir, StageDir: clDir, Workspace: "/", Egress: policy.AgentEgress("claude")}, false); err != nil {
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
