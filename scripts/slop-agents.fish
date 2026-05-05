#!/usr/bin/env fish

# Purpose:
# - One-step launchers for Claude Code (`claude`) and OpenCode (`opencode`)
#   that apply the repo's bundled defaults if no per-project settings are
#   already in place. Drops you into the agent's REPL in the right cwd so
#   the agent's own per-project precedence takes over.
#
# Settings precedence (per agent):
#   1. $cwd/.claude/settings.json    (or $cwd/opencode.json)
#   2. $repo_root/.claude/settings.json (or $repo_root/opencode.json)
#   3. None — exec from $cwd; user-level settings (if any) apply.
#
# `slop-agents seed <agent>` (or `slop-agents <agent> --seed`) writes the
# bundled defaults — the same JSON the slop-isolate compiler emits from
# the claude-code / opencode CUE presets, mirrored as golden fixtures
# under examples/isolation/fixtures/. We copy the fixtures verbatim so
# we do not require `cue` on PATH for first-time setup.
#
# References:
# - Claude Code settings hierarchy: https://docs.claude.com/en/docs/claude-code/settings
# - OpenCode config:                https://opencode.ai/docs/configuration

set -g LLM_AGENTS_REPO_ROOT (path resolve (dirname (status filename)))/..
set -g LLM_AGENTS_FIXTURE_CC "$LLM_AGENTS_REPO_ROOT/examples/isolation/fixtures/claude-code/claude-code.settings.json"
set -g LLM_AGENTS_FIXTURE_OC "$LLM_AGENTS_REPO_ROOT/examples/isolation/fixtures/opencode/opencode.json"

function __slop_agents_examples
    # BEGIN AUTOGEN: examples section="Launch agents with defaults"
    echo 'Source the helper:'
    echo '  source scripts/slop-agents.fish'
    echo
    echo 'One-time, write secure defaults to the repo root:'
    echo '  slop-agents seed all'
    echo
    echo 'Drop into Claude Code with those defaults applied:'
    echo '  slop-agents claude'
    echo
    echo 'Drop into OpenCode with those defaults applied:'
    echo '  slop-agents opencode'
    # END AUTOGEN: examples
end

function __slop_agents_help
    echo "slop-agents — one-step launchers for Claude Code / OpenCode with our defaults"
    echo ""
    echo "Description:"
    echo "  Drops you into the agent's REPL in the right cwd. Settings come"
    echo "  from the first match in this precedence order:"
    echo "    1. \$cwd/.claude/settings.json     (or \$cwd/opencode.json)"
    echo "    2. \$repo_root/.claude/settings.json (or \$repo_root/opencode.json)"
    echo "    3. user-level (if any)"
    echo ""
    echo "  --seed copies the bundled defaults to the repo root if no"
    echo "  override is already present. The bundled JSON is the compile"
    echo "  output of the claude-code / opencode CUE presets — see"
    echo "  examples/isolation/presets/."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-agents.fish"
    echo "  slop-agents claude   [--seed]"
    echo "  slop-agents opencode [--seed]"
    echo "  slop-agents seed     claude|opencode|all"
    echo "  slop-agents help"
    echo ""
    echo "Examples (synced from README → 'Launch agents with defaults'):"
    __slop_agents_examples
    echo ""
    echo "Notes:"
    echo "  - Requires \`claude\` (npm i -g @anthropic-ai/claude-code) or"
    echo "    \`opencode\` (npm i -g opencode-ai) on PATH for the launcher"
    echo "    paths; \`seed\` only needs the fixture files (always present)."
    echo "  - We never overwrite an existing override file. Move yours aside"
    echo "    if you want to start over from defaults."
    echo "  - For container-side use: scripts/slop-agent-sandbox-tools.fish"
    echo "    shell, then type \`claude\` or \`opencode\` once inside."
end

function __slop_agents_help_to_stderr
    __slop_agents_help 1>&2
end

function __slop_agents_repo_root
    # Mirrors __slop_isolate_repo_root: prefer ATB_USER_PWD (set by the
    # conf.d wrapper), fall back to PWD, ask git for the toplevel.
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end
    command git -C "$cwd" rev-parse --show-toplevel 2>/dev/null
end

# Resolve which directory's settings should win for an agent.
# Args: agent="claude"|"opencode"
# Echoes the directory whose settings we will end up using (cwd or repo
# root or empty if no override is present anywhere). The caller cd's there
# before exec so the agent's own per-project precedence picks it up.
function __slop_agents_resolve_root --argument-names agent
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end
    set -l rel
    switch "$agent"
        case claude
            set rel ".claude/settings.json"
        case opencode
            set rel "opencode.json"
        case '*'
            return 1
    end
    if test -f "$cwd/$rel"
        echo "$cwd"
        return 0
    end
    set -l root (__slop_agents_repo_root)
    if test -n "$root"; and test -f "$root/$rel"
        echo "$root"
        return 0
    end
    # No override anywhere; signal that by echoing nothing and returning 0
    # so the caller can decide what to do (use cwd, exec anyway, etc.).
    return 0
end

function __slop_agents_seed_one --argument-names agent
    set -l root (__slop_agents_repo_root)
    if test -z "$root"
        echo "Error: 'seed' could not infer the repo root from \$ATB_USER_PWD or \$PWD." 1>&2
        echo "  Run from inside a git checkout, or copy the fixture file by hand:" 1>&2
        switch "$agent"
            case claude
                echo "    cp '$LLM_AGENTS_FIXTURE_CC' /your/repo/.claude/settings.json" 1>&2
            case opencode
                echo "    cp '$LLM_AGENTS_FIXTURE_OC' /your/repo/opencode.json" 1>&2
        end
        return 1
    end
    set -l fixture
    set -l target
    switch "$agent"
        case claude
            set fixture "$LLM_AGENTS_FIXTURE_CC"
            set target "$root/.claude/settings.json"
        case opencode
            set fixture "$LLM_AGENTS_FIXTURE_OC"
            set target "$root/opencode.json"
        case '*'
            echo "Error: unknown agent '$agent' (use claude|opencode|all)." 1>&2
            return 1
    end
    if not test -f "$fixture"
        echo "Error: bundled defaults missing at $fixture" 1>&2
        echo "  Repo install may be incomplete. Re-clone or re-run \`./install\`." 1>&2
        return 1
    end
    if test -f "$target"
        echo "$agent: $target already present — leaving as-is."
        return 0
    end
    mkdir -p (dirname "$target")
    or return 1
    cp "$fixture" "$target"
    or return 1
    echo "$agent: wrote bundled defaults to $target"
end

function __slop_agents_seed --argument-names which
    if test -z "$which"
        echo "Error: 'seed' requires an argument (claude|opencode|all)." 1>&2
        return 1
    end
    switch "$which"
        case claude
            __slop_agents_seed_one claude
        case opencode
            __slop_agents_seed_one opencode
        case all
            __slop_agents_seed_one claude
            and __slop_agents_seed_one opencode
        case '*'
            echo "Error: unknown seed target '$which' (use claude|opencode|all)." 1>&2
            return 1
    end
end

function __slop_agents_launch --argument-names agent
    set -l seed "false"
    set -l args $argv[2..-1]
    for a in $args
        switch "$a"
            case --seed
                set seed "true"
            case '*'
                echo "Error: unknown argument '$a'." 1>&2
                __slop_agents_help_to_stderr
                return 1
        end
    end

    # Pre-flight: agent CLI on PATH?
    set -l bin
    set -l install_hint
    switch "$agent"
        case claude
            set bin "claude"
            set install_hint "npm install -g @anthropic-ai/claude-code"
        case opencode
            set bin "opencode"
            set install_hint "npm install -g opencode-ai"
    end
    if not command -sq "$bin"
        echo "Error: '$bin' not found on PATH." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  $install_hint" 1>&2
        echo "" 1>&2
        echo "Or use the container-side flow:" 1>&2
        echo "  slop-agent-sandbox-tools shell    # then type '$bin' inside" 1>&2
        return 1
    end

    if test "$seed" = "true"
        __slop_agents_seed_one "$agent"
        or return 1
    end

    # Resolve where to run from (so agent's per-project settings win).
    set -l root (__slop_agents_resolve_root "$agent")
    if test -z "$root"
        # Nothing to apply; just exec from cwd.
        set root "$PWD"
        if test -n "$ATB_USER_PWD"
            set root "$ATB_USER_PWD"
        end
    end

    echo "Equivalent CLI:"
    echo "  cd '$root' && $bin"
    echo ""

    # cd before exec so Claude Code / OpenCode pick up the per-project
    # settings file relative to cwd.
    cd "$root"
    or return 1
    exec "$bin"
end

function slop-agents --description "Launch Claude Code / OpenCode with our defaults"
    if test (count $argv) -eq 0
        __slop_agents_help
        return 0
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    switch "$cmd"
        case help --help -h
            __slop_agents_help
            return 0
        case claude opencode
            __slop_agents_launch "$cmd" $argv
        case seed
            __slop_agents_seed $argv[1]
        case '*'
            echo "Error: unknown subcommand '$cmd'." 1>&2
            echo "" 1>&2
            __slop_agents_help_to_stderr
            return 1
    end
end
