package cli

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// The JSON fixtures are the Go-era replacement for `slop-agents seed`: profile launch seeds the
// same non-clobbering Claude/OpenCode project defaults without depending on fish, Python, or cue.
//
//go:embed agentfixtures/claude-code.settings.json agentfixtures/opencode.json
var agentFixtureFS embed.FS

func seedAgentDefaults(prof policy.Profile, ws string) error {
	switch prof.Agent {
	case "claude":
		return seedFixture(filepath.Join(ws, ".claude", "settings.json"), "agentfixtures/claude-code.settings.json")
	case "opencode":
		return seedFixture(filepath.Join(ws, "opencode.json"), "agentfixtures/opencode.json")
	default:
		return nil
	}
}

func seedFixture(target, fixture string) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := agentFixtureFS.ReadFile(fixture)
	if err != nil {
		return fmt.Errorf("read bundled agent fixture %s: %w", fixture, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, b, 0o600)
}
