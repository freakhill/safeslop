#!/usr/bin/env fish

# Purpose:
# - Mirror GitHub deploy-key lifecycle for Forgejo with multi-instance support.
# - Keep credentials in env vars (token-env indirection), not CLI plaintext.
# - Keep RO/RW key separation and easy revocation workflows.
#
# References:
# - Forgejo docs: https://forgejo.org/docs/latest/
# - Gitea/Forgejo repo keys API: https://docs.gitea.com/api/1.21/#tag/repository/operation/repoListKeys
# - OpenSSH config: https://man.openbsd.org/ssh_config

set -g LLM_FORGEJO_KEY_PREFIX "llm-agent"
set -g LLM_FORGEJO_KEY_DIR "$HOME/.ssh"
set -g LLM_FORGEJO_KEY_TTL "24h"
set -g LLM_FORGEJO_CONFIG_DIR "$HOME/.config/llm-key-tools"
set -g LLM_FORGEJO_INSTANCES_FILE "$LLM_FORGEJO_CONFIG_DIR/forgejo-instances.json"
set -g LLM_FORGEJO_TEMPLATE_FILE (path resolve (dirname (status filename)))/../examples/forgejo-instances.example.json
# Python helpers run via uv with PEP-723 inline metadata. See scripts/_py/.
set -g LLM_FORGEJO_PY (path resolve (dirname (status filename)))"/_py/llm_forgejo_keys.py"

function __llm_forgejo_usage
    echo "Usage:"
    echo "  source scripts/slop-forgejo-key.fish"
    echo "  slop-forgejo-key instance-set --name <instance> --url <https://forgejo.example.com> --token-env <ENV_VAR>"
    echo "  slop-forgejo-key instance-list"
    echo "  slop-forgejo-key instance-remove --name <instance>"
    echo "  slop-forgejo-key bootstrap-config [--force]"
    echo "  slop-forgejo-key create --instance <name>|--forgejo-url <url> --repo owner/repo --access ro|rw [--ttl 24h] [--name label] [--token-env ENV]"
    echo "  slop-forgejo-key create-pair --instance <name>|--forgejo-url <url> --repo owner/repo [--ttl 24h] [--name label] [--token-env ENV] [--install-ssh-config]"
    echo "  slop-forgejo-key list --instance <name>|--forgejo-url <url> --repo owner/repo [--token-env ENV]"
    echo "  slop-forgejo-key revoke --instance <name>|--forgejo-url <url> --repo owner/repo --id <key-id> [--token-env ENV]"
    echo "  slop-forgejo-key revoke-by-title --instance <name>|--forgejo-url <url> --repo owner/repo --match <regex> [--token-env ENV] [--yes]"
    echo "  slop-forgejo-key revoke-expired --instance <name>|--forgejo-url <url> --repo owner/repo [--token-env ENV] [--yes]"
    echo "  slop-forgejo-key print-ssh-config --forgejo-url <url> --ro-key <path> --rw-key <path> [--host-prefix forgejo-llm]"
    echo "  slop-forgejo-key install-ssh-config --repo owner/repo --name <label> --forgejo-url <url> --ro-key <path> --rw-key <path> [--host-prefix forgejo-llm]"
    echo "  slop-forgejo-key uninstall-ssh-config [--repo owner/repo --name <label> | --marker <marker-regex>] [--yes]"
    echo ""
    echo "Repo-aware shortcuts (infer --instance and --repo from cwd's git origin):"
    echo "  slop-forgejo-key here create-pair [--ttl 24h] [--name label] [--no-install-config]"
    echo "  slop-forgejo-key here list"
    echo "  slop-forgejo-key here revoke <key-id>"
    echo "  slop-forgejo-key here cleanup            (revoke-expired --yes for current repo)"
    echo "  slop-forgejo-key here revoke-all         (revoke-by-title '^$LLM_FORGEJO_KEY_PREFIX:' --yes)"
    echo ""
    echo "TUI:"
    echo "  slop-forgejo-key tui                     (interactive launcher; requires gum)"
    echo ""
    echo "Notes:"
    echo "  - Forgejo tokens are read from env var names, not plaintext args."
    echo "  - Titles embed expiry as exp=<UTC-ISO8601>."
    echo "  - 'here' looks up the matching instance profile by host. Run"
    echo "    'slop-forgejo-key bootstrap-config' then 'instance-set' first."
end

function __llm_forgejo_bootstrap_config --argument-names force
    # Bootstrap keeps onboarding repeatable across hosts and CI runners.
    if not test -f "$LLM_FORGEJO_TEMPLATE_FILE"
        echo "Missing template file: $LLM_FORGEJO_TEMPLATE_FILE" 1>&2
        return 1
    end

    mkdir -p "$LLM_FORGEJO_CONFIG_DIR"

    if test -f "$LLM_FORGEJO_INSTANCES_FILE"; and test "$force" != "true"
        echo "Config already exists: $LLM_FORGEJO_INSTANCES_FILE" 1>&2
        echo "Use --force to overwrite from template." 1>&2
        return 1
    end

    cp "$LLM_FORGEJO_TEMPLATE_FILE" "$LLM_FORGEJO_INSTANCES_FILE"
    echo "Wrote Forgejo instance config template: $LLM_FORGEJO_INSTANCES_FILE"
end

function __llm_forgejo_require_tools
    # uv replaces bare python3 so the helper interpreter is pinned via PEP-723.
    for tool in curl uv ssh-keygen
        if not command -sq "$tool"
            echo "Missing required tool: $tool" 1>&2
            return 1
        end
    end
end

function __llm_forgejo_validate_repo --argument-names repo
    if not string match -rq '^[^/]+/[^/]+$' -- "$repo"
        echo "Invalid --repo value. Expected owner/repo" 1>&2
        return 1
    end
end

function __llm_forgejo_ttl_to_iso --argument-names ttl
    if not string match -rq '^[0-9]+[mhdw]$' -- "$ttl"
        echo "Invalid --ttl '$ttl'. Use formats like 30m, 12h, 7d, 2w" 1>&2
        return 1
    end

    uv run --script "$LLM_FORGEJO_PY" ttl-to-iso "$ttl"
end

function __llm_forgejo_repo_slug --argument-names repo
    string replace -a '/' '-' -- "$repo"
end

function __llm_forgejo_host_from_url --argument-names url
    uv run --script "$LLM_FORGEJO_PY" host-from-url "$url"
end

function __llm_forgejo_normalize_url --argument-names url
    string replace -r '/+$' '' -- "$url"
end

function __llm_forgejo_ensure_instance_file
    mkdir -p "$LLM_FORGEJO_CONFIG_DIR"
    if not test -f "$LLM_FORGEJO_INSTANCES_FILE"
        echo '{"instances":{}}' > "$LLM_FORGEJO_INSTANCES_FILE"
    end
end

function __llm_forgejo_instance_set --argument-names name url token_env
    if test -z "$name"; or test -z "$url"; or test -z "$token_env"
        echo "instance-set requires --name, --url, and --token-env" 1>&2
        return 1
    end

    __llm_forgejo_ensure_instance_file
    set -l norm_url (__llm_forgejo_normalize_url "$url")

    uv run --script "$LLM_FORGEJO_PY" instance-set "$LLM_FORGEJO_INSTANCES_FILE" "$name" "$norm_url" "$token_env"

    echo "Saved Forgejo instance profile: $name"
    echo "  url: $norm_url"
    echo "  token env: $token_env"
end

function __llm_forgejo_instance_list
    __llm_forgejo_ensure_instance_file
    uv run --script "$LLM_FORGEJO_PY" instance-list "$LLM_FORGEJO_INSTANCES_FILE"
end

function __llm_forgejo_instance_remove --argument-names name
    if test -z "$name"
        echo "instance-remove requires --name" 1>&2
        return 1
    end

    __llm_forgejo_ensure_instance_file
    uv run --script "$LLM_FORGEJO_PY" instance-remove "$LLM_FORGEJO_INSTANCES_FILE" "$name"
end

function __llm_forgejo_resolve_connection --argument-names instance forgejo_url token_env
    # Resolve from profile or explicit URL, then fail closed if token missing.
    set -l resolved_url ""
    set -l resolved_env "$token_env"

    if test -n "$forgejo_url"
        set resolved_url (__llm_forgejo_normalize_url "$forgejo_url")
        if test -z "$resolved_env"
            set resolved_env "FORGEJO_TOKEN"
        end
    else
        if test -z "$instance"
            echo "Pass --instance <name> or --forgejo-url <url>" 1>&2
            return 1
        end

        __llm_forgejo_ensure_instance_file
        set -l line (uv run --script "$LLM_FORGEJO_PY" instance-get "$LLM_FORGEJO_INSTANCES_FILE" "$instance")

        if test $status -ne 0; or test -z "$line"
            echo "Unknown instance profile: $instance" 1>&2
            return 1
        end

        set -l parts (string split "\t" -- "$line")
        set resolved_url "$parts[1]"
        if test -z "$resolved_env"
            set resolved_env "$parts[2]"
        end
        if test -z "$resolved_env"
            set resolved_env "FORGEJO_TOKEN"
        end
    end

    set resolved_url (__llm_forgejo_normalize_url "$resolved_url")
    set -l host (__llm_forgejo_host_from_url "$resolved_url")
    if test -z "$host"
        echo "Invalid Forgejo URL: $resolved_url" 1>&2
        return 1
    end

    set -l token $$resolved_env
    if test -z "$token"
        echo "Missing token in env var: $resolved_env" 1>&2
        return 1
    end

    set -g __llm_forgejo_url "$resolved_url"
    set -g __llm_forgejo_host "$host"
    set -g __llm_forgejo_token_env "$resolved_env"
    set -g __llm_forgejo_token "$token"
end

function __llm_forgejo_api --argument-names method path payload
    set -l url "$__llm_forgejo_url/api/v1/$path"
    set -l common_args \
        -sS \
        -X "$method" \
        -H "Authorization: token $__llm_forgejo_token" \
        -H "Accept: application/json"

    if test -n "$payload"
        curl $common_args -H "Content-Type: application/json" "$url" --data "$payload"
    else
        curl $common_args "$url"
    end
end

function __llm_forgejo_generate_key --argument-names access name expiry
    # ed25519 + higher KDF rounds for local key hardening.
    set -l stamp (date -u +%Y%m%dT%H%M%SZ)
    set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
    set -l base "$LLM_FORGEJO_KEY_DIR/llm_agent_forgejo_"$access"_"$safe_name"_"$stamp
    set -l title "$LLM_FORGEJO_KEY_PREFIX:forgejo:"$access":"$safe_name":exp="$expiry

    if test -e "$base"; or test -e "$base.pub"
        echo "Refusing to overwrite existing key files: $base" 1>&2
        return 1
    end

    mkdir -p "$LLM_FORGEJO_KEY_DIR"
    chmod 700 "$LLM_FORGEJO_KEY_DIR"

    ssh-keygen -t ed25519 -a 100 -N "" -f "$base" -C "$title" >/dev/null
    if test $status -ne 0
        echo "ssh-keygen failed" 1>&2
        return 1
    end

    set -g __llm_forgejo_last_key_path "$base"
    set -g __llm_forgejo_last_key_title "$title"
    set -g __llm_forgejo_last_key_pub (string trim -- (string collect < "$base.pub"))
end

function __llm_forgejo_create_deploy_key --argument-names repo title pub access
    set -l read_only true
    if test "$access" = "rw"
        set read_only false
    end

    set -l payload (uv run --script "$LLM_FORGEJO_PY" make-payload "$title" "$pub" "$read_only")
    set -l resp (__llm_forgejo_api POST "repos/$repo/keys" "$payload")
    if test $status -ne 0
        return 1
    end

    echo "$resp" | uv run --script "$LLM_FORGEJO_PY" parse-key-id
end

function __llm_forgejo_create_one --argument-names repo access ttl name
    if not contains -- "$access" ro rw
        echo "Invalid --access '$access'. Use ro or rw" 1>&2
        return 1
    end

    set -l expiry (__llm_forgejo_ttl_to_iso "$ttl")
    if test $status -ne 0
        return 1
    end

    __llm_forgejo_generate_key "$access" "$name" "$expiry"; or return 1

    set -l key_path "$__llm_forgejo_last_key_path"
    set -l title "$__llm_forgejo_last_key_title"
    set -l pub "$__llm_forgejo_last_key_pub"

    set -l key_id (__llm_forgejo_create_deploy_key "$repo" "$title" "$pub" "$access")
    if test $status -ne 0; or test -z "$key_id"
        echo "Failed to create Forgejo deploy key for $repo" 1>&2
        return 1
    end

    echo "Created $access deploy key"
    echo "  instance: $__llm_forgejo_url"
    echo "  repo: $repo"
    echo "  id: $key_id"
    echo "  title: $title"
    echo "  private key: $key_path"
    echo "  public key: $key_path.pub"

    switch "$access"
        case ro
            set -g __llm_forgejo_last_key_path_ro "$key_path"
        case rw
            set -g __llm_forgejo_last_key_path_rw "$key_path"
    end
end

function __llm_forgejo_confirm --argument-names prompt no_prompt
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

function __llm_forgejo_list --argument-names repo
    set -l resp (__llm_forgejo_api GET "repos/$repo/keys" "")
    if test $status -ne 0
        return 1
    end

    echo "$resp" | uv run --script "$LLM_FORGEJO_PY" list-keys
end

function __llm_forgejo_revoke_by_ids --argument-names repo ids
    if test (count $ids) -eq 0
        echo "No matching keys found."
        return 0
    end

    for id in $ids
        __llm_forgejo_api DELETE "repos/$repo/keys/$id" "" >/dev/null
        and echo "Revoked deploy key id=$id"
    end
end

# Infer (host, owner/repo) from the current working directory's git origin.
# Echoes two lines: host on line 1, owner/repo on line 2. Returns 1 on
# failure. Reads from $ATB_USER_PWD first (set by the bin-shim dispatcher
# before it cds into the agentic_tactical_boots repo) and falls back to $PWD.
function __llm_forgejo_repo_from_git
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

    # Resolve forgejo-* ssh-config aliases via `ssh -G` so users with custom
    # alias hosts still get a usable real hostname.
    if string match -q 'forgejo-*' -- "$host"
        set -l real_host (command ssh -G "$host" 2>/dev/null | string match -r '^hostname (.+)$' | string replace -r '^hostname ' '')
        if test -n "$real_host[2]"
            set host "$real_host[2]"
        end
    end

    echo "$host"
    echo "$rest"
end

function __llm_forgejo_default_name
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

# Given a host, return a tab-separated "instance_name<TAB>url<TAB>token_env"
# row from the saved profiles, or empty if no profile matches.
function __llm_forgejo_instance_for_host --argument-names host
    __llm_forgejo_ensure_instance_file
    uv run --script "$LLM_FORGEJO_PY" instance-by-host "$LLM_FORGEJO_INSTANCES_FILE" "$host"
end

function __llm_forgejo_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: slop-forgejo-key --help" 1>&2
        return 1
    end

    set -l inferred (__llm_forgejo_repo_from_git)
    if test (count $inferred) -ne 2
        echo "Error: not in a git repo with a recognized origin." 1>&2
        echo "  Use the CLI explicitly: slop-forgejo-key <subcommand> --instance <name> --repo owner/repo ..." 1>&2
        return 1
    end
    set -l host "$inferred[1]"
    set -l repo "$inferred[2]"
    set -l profile (__llm_forgejo_instance_for_host "$host")
    set -l instance_name ""
    if test -n "$profile"
        set instance_name (string split "\t" -- "$profile")[1]
    end

    while true
        echo ""
        gum style --bold --foreground 212 "Forgejo deploy keys → $repo @ $host"
        if test -n "$instance_name"
            gum style --faint "instance profile: $instance_name"
        else
            gum style --foreground 196 "no instance profile saved for $host — use `slop-forgejo-key instance-set` first"
        end
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
                __llm_forgejo_show_cli "slop-forgejo-key here create-pair"
                if gum confirm --default=true "Create RO+RW pair for $repo?"
                    slop-forgejo-key here create-pair
                end
            case "List*"
                __llm_forgejo_show_cli "slop-forgejo-key here list"
                slop-forgejo-key here list
            case "Revoke a key*"
                set -l id (gum input --placeholder "deploy key id (numeric)" --prompt "key id › ")
                if test -z "$id"
                    continue
                end
                if not string match -rq '^[0-9]+$' -- "$id"
                    gum style --foreground 196 "Not a numeric id: $id"
                    continue
                end
                __llm_forgejo_show_cli "slop-forgejo-key here revoke $id"
                if gum confirm --default=false "Revoke key $id from $repo?"
                    slop-forgejo-key here revoke "$id"
                end
            case "Revoke ALL*"
                gum style --foreground 196 --bold "DESTRUCTIVE: removes every deploy key whose title starts with '$LLM_FORGEJO_KEY_PREFIX:' on $repo."
                __llm_forgejo_show_cli "slop-forgejo-key here revoke-all"
                if gum confirm --default=false "Really revoke ALL '$LLM_FORGEJO_KEY_PREFIX:' deploy keys for $repo?"
                    slop-forgejo-key here revoke-all
                end
            case "Cleanup*"
                __llm_forgejo_show_cli "slop-forgejo-key here cleanup"
                slop-forgejo-key here cleanup
            case "Quit"
                return 0
        end
    end
end

function __llm_forgejo_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __llm_forgejo_print_alias_block --argument-names host_alias host_name key_path
    echo "Host $host_alias"
    echo "  HostName $host_name"
    echo "  User git"
    echo "  IdentityFile $key_path"
    echo "  IdentitiesOnly yes"
    echo ""
end

function __llm_forgejo_render_config --argument-names host_name ro_key rw_key host_prefix
    __llm_forgejo_print_alias_block "$host_prefix-ro" "$host_name" "$ro_key"
    __llm_forgejo_print_alias_block "$host_prefix-rw" "$host_name" "$rw_key"
end

function __llm_forgejo_install_config --argument-names repo name host_name ro_key rw_key host_prefix
    # Marker blocks let us remove aliases safely without touching other entries.
    if test -z "$repo"; or test -z "$name"; or test -z "$host_name"
        echo "install-ssh-config requires --repo, --name, and a resolvable Forgejo host" 1>&2
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

    mkdir -p "$LLM_FORGEJO_KEY_DIR"
    chmod 700 "$LLM_FORGEJO_KEY_DIR"

    set -l config_file "$LLM_FORGEJO_KEY_DIR/config"
    if not test -f "$config_file"
        touch "$config_file"
    end
    chmod 600 "$config_file"

    set -l stamp (date -u +%Y%m%dT%H%M%SZ)
    set -l slug (__llm_forgejo_repo_slug "$repo")
    set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
    set -l marker "slop-forgejo-key:$slug:$safe_name:$stamp"

    {
        echo ""
        echo "# BEGIN $marker"
        __llm_forgejo_render_config "$host_name" "$ro_key" "$rw_key" "$host_prefix"
        echo "# END $marker"
    } >> "$config_file"

    echo "Added SSH aliases to $config_file"
    echo "  RO host alias: $host_prefix-ro"
    echo "  RW host alias: $host_prefix-rw"
    echo "  marker: $marker"
end

function __llm_forgejo_uninstall_config --argument-names repo name marker yes
    set -l config_file "$LLM_FORGEJO_KEY_DIR/config"
    if not test -f "$config_file"
        echo "No SSH config file found at $config_file"
        return 0
    end

    set -l pattern ""
    if test -n "$marker"
        set pattern "$marker"
    else
        set -l slug (__llm_forgejo_repo_slug "$repo")
        set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
        set pattern "^slop-forgejo-key:"$slug":"$safe_name":"
    end

    if not __llm_forgejo_confirm "Remove matching SSH config blocks from $config_file?" "$yes"
        return 1
    end

    set -l result (uv run --script "$LLM_FORGEJO_PY" ssh-config-uninstall "$config_file" "$pattern")

    if test "$result[1]" = "0"
        echo "No matching SSH config blocks found."
        return 0
    end

    echo "Removed $result[1] SSH config block(s) from $config_file"
    for m in $result[2..-1]
        echo "  - $m"
    end
end

function slop-forgejo-key --description "Manage ephemeral Forgejo deploy keys for LLM agents"
    if test (count $argv) -eq 0
        __llm_forgejo_usage
        return 1
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    if test "$cmd" = "-h"; or test "$cmd" = "--help"
        __llm_forgejo_usage
        return 0
    end

    if test "$cmd" = "tui"
        __llm_forgejo_tui $argv
        return $status
    end

    # `here` infers --instance and --repo from the cwd's git origin and an
    # entry in the saved instance profiles, then falls through to the normal
    # dispatcher. Aliases: cleanup → revoke-expired --yes, revoke-all →
    # revoke-by-title --match '^llm-agent:' --yes.
    if test "$cmd" = "here"
        if test (count $argv) -eq 0
            echo "Error: 'here' requires a subcommand." 1>&2
            echo "" 1>&2
            echo "Available 'here' subcommands:" 1>&2
            echo "  create-pair, list, revoke <id>, cleanup, revoke-all" 1>&2
            return 1
        end

        set cmd "$argv[1]"
        set -e argv[1]

        set -l inferred (__llm_forgejo_repo_from_git)
        if test (count $inferred) -ne 2
            echo "Error: could not infer repo from $PWD." 1>&2
            echo "  Tried: git -C \$PWD remote get-url origin" 1>&2
            echo "  Workaround: invoke the underlying subcommand with --instance <name> --repo owner/repo." 1>&2
            return 1
        end
        set -l host "$inferred[1]"
        set -l inferred_repo "$inferred[2]"

        set -l profile (__llm_forgejo_instance_for_host "$host")
        if test -z "$profile"
            echo "Error: no Forgejo instance profile matches host '$host'." 1>&2
            echo "" 1>&2
            echo "Add one with:" 1>&2
            echo "  slop-forgejo-key bootstrap-config" 1>&2
            echo "  slop-forgejo-key instance-set --name <label> --url https://$host --token-env <ENV>" 1>&2
            return 1
        end
        set -l parts (string split "\t" -- "$profile")
        set -l inferred_instance "$parts[1]"

        set -l prepend --instance "$inferred_instance" --repo "$inferred_repo"
        set -l strip_install_config "false"

        switch "$cmd"
            case create-pair
                set -a prepend --install-ssh-config
                set -a prepend --name (__llm_forgejo_default_name)
            case list
                # No extras.
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
                set -a prepend --match '^'$LLM_FORGEJO_KEY_PREFIX':' --yes
            case '*'
                echo "Error: unknown 'here' subcommand: $cmd" 1>&2
                echo "Available: create-pair, list, revoke <id>, cleanup, revoke-all" 1>&2
                return 1
        end

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

    set -l instance ""
    set -l forgejo_url ""
    set -l token_env ""
    set -l repo ""
    set -l access ""
    set -l ttl "$LLM_FORGEJO_KEY_TTL"
    set -l name "ephemeral"
    set -l name_set "false"
    set -l id ""
    set -l pattern ""
    set -l yes "false"
    set -l host_prefix "forgejo-llm"
    set -l ro_key ""
    set -l rw_key ""
    set -l install_config "false"
    set -l marker ""
    set -l force "false"

    while test (count $argv) -gt 0
        switch "$argv[1]"
            case --name
                set name "$argv[2]"
                set name_set "true"
                set -e argv[1..2]
            case --instance
                set instance "$argv[2]"
                set -e argv[1..2]
            case --forgejo-url
                set forgejo_url "$argv[2]"
                set -e argv[1..2]
            case --url
                set forgejo_url "$argv[2]"
                set -e argv[1..2]
            case --token-env
                set token_env "$argv[2]"
                set -e argv[1..2]
            case --repo
                set repo "$argv[2]"
                set -e argv[1..2]
            case --access
                set access "$argv[2]"
                set -e argv[1..2]
            case --ttl
                set ttl "$argv[2]"
                set -e argv[1..2]
            case --id
                set id "$argv[2]"
                set -e argv[1..2]
            case --match
                set pattern "$argv[2]"
                set -e argv[1..2]
            case --host-prefix
                set host_prefix "$argv[2]"
                set -e argv[1..2]
            case --ro-key
                set ro_key "$argv[2]"
                set -e argv[1..2]
            case --rw-key
                set rw_key "$argv[2]"
                set -e argv[1..2]
            case --install-ssh-config
                set install_config "true"
                set -e argv[1]
            case --marker
                set marker "$argv[2]"
                set -e argv[1..2]
            case --yes
                set yes "true"
                set -e argv[1]
            case --force
                set force "true"
                set -e argv[1]
            case -h --help
                __llm_forgejo_usage
                return 0
            case '*'
                echo "Unknown argument: $argv[1]" 1>&2
                return 1
        end
    end

    switch "$cmd"
        case instance-set
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_instance_set "$name" "$forgejo_url" "$token_env"

        case bootstrap-config
            __llm_forgejo_bootstrap_config "$force"

        case instance-list
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_instance_list

        case instance-remove
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_instance_remove "$name"

        case create
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1
            __llm_forgejo_create_one "$repo" "$access" "$ttl" "$name"

        case create-pair
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1

            __llm_forgejo_create_one "$repo" ro "$ttl" "$name"; or return 1
            __llm_forgejo_create_one "$repo" rw "$ttl" "$name"; or return 1

            if test "$install_config" = "true"
                __llm_forgejo_install_config "$repo" "$name" "$__llm_forgejo_host" "$__llm_forgejo_last_key_path_ro" "$__llm_forgejo_last_key_path_rw" "$host_prefix"; or return 1
            end

            echo ""
            echo "Use these remote URLs:"
            echo "  git@"$host_prefix"-ro:$repo.git"
            echo "  git@"$host_prefix"-rw:$repo.git"

        case list
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1
            echo "id\taccess\tcreated_at\ttitle"
            __llm_forgejo_list "$repo"

        case revoke
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1
            __llm_forgejo_api DELETE "repos/$repo/keys/$id" "" >/dev/null
            and echo "Revoked deploy key id=$id"

        case revoke-by-title
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1

            set -l keys_json (__llm_forgejo_api GET "repos/$repo/keys" "")
            set -l ids (printf "%s\n" "$keys_json" | uv run --script "$LLM_FORGEJO_PY" filter-by-title "$pattern")
            if not __llm_forgejo_confirm "Revoke matching deploy keys from $repo?" "$yes"
                return 1
            end
            __llm_forgejo_revoke_by_ids "$repo" $ids

        case revoke-expired
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1

            set -l keys_json (__llm_forgejo_api GET "repos/$repo/keys" "")
            set -l ids (printf "%s\n" "$keys_json" | uv run --script "$LLM_FORGEJO_PY" filter-expired)
            if not __llm_forgejo_confirm "Revoke expired deploy keys from $repo?" "$yes"
                return 1
            end
            __llm_forgejo_revoke_by_ids "$repo" $ids

        case print-ssh-config
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1
            if test -z "$ro_key"; or test -z "$rw_key"
                echo "print-ssh-config requires --ro-key and --rw-key" 1>&2
                return 1
            end
            __llm_forgejo_render_config "$__llm_forgejo_host" "$ro_key" "$rw_key" "$host_prefix"

        case install-ssh-config
            __llm_forgejo_require_tools; or return 1
            __llm_forgejo_validate_repo "$repo"; or return 1
            __llm_forgejo_resolve_connection "$instance" "$forgejo_url" "$token_env"; or return 1
            if test -z "$ro_key"; or test -z "$rw_key"
                echo "install-ssh-config requires --ro-key and --rw-key" 1>&2
                return 1
            end
            __llm_forgejo_install_config "$repo" "$name" "$__llm_forgejo_host" "$ro_key" "$rw_key" "$host_prefix"

        case uninstall-ssh-config
            __llm_forgejo_require_tools; or return 1
            if test -z "$marker"
                if test -z "$repo"; or test "$name_set" != "true"
                    echo "uninstall-ssh-config requires --marker OR both --repo and --name" 1>&2
                    return 1
                end
            end
            __llm_forgejo_uninstall_config "$repo" "$name" "$marker" "$yes"

        case '*'
            echo "Unknown command: $cmd" 1>&2
            __llm_forgejo_usage
            return 1
    end
end
