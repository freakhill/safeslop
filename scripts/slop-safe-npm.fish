#!/usr/bin/env fish

# Why this wrapper exists:
# - `npm` lifecycle scripts can execute arbitrary shell code at install time.
# - We enforce lockfile-only + ignore-scripts for safer automation defaults.
#
# References:
# - npm ci: https://docs.npmjs.com/cli/v10/commands/npm-ci
# - npm scripts/lifecycle: https://docs.npmjs.com/cli/v10/using-npm/scripts

function __safe_npm_help
    echo "slop-safe-npm — strict npm install wrapper"
    echo ""
    echo "Description:"
    echo "  Runs 'npm ci --ignore-scripts --no-audit --fund=false' against the"
    echo "  current directory's package-lock.json. Refuses to install ad-hoc"
    echo "  package names so every install is reproducible from the lockfile,"
    echo "  and lifecycle scripts (preinstall/install/postinstall) cannot run."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-safe-npm.fish"
    echo "  slop-safe-npm"
    echo "  slop-safe-npm --help"
    echo ""
    echo "Examples:"
    echo "  # In a project with a committed lockfile"
    echo "  source scripts/slop-safe-npm.fish"
    echo "  slop-safe-npm"
    echo ""
    echo "  # Through the unified hub"
    echo "  scripts/slop-sandboxctl.fish safe-npm"
    echo ""
    echo "Notes:"
    echo "  - Requires package-lock.json in current directory; will refuse without one."
    echo "  - Does NOT accept package names — keeps installs reproducible."
    echo "  - Lifecycle scripts are disabled (--ignore-scripts) by default."
    echo "  - Full reference: README.md → 'How to sandbox npm and uv installs'."
end

function __safe_npm_help_to_stderr
    __safe_npm_help 1>&2
end

# Backwards-compat alias.
function __safe_npm_usage
    __safe_npm_help
end

function slop-safe-npm --description "Install npm dependencies with safer defaults"
    if test (count $argv) -eq 1; and contains -- "$argv[1]" --help -h help
        __safe_npm_help
        return 0
    end

    if not test -f package-lock.json
        echo "Error: package-lock.json is required for slop-safe-npm (run from the project root)." 1>&2
        echo "" 1>&2
        __safe_npm_help_to_stderr
        return 1
    end

    if test (count $argv) -gt 0
        echo "Error: slop-safe-npm does not accept package names; it installs from the lockfile only." 1>&2
        echo "" 1>&2
        __safe_npm_help_to_stderr
        return 1
    end

    npm ci --ignore-scripts --no-audit --fund=false
end
