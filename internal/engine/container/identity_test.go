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
	baseImg, toolsImg, toolsArgs, err := agentImageTags()
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
	for _, want := range []string{"BASE=" + baseImg, "ENABLE_CLAUDE_CODE=true", "ENABLE_PI=true"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tools build-args missing %q: %v", want, toolsArgs)
		}
	}
	if b2, t2, _, _ := agentImageTags(); b2 != baseImg || t2 != toolsImg {
		t.Fatal("agentImageTags must be deterministic")
	}
}
