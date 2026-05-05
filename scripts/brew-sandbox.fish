#!/usr/bin/env fish

# Legacy helper for prefix separation only.
# This is intentionally retained for compatibility, but it is NOT a security
# sandbox. Use scripts/slop-brew-vm.fish for VM-backed isolation.
#
# Reference:
# - Homebrew docs: https://docs.brew.sh/

set -g BREW_SANDBOX_PREFIX "$HOME/.homebrew-sandbox"

function brew-sandbox-help --description "Show brew-sandbox usage"
    echo "Usage:"
    echo "  source scripts/brew-sandbox.fish"
    echo "  brew-sandbox-init"
    echo "  brew-sandbox <brew args>"
    echo ""
    echo "Warning:"
    echo "  - brew-sandbox is prefix isolation only, not a security sandbox."
    echo "  - Use scripts/slop-brew-vm.fish for VM-backed isolation."
end

function brew-sandbox-init --description "Initialize separate Homebrew prefix"
    if test -d "$BREW_SANDBOX_PREFIX/.git"
        echo "Sandbox Homebrew already initialized at $BREW_SANDBOX_PREFIX"
        return 0
    end

    git clone https://github.com/Homebrew/brew "$BREW_SANDBOX_PREFIX"
end

function brew-sandbox --description "Run isolated Homebrew instance"
    if test (count $argv) -eq 1; and contains -- "$argv[1]" --help -h help
        brew-sandbox-help
        return 0
    end

    echo "Warning: brew-sandbox is prefix isolation only, not a security sandbox." 1>&2
    echo "Use scripts/slop-brew-vm.fish for real VM-backed isolation." 1>&2

    if not test -x "$BREW_SANDBOX_PREFIX/bin/brew"
        echo "Sandbox Homebrew not initialized. Run: brew-sandbox-init" 1>&2
        return 1
    end

    "$BREW_SANDBOX_PREFIX/bin/brew" $argv
end
