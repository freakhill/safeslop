#!/usr/bin/env fish

# Purpose:
# - <what this script does>
# - <why it exists>
#
# Safety/model notes:
# - <security assumptions and default policy>
#
# References:
# - <official doc link 1>
# - <official doc link 2>

# ---------------------------------------------------------------------------
# Help block — follows scripts/CONVENTIONS.md "Enriched help structure".
#
# Layout:
#   <tool> — <one-line description>
#
#   Description:
#     <2-4 lines: what it does, default safety stance, when to use it>
#
#   Usage:
#     <subcommand synopsis lines>
#
#   Options:
#     --flag value    <one-line meaning, including default>
#
#   Examples (synced from README → '<exact heading text>'):
#     <step caption>
#       <command>
#
#   Notes:
#     - <one bullet per practice-safe-slop reminder>
#     - Full reference: README.md → '<exact heading text>'.
#
# Every error path prints a single-line "Error: ..." to stderr followed by a
# blank line and the full help (also to stderr). Never leave the user with
# only "Usage:".
# ---------------------------------------------------------------------------

function __example_examples
    # Replace the section= value with the README heading you want this block
    # to mirror, then run: scripts/slop-sync-help.fish sync
    # CI gates drift via .github/workflows/help-sync-check.yml.
    # BEGIN AUTOGEN: examples section="<exact README heading text>"
    echo 'placeholder — run scripts/slop-sync-help.fish sync'
    # END AUTOGEN: examples
end

function __example_help
    echo "<command> — <one-line description>"
    echo ""
    echo "Description:"
    echo "  <2-4 lines: what it does, default safety stance, when to use it>"
    echo ""
    echo "Usage:"
    echo "  source scripts/<file>.fish"
    echo "  <command> <subcommand> [options]"
    echo "  <command> tui                          (interactive launcher; requires gum)"
    echo "  <command> help"
    echo ""
    echo "Options:"
    echo "  --flag <value>                         <meaning, including default>"
    echo ""
    echo "Examples (synced from README → '<exact heading text>'):"
    __example_examples
    echo ""
    echo "Notes:"
    echo "  - <one bullet per practice-safe-slop reminder>"
    echo "  - Full reference: README.md → '<exact heading text>'."
end

function __example_help_to_stderr
    __example_help 1>&2
end

# ---------------------------------------------------------------------------
# Optional: per-tool TUI subcommand. Soft-deps on gum (graceful message if
# missing). Every action prints its equivalent CLI before running, so the TUI
# is a learning aid, not a replacement.
# ---------------------------------------------------------------------------

function __example_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __example_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: <command> help" 1>&2
        return 1
    end

    while true
        echo ""
        gum style --bold --foreground 212 "<command> — interactive launcher"
        gum style --faint "(Esc on the menu to quit. Every action prints its equivalent CLI.)"
        echo ""

        set -l choice (gum choose \
            "Action one (read-only)" \
            "Action two (mutating, confirms)" \
            "Quit")

        if test -z "$choice"
            return 0
        end

        echo ""
        switch "$choice"
            case "Action one*"
                __example_show_cli "<command> action-one"
                <command> action-one
            case "Action two*"
                __example_show_cli "<command> action-two"
                if gum confirm --default=false "Run action two? (this mutates state)"
                    <command> action-two
                end
            case "Quit"
                return 0
        end
    end
end

# ---------------------------------------------------------------------------
# Optional: repo-aware `here` shortcuts. See scripts/CONVENTIONS.md → "Repo-
# aware `here` shortcuts" for the full pattern. Inference must read from
# $ATB_USER_PWD first (set by the bin-shim dispatcher) before falling back to
# $PWD. A failed inference must print the underlying CLI flag the user can
# supply, not just "could not infer".
# ---------------------------------------------------------------------------

# function __example_thing_from_git
#     set -l cwd "$ATB_USER_PWD"
#     if test -z "$cwd"
#         set cwd "$PWD"
#     end
#     # ...lookup logic...
# end

# ---------------------------------------------------------------------------
# Dispatcher.
# ---------------------------------------------------------------------------

function <command> --description "<short description>"
    if test (count $argv) -eq 0
        __example_help
        return 0
    end

    set -l subcmd "$argv[1]"
    set -e argv[1]

    switch "$subcmd"
        case help --help -h
            __example_help
            return 0
        case tui
            __example_tui
            return $status
        # case here
        #     # See scripts/slop-gh-key.fish for the full pattern: infer
        #     # value, prepend it to argv, possibly rewrite the subcommand name,
        #     # then fall through to the normal dispatcher below.
        case '*'
            echo "Error: Unknown command: $subcmd" 1>&2
            echo "" 1>&2
            __example_help_to_stderr
            return 1
    end
end
