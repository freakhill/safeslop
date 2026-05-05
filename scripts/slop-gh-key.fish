#!/usr/bin/env fish

# Purpose:
# - Create short-lived, repo-scoped GitHub deploy keys for agent usage.
# - Separate RO and RW identities to minimize blast radius.
# - Make revocation and SSH config cleanup routine and scriptable.
#
# Why deploy keys:
# - They are scoped to a single repository and can be read-only.
#
# References:
# - GitHub deploy keys: https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys
# - GitHub REST repo keys API: https://docs.github.com/en/rest/deploy-keys/deploy-keys
# - OpenSSH config: https://man.openbsd.org/ssh_config

set -g LLM_GH_KEY_PREFIX "llm-agent"
set -g LLM_GH_KEY_DIR "$HOME/.ssh"
set -g LLM_GH_KEY_TTL "24h"
# Python helpers are run via uv with PEP-723 inline metadata so the interpreter
# version and dependency set are pinned at the script level. See scripts/_py/.
set -g LLM_GH_PY (path resolve (dirname (status filename)))"/_py/llm_github_keys.py"

function __llm_gh_usage
    echo "Usage:"
    echo "  source scripts/slop-gh-key.fish"
    echo "  slop-gh-key create --repo owner/repo --access ro|rw [--ttl 24h] [--name label]"
    echo "  slop-gh-key create-pair --repo owner/repo [--ttl 24h] [--name label]"
    echo "  slop-gh-key list --repo owner/repo"
    echo "  slop-gh-key revoke --repo owner/repo --id <key-id>"
    echo "  slop-gh-key revoke-by-title --repo owner/repo --match <regex> [--yes]"
    echo "  slop-gh-key revoke-expired --repo owner/repo [--yes]"
    echo "  slop-gh-key print-ssh-config --ro-key <path> --rw-key <path> [--host-prefix github-llm]"
    echo "  slop-gh-key install-ssh-config --repo owner/repo --name <label> --ro-key <path> --rw-key <path> [--host-prefix github-llm]"
    echo "  slop-gh-key uninstall-ssh-config [--repo owner/repo --name <label> | --marker <marker-regex>] [--yes]"
    echo ""
    echo "Repo-aware shortcuts (infer --repo from cwd's git origin):"
    echo "  slop-gh-key here create-pair [--ttl 24h] [--name label] [--no-install-config]"
    echo "  slop-gh-key here list"
    echo "  slop-gh-key here revoke <key-id>"
    echo "  slop-gh-key here cleanup            (revoke-expired --yes for current repo)"
    echo "  slop-gh-key here revoke-all         (revoke-by-title '^$LLM_GH_KEY_PREFIX:' --yes)"
    echo ""
    echo "TUI:"
    echo "  slop-gh-key tui                     (interactive launcher; requires gum)"
    echo ""
    echo "Notes:"
    echo "  - Titles embed expiry as: exp=<UTC-ISO8601>."
    echo "  - Deploy keys are repo-scoped and need repo admin rights."
    echo "  - 'here' supports github.com URLs and 'github-*' ssh-config aliases."
end

function __llm_gh_repo_slug --argument-names repo
    string replace -a '/' '-' -- "$repo"
end

function __llm_gh_print_alias_block --argument-names host_prefix key_path
    echo "Host $host_prefix"
    echo "  HostName github.com"
    echo "  User git"
    echo "  IdentityFile $key_path"
    echo "  IdentitiesOnly yes"
    echo ""
end

function __llm_gh_render_config --argument-names ro_key rw_key host_prefix
    __llm_gh_print_alias_block "$host_prefix-ro" "$ro_key"
    __llm_gh_print_alias_block "$host_prefix-rw" "$rw_key"
end

function __llm_gh_install_config --argument-names repo name ro_key rw_key host_prefix
    # Marker blocks are used so uninstall is deterministic and safe. We avoid
    # editing unrelated ~/.ssh/config lines.
    if test -z "$repo"; or test -z "$name"
        echo "install-ssh-config requires --repo and --name" 1>&2
        return 1
    end

    if not test -f "$ro_key"
        echo "Missing --ro-key file: $ro_key" 1>&2
        return 1
    end

    if not test -f "$rw_key"
        echo "Missing --rw-key file: $rw_key" 1>&2
        return 1
    end

    mkdir -p "$LLM_GH_KEY_DIR"
    chmod 700 "$LLM_GH_KEY_DIR"

    set -l config_file "$LLM_GH_KEY_DIR/config"
    if not test -f "$config_file"
        touch "$config_file"
    end
    chmod 600 "$config_file"

    set -l stamp (date -u +%Y%m%dT%H%M%SZ)
    set -l slug (__llm_gh_repo_slug "$repo")
    set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
    set -l marker "slop-gh-key:$slug:$safe_name:$stamp"

    {
        echo ""
        echo "# BEGIN $marker"
        __llm_gh_render_config "$ro_key" "$rw_key" "$host_prefix"
        echo "# END $marker"
    } >> "$config_file"

    echo "Added SSH aliases to $config_file"
    echo "  RO host alias: $host_prefix-ro"
    echo "  RW host alias: $host_prefix-rw"
    echo "  marker: $marker"
end

function __llm_gh_uninstall_config --argument-names repo name marker yes
    # Remove only our own marker blocks; never rewrite arbitrary SSH config.
    set -l config_file "$LLM_GH_KEY_DIR/config"
    if not test -f "$config_file"
        echo "No SSH config file found at $config_file"
        return 0
    end

    set -l pattern ""
    if test -n "$marker"
        set pattern "$marker"
    else
        set -l slug (__llm_gh_repo_slug "$repo")
        set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
        set pattern "^slop-gh-key:"$slug":"$safe_name":"
    end

    if not __llm_gh_confirm "Remove matching SSH config blocks from $config_file?" "$yes"
        return 1
    end

    set -l result (uv run --script "$LLM_GH_PY" ssh-config-uninstall "$config_file" "$pattern")

    set -l removed_count "$result[1]"
    if test "$removed_count" = "0"
        echo "No matching SSH config blocks found."
        return 0
    end

    echo "Removed $removed_count SSH config block(s) from $config_file"
    for m in $result[2..-1]
        echo "  - $m"
    end
end

function __llm_gh_require_tools
    # uv replaces bare python3 so the helper interpreter is pinned via PEP-723.
    for tool in gh ssh-keygen uv
        if not command -sq "$tool"
            echo "Missing required tool: $tool" 1>&2
            return 1
        end
    end

    gh auth status -h github.com >/dev/null 2>&1
    if test $status -ne 0
        echo "GitHub CLI is not authenticated. Run: gh auth login" 1>&2
        return 1
    end
end

function __llm_gh_validate_repo --argument-names repo
    if not string match -rq '^[^/]+/[^/]+$' -- "$repo"
        echo "Invalid --repo value. Expected owner/repo" 1>&2
        return 1
    end
end

function __llm_gh_ttl_to_iso --argument-names ttl
    if not string match -rq '^[0-9]+[mhdw]$' -- "$ttl"
        echo "Invalid --ttl '$ttl'. Use formats like 30m, 12h, 7d, 2w" 1>&2
        return 1
    end

    uv run --script "$LLM_GH_PY" ttl-to-iso "$ttl"
end

function __llm_gh_generate_key --argument-names access name expiry
    # ed25519 + higher KDF rounds: better brute-force resistance for local keys.
    set -l stamp (date -u +%Y%m%dT%H%M%SZ)
    set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
    set -l base "$LLM_GH_KEY_DIR/llm_agent_github_"$access"_"$safe_name"_"$stamp
    set -l title "$LLM_GH_KEY_PREFIX:"$access":"$safe_name":exp="$expiry

    if test -e "$base"; or test -e "$base.pub"
        echo "Refusing to overwrite existing key files: $base" 1>&2
        return 1
    end

    mkdir -p "$LLM_GH_KEY_DIR"
    chmod 700 "$LLM_GH_KEY_DIR"

    ssh-keygen -t ed25519 -a 100 -N "" -f "$base" -C "$title" >/dev/null
    if test $status -ne 0
        echo "ssh-keygen failed" 1>&2
        return 1
    end

    set -g __llm_gh_last_key_path "$base"
    set -g __llm_gh_last_key_title "$title"
    set -g __llm_gh_last_key_pub (string trim -- (string collect < "$base.pub"))
end

function __llm_gh_create_deploy_key --argument-names repo title pub access
    set -l read_only true
    if test "$access" = "rw"
        set read_only false
    end

    gh api --method POST "repos/$repo/keys" \
        -f title="$title" \
        -f key="$pub" \
        -F read_only="$read_only" \
        --jq '.id'
end

function __llm_gh_create_one --argument-names repo access ttl name
    # Title embeds expiry metadata so revoke-expired can be stateless.
    if not contains -- "$access" ro rw
        echo "Invalid --access '$access'. Use ro or rw" 1>&2
        return 1
    end

    set -l expiry (__llm_gh_ttl_to_iso "$ttl")
    if test $status -ne 0
        return 1
    end

    __llm_gh_generate_key "$access" "$name" "$expiry"
    if test $status -ne 0
        return 1
    end

    set -l key_path "$__llm_gh_last_key_path"
    set -l title "$__llm_gh_last_key_title"
    set -l pub "$__llm_gh_last_key_pub"

    set -l key_id (__llm_gh_create_deploy_key "$repo" "$title" "$pub" "$access")
    if test $status -ne 0
        echo "Failed to create deploy key on GitHub for $repo" 1>&2
        return 1
    end

    echo "Created $access deploy key"
    echo "  repo: $repo"
    echo "  id: $key_id"
    echo "  title: $title"
    echo "  private key: $key_path"
    echo "  public key: $key_path.pub"

    switch "$access"
        case ro
            set -g __llm_gh_last_key_path_ro "$key_path"
            set -g __llm_gh_last_key_id_ro "$key_id"
        case rw
            set -g __llm_gh_last_key_path_rw "$key_path"
            set -g __llm_gh_last_key_id_rw "$key_id"
    end
end

function __llm_gh_confirm --argument-names prompt no_prompt
    if test "$no_prompt" = "true"
        return 0
    end

    read -P "$prompt [y/N]: " answer
    switch (string lower -- "$answer")
        case y yes
            return 0
    end

    echo "Cancelled."
    return 1
end

function __llm_gh_list --argument-names repo
    gh api "repos/$repo/keys" --jq '.[] | "\(.id)\t\(if .read_only then "ro" else "rw" end)\t\(.created_at)\t\(.title)"'
end

function __llm_gh_revoke_by_ids --argument-names repo ids
    if test (count $ids) -eq 0
        echo "No matching keys found."
        return 0
    end

    for id in $ids
        gh api --method DELETE "repos/$repo/keys/$id" >/dev/null
        and echo "Revoked deploy key id=$id"
    end
end

# Infer "owner/repo" from the current working directory's git origin remote.
# Why this exists: the most common workflow is "create a key for THIS repo",
# and forcing the user to copy/paste the slug every time is friction.
# Supports HTTPS, SSH, and ssh-config aliases that start with "github-"
# (the convention used by `install-ssh-config --host-prefix github-llm`).
# For any other custom alias, the user can still pass --repo explicitly.
function __llm_gh_repo_from_git
    # Prefer ATB_USER_PWD when set by the bin-shim dispatcher, since the
    # dispatcher cds into the agentic_tactical_boots repo before invoking us.
    # Falls back to $PWD when sourced directly by the user.
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end
    set -l url (command git -C "$cwd" remote get-url origin 2>/dev/null)
    if test -z "$url"
        return 1
    end

    set -l u "$url"
    set u (string replace -r '\.git$' '' -- "$u")
    set u (string replace -r '^ssh://' '' -- "$u")
    set u (string replace -r '^[^@/]+@' '' -- "$u")
    set u (string replace -r '^https?://' '' -- "$u")

    set -l groups (string match -r '^([^:/]+)[:/](.+)$' -- "$u")
    if test (count $groups) -lt 3
        return 1
    end
    set -l host "$groups[2]"
    set -l rest "$groups[3]"

    if not string match -rq '^[^/]+/[^/]+$' -- "$rest"
        return 1
    end

    switch "$host"
        case github.com 'github-*'
            echo "$rest"
            return 0
    end
    return 1
end

# Default --name when caller does not provide one. Embeds short git sha + UTC
# date so concurrent sessions for the same repo do not collide and so the
# title encodes when/where the key was created.
function __llm_gh_default_name
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end
    set -l short_sha (command git -C "$cwd" rev-parse --short=7 HEAD 2>/dev/null)
    if test -z "$short_sha"
        set short_sha "no-sha"
    end
    set -l stamp (date -u +%Y%m%d)
    echo "auto-$short_sha-$stamp"
end

# Soft-dep TUI for managing deploy keys for the current repo.
# Why we always show the equivalent CLI: the TUI is meant to be a teaching
# layer, not a replacement. After running once, users should know which raw
# command to put in a script.
function __llm_gh_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: slop-gh-key --help" 1>&2
        return 1
    end

    set -l inferred (__llm_gh_repo_from_git)
    if test -z "$inferred"
        echo "Error: not in a git repo with a recognized GitHub origin." 1>&2
        echo "  Run from inside a checkout whose 'origin' remote points at github.com," 1>&2
        echo "  or use the CLI explicitly: slop-gh-key <subcommand> --repo owner/repo ..." 1>&2
        return 1
    end

    while true
        echo ""
        gum style --bold --foreground 212 "GitHub deploy keys → $inferred"
        gum style --faint "(Esc on the menu to quit. Every action prints its equivalent CLI.)"
        echo ""

        set -l action (gum choose \
            "Create RO+RW pair (24h, install ssh config)" \
            "List current deploy keys" \
            "Revoke a key by id" \
            "Revoke ALL llm-agent keys for this repo" \
            "Cleanup expired keys" \
            "Quit")

        if test -z "$action"
            return 0
        end

        echo ""
        switch "$action"
            case "Create RO+RW*"
                __llm_gh_tui_show_cli "slop-gh-key here create-pair"
                if gum confirm --default=true "Create RO+RW pair for $inferred?"
                    slop-gh-key here create-pair
                end
            case "List*"
                __llm_gh_tui_show_cli "slop-gh-key here list"
                slop-gh-key here list
            case "Revoke a key*"
                set -l id (gum input --placeholder "deploy key id (numeric, from the list)" --prompt "key id › ")
                if test -z "$id"
                    continue
                end
                if not string match -rq '^[0-9]+$' -- "$id"
                    gum style --foreground 196 "Not a numeric id: $id"
                    continue
                end
                __llm_gh_tui_show_cli "slop-gh-key here revoke $id"
                if gum confirm --default=false "Revoke key $id from $inferred?"
                    slop-gh-key here revoke "$id"
                end
            case "Revoke ALL*"
                gum style --foreground 196 --bold "DESTRUCTIVE: removes every deploy key whose title starts with '$LLM_GH_KEY_PREFIX:' on $inferred."
                __llm_gh_tui_show_cli "slop-gh-key here revoke-all"
                if gum confirm --default=false "Really revoke ALL '$LLM_GH_KEY_PREFIX:' deploy keys for $inferred?"
                    slop-gh-key here revoke-all
                end
            case "Cleanup*"
                __llm_gh_tui_show_cli "slop-gh-key here cleanup"
                slop-gh-key here cleanup
            case "Quit"
                return 0
        end
    end
end

function __llm_gh_tui_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function slop-gh-key --description "Manage ephemeral GitHub deploy keys for LLM agents"
    if test (count $argv) -eq 0
        __llm_gh_usage
        return 1
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    if test "$cmd" = "-h"; or test "$cmd" = "--help"
        __llm_gh_usage
        return 0
    end

    # `tui` opens the interactive launcher and never falls through to the
    # CLI dispatcher below.
    if test "$cmd" = "tui"
        __llm_gh_tui $argv
        return $status
    end

    # `here` is sugar: infer --repo from git origin in cwd, optionally rewrite
    # the subcommand to its underlying form, and prepend convenience flags.
    # Parsing then continues through the normal arg loop so behavior stays
    # consistent with explicit invocations.
    if test "$cmd" = "here"
        if test (count $argv) -eq 0
            echo "Error: 'here' requires a subcommand." 1>&2
            echo "" 1>&2
            echo "Available 'here' subcommands:" 1>&2
            echo "  create-pair, list, revoke <id>, cleanup, revoke-all" 1>&2
            echo "" 1>&2
            __llm_gh_usage 1>&2
            return 1
        end

        set cmd "$argv[1]"
        set -e argv[1]

        set -l inferred (__llm_gh_repo_from_git)
        if test -z "$inferred"
            echo "Error: could not infer GitHub repo from $PWD." 1>&2
            echo "  Tried: git -C \$PWD remote get-url origin" 1>&2
            echo "  Supported origins: github.com URLs and ssh-config aliases starting with 'github-'." 1>&2
            echo "  Workaround: invoke the underlying subcommand with --repo owner/repo." 1>&2
            return 1
        end

        set -l prepend --repo "$inferred"
        set -l strip_install_config "false"

        switch "$cmd"
            case create-pair
                # Default to installing SSH aliases — the most common follow-on step.
                set -a prepend --install-ssh-config
                # Default --name embeds short sha + UTC date so concurrent sessions
                # for the same repo do not collide. User --name (parsed below) wins.
                set -a prepend --name (__llm_gh_default_name)
            case list
                # No extra flags needed.
            case revoke
                if test (count $argv) -ge 1; and string match -rq '^[0-9]+$' -- "$argv[1]"
                    set -a prepend --id "$argv[1]"
                    set -e argv[1]
                end
            case cleanup
                set cmd revoke-expired
                set -a prepend --yes
            case revoke-all
                set cmd revoke-by-title
                set -a prepend --match '^'$LLM_GH_KEY_PREFIX':' --yes
            case '*'
                echo "Error: unknown 'here' subcommand: $cmd" 1>&2
                echo "" 1>&2
                echo "Available: create-pair, list, revoke <id>, cleanup, revoke-all" 1>&2
                return 1
        end

        # Honor the --no-install-config sugar by both removing it from argv
        # and dropping --install-ssh-config from prepend.
        set -l filtered
        for a in $argv
            if test "$a" = "--no-install-config"
                set strip_install_config "true"
                continue
            end
            set -a filtered "$a"
        end
        set argv $filtered

        if test "$strip_install_config" = "true"
            set -l p2
            for a in $prepend
                if test "$a" = "--install-ssh-config"
                    continue
                end
                set -a p2 "$a"
            end
            set prepend $p2
        end

        set argv $prepend $argv
    end

    set -l repo ""
    set -l access ""
    set -l ttl "$LLM_GH_KEY_TTL"
    set -l name "ephemeral"
    set -l name_set "false"
    set -l id ""
    set -l pattern ""
    set -l yes "false"
    set -l ro_key ""
    set -l rw_key ""
    set -l host_prefix "github-llm"
    set -l install_config "false"
    set -l marker ""

    while test (count $argv) -gt 0
        switch "$argv[1]"
            case --repo
                if test (count $argv) -lt 2
                    echo "Missing value for --repo" 1>&2
                    return 1
                end
                set repo "$argv[2]"
                set -e argv[1..2]
            case --access
                if test (count $argv) -lt 2
                    echo "Missing value for --access" 1>&2
                    return 1
                end
                set access "$argv[2]"
                set -e argv[1..2]
            case --ttl
                if test (count $argv) -lt 2
                    echo "Missing value for --ttl" 1>&2
                    return 1
                end
                set ttl "$argv[2]"
                set -e argv[1..2]
            case --name
                if test (count $argv) -lt 2
                    echo "Missing value for --name" 1>&2
                    return 1
                end
                set name "$argv[2]"
                set name_set "true"
                set -e argv[1..2]
            case --id
                if test (count $argv) -lt 2
                    echo "Missing value for --id" 1>&2
                    return 1
                end
                set id "$argv[2]"
                set -e argv[1..2]
            case --match
                if test (count $argv) -lt 2
                    echo "Missing value for --match" 1>&2
                    return 1
                end
                set pattern "$argv[2]"
                set -e argv[1..2]
            case --yes
                set yes "true"
                set -e argv[1]
            case --ro-key
                if test (count $argv) -lt 2
                    echo "Missing value for --ro-key" 1>&2
                    return 1
                end
                set ro_key "$argv[2]"
                set -e argv[1..2]
            case --rw-key
                if test (count $argv) -lt 2
                    echo "Missing value for --rw-key" 1>&2
                    return 1
                end
                set rw_key "$argv[2]"
                set -e argv[1..2]
            case --host-prefix
                if test (count $argv) -lt 2
                    echo "Missing value for --host-prefix" 1>&2
                    return 1
                end
                set host_prefix "$argv[2]"
                set -e argv[1..2]
            case --install-ssh-config
                set install_config "true"
                set -e argv[1]
            case --marker
                if test (count $argv) -lt 2
                    echo "Missing value for --marker" 1>&2
                    return 1
                end
                set marker "$argv[2]"
                set -e argv[1..2]
            case -h --help
                __llm_gh_usage
                return 0
            case '*'
                echo "Unknown argument: $argv[1]" 1>&2
                return 1
        end
    end

    switch "$cmd"
        case create
            __llm_gh_require_tools; or return 1
            if test -z "$repo"; or test -z "$access"
                echo "create requires --repo and --access" 1>&2
                return 1
            end
            __llm_gh_validate_repo "$repo"; or return 1
            __llm_gh_create_one "$repo" "$access" "$ttl" "$name"

        case create-pair
            __llm_gh_require_tools; or return 1
            if test -z "$repo"
                echo "create-pair requires --repo" 1>&2
                return 1
            end
            __llm_gh_validate_repo "$repo"; or return 1
            __llm_gh_create_one "$repo" ro "$ttl" "$name"; or return 1
            __llm_gh_create_one "$repo" rw "$ttl" "$name"; or return 1

            if test "$install_config" = "true"
                __llm_gh_install_config "$repo" "$name" "$__llm_gh_last_key_path_ro" "$__llm_gh_last_key_path_rw" "$host_prefix"; or return 1
            end

            echo ""
            echo "Use these remote URLs:"
            echo "  git@"$host_prefix"-ro:$repo.git"
            echo "  git@"$host_prefix"-rw:$repo.git"

        case print-ssh-config
            if test -z "$ro_key"; or test -z "$rw_key"
                echo "print-ssh-config requires --ro-key and --rw-key" 1>&2
                return 1
            end
            __llm_gh_render_config "$ro_key" "$rw_key" "$host_prefix"

        case install-ssh-config
            __llm_gh_validate_repo "$repo"; or return 1
            if test -z "$ro_key"; or test -z "$rw_key"
                echo "install-ssh-config requires --ro-key and --rw-key" 1>&2
                return 1
            end
            __llm_gh_install_config "$repo" "$name" "$ro_key" "$rw_key" "$host_prefix"

        case uninstall-ssh-config
            if test -z "$marker"
                if test -z "$repo"; or test "$name_set" != "true"
                    echo "uninstall-ssh-config requires --marker OR both --repo and --name" 1>&2
                    return 1
                end
                __llm_gh_validate_repo "$repo"; or return 1
            end

            __llm_gh_uninstall_config "$repo" "$name" "$marker" "$yes"

        case list
            __llm_gh_require_tools; or return 1
            if test -z "$repo"
                echo "list requires --repo" 1>&2
                return 1
            end
            __llm_gh_validate_repo "$repo"; or return 1
            echo "id\taccess\tcreated_at\ttitle"
            __llm_gh_list "$repo"

        case revoke
            __llm_gh_require_tools; or return 1
            if test -z "$repo"; or test -z "$id"
                echo "revoke requires --repo and --id" 1>&2
                return 1
            end
            __llm_gh_validate_repo "$repo"; or return 1
            gh api --method DELETE "repos/$repo/keys/$id" >/dev/null
            and echo "Revoked deploy key id=$id"

        case revoke-by-title
            __llm_gh_require_tools; or return 1
            if test -z "$repo"; or test -z "$pattern"
                echo "revoke-by-title requires --repo and --match" 1>&2
                return 1
            end
            __llm_gh_validate_repo "$repo"; or return 1

            set -l keys_json (gh api "repos/$repo/keys")
            set -l ids (printf "%s\n" "$keys_json" | uv run --script "$LLM_GH_PY" filter-by-title "$pattern")

            if not __llm_gh_confirm "Revoke matching deploy keys from $repo?" "$yes"
                return 1
            end

            __llm_gh_revoke_by_ids "$repo" $ids

        case revoke-expired
            __llm_gh_require_tools; or return 1
            if test -z "$repo"
                echo "revoke-expired requires --repo" 1>&2
                return 1
            end
            __llm_gh_validate_repo "$repo"; or return 1

            set -l keys_json (gh api "repos/$repo/keys")
            set -l ids (printf "%s\n" "$keys_json" | uv run --script "$LLM_GH_PY" filter-expired)

            if not __llm_gh_confirm "Revoke expired deploy keys from $repo?" "$yes"
                return 1
            end

            __llm_gh_revoke_by_ids "$repo" $ids

        case '*'
            echo "Unknown command: $cmd" 1>&2
            __llm_gh_usage
            return 1
    end
end
