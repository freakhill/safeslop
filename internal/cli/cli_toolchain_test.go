package cli

import (
	"testing"

	"github.com/freakhill/agentic_tactical_boots/internal/engine/toolchain"
)

// Guards the contract that cmdRun applies before the environment switch: a mise toolchain
// rewrites the launch argv every environment (and the dry-run) sees.
func TestToolchainWrapApplied(t *testing.T) {
	got := toolchain.Wrap("mise", "", []string{"claude"})
	if len(got) == 0 || got[0] != "mise" || got[len(got)-1] != "claude" {
		t.Fatalf("toolchain wrap not applied: %v", got)
	}
}
