#!/usr/bin/env fish

# Why this check exists:
# - Prevent drift to unpinned or `latest` tool versions in sandbox images.
# - Keep automation reproducible and supply-chain risk reviewable.

function __check_pinning_examples
    # BEGIN AUTOGEN: examples section="Artifact pinning and attestation reference"
    echo '  npm view @anthropic-ai/claude-code@2.1.121 dist.integrity'
    echo '  npm view opencode-ai@1.14.28 dist.integrity'
    echo '  "/opt/homebrew/bin/python3" -m pip index versions crewai'
    echo '  "/opt/homebrew/bin/python3" -m pip index versions pydantic-ai'
    echo '  "/opt/homebrew/bin/python3" -m pip index versions ag2'
    echo '  ./scripts/slop-pinning.fish'
    # END AUTOGEN: examples
end

function __check_pinning_help
    echo "slop-pinning — CI/local gate that fails on unpinned tool versions"
    echo ""
    echo "Description:"
    echo "  Walks the pinning-relevant files in library/ + any *.cue under the repo"
    echo "  and fails on unpinned tool versions: 'latest' tags / defaults in"
    echo "  agent-tools env+Dockerfile+compose, 'uv pip install' without ==,"
    echo "  and ':latest' / '@latest' / '==latest' inside slop.cue image specs."
    echo ""
    echo "Usage:"
    echo "  ./scripts/slop-pinning.fish"
    echo "  ./scripts/slop-pinning.fish help"
    echo ""
    echo "Checks:"
    echo "  - library/layer/container/agent-tools.env(.example): no <CLI>_VERSION=latest"
    echo "  - library/layer/container/Dockerfile.agent.tools: no ARG <CLI>_VERSION=latest"
    echo "  - library/layer/container/docker-compose.yml: no '\${VAR:-latest}' default"
    echo "  - library/layer/container/Dockerfile.agent.tools: every 'uv pip install' has =="
    echo "  - **/*.cue (excluding cue.mod/, .generated/): no ':latest', '@latest',"
    echo "    or '==latest' in image.base / image.extra-{npm,pip}"
    echo ""
    echo "Examples (synced from README → 'Artifact pinning and attestation reference'):"
    __check_pinning_examples
    echo ""
    echo "Notes:"
    echo "  - Exits 0 with 'pinning check passed' on success, 1 with details on failure."
    echo "  - CI runs this gate via .github/workflows/pinning-check.yml."
    echo "  - Full reference: README.md → 'Artifact pinning and attestation reference'."
end

function __check_pinning_help_to_stderr
    __check_pinning_help 1>&2
end

if test (count $argv) -gt 0
    if contains -- "$argv[1]" --help -h help
        __check_pinning_help
        exit 0
    end
    echo "Error: slop-pinning takes no arguments (got: $argv[1])." 1>&2
    echo "" 1>&2
    __check_pinning_help_to_stderr
    exit 1
end

# agent-tools.env is gitignored — it's the user's local override of the
# .example file. On fresh CI checkouts only .example is present, so
# require .example + Dockerfile + compose, and only check .env when the
# user has actually copied it into place.
set -l required_files \
    library/layer/container/agent-tools.env.example \
    library/layer/container/Dockerfile.agent.tools \
    library/layer/container/docker-compose.yml

set -l failed 0

for f in $required_files
    if not test -f $f
        echo "missing required file: $f" 1>&2
        set failed 1
    end
end

if test $failed -eq 1
    exit 1
end

# Build the env-file list: always include .example, include .env only if
# the user has it.
set -l env_files library/layer/container/agent-tools.env.example
if test -f library/layer/container/agent-tools.env
    set -a env_files library/layer/container/agent-tools.env
end

if grep -nE '^(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' $env_files >/dev/null
    echo "unpinned npm CLI version found in agent-tools env files" 1>&2
    grep -nE '^(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' $env_files 1>&2
    set failed 1
end

if grep -nE '^ARG (CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' library/layer/container/Dockerfile.agent.tools >/dev/null
    echo "unpinned npm CLI ARG default found in library/layer/container/Dockerfile.agent.tools" 1>&2
    grep -nE '^ARG (CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' library/layer/container/Dockerfile.agent.tools 1>&2
    set failed 1
end

if grep -nE '(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION): \$\{\1:-latest\}' library/layer/container/docker-compose.yml >/dev/null
    echo "unpinned compose build arg default found in library/layer/container/docker-compose.yml" 1>&2
    grep -nE '(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION): \$\{\1:-latest\}' library/layer/container/docker-compose.yml 1>&2
    set failed 1
end

set -l unpinned_uv_lines (grep -n 'uv pip install' library/layer/container/Dockerfile.agent.tools | grep -v '==')
if test (count $unpinned_uv_lines) -gt 0
    echo "found uv pip install without exact pins in library/layer/container/Dockerfile.agent.tools" 1>&2
    for line in $unpinned_uv_lines
        echo $line 1>&2
    end
    set failed 1
end

# slop.cue scan: the orchestrator reads `image.extra-{apt,pip,npm}` and
# `image.base` from any *.cue file declaring profile shapes. Three
# user-authored leaks to catch:
#   1. `:latest"` in `base:` tags (use a digest or pinned tag instead).
#   2. `@latest` in npm specs (pin to a numeric version).
#   3. `==latest` in pip specs (pin to a numeric version).
# Scoped to *.cue files under the repo, excluding cue.mod/ (modules are
# version-pinned by the CUE tooling separately) and the .generated/
# tree (slop-isolate compile output).
set -l cue_files
for f in (find . -type f -name '*.cue' \
        -not -path './library/layer/policy/cue.mod/*' \
        -not -path './library/.generated/*' \
        -not -path './.git/*' 2>/dev/null)
    set -a cue_files "$f"
end

if test (count $cue_files) -gt 0
    set -l unpinned_cue_lines (grep -nHE ':latest"|@latest"|==latest' $cue_files)
    if test (count $unpinned_cue_lines) -gt 0
        echo "unpinned versions in slop.cue / preset .cue files:" 1>&2
        for line in $unpinned_cue_lines
            echo "  $line" 1>&2
        end
        echo "  (pin image.base to a digest or numeric tag; pin extra-pip/npm" 1>&2
        echo "   entries to exact versions like 'ruff==0.6.0' / 'gh@2.0.0')" 1>&2
        set failed 1
    end
end

if test $failed -eq 1
    echo "pinning check failed" 1>&2
    exit 1
end

echo "pinning check passed"
