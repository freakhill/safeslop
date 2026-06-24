package policy

import (
	"strings"
	"testing"
)

func egHas(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestAgentEgressPiHasProvidersOthersNil(t *testing.T) {
	pi := AgentEgress("pi")
	if len(pi) == 0 {
		t.Fatal("pi must carry a built-in provider allowlist")
	}
	if !egHas(pi, ".pi.dev") {
		t.Errorf("pi egress must include .pi.dev, got %v", pi)
	}
	// anthropic + openrouter live in the shared base, NOT the per-agent set (specs/0046).
	for _, b := range []string{".anthropic.com", ".openrouter.ai"} {
		if egHas(pi, b) {
			t.Errorf("%s belongs to the shared base allowlist, not pi's per-agent set", b)
		}
	}
	// OpenAI/xAI are a privacy hard line — never in any agent's set.
	joined := strings.Join(pi, " ")
	for _, banned := range []string{"openai", "x.ai"} {
		if strings.Contains(joined, banned) {
			t.Errorf("banned provider %q must never appear: %v", banned, pi)
		}
	}
	// claude/opencode/shell rely on the shared base — no per-agent extras.
	for _, a := range []string{"claude", "opencode", "shell", ""} {
		if AgentEgress(a) != nil {
			t.Errorf("AgentEgress(%q) must be nil (relies on shared base), got %v", a, AgentEgress(a))
		}
	}
}
