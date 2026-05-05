#!/usr/bin/env fish

# Why this wrapper exists:
# - Keep Python dependency installs reproducible and less exposed to arbitrary
#   build execution by requiring frozen lock sync and pinned wheels-only install.
#
# References:
# - uv sync: https://docs.astral.sh/uv/concepts/projects/sync/
# - uv pip install: https://docs.astral.sh/uv/pip/

function __safe_uv_help
    echo "slop-safe-uv — strict uv install wrapper"
    echo ""
    echo "Description:"
    echo "  Two operations, both reproducibility-first:"
    echo "  - 'sync' runs 'uv sync --frozen' against uv.lock."
    echo "  - 'pip-install' runs 'uv pip install --only-binary :all:' on a"
    echo "    pinned 'name==version' so source builds (which can execute"
    echo "    arbitrary code) are blocked."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-safe-uv.fish"
    echo "  slop-safe-uv sync                          (requires uv.lock in cwd)"
    echo "  slop-safe-uv pip-install <name==version>   (wheels only, exact pin only)"
    echo "  slop-safe-uv help"
    echo ""
    echo "Examples:"
    echo "  # Sync a frozen project"
    echo "  source scripts/slop-safe-uv.fish"
    echo "  slop-safe-uv sync"
    echo ""
    echo "  # Install a single pinned package as a wheel"
    echo "  slop-safe-uv pip-install requests==2.32.3"
    echo ""
    echo "  # Through the unified hub"
    echo "  scripts/slop-sandboxctl.fish slop-safe-uv sync"
    echo ""
    echo "Notes:"
    echo "  - 'sync' will refuse without uv.lock in the current directory."
    echo "  - 'pip-install' rejects unpinned packages and source distributions."
    echo "  - Full reference: README.md → 'How to sandbox npm and uv installs'."
end

function __safe_uv_help_to_stderr
    __safe_uv_help 1>&2
end

function __safe_uv_usage
    __safe_uv_help
end

function slop-safe-uv-sync --description "Sync dependencies with frozen lockfile"
    if not test -f uv.lock
        echo "Error: uv.lock is required for slop-safe-uv-sync (run from the project root)." 1>&2
        echo "" 1>&2
        __safe_uv_help_to_stderr
        return 1
    end

    uv sync --frozen
end

function slop-safe-uv-pip-install --description "Install pinned wheel-only package in active env"
    if test (count $argv) -ne 1
        echo "Error: slop-safe-uv pip-install requires exactly one <name==version> argument." 1>&2
        echo "" 1>&2
        __safe_uv_help_to_stderr
        return 1
    end

    set pkg "$argv[1]"
    if not string match -rq '.+==.+' -- "$pkg"
        echo "Error: package must be pinned as name==version (got: $pkg)." 1>&2
        echo "" 1>&2
        __safe_uv_help_to_stderr
        return 1
    end

    uv pip install --only-binary :all: "$pkg"
end

function slop-safe-uv --description "Unified wrapper for safe uv operations"
    if test (count $argv) -eq 0
        __safe_uv_help
        return 0
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    switch "$cmd"
        case sync
            slop-safe-uv-sync $argv
        case pip-install
            slop-safe-uv-pip-install $argv
        case --help -h help
            __safe_uv_help
        case '*'
            echo "Error: Unknown command: $cmd" 1>&2
            echo "" 1>&2
            __safe_uv_help_to_stderr
            return 1
    end
end
