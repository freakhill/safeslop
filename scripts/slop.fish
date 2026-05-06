#!/usr/bin/env fish

# Purpose:
# - Thin fish wrapper around the modern Textual-based TUI in
#   scripts/_py/slop_tui.py. Keeps `slop`, `slop help`, `slop --version`,
#   and the unknown-arg error path on the fast fish path so they do not
#   require uv. Interactive launches exec the Python TUI via `uv run --script`
#   which auto-installs Textual on first run via PEP-723.
#
# TLS / first-run install:
# - On machines behind a TLS-intercepting proxy (Cloudflare WARP, Zscaler,
#   corporate MITM) uv's bundled rustls trust store does not trust the
#   intercepting CA, so PyPI fetches fail with `UnknownIssuer`. We try
#   four strategies in order and stop at the first one that succeeds:
#     1. uv with rustls only — fine on clean networks.
#     2. UV_NATIVE_TLS=1 (OS trust store, includes user-installed CAs).
#     3. SSL_CERT_FILE=/etc/ssl/cert.pem (forces macOS bundle).
#     4. --allow-insecure-host pypi.org files.pythonhosted.org — opt-in
#        last resort that disables cert verification for those hosts.
#   Strategy 4 only runs when SLOP_INSECURE_HOSTS=1 is set OR `slop --check`
#   is asked for one explicitly. Interactive launches stop after step 3
#   and tell the user to set SLOP_INSECURE_HOSTS=1 if they accept the risk.
#
# References:
# - Textual: https://textual.textualize.io/
# - uv PEP-723: https://docs.astral.sh/uv/guides/scripts/
# - uv TLS flags: https://docs.astral.sh/uv/reference/cli/#uv-run

set -g SLOP_VERSION "0.2"
set -g SLOP_REPO_ROOT (path resolve (dirname (status filename)))/..
set -g SLOP_TUI_PY "$SLOP_REPO_ROOT/scripts/_py/slop_tui.py"
set -g SLOP_ORCHESTRATOR_PY "$SLOP_REPO_ROOT/scripts/_py/slop_orchestrator.py"

# Where the user actually invoked us from. The conf.d wrapper sets this
# before exec'ing slop.fish so resolve-from-cwd doesn't read scripts/.
function __slop_user_pwd
    if set -q ATB_USER_PWD; and test -n "$ATB_USER_PWD"
        echo "$ATB_USER_PWD"
    else
        echo "$PWD"
    end
end

# Walk up from the user's pwd looking for slop.cue. Echo the path of
# the first match, or empty if none. We mirror the orchestrator's
# resolve logic so `slop run` from a subdir Just Works.
function __slop_find_cue
    set -l cur (__slop_user_pwd)
    while test -n "$cur"; and test "$cur" != "/"
        if test -f "$cur/slop.cue"
            echo "$cur/slop.cue"
            return 0
        end
        set cur (dirname "$cur")
    end
    return 1
end

function __slop_help
    echo "slop — interactive launcher for the agentic_tactical_boots toolkit"
    echo ""
    echo "Description:"
    echo "  A modern Textual-based TUI that wraps every tool in this repo."
    echo "  Each action shows the equivalent CLI before executing, so you can"
    echo "  learn the underlying commands and copy them into scripts."
    echo ""
    echo "Usage:"
    echo "  slop                  If ./slop.cue exists, run its default profile;"
    echo "                        otherwise launch the global TUI."
    echo "  slop run [<profile>]  Run a profile declared in slop.cue."
    echo "  slop validate         Validate slop.cue against the bundled schema."
    echo "  slop list             List declared profiles + their state."
    echo "  slop down             Run on-exit hooks for active profiles."
    echo "  slop --check          Probe whether the Textual install path works."
    echo "  slop help             Show this message and exit."
    echo "  slop --version        Print version."
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
    echo "Environment:"
    echo "  SLOP_INSECURE_HOSTS=1  Disable TLS cert verification for pypi.org and"
    echo "                         files.pythonhosted.org. Use only on machines"
    echo "                         behind an intercepting proxy whose CA you"
    echo "                         cannot install into the system trust store."
    echo ""
    echo "Examples:"
    echo "  # Launch the TUI"
    echo "  slop"
    echo ""
    echo "  # Probe dependency install path (useful behind corporate proxies)"
    echo "  slop --check"
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

# Try one of the four TLS strategies. Each strategy invokes the wrapped
# Python script with whatever uv flags / env vars apply. Strategies that
# rely on env vars set them in a child env so they do not leak into the
# parent shell's environment.
function __slop_try_strategy --argument-names label
    set -l args $argv[2..-1]
    echo "  trying: $label" 1>&2
    if env $args uv run --script --quiet "$SLOP_TUI_PY" --self-check 2>&1
        return 0
    end
    return 1
end

function __slop_check
    __slop_require_uv; or return 1

    echo "slop --check: probing the Textual install path…" 1>&2

    # Strategy 1: uv defaults (rustls).
    if __slop_try_strategy "uv defaults"
        echo "OK (default rustls trust store)"
        return 0
    end

    # Strategy 2: native TLS, OS trust store.
    if __slop_try_strategy "UV_NATIVE_TLS=1" UV_NATIVE_TLS=1
        echo "OK (OS trust store)"
        return 0
    end

    # Strategy 3: explicit cert bundle (macOS).
    if test -f /etc/ssl/cert.pem
        if __slop_try_strategy "SSL_CERT_FILE=/etc/ssl/cert.pem" \
            UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem
            echo "OK (system cert bundle)"
            return 0
        end
    end

    # Strategy 4: --allow-insecure-host (only at user request via SLOP_INSECURE_HOSTS=1).
    if test "$SLOP_INSECURE_HOSTS" = "1"
        echo "  trying: --allow-insecure-host pypi.org files.pythonhosted.org" 1>&2
        if uv run --script --quiet \
            --allow-insecure-host pypi.org \
            --allow-insecure-host files.pythonhosted.org \
            "$SLOP_TUI_PY" --self-check 2>&1
            echo "OK (insecure-host bypass; TLS verification disabled for pypi.org)"
            return 0
        end
    else
        echo "  skipped: --allow-insecure-host (set SLOP_INSECURE_HOSTS=1 to enable)" 1>&2
    end

    echo "" 1>&2
    echo "Error: every TLS strategy failed. Likely an intercepting proxy whose CA" 1>&2
    echo "is not in the system keychain. Options:" 1>&2
    echo "  - Install your proxy's CA into the macOS login keychain (System Roots)." 1>&2
    echo "  - Set SLOP_INSECURE_HOSTS=1 and rerun slop --check (disables verification" 1>&2
    echo "    for pypi.org and files.pythonhosted.org only — accept this risk if you" 1>&2
    echo "    trust the network you are on)." 1>&2
    echo "  - Point SSL_CERT_FILE at a CA bundle that includes the intercepting CA." 1>&2
    return 2
end

# Pick the right strategy for the interactive launch. Mirrors __slop_check's
# preference order but stops before the insecure-host bypass unless the user
# opted in via SLOP_INSECURE_HOSTS=1. Sets uv-related env vars in the
# current process and execs uv so the TUI inherits a normal stdio attachment.
function __slop_exec_tui
    if not set -q UV_NATIVE_TLS
        set -gx UV_NATIVE_TLS 1
    end
    if not set -q SSL_CERT_FILE; and test -f /etc/ssl/cert.pem
        set -gx SSL_CERT_FILE /etc/ssl/cert.pem
    end
    if test "$SLOP_INSECURE_HOSTS" = "1"
        exec uv run --script \
            --allow-insecure-host pypi.org \
            --allow-insecure-host files.pythonhosted.org \
            "$SLOP_TUI_PY"
    end
    exec uv run --script "$SLOP_TUI_PY"
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
        case --check
            __slop_check
            exit $status
        case run validate list down
            # Orchestrator subcommands. Always go to the orchestrator
            # whether or not slop.cue is present — `slop validate` for
            # instance gives a useful "no slop.cue found" error.
            __slop_require_uv; or exit 1
            exec uv run --script "$SLOP_ORCHESTRATOR_PY" $argv
        case '*'
            echo "Error: unknown argument '$argv[1]'" 1>&2
            echo "" 1>&2
            __slop_help 1>&2
            exit 1
    end
end

__slop_require_uv; or exit 1

# No args: if there is a slop.cue at or above the user's cwd, run the
# default profile through the orchestrator instead of the Textual TUI.
# Bare `slop` keeps doing the menu when no slop.cue is around — same UX
# users had before this phase.
if set -l cue (__slop_find_cue)
    exec uv run --script "$SLOP_ORCHESTRATOR_PY" run
end

__slop_exec_tui
