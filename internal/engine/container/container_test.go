package container

import (
	"errors"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
)

func TestEmbeddedAssetsPresent(t *testing.T) {
	for _, name := range []string{"allowlist.domains", "Dockerfile.agent", "Dockerfile.agent.tools", "agent-tools.env"} {
		if b, err := readAsset(name); err != nil || len(b) == 0 {
			t.Fatalf("asset %q missing or empty: %v", name, err)
		}
	}
}

func TestAvailableFalseWithoutDocker(t *testing.T) {
	orig := detectRuntime
	t.Cleanup(func() { detectRuntime = orig })
	detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		return nil, errors.New("runtime unavailable")
	}
	if Available() {
		t.Fatal("Available must be false when docker is not on PATH")
	}
}
