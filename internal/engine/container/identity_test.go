package container

import (
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestRecipeIDDeterministicAndSensitive(t *testing.T) {
	df := []byte("FROM base\nRUN x")
	a := RecipeID(df, map[string]string{"K": "1", "Z": "2"})
	if b := RecipeID(df, map[string]string{"Z": "2", "K": "1"}); a != b {
		t.Fatalf("recipeID must be build-arg-order-independent: %s vs %s", a, b)
	}
	if len(a) != 12 {
		t.Fatalf("recipeID must be 12 hex chars, got %q (len %d)", a, len(a))
	}
	if RecipeID(df, map[string]string{"K": "1"}) == a {
		t.Fatal("changing the build-arg set must change the id")
	}
	if RecipeID([]byte("FROM other"), nil) == RecipeID([]byte("FROM base"), nil) {
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
func TestAgentImageTagsAcceptsRipgrep(t *testing.T) {
	_, _, toolsArgs, err := agentImageTags([]string{"node", "pi", "ripgrep"})
	if err != nil {
		t.Fatalf("ripgrep should be buildable now: %v", err)
	}
	joined := strings.Join(toolsArgs, " ")
	for _, want := range []string{
		"ENABLE_RIPGREP=true",
		"RIPGREP_VERSION=14.1.1",
		"RIPGREP_SHA256_AMD64=4cf9f2741e6c465ffdb7c26f38056a59e2a2544b51f7cc128ef28337eeae4d8e",
		"RIPGREP_SHA256_ARM64=c827481c4ff4ea10c9dc7a4022c8de5db34a5737cb74484d62eb94a95841ab2f",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tools build-args missing %q: %v", want, toolsArgs)
		}
	}
}

func TestPersonalRecipeBuildArgs(t *testing.T) {
	resolved, err := policy.Resolve(policy.Profile{Agent: "fish", Environment: "container", Bundles: []string{"personal"}})
	if err != nil {
		t.Fatalf("resolve personal bundle: %v", err)
	}
	recipe, err := ResolveRecipe(resolved.IdentitySet)
	if err != nil {
		t.Fatalf("resolve personal recipe: %v", err)
	}
	catalog := policy.DefaultCatalog()
	for _, name := range resolved.IdentitySet {
		pkg, ok := catalog.Lookup(name)
		if !ok {
			t.Fatalf("resolved package %q missing from catalog", name)
		}
		prefix := argPrefix(name)
		if got := recipe.BuildArgs[enableArg(name)]; got != "true" {
			t.Errorf("%s = %q, want true", enableArg(name), got)
		}
		if got := recipe.BuildArgs[prefix+"_VERSION"]; got != pkg.Version {
			t.Errorf("%s_VERSION = %q, want %q", prefix, got, pkg.Version)
		}
		if pkg.Kind == policy.KindBinary {
			for arch, digest := range pkg.SHA256 {
				suffix := strings.ToUpper(arch)
				key := prefix + "_SHA256_" + suffix
				if got := recipe.BuildArgs[key]; got != digest {
					t.Errorf("%s = %q, want catalog digest %q", key, got, digest)
				}
				urlKey := prefix + "_URL_" + suffix
				wantURL := strings.ReplaceAll(pkg.Upstream.Asset[arch], "{version}", pkg.Version)
				if got := recipe.BuildArgs[urlKey]; got != wantURL {
					t.Errorf("%s = %q, want catalog URL %q", urlKey, got, wantURL)
				}
			}
		}
	}
}

func TestPersonalDockerfileHandlersVerifyBinaryArtifacts(t *testing.T) {
	resolved, err := policy.Resolve(policy.Profile{Agent: "fish", Environment: "container", Bundles: []string{"personal"}})
	if err != nil {
		t.Fatal(err)
	}
	asset, err := readAsset("Dockerfile.agent.tools")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(asset)
	catalog := policy.DefaultCatalog()
	for _, name := range resolved.IdentitySet {
		pkg, _ := catalog.Lookup(name)
		prefix := argPrefix(name)
		start := strings.Index(dockerfile, "ARG ENABLE_"+prefix+"=false")
		if start < 0 {
			t.Errorf("missing guarded Dockerfile handler for %q", name)
			continue
		}
		end := strings.Index(dockerfile[start:], "\n# ---")
		block := dockerfile[start:]
		if end >= 0 {
			block = dockerfile[start : start+end]
		}
		if pkg.Kind == policy.KindBinary {
			for _, want := range []string{
				prefix + "_URL_AMD64", prefix + "_URL_ARM64",
				prefix + "_SHA256_AMD64", prefix + "_SHA256_ARM64",
			} {
				if !strings.Contains(block, want) {
					t.Errorf("%s handler missing %q", name, want)
				}
			}
			if !strings.Contains(block, "sha256sum -c -") && !strings.Contains(block, "install-catalog-artifact") {
				t.Errorf("%s handler does not invoke checksum verification", name)
			}
		}
	}
	pythonAt := strings.Index(dockerfile, "ARG ENABLE_PYTHON3=false")
	if pythonAt < 0 || !strings.Contains(dockerfile[pythonAt:], `apt-get install -y --no-install-recommends "python3=${PYTHON3_VERSION}"`) {
		t.Error("python3 handler must install the exact catalog leaf from the inherited snapshot")
	}
}

func TestRecipeRejectsUnbuildablePackages(t *testing.T) {
	if _, _, _, err := agentImageTags([]string{"bun"}); err == nil {
		t.Fatal("expected error for a sentinel-digest binary (bun); got nil")
	}
	if _, _, _, err := agentImageTags([]string{"typescript"}); err == nil {
		t.Fatal("expected error for an unwired catalog package (typescript); got nil")
	}
}
