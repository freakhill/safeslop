package container

import (
	"strings"
	"testing"
)

func TestToolsImageHasMise(t *testing.T) {
	b, err := readAsset("Dockerfile.agent.tools")
	if err != nil || !strings.Contains(string(b), "mise") {
		t.Fatalf("tools image missing mise: %v", err)
	}
}
