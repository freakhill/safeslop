package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestSeedAgentDefaultsWritesNonClobberingFixtures(t *testing.T) {
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

	if err := os.WriteFile(claudePath, []byte(`{"custom":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedAgentDefaults(policy.Profile{Agent: "claude"}, ws); err != nil {
		t.Fatalf("seed claude second time: %v", err)
	}
	if got, _ := os.ReadFile(claudePath); string(got) != `{"custom":true}` {
		t.Fatalf("seed must not overwrite existing settings, got %s", got)
	}

	if err := seedAgentDefaults(policy.Profile{Agent: "opencode"}, ws); err != nil {
		t.Fatalf("seed opencode: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "opencode.json")); err != nil || !strings.Contains(string(b), `"permission"`) {
		t.Fatalf("opencode config not seeded from fixture: %v %s", err, b)
	}
}

func TestSeedAgentDefaultsSkipsNonAgentSettings(t *testing.T) {
	ws := t.TempDir()
	if err := seedAgentDefaults(policy.Profile{Agent: "shell"}, ws); err != nil {
		t.Fatalf("shell seed should be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("shell profile must not seed claude settings: %v", err)
	}
}
