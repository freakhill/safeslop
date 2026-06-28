package container

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

const (
	baseImageRepo  = "local/safeslop-base"
	toolsImageRepo = "local/safeslop-tools"
)

// recipeID is the content-hash identity of a build: the first 12 hex chars of
// sha256(dockerfile-bytes followed by each sorted "\nkey=value" build-arg). It is pure and
// deterministic, so an unchanged recipe yields an unchanged tag — imageExists(<id-tag>) becomes a
// CORRECT skip — while any change to the Dockerfile or a build-arg yields a new tag and forces a
// rebuild. This is what kills the stale-":latest" rebuild-skip (specs/0055 Bug B / W1): the old
// code skipped a rebuild whenever the floating :latest tag existed, so an image went stale forever.
func recipeID(dockerfile []byte, buildArgs map[string]string) string {
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

// agentImageTags resolves the content-addressed tags for the base + tools images from the embedded
// Dockerfiles, plus the tools build-args. The caller uses these to BOTH build (buildImages) and
// reference the same tag (composeParams.AgentImage), so the compose file always names exactly the
// image that gets built. The tools id folds in BASE=<base tag>, so a base change propagates into a
// new tools id (a tools image built on a stale base is itself stale).
func agentImageTags() (baseImg, toolsImg string, toolsArgs []string, err error) {
	baseDockerfile, err := readAsset("Dockerfile.agent")
	if err != nil {
		return "", "", nil, err
	}
	toolsDockerfile, err := readAsset("Dockerfile.agent.tools")
	if err != nil {
		return "", "", nil, err
	}
	baseImg = baseImageRepo + ":" + recipeID(baseDockerfile, nil)
	buildArgs := map[string]string{
		"BASE":               baseImg,
		"ENABLE_CLAUDE_CODE": "true",
		"ENABLE_PI":          "true",
	}
	toolsImg = toolsImageRepo + ":" + recipeID(toolsDockerfile, buildArgs)
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		toolsArgs = append(toolsArgs, k+"="+buildArgs[k])
	}
	return baseImg, toolsImg, toolsArgs, nil
}
