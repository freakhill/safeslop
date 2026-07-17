package container

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/policy"
)

// TestContainerImages is the opt-in CI image gate behind make
// test-container-images. Normal unit suites stay network-free.
func TestContainerImages(t *testing.T) {
	if os.Getenv("SAFESLOP_TEST_CONTAINER_IMAGES") != "1" {
		t.Skip("set SAFESLOP_TEST_CONTAINER_IMAGES=1 to build locked images")
	}
	engine, err := runtimepkg.Detect(runtimepkg.PolicyAllow)
	if err != nil {
		t.Fatalf("detect container runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Minute)
	defer cancel()
	catalog := policy.DefaultCatalog()
	cases := []struct {
		name       string
		enabled    []string
		binary     string
		versionFor string
	}{
		{name: "shell", binary: "fish"},
		{name: "claude", enabled: []string{"claude-code", "node"}, binary: "claude", versionFor: "claude-code"},
		{name: "pi", enabled: []string{"node", "pi"}, binary: "pi", versionFor: "pi"},
		{name: "pnpm", enabled: []string{"node", "pnpm"}, binary: "pnpm", versionFor: "pnpm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := buildImages(ctx, engine, tc.enabled); err != nil {
				t.Fatalf("build locked image: %v", err)
			}
			recipe, err := ResolveRecipe(tc.enabled)
			if err != nil {
				t.Fatal(err)
			}
			output, err := engine.Command(ctx, "run", "--rm", "--entrypoint", tc.binary, recipe.AgentImage, "--version").Output()
			if err != nil {
				t.Fatalf("run %s --version: %v", tc.binary, err)
			}
			if len(strings.TrimSpace(string(output))) == 0 {
				t.Fatalf("%s produced no version", tc.binary)
			}
			if tc.versionFor != "" {
				pkg, ok := catalog.Lookup(tc.versionFor)
				if !ok || !strings.Contains(string(output), pkg.Version) {
					t.Fatalf("%s version output %q does not contain catalog pin", tc.binary, output)
				}
			}
		})
	}
}
