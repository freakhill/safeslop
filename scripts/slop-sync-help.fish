#!/usr/bin/env fish

# Purpose:
# - Keep --help/error output in scripts/*.fish in sync with README.md.
# - Each script declares which README section to pull examples from via
#   AUTOGEN markers; this tool rewrites those blocks in place.
# - CI uses the `check` subcommand to fail PRs that drift.
#
# Why uv:
# - Markdown parsing is awkward in fish; the actual logic lives in
#   scripts/_py/sync_help_from_readme.py, invoked via uv run --script with
#   PEP-723 inline metadata, matching the repo's `llm-*` Python helper pattern.
#
# References:
# - PEP 723 (inline script metadata): https://peps.python.org/pep-0723/

set -g SYNC_HELP_PY (path resolve (dirname (status filename)))"/_py/sync_help_from_readme.py"

function __sync_help_print_help
    echo "slop-sync-help — keep fish-script --help text in sync with README.md"
    echo ""
    echo "Usage:"
    echo "  scripts/slop-sync-help.fish sync"
    echo "  scripts/slop-sync-help.fish check"
    echo "  scripts/slop-sync-help.fish help"
    echo ""
    echo "Subcommands:"
    echo "  sync    Rewrite AUTOGEN: examples blocks in scripts/*.fish from README.md."
    echo "  check   Exit non-zero if any AUTOGEN block would change. Used by CI."
    echo "  help    Show this message."
    echo ""
    echo "How it works:"
    echo "  Each fish script may contain markers of the form:"
    echo ""
    echo "    # BEGIN AUTOGEN: examples section=\"<README heading text>\""
    echo "    echo '...'"
    echo "    # END AUTOGEN: examples"
    echo ""
    echo "  The tool finds the matching README section, extracts every fenced"
    echo "  ```fish code block, and rewrites the block as fish `echo` lines."
    echo ""
    echo "Examples:"
    echo "  # Apply README changes to all scripts that opt in via AUTOGEN markers"
    echo "  scripts/slop-sync-help.fish sync"
    echo ""
    echo "  # Verify no drift (used in CI)"
    echo "  scripts/slop-sync-help.fish check"
    echo ""
    echo "Notes:"
    echo "  - Requires uv on PATH (already required by the llm-* helpers)."
    echo "  - Update README.md first; then run sync to propagate to scripts."
end

function __sync_help_require_uv
    if not command -q uv
        echo "uv is required (see README, section 'Python helpers run via uv')." 1>&2
        return 1
    end
end

if test (count $argv) -eq 0
    __sync_help_print_help 1>&2
    exit 1
end

set -l cmd "$argv[1]"

switch "$cmd"
    case help --help -h
        __sync_help_print_help
        exit 0
    case sync check
        __sync_help_require_uv; or exit 1
        uv run --script "$SYNC_HELP_PY" "$cmd"
        exit $status
    case '*'
        echo "Error: unknown subcommand '$cmd'" 1>&2
        echo "" 1>&2
        __sync_help_print_help 1>&2
        exit 1
end
