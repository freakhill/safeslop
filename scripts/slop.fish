#!/usr/bin/env fish

# Purpose:
# - One interactive launcher for every tool in this repo.
# - Hard-deps on gum so we can rely on rich primitives (style/choose/input/confirm).
# - Per-tool TUIs (e.g. `slop-gh-key tui`) are smaller, focused launchers and
#   only soft-dep on gum; this global TUI is the discoverable entry point.
#
# TUI principles enforced here:
# - Discoverable: every screen is a menu of named actions.
# - Teachable: every action prints its equivalent CLI before running, so the
#   TUI is a learning aid, not a replacement.
# - Recoverable: destructive actions confirm; Esc on any menu returns/quits.
# - Predictable: arrow keys + Enter to choose; Esc/q to back out.
# - Contextual: top of every screen shows cwd, git remote, and tool status.
#
# References:
# - gum: https://github.com/charmbracelet/gum

set -g SLOP_VERSION "0.1"
set -g SLOP_REPO_ROOT (path resolve (dirname (status filename)))/..

function __slop_help
    echo "slop — interactive launcher for the agentic_tactical_boots toolkit"
    echo ""
    echo "Description:"
    echo "  One menu-driven entry point that wraps every tool in this repo."
    echo "  Each action shows the equivalent CLI before executing, so you can"
    echo "  learn the underlying commands and copy them into scripts."
    echo ""
    echo "Usage:"
    echo "  slop            Launch the global TUI (requires gum)."
    echo "  slop help       Show this message and exit."
    echo "  slop --version  Print version."
    echo ""
    echo "Per-tool TUIs (lighter, focused launchers):"
    echo "  slop-gh-key tui     GitHub deploy keys for the current repo."
    echo "  (more coming as the pattern rolls out)"
    echo ""
    echo "Requirements:"
    echo "  - gum (charmbracelet/gum). Install: brew install gum"
    echo "  - Each underlying tool has its own dependencies; the TUI surfaces"
    echo "    a status line so you can see at a glance what is missing."
    echo ""
    echo "Examples:"
    echo "  # Launch the TUI"
    echo "  slop"
    echo ""
    echo "  # Per-tool TUI for keys (faster than the global menu)"
    echo "  slop-gh-key tui"
    echo ""
    echo "Notes:"
    echo "  - Esc on any menu exits/returns to the previous screen."
    echo "  - Every action shows 'Equivalent CLI:' so the TUI is teachable."
    echo "  - For a non-interactive workflow, see: scripts/slop-sandboxctl.fish help"
end

function __slop_require_gum
    if not command -sq gum
        echo "Error: 'gum' is required for slop (hard dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum                                      (macOS)" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation     (other OSes)" 1>&2
        echo "" 1>&2
        echo "If you do not want to install gum, every tool also has a CLI:" 1>&2
        echo "  scripts/slop-sandboxctl.fish help" 1>&2
        echo "  slop-gh-key --help" 1>&2
        return 1
    end
end

function __slop_status_line
    # One-liner that summarises the current operating context. Shown at the
    # top of every menu so the user does not have to dig for it.
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end

    set -l origin (command git -C "$cwd" remote get-url origin 2>/dev/null)
    if test -z "$origin"
        set origin "(no git origin)"
    end

    set -l on_path "no"
    if contains -- "$HOME/.local/bin" $PATH
        set on_path "yes"
    end

    set -l deps
    for tool in gum uv gh tart docker
        if command -sq "$tool"
            set -a deps "$tool=ok"
        else
            set -a deps "$tool=missing"
        end
    end

    gum style --faint "cwd: $cwd"
    gum style --faint "origin: $origin"
    gum style --faint "~/.local/bin on PATH: $on_path"
    gum style --faint (string join "  " $deps)
end

function __slop_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __slop_pause
    echo ""
    gum input --placeholder "Press Enter to continue…" --prompt "" >/dev/null 2>/dev/null
end

function __slop_top_menu
    while true
        clear
        gum style --bold --foreground 212 "slop — agentic_tactical_boots launcher (v$SLOP_VERSION)"
        echo ""
        __slop_status_line
        echo ""

        set -l choice (gum choose \
            "GitHub deploy keys (here = current repo)" \
            "Forgejo deploy keys" \
            "Radicle access" \
            "macOS local sandbox (sandbox-exec)" \
            "Docker agent stack" \
            "Docker agent + tools stack" \
            "Brew via disposable Tart VM" \
            "Install / uninstall fish-tool shims" \
            "Install / uninstall local skills" \
            "Verifications (pinning, help-sync)" \
            "Show README quickstart" \
            "Quit")

        if test -z "$choice"
            return 0
        end

        switch "$choice"
            case "GitHub deploy keys*"
                __slop_dispatch_gh
            case "Forgejo*"
                __slop_dispatch_forgejo
            case "Radicle*"
                __slop_dispatch_radicle
            case "macOS local sandbox*"
                __slop_dispatch_macos_sandbox
            case "Docker agent stack"
                __slop_dispatch_agent_sandbox
            case "Docker agent + tools stack"
                __slop_dispatch_agent_sandbox_tools
            case "Brew via disposable Tart VM"
                __slop_dispatch_brew_vm
            case "Install / uninstall fish-tool shims"
                __slop_dispatch_install_shims
            case "Install / uninstall local skills"
                __slop_placeholder "Install local skills" "scripts/slop-skills-install.fish install" "scripts/slop-skills-install.fish uninstall"
            case "Verifications*"
                __slop_dispatch_verifications
            case "Show README quickstart"
                __slop_show_readme_quickstart
            case "Quit"
                return 0
        end
    end
end

function __slop_placeholder --argument-names label cli_a cli_b
    # Used for domains not yet wired into a full TUI flow. Surfaces the most
    # common CLI snippets so the user is unblocked, and prints them in the
    # teachable "Equivalent CLI:" format.
    clear
    gum style --bold --foreground 212 "$label"
    gum style --faint "Not yet wired into the TUI. Most common CLI snippets:"
    echo ""
    __slop_show_cli "$cli_a"
    if test -n "$cli_b"
        __slop_show_cli "$cli_b"
    end
    gum style --faint "Full reference: scripts/slop-sandboxctl.fish help, README.md."
    __slop_pause
end

function __slop_dispatch_gh
    # Delegates to slop-gh-key's per-tool TUI; that flow already follows the
    # equivalent-CLI convention, so we stay out of its way.
    clear
    gum style --bold --foreground 212 "GitHub deploy keys"
    echo ""
    __slop_show_cli "slop-gh-key tui"
    command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-gh-key.fish'; slop-gh-key tui"
    __slop_pause
end

function __slop_dispatch_forgejo
    clear
    gum style --bold --foreground 212 "Forgejo deploy keys"
    echo ""
    __slop_show_cli "slop-forgejo-key tui"
    command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-forgejo-key.fish'; slop-forgejo-key tui"
    __slop_pause
end

function __slop_dispatch_radicle
    clear
    gum style --bold --foreground 212 "Radicle access"
    echo ""
    __slop_show_cli "slop-radicle tui"
    command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-radicle.fish'; slop-radicle tui"
    __slop_pause
end

function __slop_dispatch_brew_vm
    clear
    gum style --bold --foreground 212 "Brew via disposable Tart VM"
    echo ""
    __slop_show_cli "slop-brew-vm tui"
    command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-brew-vm.fish'; slop-brew-vm tui"
    __slop_pause
end

function __slop_dispatch_agent_sandbox
    clear
    gum style --bold --foreground 212 "Docker agent stack"
    echo ""
    __slop_show_cli "slop-agent-sandbox tui"
    command fish -c "cd '$SLOP_REPO_ROOT'; source '$SLOP_REPO_ROOT/scripts/slop-agent-sandbox.fish'; slop-agent-sandbox tui"
    __slop_pause
end

function __slop_dispatch_agent_sandbox_tools
    clear
    gum style --bold --foreground 212 "Docker agent + tools stack"
    echo ""
    __slop_show_cli "slop-agent-sandbox-tools tui"
    command fish -c "cd '$SLOP_REPO_ROOT'; source '$SLOP_REPO_ROOT/scripts/slop-agent-sandbox-tools.fish'; slop-agent-sandbox-tools tui"
    __slop_pause
end

function __slop_dispatch_macos_sandbox
    clear
    gum style --bold --foreground 212 "macOS local sandbox (sandbox-exec)"
    __slop_status_line
    echo ""

    set -l choice (gum choose \
        "Print the sandbox profile that would be applied" \
        "Run a one-off command in the sandbox" \
        "Open a sandboxed shell" \
        "Back")

    if test -z "$choice"; or test "$choice" = "Back"
        return 0
    end

    switch "$choice"
        case "Print*"
            __slop_show_cli "slop-macos-sandbox print-profile"
            command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-macos-sandbox.fish'; slop-macos-sandbox print-profile"
        case "Run*"
            set -l cmd (gum input --placeholder "command (e.g. /bin/pwd)" --prompt "command › ")
            if test -z "$cmd"
                return 0
            end
            __slop_show_cli "slop-macos-sandbox run -- $cmd"
            if gum confirm --default=true "Run '$cmd' in sandbox?"
                command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-macos-sandbox.fish'; slop-macos-sandbox run -- $cmd"
            end
        case "Open*"
            __slop_show_cli "slop-macos-sandbox shell"
            if gum confirm --default=true "Open sandboxed /bin/zsh?"
                command fish -c "source '$SLOP_REPO_ROOT/scripts/slop-macos-sandbox.fish'; slop-macos-sandbox shell"
            end
    end
    __slop_pause
end

function __slop_dispatch_install_shims
    clear
    gum style --bold --foreground 212 "Fish tool shims (~/.local/bin)"
    echo ""

    set -l choice (gum choose \
        "Install (idempotent)" \
        "Uninstall" \
        "Show install status" \
        "Back")

    if test -z "$choice"; or test "$choice" = "Back"
        return 0
    end

    switch "$choice"
        case "Install*"
            __slop_show_cli "scripts/slop-install.fish install"
            command fish "$SLOP_REPO_ROOT/scripts/slop-install.fish" install
        case "Uninstall"
            __slop_show_cli "scripts/slop-install.fish uninstall"
            if gum confirm --default=false "Remove all installed shims?"
                command fish "$SLOP_REPO_ROOT/scripts/slop-install.fish" uninstall
            end
        case "Show install status"
            __slop_show_cli "cat ~/.config/agentic_tactical_boots/fish-tools.env"
            if test -f "$HOME/.config/agentic_tactical_boots/fish-tools.env"
                cat "$HOME/.config/agentic_tactical_boots/fish-tools.env"
            else
                echo "Not installed yet."
            end
    end
    __slop_pause
end

function __slop_dispatch_verifications
    clear
    gum style --bold --foreground 212 "Verifications"
    echo ""

    set -l choice (gum choose \
        "Run pinning check" \
        "Run help-text sync check (CI gate)" \
        "Run full test suite" \
        "Back")

    if test -z "$choice"; or test "$choice" = "Back"
        return 0
    end

    switch "$choice"
        case "Run pinning check"
            __slop_show_cli "scripts/slop-pinning.fish"
            command fish "$SLOP_REPO_ROOT/scripts/slop-pinning.fish"
        case "Run help-text sync check*"
            __slop_show_cli "scripts/slop-sync-help.fish check"
            command fish "$SLOP_REPO_ROOT/scripts/slop-sync-help.fish" check
        case "Run full test suite"
            __slop_show_cli "fish tests/run.fish"
            command fish "$SLOP_REPO_ROOT/tests/run.fish"
    end
    __slop_pause
end

function __slop_show_readme_quickstart
    clear
    gum style --bold --foreground 212 "README quickstart"
    echo ""
    set -l readme "$SLOP_REPO_ROOT/README.md"
    if not test -f "$readme"
        echo "README.md not found at $readme"
        __slop_pause
        return 0
    end
    if command -sq gum
        # Render the first ~60 lines of the README in a scrollable pager.
        head -n 60 "$readme" | gum pager
    else
        head -n 60 "$readme"
        __slop_pause
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

__slop_require_gum; or exit 1
__slop_top_menu
