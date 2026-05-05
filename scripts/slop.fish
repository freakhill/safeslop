#!/usr/bin/env fish

# Purpose:
# - Thin fish wrapper around the modern Textual-based TUI in
#   scripts/_py/slop_tui.py. Keeps `slop`, `slop help`, `slop --version`,
#   and the unknown-arg error path on the fast fish path so they do not
#   require uv. Interactive launches exec the Python TUI via `uv run --script`
#   which auto-installs Textual on first run via PEP-723.
#
# Why a Python rewrite (see scripts/_py/slop_tui.py for the long form):
# - Per-action single-key shortcuts, fuzzy filter, sub-menu screens, an
#   always-visible "Equivalent CLI" preview, and full keyboard navigation —
#   all things the previous fish/gum version could not do without a much
#   heavier rewrite. Drops the `gum` hard dep; uv is the only requirement.
#
# References:
# - Textual: https://textual.textualize.io/
# - uv PEP-723: https://docs.astral.sh/uv/guides/scripts/

set -g SLOP_VERSION "0.2"
set -g SLOP_REPO_ROOT (path resolve (dirname (status filename)))/..
set -g SLOP_TUI_PY "$SLOP_REPO_ROOT/scripts/_py/slop_tui.py"

function __slop_help
    echo "slop — interactive launcher for the agentic_tactical_boots toolkit"
    echo ""
    echo "Description:"
    echo "  A modern Textual-based TUI that wraps every tool in this repo."
    echo "  Each action shows the equivalent CLI before executing, so you can"
    echo "  learn the underlying commands and copy them into scripts."
    echo ""
    echo "Usage:"
    echo "  slop            Launch the global TUI (requires uv)."
    echo "  slop help       Show this message and exit."
    echo "  slop --version  Print version."
    echo ""
    echo "Per-tool TUIs (lighter, focused launchers):"
    echo "  slop-gh-key tui     GitHub deploy keys for the current repo."
    echo "  slop-forgejo-key tui"
    echo "  slop-radicle tui"
    echo "  slop-isolate tui"
    echo ""
    echo "Requirements:"
    echo "  - uv (https://github.com/astral-sh/uv)."
    echo "    Install: brew install uv"
    echo "  - Textual is fetched automatically on first run via PEP-723."
    echo ""
    echo "Examples:"
    echo "  # Launch the TUI"
    echo "  slop"
    echo ""
    echo "  # Per-tool TUI for keys (faster than the global menu)"
    echo "  slop-gh-key tui"
    echo ""
    echo "Notes:"
    echo "  - Esc on any menu returns to the previous screen / quits."
    echo "  - Single-key shortcuts on every menu (the leading letter of each row)."
    echo "  - / filters the visible actions; ? shows the keyboard reference."
    echo "  - Every action shows 'Equivalent CLI:' so the TUI is teachable."
    echo "  - For a non-interactive workflow, see: scripts/slop-sandboxctl.fish help"
end

function __slop_require_uv
    if not command -sq uv
        echo "Error: 'uv' is required for slop (used to run the Python TUI)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install uv                                       (macOS)" 1>&2
        echo "  https://github.com/astral-sh/uv#installation          (other OSes)" 1>&2
        echo "" 1>&2
        echo "If you do not want to install uv, every tool also has a CLI:" 1>&2
        echo "  scripts/slop-sandboxctl.fish help" 1>&2
        echo "  slop-gh-key --help" 1>&2
        return 1
    end
end

# Entry point.
if test (count $argv) -gt 0
    switch "$argv[1]"
        case help --help -h
            __slop_help
            exit 0
        case --version
            echo "slop $SLOP_VERSION"
            exit 0
        case '*'
            echo "Error: unknown argument '$argv[1]'" 1>&2
            echo "" 1>&2
            __slop_help 1>&2
            exit 1
    end
end

__slop_require_uv; or exit 1

# UV_NATIVE_TLS=1 makes uv use the OS trust store instead of its bundled
# rustls. Without it, uv fails on machines behind a TLS-intercepting proxy
# (Cloudflare WARP, Zscaler, corporate MITM) with "invalid peer certificate:
# UnknownIssuer" when fetching Textual on first run.
if not set -q UV_NATIVE_TLS
    set -gx UV_NATIVE_TLS 1
end

exec uv run --script "$SLOP_TUI_PY"
