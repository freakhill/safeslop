#!/usr/bin/env fish

# Purpose:
# - Compile a single CUE isolation policy to per-tool configs
#   (sandbox-exec, docker-compose, squid, envoy, lulu, pf, claude-code, ...).
# - Drive the optional Envoy + CoreDNS + notifier proxy stack that pages
#   the operator on a blocked flow and accepts approve --once / --always.
#
# Safety/model notes:
# - Default network policy in every preset is strict-egress.
# - `apply` is bounded: it never touches sudo, pf, lulu, or /etc.
#   It writes generated files under examples/.generated/ and may install
#   ~/.config/{claude-code,opencode}/settings.json with a .bak backup.
# - sandbox-exec, tart, orbstack get printed Equivalent CLI lines instead
#   of being executed by `apply`.
#
# References:
# - CUE: https://cuelang.org/docs/
# - Envoy SNI listener: https://www.envoyproxy.io/docs/envoy/latest/configuration/listeners/listener_filters/tls_inspector_filter
# - terminal-notifier: https://github.com/julienXX/terminal-notifier

set -g SLOP_ISOLATE_PY (path resolve (dirname (status filename)))"/_py/isolation.py"

function __slop_isolate_examples
    # BEGIN AUTOGEN: examples section="Unified isolation config"
    echo 'Install dependencies:'
    echo '  brew install cue-lang/tap/cue'
    echo '  brew install --cask terminal-notifier   # optional: macOS deny notifications'
    echo
    echo 'Pick a preset and validate it:'
    echo '  source scripts/slop-isolate.fish'
    echo '  slop-isolate presets list'
    echo '  slop-isolate presets show claude-code'
    echo
    echo 'Author your config (extend a preset via the extras struct):'
    echo '  cat > .isolation.cue <<\'CUE\''
    echo '  package isolation'
    echo '  import "slop.dev/isolation/presets"'
    echo '  isolation: presets.#ClaudeCode & {'
    echo '      extras: "allow-domains": ["github.example.internal"]'
    echo '      tool: pf: "domain-fallback": "fail"'
    echo '  }'
    echo '  CUE'
    echo '  slop-isolate validate .isolation.cue'
    echo
    echo 'Compile to one or every adapter:'
    echo '  slop-isolate compile .isolation.cue --adapter sandbox-exec --out ./out'
    echo '  slop-isolate compile .isolation.cue --adapter envoy --out ./out'
    echo '  slop-isolate compile .isolation.cue   # uses adapters.enabled list'
    echo
    echo 'Apply (bounded — never touches sudo/pf/lulu):'
    echo '  slop-isolate apply .isolation.cue --yes'
    echo
    echo 'Boot the interactive proxy and approve flows on the fly:'
    echo '  slop-isolate proxy start'
    echo '  slop-isolate approve --once api.example.com'
    echo '  slop-isolate denials --since 10m'
    echo '  slop-isolate proxy stop'
    # END AUTOGEN: examples
end

function __slop_isolate_help
    echo "slop-isolate — unified isolation policy compiler (CUE → adapters)"
    echo ""
    echo "Description:"
    echo "  Author one CUE file describing network/filesystem/process intent."
    echo "  Compile it to per-tool configs (sandbox-exec, docker-compose,"
    echo "  envoy + coredns + notifier, claude-code, opencode, lulu, pf, ...)."
    echo "  Default presets cover ten agent solutions; extend any preset via the"
    echo "  `extras` struct in your isolation.cue."
    echo ""
    echo "Usage:"
    echo "  slop-isolate validate <config.cue>"
    echo "  slop-isolate compile  <config.cue> --adapter <name> [--out <dir>] [--strict]"
    echo "  slop-isolate apply    <config.cue> [--adapters a,b] [--dry-run] [--yes]"
    echo "  slop-isolate presets  list"
    echo "  slop-isolate presets  show <name>"
    echo "  slop-isolate proxy    start|stop|status [--mitm]"
    echo "  slop-isolate approve  --once|--always <host[:port]>"
    echo "  slop-isolate denials  [--since 5m]"
    echo "  slop-isolate here     <subcommand>           (auto-picks .isolation.cue)"
    echo "  slop-isolate tui                              (interactive launcher; requires gum)"
    echo "  slop-isolate help"
    echo ""
    echo "Adapters:"
    echo "  sandbox-exec   docker-compose   squid   envoy   pf   lulu"
    echo "  claude-code-settings   opencode-settings   ag2-executor   tart   orbstack"
    echo ""
    echo "Presets:"
    echo "  any-agent   claude-code   opencode   crewai   pydantic-ai   ag2"
    echo "  openclaw    zeroclaw      nous-hermes-local   nous-hermes-remote"
    echo ""
    echo "Examples (synced from README → 'Unified isolation config'):"
    __slop_isolate_examples
    echo ""
    echo "Notes:"
    echo "  - apply never touches sudo / pf / lulu / /etc; those adapters print Equivalent CLI."
    echo "  - The Envoy stack defaults to SNI-only; opt into MITM with --mitm (per-stack CA)."
    echo "  - Full reference: README.md → 'Unified isolation config'."
end

function __slop_isolate_help_to_stderr
    __slop_isolate_help 1>&2
end

function __slop_isolate_require_tools
    if not command -sq uv
        echo "Error: 'uv' is required (Python helpers run via uv run --script)." 1>&2
        echo "Install: brew install uv" 1>&2
        return 1
    end
    if not command -sq cue
        echo "Error: 'cue' is required (CUE schemas validate isolation policy)." 1>&2
        echo "Install: brew install cue-lang/tap/cue" 1>&2
        return 1
    end
end

function __slop_isolate_repo_root
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end
    git -C "$cwd" rev-parse --show-toplevel 2>/dev/null
end

function __slop_isolate_here
    set -l root (__slop_isolate_repo_root)
    if test -z "$root"
        echo "Error: 'here' could not infer the repo root from \$ATB_USER_PWD or \$PWD." 1>&2
        echo "Pass the config explicitly: slop-isolate <subcommand> <config.cue>" 1>&2
        return 1
    end
    set -l candidate "$root/.isolation.cue"
    if not test -f "$candidate"
        echo "Error: no .isolation.cue at $root" 1>&2
        echo "Create one (see examples/isolation/examples/user-config.cue)." 1>&2
        return 1
    end
    echo "$candidate"
end

function __slop_isolate_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __slop_isolate_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "Install: brew install gum" 1>&2
        return 1
    end
    __slop_isolate_require_tools; or return 1

    gum style --bold --foreground 212 "slop-isolate — interactive launcher"
    set -l preset (uv run --script "$SLOP_ISOLATE_PY" presets list | gum choose --header "Pick a preset:")
    test -z "$preset"; and return 0
    set -l adapter (gum choose --header "Pick an adapter:" \
        sandbox-exec docker-compose squid envoy pf lulu \
        claude-code-settings opencode-settings ag2-executor tart orbstack)
    test -z "$adapter"; and return 0

    set -l tmp (mktemp -d)
    echo "package isolation
import \"slop.dev/isolation/presets\"
isolation: presets.#"(echo $preset | string upper -1)"" >"$tmp/preset.cue"
    set -l cmd "slop-isolate compile $tmp/preset.cue --adapter $adapter --out $tmp"
    __slop_isolate_show_cli "$cmd"
    eval $cmd
end

function slop-isolate --description "Unified isolation policy compiler (CUE → adapters)"
    if test (count $argv) -eq 0
        __slop_isolate_help
        return 0
    end

    set -l subcmd "$argv[1]"
    set -e argv[1]

    switch "$subcmd"
        case help --help -h
            __slop_isolate_help
            return 0
        case tui
            __slop_isolate_tui
            return $status
        case here
            set -l cfg (__slop_isolate_here)
            or return 1
            if test (count $argv) -eq 0
                echo "Error: 'here' needs a subcommand (validate|compile|apply|...)." 1>&2
                return 1
            end
            set -l next "$argv[1]"
            set -e argv[1]
            slop-isolate "$next" "$cfg" $argv
            return $status
        case validate compile apply presets proxy approve denials
            __slop_isolate_require_tools; or return 1
            uv run --script "$SLOP_ISOLATE_PY" "$subcmd" $argv
            return $status
        case '*'
            echo "Error: Unknown command: $subcmd" 1>&2
            echo "" 1>&2
            __slop_isolate_help_to_stderr
            return 1
    end
end
