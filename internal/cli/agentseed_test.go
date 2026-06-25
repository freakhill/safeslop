package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestAgentSeedAcceptsClaudeCode(t *testing.T) {
	ws := t.TempDir()
	if err := seedAgentDefaults(policy.Profile{Agent: "claude"}, ws); err != nil {
		t.Fatalf("seed claude: %v", err)
	}
	claudePath := filepath.Join(ws, ".claude", "settings.json")
	b, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("claude settings not seeded: %v", err)
	}
	if !strings.Contains(string(b), `"sandbox"`) {
		t.Fatalf("seeded claude settings do not look like the bundled fixture: %s", b)
	}
}

func TestAgentSeedClaudeCodeIsNonClobbering(t *testing.T) {
	ws := t.TempDir()
	claudePath := filepath.Join(ws, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(`{"custom":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedAgentDefaults(policy.Profile{Agent: "claude"}, ws); err != nil {
		t.Fatalf("seed claude with existing settings: %v", err)
	}
	if got, err := os.ReadFile(claudePath); err != nil || string(got) != `{"custom":true}` {
		t.Fatalf("seed must not overwrite existing settings, got %q err=%v", got, err)
	}
}

func TestAgentSeedAcceptsPiAsNoop(t *testing.T) {
	ws := t.TempDir()
	if err := seedAgentDefaults(policy.Profile{Agent: "pi"}, ws); err != nil {
		t.Fatalf("pi seed should be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("pi profile must not seed claude settings: %v", err)
	}
}

func TestAgentSeedRejectsOpenCode(t *testing.T) {
	ws := t.TempDir()
	err := seedAgentDefaults(policy.Profile{Agent: "opencode"}, ws)
	if err == nil || !strings.Contains(err.Error(), "unsupported agent") {
		t.Fatalf("seed opencode error = %v, want unsupported agent", err)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "opencode.json")); !os.IsNotExist(statErr) {
		t.Fatalf("opencode fixture must not be written: %v", statErr)
	}
}

func TestAgentSeedRejectsVSCode(t *testing.T) {
	ws := t.TempDir()
	err := seedAgentDefaults(policy.Profile{Agent: "vscode"}, ws)
	if err == nil || !strings.Contains(err.Error(), "unsupported agent") {
		t.Fatalf("seed vscode error = %v, want unsupported agent", err)
	}
}

func TestAgentSeedDoesNotEmbedOpenCodeFixture(t *testing.T) {
	if _, err := agentFixtureFS.ReadFile("agentfixtures/opencode.json"); err == nil {
		t.Fatal("opencode fixture must not be embedded")
	}
}

func TestAgentSeedSkipsShellSettings(t *testing.T) {
	ws := t.TempDir()
	if err := seedAgentDefaults(policy.Profile{Agent: "shell"}, ws); err != nil {
		t.Fatalf("shell seed should be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("shell profile must not seed claude settings: %v", err)
	}
}
