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
    echo "  Walks the four pinning-relevant files in examples/ and fails if any"
    echo "  CLI version is set to 'latest' (env file, Dockerfile ARG default,"
    echo "  compose build arg) or if examples/Dockerfile.agent.tools contains a"
    echo "  'uv pip install' without an exact ==version pin."
    echo ""
    echo "Usage:"
    echo "  ./scripts/slop-pinning.fish"
    echo "  ./scripts/slop-pinning.fish help"
    echo ""
    echo "Checks:"
    echo "  - examples/agent-tools.env(.example): no <CLI>_VERSION=latest"
    echo "  - examples/Dockerfile.agent.tools: no ARG <CLI>_VERSION=latest"
    echo "  - examples/docker-compose.yml: no '\${VAR:-latest}' default"
    echo "  - examples/Dockerfile.agent.tools: every 'uv pip install' has =="
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

set -l files \
    examples/agent-tools.env \
    examples/agent-tools.env.example \
    examples/Dockerfile.agent.tools \
    examples/docker-compose.yml

set -l failed 0

for f in $files
    if not test -f $f
        echo "missing required file: $f" 1>&2
        set failed 1
    end
end

if test $failed -eq 1
    exit 1
end

if grep -nE '^(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' examples/agent-tools.env examples/agent-tools.env.example >/dev/null
    echo "unpinned npm CLI version found in agent-tools env files" 1>&2
    grep -nE '^(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' examples/agent-tools.env examples/agent-tools.env.example 1>&2
    set failed 1
end

if grep -nE '^ARG (CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' examples/Dockerfile.agent.tools >/dev/null
    echo "unpinned npm CLI ARG default found in examples/Dockerfile.agent.tools" 1>&2
    grep -nE '^ARG (CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION)=latest$' examples/Dockerfile.agent.tools 1>&2
    set failed 1
end

if grep -nE '(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION): \$\{\1:-latest\}' examples/docker-compose.yml >/dev/null
    echo "unpinned compose build arg default found in examples/docker-compose.yml" 1>&2
    grep -nE '(CLAUDE_CODE_VERSION|OPENCODE_VERSION|OPENCLAW_VERSION|ZEROCLAW_VERSION): \$\{\1:-latest\}' examples/docker-compose.yml 1>&2
    set failed 1
end

set -l unpinned_uv_lines (grep -n 'uv pip install' examples/Dockerfile.agent.tools | grep -v '==')
if test (count $unpinned_uv_lines) -gt 0
    echo "found uv pip install without exact pins in examples/Dockerfile.agent.tools" 1>&2
    for line in $unpinned_uv_lines
        echo $line 1>&2
    end
    set failed 1
end

if test $failed -eq 1
    echo "pinning check failed" 1>&2
    exit 1
end

echo "pinning check passed"
