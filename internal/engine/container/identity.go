package container

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

const (
	baseImageRepo  = "local/safeslop-base"
	toolsImageRepo = "local/safeslop-tools"
)

// iw2BuildablePackages are the catalog packages Dockerfile.agent.tools explicitly wires.
// A profile that resolves anything else fails fast in agentImageTags rather than silently
// dropping a tool (specs/0058 N1).
var iw2BuildablePackages = map[string]bool{
	"bat":         true,
	"claude-code": true,
	"eza":         true,
	"fd":          true,
	"fzf":         true,
	"go":          true,
	"hyperfine":   true,
	"node":        true,
	"pi":          true,
	"pnpm":        true,
	"python3":     true,
	"ripgrep":     true,
	"ruff":        true,
	"rust":        true,
	"sccache":     true,
	"tokei":       true,
	"uv":          true,
	"yq":          true,
	"zoxide":      true,
}

// RecipeID is the content-hash identity of a build: the first 12 hex chars of
// sha256(dockerfile-bytes followed by each sorted "\nkey=value" build-arg). It is pure and
// deterministic, so an unchanged recipe yields an unchanged tag — imageExists(<id-tag>) becomes a
// CORRECT skip — while any change to the Dockerfile or a build-arg yields a new tag and forces a
// rebuild. This is what kills the stale-":latest" rebuild-skip (specs/0055 Bug B / W1): the old
// code skipped a rebuild whenever the floating :latest tag existed, so an image went stale forever.
func RecipeID(dockerfile []byte, buildArgs map[string]string) string {
	h := sha256.New()
	h.Write(dockerfile)
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte("\n" + k + "=" + buildArgs[k]))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// enableArg returns the Dockerfile build-arg toggle name for a catalog package, e.g.
// "claude-code" -> "ENABLE_CLAUDE_CODE". The version/digest arg prefix is the same upper
// form ("CLAUDE_CODE_VERSION", "NODE_SHA256_AMD64"), matching Dockerfile.agent.tools.
func enableArg(name string) string {
	return "ENABLE_" + argPrefix(name)
}

func argPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// toolsBuildArgs builds the agent-image build-args from the resolved package set: BASE,
// one ENABLE_<PKG>=true per enabled package, and that package's pinned version (+ per-arch
// sha256 for binary kinds), all read from the in-tree catalog so the catalog stays the
// single source of truth and a version/digest bump rotates the recipeID. It refuses any
// package IW2 cannot build (sentinel-digest binaries; not-yet-wired packages) so a
// resolvable-but-unbuildable profile fails fast rather than silently dropping a tool.
func toolsBuildArgs(baseImg string, enabled []string) (map[string]string, error) {
	cat := policy.DefaultCatalog()
	if pending := cat.BuildReadyFor(enabled); len(pending) > 0 {
		return nil, fmt.Errorf("cannot build: packages %v have no resolved sha256 digest yet (IW2 sentinel)", pending)
	}
	args := map[string]string{"BASE": baseImg}
	for _, name := range enabled {
		p, ok := cat.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("cannot build: resolved package %q is not in the catalog", name)
		}
		if !iw2BuildablePackages[name] {
			return nil, fmt.Errorf("cannot build: package %q is in the catalog but not wired into the agent image", name)
		}
		prefix := argPrefix(name)
		args[enableArg(name)] = "true"
		args[prefix+"_VERSION"] = p.Version
		if p.Kind == policy.KindBinary {
			if p.Upstream == nil {
				return nil, fmt.Errorf("cannot build: binary package %q has no upstream artifact metadata", name)
			}
			for arch, digest := range p.SHA256 {
				url := p.Upstream.Asset[arch]
				if url == "" {
					return nil, fmt.Errorf("cannot build: binary package %q has no %s artifact URL", name, arch)
				}
				suffix := strings.ToUpper(arch)
				args[prefix+"_SHA256_"+suffix] = digest
				args[prefix+"_URL_"+suffix] = strings.ReplaceAll(url, "{version}", p.Version)
			}
		}
	}
	return args, nil
}

// agentImageTags resolves the content-addressed tags for the base + agent images from the embedded
// Dockerfiles, plus the agent build-args derived from the profile's resolved package set (enabled).
// The caller uses these to BOTH build (buildImages) and reference the same tag (composeParams.
// AgentImage), so the compose file always names exactly the image that gets built. The agent id
// folds in BASE=<base tag> + the sorted ENABLE_*/version/digest args, so a base change OR a package
// change yields a new agent id (an agent image built on a stale base or stale package pin is itself
// stale). enabled is the resolver's identity set (sorted); a different profile's package set yields a
// different agent image — replacing the old hardcoded ENABLE_CLAUDE_CODE/PI=true (specs/0058 N1).
// Recipe describes the dry-run image identity that profile show/lock can compute
// without invoking a container engine or network. It mirrors agentImageTags.
type Recipe struct {
	BaseImage       string            `json:"baseImage"`
	AgentImage      string            `json:"agentImage"`
	RecipeID        string            `json:"recipeID"`
	SourceBaseImage string            `json:"sourceBaseImage"`
	BuildArgs       map[string]string `json:"buildArgs,omitempty"`
}

func agentImageTags(enabled []string) (baseImg, toolsImg string, toolsArgs []string, err error) {
	recipe, err := ResolveRecipe(enabled)
	if err != nil {
		return "", "", nil, err
	}
	keys := make([]string, 0, len(recipe.BuildArgs))
	for k := range recipe.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		toolsArgs = append(toolsArgs, k+"="+recipe.BuildArgs[k])
	}
	return recipe.BaseImage, recipe.AgentImage, toolsArgs, nil
}

// ResolveRecipe computes the content-addressed base + agent image tags for a resolved
// package identity set, without building. It is the shared dry-run core for build,
// profile show, and safeslop.lock.json.
func ResolveRecipe(enabled []string) (*Recipe, error) {
	baseDockerfile, err := readAsset("Dockerfile.agent")
	if err != nil {
		return nil, err
	}
	toolsDockerfile, err := readAsset("Dockerfile.agent.tools")
	if err != nil {
		return nil, err
	}
	baseImg := baseImageRepo + ":" + RecipeID(baseDockerfile, nil)
	buildArgs, err := toolsBuildArgs(baseImg, enabled)
	if err != nil {
		return nil, err
	}
	toolsID := RecipeID(toolsDockerfile, buildArgs)
	return &Recipe{
		BaseImage:       baseImg,
		AgentImage:      toolsImageRepo + ":" + toolsID,
		RecipeID:        toolsID,
		SourceBaseImage: GoldenBaseSourceImage,
		BuildArgs:       buildArgs,
	}, nil
}
