package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestProfileShowEnvelopeIncludesResolvedRecipe(t *testing.T) {
	dir := t.TempDir()
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {agent: "claude", environment: "container", network: "deny", packages: ["pnpm"]}
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, dir, "profile", "show", "review", "--output", "json")
	if err != nil {
		t.Fatalf("profile show --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile show returned error envelope: %+v", env.Errors)
	}
	profile, ok := env.Data["profile"].(map[string]any)
	if !ok || profile["agent"] != "claude" || profile["environment"] != "container" {
		t.Fatalf("profile data wrong: %#v", env.Data["profile"])
	}
	resolved, ok := env.Data["resolved"].(map[string]any)
	if !ok {
		t.Fatalf("resolved data missing: %#v", env.Data)
	}
	ids, ok := resolved["identitySet"].([]any)
	if !ok {
		t.Fatalf("resolved.identitySet malformed: %#v", resolved)
	}
	for _, want := range []string{"claude-code", "node", "pnpm"} {
		if !stringSliceAnyContains(ids, want) {
			t.Fatalf("resolved identity missing %q: %#v", want, ids)
		}
	}
	if recipeID, _ := env.Data["recipeID"].(string); len(recipeID) != 12 {
		t.Fatalf("recipeID = %q, want 12 hex chars", recipeID)
	}
	if image, _ := env.Data["image"].(string); !strings.HasPrefix(image, "local/safeslop-tools:") {
		t.Fatalf("image = %q, want local/safeslop-tools tag", image)
	}
	if base, _ := env.Data["base"].(string); !strings.HasPrefix(base, "debian:bookworm-slim@sha256:") {
		t.Fatalf("base = %q, want pinned debian source", base)
	}
}

func TestProfileCreateWritesNewCue(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--name", "review",
		"--agent", "fish",
		"--environment", "container",
		"--bundle", "pi",
		"--package", "pnpm",
		"--workspace", ".",
		"--network", "deny",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile create returned error envelope: %+v", env.Errors)
	}
	if env.Data["name"] != "review" {
		t.Fatalf("name = %#v", env.Data["name"])
	}
	cfg, err := policy.Load(filepath.Join(dir, "safeslop.cue"))
	if err != nil {
		t.Fatalf("created safeslop.cue should validate: %v", err)
	}
	p := cfg.Profiles["review"]
	if p.Agent != "fish" || p.Environment != "container" || p.Network != "deny" || p.Workspace != "." {
		t.Fatalf("created profile fields wrong: %+v", p)
	}
	if len(p.Bundles) != 1 || p.Bundles[0] != "pi" || len(p.Packages) != 1 || p.Packages[0] != "pnpm" {
		t.Fatalf("created package selectors wrong: bundles=%v packages=%v", p.Bundles, p.Packages)
	}
	resolved, ok := env.Data["resolved"].(map[string]any)
	if !ok || !stringSliceAnyContains(resolved["identitySet"].([]any), "pi") || !stringSliceAnyContains(resolved["identitySet"].([]any), "pnpm") {
		t.Fatalf("resolved output wrong: %#v", env.Data["resolved"])
	}
}

func TestProfileCreateNoDefaultBundleWritesBareAgent(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--name", "bare",
		"--agent", "claude",
		"--environment", "container",
		"--no-default-bundle",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create --no-default-bundle: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile create returned error envelope: %+v", env.Errors)
	}
	cfg, err := policy.Load(filepath.Join(dir, "safeslop.cue"))
	if err != nil {
		t.Fatalf("created safeslop.cue should validate: %v", err)
	}
	if !cfg.Profiles["bare"].BareAgent {
		t.Fatalf("BareAgent was not persisted: %+v", cfg.Profiles["bare"])
	}
	resolved := env.Data["resolved"].(map[string]any)
	ids, _ := resolved["identitySet"].([]any)
	if len(ids) != 0 {
		t.Fatalf("bare claude profile resolved default packages despite opt-out: %#v", ids)
	}
}

func TestProfileCreateRequiresOutputJSON(t *testing.T) {
	if _, err := runRootForTest(t, t.TempDir(), "profile", "create", "--name", "x", "--agent", "fish", "--environment", "host"); err == nil {
		t.Fatal("profile create without --output json should error")
	}
}

func stringSliceAnyContains(values []any, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
