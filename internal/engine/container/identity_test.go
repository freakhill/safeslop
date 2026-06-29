package container

import (
	"strings"
	"testing"
)

func TestRecipeIDDeterministicAndSensitive(t *testing.T) {
	df := []byte("FROM base\nRUN x")
	a := recipeID(df, map[string]string{"K": "1", "Z": "2"})
	if b := recipeID(df, map[string]string{"Z": "2", "K": "1"}); a != b {
		t.Fatalf("recipeID must be build-arg-order-independent: %s vs %s", a, b)
	}
	if len(a) != 12 {
		t.Fatalf("recipeID must be 12 hex chars, got %q (len %d)", a, len(a))
	}
	if recipeID(df, map[string]string{"K": "1"}) == a {
		t.Fatal("changing the build-arg set must change the id")
	}
	if recipeID([]byte("FROM other"), nil) == recipeID([]byte("FROM base"), nil) {
		t.Fatal("changing the Dockerfile must change the id")
	}
}

func TestAgentImageTagsAreContentAddressedNotLatest(t *testing.T) {
	// A claude profile resolves to {node, claude-code} (sorted identity set).
	enabled := []string{"claude-code", "node"}
	baseImg, toolsImg, toolsArgs, err := agentImageTags(enabled)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(baseImg, "local/safeslop-base:") {
		t.Fatalf("base image = %q, want local/safeslop-base:<id>", baseImg)
	}
	if !strings.HasPrefix(toolsImg, "local/safeslop-tools:") {
		t.Fatalf("tools image = %q, want local/safeslop-tools:<id>", toolsImg)
	}
	if strings.Contains(baseImg, "latest") || strings.Contains(toolsImg, "latest") {
		t.Fatalf("id-tags must not be :latest (Bug B): base=%q tools=%q", baseImg, toolsImg)
	}
	joined := strings.Join(toolsArgs, " ")
	// BASE threads in; each enabled package emits ENABLE_<PKG>=true + its pinned version;
	// node (binary) also emits per-arch digests. No hardcoded ENABLE_PI here (specs/0058 N1).
	for _, want := range []string{
		"BASE=" + baseImg,
		"ENABLE_NODE=true", "NODE_VERSION=22.23.1",
		"NODE_SHA256_AMD64=", "NODE_SHA256_ARM64=",
		"ENABLE_CLAUDE_CODE=true", "CLAUDE_CODE_VERSION=2.1.121",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tools build-args missing %q: %v", want, toolsArgs)
		}
	}
	if strings.Contains(joined, "ENABLE_PI=true") {
		t.Fatalf("a claude profile must NOT enable pi (no hardcoded toggles): %v", toolsArgs)
	}
	if b2, t2, _, _ := agentImageTags(enabled); b2 != baseImg || t2 != toolsImg {
		t.Fatal("agentImageTags must be deterministic")
	}
}

// The agent image tag is SENSITIVE to the resolved package set: a different set yields a
// different tag (so two profiles with different tools never collide on one image), and
// the set is ORDER-INDEPENDENT (it is the sorted identity set).
func TestAgentImageTagsSensitiveToPackageSet(t *testing.T) {
	_, claudeImg, _, err := agentImageTags([]string{"claude-code", "node"})
	if err != nil {
		t.Fatal(err)
	}
	_, piImg, _, err := agentImageTags([]string{"node", "pi"})
	if err != nil {
		t.Fatal(err)
	}
	if claudeImg == piImg {
		t.Fatalf("different package sets must yield different agent tags: %q == %q", claudeImg, piImg)
	}
	_, a, _, _ := agentImageTags([]string{"node", "claude-code"})
	_, b, _, _ := agentImageTags([]string{"claude-code", "node"})
	if a != b {
		t.Fatalf("agent tag must be order-independent over the package set: %q != %q", a, b)
	}
	// shell-only (empty set) differs from any tooled set and still pins BASE.
	_, shellImg, shellArgs, err := agentImageTags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if shellImg == claudeImg {
		t.Fatal("shell-only image must differ from a tooled image")
	}
	if !strings.Contains(strings.Join(shellArgs, " "), "BASE=") {
		t.Fatalf("shell-only args must still thread BASE: %v", shellArgs)
	}
}

// A profile that resolves a not-yet-built package (sentinel-digest binary or an
// unwired catalog entry) must fail fast, never silently drop the tool (specs/0058 N1).
func TestAgentImageTagsRejectsUnbuildablePackages(t *testing.T) {
	if _, _, _, err := agentImageTags([]string{"uv"}); err == nil {
		t.Fatal("expected error for a sentinel-digest binary (uv); got nil")
	}
	if _, _, _, err := agentImageTags([]string{"python3"}); err == nil {
		t.Fatal("expected error for an unwired catalog package (python3); got nil")
	}
}
