#!/usr/bin/env fish

# Purpose:
# - Same UX as slop-agent-sandbox, but targets tool-preinstalled runtime.
# - Defaults to strict-egress to keep dependency/tooling pulls behind proxy.
#
# References:
# - Docker Compose env files: https://docs.docker.com/compose/environment-variables/

function __agent_sandbox_tools_examples
    # BEGIN AUTOGEN: examples section="How to run with preinstalled CLIs/frameworks"
    echo 'Copy env template and pin versions:'
    echo '  cp examples/agent-tools.env.example examples/agent-tools.env'
    echo
    echo 'Build and run the tools image:'
    echo '  docker compose --env-file examples/agent-tools.env -f examples/docker-compose.yml build agent-tools'
    echo '  docker compose --env-file examples/agent-tools.env -f examples/docker-compose.yml run --rm agent-tools'
    echo
    echo 'Optional convenience wrapper:'
    echo '  source scripts/slop-agent-sandbox-tools.fish'
    echo '  slop-agent-sandbox-tools shell'
    echo '  scripts/slop-sandboxctl.fish docker-tools shell'
    # END AUTOGEN: examples
end

function __agent_sandbox_tools_help
    echo "slop-agent-sandbox-tools — Docker stack runner for the 'agent-tools' container"
    echo ""
    echo "Description:"
    echo "  Same UX as slop-agent-sandbox but targets the tool-preinstalled image"
    echo "  ('agent-tools' service in examples/docker-compose.yml). Picks up"
    echo "  pinned versions from examples/agent-tools.env when present."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-agent-sandbox-tools.fish"
    echo "  slop-agent-sandbox-tools run [options] [command ...]"
    echo "  slop-agent-sandbox-tools shell [options]"
    echo "  slop-agent-sandbox-tools up"
    echo "  slop-agent-sandbox-tools down"
    echo "  slop-agent-sandbox-tools tui                 (interactive launcher; requires gum)"
    echo "  slop-agent-sandbox-tools help"
    echo ""
    echo "Options:"
    echo "  --network-policy strict-egress|proxy-only|off"
    echo "                                          Default: strict-egress."
    echo ""
    echo "Examples (synced from README → 'How to run with preinstalled CLIs/frameworks'):"
    __agent_sandbox_tools_examples
    echo ""
    echo "Notes:"
    echo "  - Host project is mounted at /workspace inside the container."
    echo "  - examples/agent-tools.env (when present) pins CLI versions for the build."
    echo "  - For the bare agent without preinstalled tools, use 'slop-agent-sandbox' instead."
    echo "  - Full reference: README.md → 'How to run with preinstalled CLIs/frameworks'."
end

function __agent_sandbox_tools_help_to_stderr
    __agent_sandbox_tools_help 1>&2
end

function __agent_sandbox_tools_usage
    __agent_sandbox_tools_help
end

# Keep compose file checks centralized so every subcommand fails consistently.
function __agent_sandbox_tools_check_files
    if not test -f examples/docker-compose.yml
        echo "Error: Missing examples/docker-compose.yml (run from repo root)." 1>&2
        echo "" 1>&2
        __agent_sandbox_tools_help_to_stderr
        return 1
    end
end

# Allowed values are explicit to avoid insecure typos.
function __agent_sandbox_tools_validate_policy --argument-names policy
    if not contains -- "$policy" strict-egress proxy-only off
        echo "Error: Invalid --network-policy: $policy (allowed: strict-egress, proxy-only, off)" 1>&2
        echo "" 1>&2
        __agent_sandbox_tools_help_to_stderr
        return 1
    end
end

function __agent_sandbox_tools_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __agent_sandbox_tools_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: slop-agent-sandbox-tools help" 1>&2
        return 1
    end

    while true
        echo ""
        gum style --bold --foreground 212 "slop-agent-sandbox-tools — Docker stack ('agent-tools' service)"
        gum style --faint "compose file: examples/docker-compose.yml"
        if test -f examples/agent-tools.env
            gum style --faint "env file: examples/agent-tools.env (pinned versions in use)"
        else
            gum style --foreground 196 "no examples/agent-tools.env — versions will be from compose defaults"
        end
        gum style --faint "(Esc on the menu to quit. Every action prints its equivalent CLI.)"
        echo ""

        set -l policy (gum choose --header "Network policy:" "strict-egress (default)" "proxy-only" "off")
        if test -z "$policy"
            return 0
        end
        set policy (string replace -r ' .*' '' -- "$policy")

        set -l choice (gum choose \
            "Bring the stack up (build + proxy in background)" \
            "Open a one-shot agent-tools shell" \
            "Run a one-off command in the agent-tools container" \
            "Bring the stack down" \
            "Quit")

        if test -z "$choice"
            return 0
        end

        echo ""
        switch "$choice"
            case "Bring the stack up*"
                __agent_sandbox_tools_show_cli "slop-agent-sandbox-tools up"
                if gum confirm --default=true "Build agent-tools + start proxy?"
                    slop-agent-sandbox-tools up
                end
            case "Open a one-shot*"
                __agent_sandbox_tools_show_cli "slop-agent-sandbox-tools shell --network-policy $policy"
                if gum confirm --default=true "Open shell in agent-tools container?"
                    slop-agent-sandbox-tools shell --network-policy "$policy"
                end
            case "Run a one-off*"
                set -l cmd (gum input --placeholder "command (e.g. uv --version)" --prompt "command › ")
                if test -z "$cmd"
                    continue
                end
                __agent_sandbox_tools_show_cli "slop-agent-sandbox-tools run --network-policy $policy $cmd"
                if gum confirm --default=true "Run '$cmd' in agent-tools container?"
                    slop-agent-sandbox-tools run --network-policy "$policy" $cmd
                end
            case "Bring the stack down"
                __agent_sandbox_tools_show_cli "slop-agent-sandbox-tools down"
                if gum confirm --default=false "Stop and remove the stack?"
                    slop-agent-sandbox-tools down
                end
            case "Quit"
                return 0
        end
    end
end

function __agent_sandbox_tools_compose_cmd --argument-names policy
    # We intentionally preserve support for optional examples/agent-tools.env.
    # This keeps pinned versions configurable without changing command UX.
    set -e argv[1]

    if test -f examples/agent-tools.env
        docker compose --env-file examples/agent-tools.env -f examples/docker-compose.yml $argv
    else
        docker compose -f examples/docker-compose.yml $argv
    end
end

function slop-agent-sandbox-tools --description "Run commands in tool-preinstalled sandbox container"
    if test (count $argv) -eq 0
        __agent_sandbox_tools_help
        return 0
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    if test "$cmd" = "--help"; or test "$cmd" = "-h"; or test "$cmd" = "help"
        __agent_sandbox_tools_help
        return 0
    end

    if test "$cmd" = "tui"
        __agent_sandbox_tools_tui
        return $status
    end

    __agent_sandbox_tools_check_files; or return 1

    set -l policy "strict-egress"
    if test (count $argv) -ge 2; and test "$argv[1]" = "--network-policy"
        set policy "$argv[2]"
        set -e argv[1..2]
    end

    __agent_sandbox_tools_validate_policy "$policy"; or return 1

    switch "$cmd"
        case run
            __agent_sandbox_tools_compose_cmd "$policy" build agent-tools
            and __agent_sandbox_tools_compose_cmd "$policy" up -d proxy
            and __agent_sandbox_tools_compose_cmd "$policy" run --rm agent-tools $argv
        case shell
            __agent_sandbox_tools_compose_cmd "$policy" build agent-tools
            and __agent_sandbox_tools_compose_cmd "$policy" up -d proxy
            and __agent_sandbox_tools_compose_cmd "$policy" run --rm agent-tools
        case up
            __agent_sandbox_tools_compose_cmd "$policy" build agent-tools
            and __agent_sandbox_tools_compose_cmd "$policy" up -d proxy
        case down
            __agent_sandbox_tools_compose_cmd "$policy" down
        case '*'
            echo "Error: Unknown command: $cmd" 1>&2
            echo "" 1>&2
            __agent_sandbox_tools_help_to_stderr
            return 1
    end
end
