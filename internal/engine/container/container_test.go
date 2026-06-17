package container

import "testing"

func TestEmbeddedAssetsPresent(t *testing.T) {
	for _, name := range []string{"allowlist.domains", "Dockerfile.agent", "Dockerfile.agent.tools", "agent-tools.env"} {
		if b, err := readAsset(name); err != nil || len(b) == 0 {
			t.Fatalf("asset %q missing or empty: %v", name, err)
		}
	}
}

func TestAvailableFalseWithoutDocker(t *testing.T) {
	t.Setenv("PATH", "")
	if Available() {
		t.Fatal("Available must be false when docker is not on PATH")
	}
}
