package container

import (
	"context"
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

func TestUpRequiresReadyProxy(t *testing.T) {
	eng := newFakeEngine(t, nil)
	composeFile := "/runtime/compose.yml"
	check := "compose -f " + composeFile + " exec -T proxy squid -k check"
	eng.fail(check, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	eng.runHook(check, cancel)
	err := Up(ctx, eng, t.TempDir(), composeFile, nil)
	if err == nil {
		t.Fatal("Up succeeded even though the proxy readiness check failed")
	}
	eng.assertRan(t, "compose -f "+composeFile+" up -d proxy")
	eng.assertRan(t, check)
	eng.assertRan(t, "compose -f "+composeFile+" down --remove-orphans")
}

func TestUpChecksReadyProxyBeforeSuccess(t *testing.T) {
	eng := newFakeEngine(t, nil)
	composeFile := "/runtime/compose.yml"

	if err := Up(t.Context(), eng, t.TempDir(), composeFile, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	eng.assertRan(t, "compose -f "+composeFile+" exec -T proxy squid -k check")
}
