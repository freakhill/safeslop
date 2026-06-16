#!/usr/bin/env fish

# Purpose:
# - Provide an optional macOS local sandbox layer using sandbox-exec.
# - Keep this as defense-in-depth when full containers/VMs are not practical.
#
# Safety/model notes:
# - Default path scope is current working directory only.
# - Default network policy is strict-egress (deny outbound network in profile).
# - This is not equivalent to VM/container isolation.
#
# References:
# - sandbox-exec man page: https://www.manpagez.com/man/1/sandbox-exec/
# - Apple Platform Security: https://support.apple.com/guide/security/welcome/web

function __macos_sandbox_examples
    # BEGIN AUTOGEN: examples section="How to run a command under the sandbox-exec boundary (macOS)"
    echo 'Load helper:'
    echo '  source scripts/slop-macos-sandbox.fish'
    echo
    echo 'Run command with default cwd scope and strict egress deny:'
    echo '  slop-macos-sandbox run -- /bin/pwd'
    echo
    echo 'Run command with repository-root scope (alternative to default cwd scope):'
    echo '  slop-macos-sandbox run --repo-root-access -- /usr/bin/env ls'
    echo '  slop-macos-sandbox run --path-scope repo-root -- /usr/bin/env ls'
    echo
    echo 'Use through the unified hub:'
    echo '  scripts/slop-sandboxctl.fish local run --repo-root-access -- /bin/pwd'
    echo
    echo 'Add explicit additional paths only when needed:'
    echo '  slop-macos-sandbox run --allow-read ~/.config --allow-write ./tmp -- /usr/bin/env ls'
    # END AUTOGEN: examples
end

function __macos_sandbox_help
    echo "slop-macos-sandbox — macOS local sandbox boundary (sandbox-exec)"
    echo ""
    echo "Description:"
    echo "  Run a command (or open a shell) under a tight sandbox-exec profile."
    echo "  Default path scope is the current working directory; default network"
    echo "  policy is strict-egress (deny network*) inside the profile."
    echo "  First-class local boundary for everyday agent and package work. For"
    echo "  untrusted code or URL-level network control, prefer the Docker/VM workflows."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-macos-sandbox.fish"
    echo "  slop-macos-sandbox run [options] -- <command...>"
    echo "  slop-macos-sandbox shell [options]"
    echo "  slop-macos-sandbox print-profile [options]"
    echo "  slop-macos-sandbox help"
    echo ""
    echo "Options:"
    echo "  --network-policy strict-egress|off   Deny or allow network in profile (default: strict-egress)."
    echo "  --path-scope cwd|repo-root           File r/w scope (default: cwd)."
    echo "  --repo-root-access                   Alias for --path-scope repo-root."
    echo "  --allow-read <path>                  Add an extra readable subpath. Repeatable."
    echo "  --allow-write <path>                 Add an extra writable subpath (also readable). Repeatable."
    echo "  --                                   End of options; everything after is the command."
    echo ""
    echo "Examples (synced from README → 'How to run a command under the sandbox-exec boundary (macOS)'):"
    __macos_sandbox_examples
    echo ""
    echo "Notes:"
    echo "  - sandbox-exec is Apple-deprecated (still works on current macOS); network control is coarse (no URL allowlist)."
    echo "  - print-profile is read-only — useful for auditing the generated policy before running it."
    echo "  - For broader isolation, see: scripts/slop-sandboxctl.fish docker ... or scripts/slop-sandboxctl.fish slop-brew-vm ..."
    echo "  - Full reference: README.md → 'How to run a command under the sandbox-exec boundary (macOS)'."
end

function __macos_sandbox_help_to_stderr
    __macos_sandbox_help 1>&2
end

function __macos_sandbox_require_support
    if not test (uname) = "Darwin"
        echo "Error: slop-macos-sandbox supports macOS only." 1>&2
        echo "" 1>&2
        __macos_sandbox_help_to_stderr
        return 1
    end

    if not command -q sandbox-exec
        echo "Error: sandbox-exec is not available on this system." 1>&2
        echo "" 1>&2
        __macos_sandbox_help_to_stderr
        return 1
    end
end

function __macos_sandbox_validate_policy --argument-names policy
    if not contains -- "$policy" strict-egress off
        echo "Error: Invalid --network-policy: $policy (allowed: strict-egress, off)" 1>&2
        echo "" 1>&2
        __macos_sandbox_help_to_stderr
        return 1
    end
end

function __macos_sandbox_validate_scope --argument-names scope
    if not contains -- "$scope" cwd repo-root
        echo "Error: Invalid --path-scope: $scope (allowed: cwd, repo-root)" 1>&2
        echo "" 1>&2
        __macos_sandbox_help_to_stderr
        return 1
    end
end

function __macos_sandbox_abs_path --argument-names candidate
    if string match -q -- '/*' "$candidate"
        echo "$candidate"
    else
        echo "$PWD/$candidate"
    end
end

function __macos_sandbox_escape_profile_path --argument-names raw_path
    set -l escaped (string replace -a '\\' '\\\\' -- "$raw_path")
    string replace -a '"' '\\"' -- "$escaped"
end

function __macos_sandbox_repo_root
    command git rev-parse --show-toplevel 2>/dev/null
end

function __macos_sandbox_build_profile --argument-names policy root_path
    set -l escaped_root (__macos_sandbox_escape_profile_path "$root_path")

    set -l profile
    set -a profile "(version 1)"
    set -a profile "(import \"system.sb\")"
    set -a profile "(allow process-exec)"
    set -a profile "(allow process-fork)"
    set -a profile "(allow signal (target self))"

    # Runtime/system reads are required for binaries, dynamic libs, and shell startup.
    for system_path in /System /usr /bin /sbin /Library /private/etc /etc /dev /var/db/timezone
        set -l escaped_system (__macos_sandbox_escape_profile_path "$system_path")
        set -a profile "(allow file-read* (subpath \"$escaped_system\"))"
    end

    set -a profile "(allow file-read* (literal \"/private/var/run/resolv.conf\"))"
    set -a profile "(allow file-read* (literal \"/private/var/run/utmpx\"))"

    set -a profile "(allow file-read* (subpath \"$escaped_root\"))"
    set -a profile "(allow file-write* (subpath \"$escaped_root\"))"

    # Commands and shells commonly need temporary directories even with tight path scope.
    for temp_path in /tmp /private/tmp /private/var/tmp
        set -l escaped_temp (__macos_sandbox_escape_profile_path "$temp_path")
        set -a profile "(allow file-read* (subpath \"$escaped_temp\"))"
        set -a profile "(allow file-write* (subpath \"$escaped_temp\"))"
    end

    for read_path in $__macos_sandbox_allow_read
        set -l abs_read (__macos_sandbox_abs_path "$read_path")
        set -l escaped_read (__macos_sandbox_escape_profile_path "$abs_read")
        set -a profile "(allow file-read* (subpath \"$escaped_read\"))"
    end

    for write_path in $__macos_sandbox_allow_write
        set -l abs_write (__macos_sandbox_abs_path "$write_path")
        set -l escaped_write (__macos_sandbox_escape_profile_path "$abs_write")
        set -a profile "(allow file-read* (subpath \"$escaped_write\"))"
        set -a profile "(allow file-write* (subpath \"$escaped_write\"))"
    end

    switch "$policy"
        case strict-egress
            set -a profile "(deny network*)"
        case off
            set -a profile "(allow network*)"
    end

    printf '%s\n' $profile
end

function __macos_sandbox_write_profile
    set -l profile_file (mktemp -t slop-macos-sandbox.XXXXXX.sb)
    if test $status -ne 0
        echo "Failed to create temporary sandbox profile file" 1>&2
        return 1
    end

    printf '%s\n' $__macos_sandbox_profile_lines > "$profile_file"
    echo "$profile_file"
end

function __macos_sandbox_parse_options
    set -g __macos_sandbox_policy strict-egress
    set -g __macos_sandbox_scope cwd
    set -g __macos_sandbox_scope_set false
    set -g __macos_sandbox_repo_root_access false
    set -g __macos_sandbox_allow_read
    set -g __macos_sandbox_allow_write

    set -l i 1
    while test $i -le (count $argv)
        set -l arg "$argv[$i]"
        switch "$arg"
            case --network-policy
                set -l next_i (math "$i + 1")
                if test $next_i -gt (count $argv)
                    echo "Error: --network-policy requires a value" 1>&2
                    echo "" 1>&2
                    __macos_sandbox_help_to_stderr
                    return 1
                end
                set __macos_sandbox_policy "$argv[$next_i]"
                set i (math "$i + 2")
                continue
            case '--network-policy=*'
                set __macos_sandbox_policy (string replace -- '--network-policy=' '' "$arg")
                set i (math "$i + 1")
                continue
            case --path-scope
                set -l next_i (math "$i + 1")
                if test $next_i -gt (count $argv)
                    echo "Error: --path-scope requires a value" 1>&2
                    echo "" 1>&2
                    __macos_sandbox_help_to_stderr
                    return 1
                end
                set __macos_sandbox_scope "$argv[$next_i]"
                set __macos_sandbox_scope_set true
                set i (math "$i + 2")
                continue
            case '--path-scope=*'
                set __macos_sandbox_scope (string replace -- '--path-scope=' '' "$arg")
                set __macos_sandbox_scope_set true
                set i (math "$i + 1")
                continue
            case --repo-root-access
                set __macos_sandbox_repo_root_access true
                set i (math "$i + 1")
                continue
            case --allow-read
                set -l next_i (math "$i + 1")
                if test $next_i -gt (count $argv)
                    echo "Error: --allow-read requires a value" 1>&2
                    echo "" 1>&2
                    __macos_sandbox_help_to_stderr
                    return 1
                end
                set -a __macos_sandbox_allow_read "$argv[$next_i]"
                set i (math "$i + 2")
                continue
            case '--allow-read=*'
                set -a __macos_sandbox_allow_read (string replace -- '--allow-read=' '' "$arg")
                set i (math "$i + 1")
                continue
            case --allow-write
                set -l next_i (math "$i + 1")
                if test $next_i -gt (count $argv)
                    echo "Error: --allow-write requires a value" 1>&2
                    echo "" 1>&2
                    __macos_sandbox_help_to_stderr
                    return 1
                end
                set -a __macos_sandbox_allow_write "$argv[$next_i]"
                set i (math "$i + 2")
                continue
            case '--allow-write=*'
                set -a __macos_sandbox_allow_write (string replace -- '--allow-write=' '' "$arg")
                set i (math "$i + 1")
                continue
            case --
                set -g __macos_sandbox_remaining $argv[(math "$i + 1")..-1]
                return 0
            case '*'
                if string match -q -- '-*' "$arg"
                    echo "Error: Unknown option: $arg" 1>&2
                    echo "" 1>&2
                    __macos_sandbox_help_to_stderr
                    return 1
                end
                set -g __macos_sandbox_remaining $argv[$i..-1]
                return 0
        end
    end

    set -g __macos_sandbox_remaining
    return 0
end

function __macos_sandbox_prepare
    __macos_sandbox_validate_policy "$__macos_sandbox_policy"; or return 1

    if test "$__macos_sandbox_repo_root_access" = true
        if test "$__macos_sandbox_scope_set" = true; and test "$__macos_sandbox_scope" != repo-root
            echo "Error: --repo-root-access conflicts with --path-scope $__macos_sandbox_scope" 1>&2
            echo "" 1>&2
            __macos_sandbox_help_to_stderr
            return 1
        end
        set __macos_sandbox_scope repo-root
    end

    __macos_sandbox_validate_scope "$__macos_sandbox_scope"; or return 1

    set -l root_path "$PWD"
    if test "$__macos_sandbox_scope" = repo-root
        set root_path (__macos_sandbox_repo_root)
        if test -z "$root_path"
            echo "Error: --path-scope repo-root requires running inside a git repository" 1>&2
            echo "" 1>&2
            __macos_sandbox_help_to_stderr
            return 1
        end
    end

    set -g __macos_sandbox_root_path "$root_path"

    set -l profile_lines (__macos_sandbox_build_profile \
        "$__macos_sandbox_policy" \
        "$__macos_sandbox_root_path")

    if test $status -ne 0
        return 1
    end

    set -g __macos_sandbox_profile_lines $profile_lines
    set -g __macos_sandbox_profile_file (__macos_sandbox_write_profile)
    if test $status -ne 0
        return 1
    end
end

function __macos_sandbox_cleanup
    if set -q __macos_sandbox_profile_file; and test -n "$__macos_sandbox_profile_file"; and test -f "$__macos_sandbox_profile_file"
        rm -f "$__macos_sandbox_profile_file"
    end
end

function slop-macos-sandbox --description "Run commands in optional macOS sandbox-exec profile"
    if test (count $argv) -eq 0
        __macos_sandbox_help
        return 0
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    switch "$cmd"
        case help --help -h
            __macos_sandbox_help
            return 0
    end

    __macos_sandbox_require_support; or return 1
    __macos_sandbox_parse_options $argv; or return 1
    __macos_sandbox_prepare; or return 1

    switch "$cmd"
        case run
            if test (count $__macos_sandbox_remaining) -eq 0
                echo "Error: slop-macos-sandbox run requires a <command> after the options (use -- to separate)." 1>&2
                echo "" 1>&2
                __macos_sandbox_help_to_stderr
                __macos_sandbox_cleanup
                return 1
            end
            sandbox-exec -f "$__macos_sandbox_profile_file" -- $__macos_sandbox_remaining
            set -l cmd_status $status
            __macos_sandbox_cleanup
            return $cmd_status
        case shell
            sandbox-exec -f "$__macos_sandbox_profile_file" -- /bin/zsh -f
            set -l cmd_status $status
            __macos_sandbox_cleanup
            return $cmd_status
        case print-profile
            printf '%s\n' $__macos_sandbox_profile_lines
            __macos_sandbox_cleanup
            return 0
        case '*'
            echo "Error: Unknown command: $cmd" 1>&2
            echo "" 1>&2
            __macos_sandbox_help_to_stderr
            __macos_sandbox_cleanup
            return 1
    end
end
