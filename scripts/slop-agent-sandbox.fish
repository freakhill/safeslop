#!/usr/bin/env fish

# Purpose:
# - Run the agent container with a predictable command interface.
# - Default to strict-egress policy to reduce accidental outbound access.
#
# References:
# - Docker Compose networking: https://docs.docker.com/compose/networking/

function __agent_sandbox_examples
    # BEGIN AUTOGEN: examples section="How to run any agent behind Docker + URL allowlist proxy"
    echo 'Start the proxy:'
    echo '  docker compose -f examples/docker-compose.yml build agent'
    echo '  docker compose -f examples/docker-compose.yml up -d proxy'
    echo
    echo 'Run agent container through proxy:'
    echo '  docker compose -f examples/docker-compose.yml run --rm agent'
    echo
    echo 'Verify blocking:'
    echo '  docker compose -f examples/docker-compose.yml run --rm agent sh -lc \'curl -I https://example.com\''
    # END AUTOGEN: examples
end

function __agent_sandbox_help
    echo "slop-agent-sandbox — Docker stack runner for the bare 'agent' container"
    echo ""
    echo "Description:"
    echo "  Builds the agent image, brings the proxy service up, and runs the"
    echo "  agent container with the chosen network policy. The host project"
    echo "  directory is mounted at /workspace; egress goes through the proxy"
    echo "  in strict-egress mode (default) per examples/squid.conf."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-agent-sandbox.fish"
    echo "  slop-agent-sandbox run [options] [command ...]"
    echo "  slop-agent-sandbox shell [options]"
    echo "  slop-agent-sandbox up"
    echo "  slop-agent-sandbox down"
    echo "  slop-agent-sandbox tui                       (interactive launcher; requires gum)"
    echo "  slop-agent-sandbox help"
    echo ""
    echo "Options:"
    echo "  --network-policy strict-egress|proxy-only|off"
    echo "                                          Default: strict-egress."
    echo ""
    echo "Examples (synced from README → 'How to run any agent behind Docker + URL allowlist proxy'):"
    __agent_sandbox_examples
    echo ""
    echo "Notes:"
    echo "  - Host project is mounted at /workspace inside the container."
    echo "  - 'up' starts the proxy in the background; 'down' stops the whole stack."
    echo "  - For preinstalled CLIs/frameworks, use 'slop-agent-sandbox-tools' instead."
    echo "  - Full reference: README.md → 'How to run any agent behind Docker + URL allowlist proxy'."
end

function __agent_sandbox_help_to_stderr
    __agent_sandbox_help 1>&2
end

function __agent_sandbox_usage
    __agent_sandbox_help
end

# Keep compose file checks centralized so every subcommand fails consistently.
function __agent_sandbox_check_files
    if not test -f examples/docker-compose.yml
        echo "Error: Missing examples/docker-compose.yml (run from repo root)." 1>&2
        echo "" 1>&2
        __agent_sandbox_help_to_stderr
        return 1
    end
end

# Allowed values are explicit to avoid insecure typos.
function __agent_sandbox_validate_policy --argument-names policy
    if not contains -- "$policy" strict-egress proxy-only off
        echo "Error: Invalid --network-policy: $policy (allowed: strict-egress, proxy-only, off)" 1>&2
        echo "" 1>&2
        __agent_sandbox_help_to_stderr
        return 1
    end
end

function __agent_sandbox_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __agent_sandbox_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: slop-agent-sandbox help" 1>&2
        return 1
    end

    while true
        echo ""
        gum style --bold --foreground 212 "slop-agent-sandbox — Docker stack ('agent' service)"
        gum style --faint "compose file: examples/docker-compose.yml"
        gum style --faint "(Esc on the menu to quit. Every action prints its equivalent CLI.)"
        echo ""

        set -l policy (gum choose --header "Network policy:" "strict-egress (default)" "proxy-only" "off")
        if test -z "$policy"
            return 0
        end
        set policy (string replace -r ' .*' '' -- "$policy")

        set -l choice (gum choose \
            "Bring the stack up (build + proxy in background)" \
            "Open a one-shot agent shell" \
            "Run a one-off command in the agent container" \
            "Bring the stack down" \
            "Quit")

        if test -z "$choice"
            return 0
        end

        echo ""
        switch "$choice"
            case "Bring the stack up*"
                __agent_sandbox_show_cli "slop-agent-sandbox up"
                if gum confirm --default=true "Build agent + start proxy?"
                    slop-agent-sandbox up
                end
            case "Open a one-shot*"
                __agent_sandbox_show_cli "slop-agent-sandbox shell --network-policy $policy"
                if gum confirm --default=true "Open shell in agent container?"
                    slop-agent-sandbox shell --network-policy "$policy"
                end
            case "Run a one-off*"
                set -l cmd (gum input --placeholder "command (e.g. sh -lc 'curl -I https://registry.npmjs.org')" --prompt "command › ")
                if test -z "$cmd"
                    continue
                end
                __agent_sandbox_show_cli "slop-agent-sandbox run --network-policy $policy $cmd"
                if gum confirm --default=true "Run '$cmd' in agent container?"
                    slop-agent-sandbox run --network-policy "$policy" $cmd
                end
            case "Bring the stack down"
                __agent_sandbox_show_cli "slop-agent-sandbox down"
                if gum confirm --default=false "Stop and remove the stack?"
                    slop-agent-sandbox down
                end
            case "Quit"
                return 0
        end
    end
end

function __agent_sandbox_compose_cmd --argument-names policy
    # The policy switch is currently routing-compatible for all modes.
    # Keeping this shim lets us add stricter mode-specific compose files later
    # without changing command UX.
    set -e argv[1]
    switch "$policy"
        case strict-egress
            docker compose -f examples/docker-compose.yml $argv
        case proxy-only off
            docker compose -f examples/docker-compose.yml $argv
    end
end

function slop-agent-sandbox --description "Run commands in agent sandbox container"
    if test (count $argv) -eq 0
        __agent_sandbox_help
        return 0
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    if test "$cmd" = "--help"; or test "$cmd" = "-h"; or test "$cmd" = "help"
        __agent_sandbox_help
        return 0
    end

    if test "$cmd" = "tui"
        __agent_sandbox_tui
        return $status
    end

    __agent_sandbox_check_files; or return 1

    set -l policy "strict-egress"
    if test (count $argv) -ge 2; and test "$argv[1]" = "--network-policy"
        set policy "$argv[2]"
        set -e argv[1..2]
    end

    __agent_sandbox_validate_policy "$policy"; or return 1

    switch "$cmd"
        case run
            __agent_sandbox_compose_cmd "$policy" build agent
            and __agent_sandbox_compose_cmd "$policy" up -d proxy
            and __agent_sandbox_compose_cmd "$policy" run --rm agent $argv
        case shell
            __agent_sandbox_compose_cmd "$policy" build agent
            and __agent_sandbox_compose_cmd "$policy" up -d proxy
            and __agent_sandbox_compose_cmd "$policy" run --rm agent
        case up
            __agent_sandbox_compose_cmd "$policy" build agent
            and __agent_sandbox_compose_cmd "$policy" up -d proxy
        case down
            __agent_sandbox_compose_cmd "$policy" down
        case '*'
            echo "Error: Unknown command: $cmd" 1>&2
            echo "" 1>&2
            __agent_sandbox_help_to_stderr
            return 1
    end
end
