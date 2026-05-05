#!/usr/bin/env fish

# Purpose:
# - Manage local, ephemeral Radicle identities and RID bindings across many repos.
# - Keep an auditable local state file so future repos do not require script changes.
#
# Model note:
# - Radicle access is identity/delegation based, not GitHub-style deploy keys.
#   This script tracks local policy intent and identity lifecycle.
#
# References:
# - Radicle docs: https://radicle.xyz/guides
# - OpenSSH key generation: https://man.openbsd.org/ssh-keygen

set -g LLM_RADICLE_PREFIX "llm-agent"
set -g LLM_RADICLE_KEY_DIR "$HOME/.ssh"
set -g LLM_RADICLE_TTL "24h"
set -g LLM_RADICLE_CONFIG_DIR "$HOME/.config/llm-key-tools"
set -g LLM_RADICLE_STATE_FILE "$LLM_RADICLE_CONFIG_DIR/radicle-access.json"
set -g LLM_RADICLE_TEMPLATE_FILE (path resolve (dirname (status filename)))/../examples/radicle-access-policy.example.json
# Python helpers run via uv with PEP-723 inline metadata. See scripts/_py/.
set -g LLM_RADICLE_PY (path resolve (dirname (status filename)))"/_py/llm_radicle_access.py"

function __llm_rad_usage
    echo "Usage:"
    echo "  source scripts/slop-radicle.fish"
    echo "  slop-radicle create-identity --name <label> [--ttl 24h]"
    echo "  slop-radicle bootstrap-config [--force]"
    echo "  slop-radicle list-identities [--all]"
    echo "  slop-radicle retire-identity --id <identity-id> [--yes]"
    echo "  slop-radicle retire-expired [--yes]"
    echo "  slop-radicle bind-repo --rid <rid> --identity-id <identity-id> --access ro|rw [--note <text>]"
    echo "  slop-radicle list-bindings [--rid <rid>] [--all]"
    echo "  slop-radicle unbind-repo --rid <rid> [--identity-id <identity-id>] [--yes]"
    echo "  slop-radicle print-env --identity-id <identity-id>"
    echo ""
    echo "Repo-aware shortcuts (infer --rid from cwd's git config rad.id or 'rad inspect'):"
    echo "  slop-radicle here info"
    echo "  slop-radicle here bind --identity-id <id> --access ro|rw [--note text]"
    echo "  slop-radicle here unbind [--identity-id <id>] [--yes]"
    echo "  slop-radicle here list-bindings"
    echo ""
    echo "TUI:"
    echo "  slop-radicle tui                  (interactive launcher; requires gum)"
    echo ""
    echo "Notes:"
    echo "  - This manages local ephemeral identities + repo bindings across many RIDs."
    echo "  - Radicle access control is delegate/policy based; this tool does not alter network delegates automatically."
    echo "  - 'here' needs the cwd to be a Radicle-tracked repo (rad init has set git config rad.id, or 'rad inspect' is available)."
end

function __llm_rad_bootstrap_config --argument-names force
    # Bootstrap makes initial state predictable for onboarding and automation.
    if not test -f "$LLM_RADICLE_TEMPLATE_FILE"
        echo "Missing template file: $LLM_RADICLE_TEMPLATE_FILE" 1>&2
        return 1
    end

    mkdir -p "$LLM_RADICLE_CONFIG_DIR"

    if test -f "$LLM_RADICLE_STATE_FILE"; and test "$force" != "true"
        echo "Config already exists: $LLM_RADICLE_STATE_FILE" 1>&2
        echo "Use --force to overwrite from template." 1>&2
        return 1
    end

    cp "$LLM_RADICLE_TEMPLATE_FILE" "$LLM_RADICLE_STATE_FILE"
    echo "Wrote Radicle access config template: $LLM_RADICLE_STATE_FILE"
end

function __llm_rad_require_tools
    # uv replaces bare python3 so the helper interpreter is pinned via PEP-723.
    for tool in uv ssh-keygen
        if not command -sq "$tool"
            echo "Missing required tool: $tool" 1>&2
            return 1
        end
    end
end

function __llm_rad_ensure_state
    mkdir -p "$LLM_RADICLE_CONFIG_DIR"
    if not test -f "$LLM_RADICLE_STATE_FILE"
        echo '{"identities":[],"bindings":[]}' > "$LLM_RADICLE_STATE_FILE"
    end
end

function __llm_rad_confirm --argument-names prompt no_prompt
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

function __llm_rad_ttl_to_iso --argument-names ttl
    if not string match -rq '^[0-9]+[mhdw]$' -- "$ttl"
        echo "Invalid --ttl '$ttl'. Use formats like 30m, 12h, 7d, 2w" 1>&2
        return 1
    end

    uv run --script "$LLM_RADICLE_PY" ttl-to-iso "$ttl"
end

function __llm_rad_validate_access --argument-names access
    if not contains -- "$access" ro rw
        echo "Invalid --access '$access'. Use ro or rw" 1>&2
        return 1
    end
end

function __llm_rad_validate_rid --argument-names rid
    if not string match -rq '^rad:[A-Za-z0-9]+$' -- "$rid"
        echo "Invalid --rid. Expected format like rad:z3gqcJu..." 1>&2
        return 1
    end
end

function __llm_rad_generate_identity_key --argument-names name expiry
    # ed25519 + higher KDF rounds for local key hardening.
    set -l stamp (date -u +%Y%m%dT%H%M%SZ)
    set -l safe_name (string replace -ra '[^a-zA-Z0-9._-]' '-' -- "$name")
    set -l ident_id "rid-"$stamp"-"(uv run --script "$LLM_RADICLE_PY" uuid8)
    set -l key_path "$LLM_RADICLE_KEY_DIR/llm_agent_radicle_"$safe_name"_"$stamp
    set -l comment "$LLM_RADICLE_PREFIX:radicle:"$safe_name":exp="$expiry

    if test -e "$key_path"; or test -e "$key_path.pub"
        echo "Refusing to overwrite existing key files: $key_path" 1>&2
        return 1
    end

    mkdir -p "$LLM_RADICLE_KEY_DIR"
    chmod 700 "$LLM_RADICLE_KEY_DIR"

    ssh-keygen -t ed25519 -a 100 -N "" -f "$key_path" -C "$comment" >/dev/null
    if test $status -ne 0
        echo "ssh-keygen failed" 1>&2
        return 1
    end

    set -g __llm_rad_last_identity_id "$ident_id"
    set -g __llm_rad_last_identity_key "$key_path"
    set -g __llm_rad_last_identity_pub "$key_path.pub"
end

function __llm_rad_append_identity --argument-names ident_id name key_path pub_path expiry
    __llm_rad_ensure_state
    uv run --script "$LLM_RADICLE_PY" append-identity "$LLM_RADICLE_STATE_FILE" "$ident_id" "$name" "$key_path" "$pub_path" "$expiry"
end

function __llm_rad_list_identities --argument-names show_all
    __llm_rad_ensure_state
    if test "$show_all" = "true"
        uv run --script "$LLM_RADICLE_PY" list-identities "$LLM_RADICLE_STATE_FILE" --show-all
    else
        uv run --script "$LLM_RADICLE_PY" list-identities "$LLM_RADICLE_STATE_FILE"
    end
end

function __llm_rad_retire_identity --argument-names ident_id
    __llm_rad_ensure_state
    uv run --script "$LLM_RADICLE_PY" retire-identity "$LLM_RADICLE_STATE_FILE" "$ident_id"
end

function __llm_rad_retire_expired
    __llm_rad_ensure_state
    uv run --script "$LLM_RADICLE_PY" retire-expired "$LLM_RADICLE_STATE_FILE"
end

function __llm_rad_bind_repo --argument-names rid ident_id access note
    # Bindings are explicit (RID, identity, access) so multi-repo intent is
    # machine-readable and easy to rotate/review.
    __llm_rad_ensure_state
    uv run --script "$LLM_RADICLE_PY" bind-repo "$LLM_RADICLE_STATE_FILE" "$rid" "$ident_id" "$access" "$note"
end

function __llm_rad_list_bindings --argument-names rid show_all
    __llm_rad_ensure_state
    if test "$show_all" = "true"
        uv run --script "$LLM_RADICLE_PY" list-bindings "$LLM_RADICLE_STATE_FILE" "$rid" --show-all
    else
        uv run --script "$LLM_RADICLE_PY" list-bindings "$LLM_RADICLE_STATE_FILE" "$rid"
    end
end

function __llm_rad_unbind_repo --argument-names rid ident_id
    __llm_rad_ensure_state
    uv run --script "$LLM_RADICLE_PY" unbind-repo "$LLM_RADICLE_STATE_FILE" "$rid" "$ident_id"
end

function __llm_rad_print_env --argument-names ident_id
    __llm_rad_ensure_state
    set -l line (uv run --script "$LLM_RADICLE_PY" get-active-key "$LLM_RADICLE_STATE_FILE" "$ident_id")

    if test $status -ne 0; or test -z "$line"
        echo "Active identity not found: $ident_id" 1>&2
        return 1
    end

    echo "set -x RADICLE_SSH_KEY $line"
end

# Infer the Radicle identifier (RID) of the cwd repo.
# Strategy: try `git config --local rad.id` first (set by `rad init`); if that
# fails, try `rad inspect` if the rad CLI is on PATH. Reads from $ATB_USER_PWD
# first so the bin-shim dispatcher's cd doesn't break us.
function __llm_rad_rid_from_repo
    set -l cwd "$ATB_USER_PWD"
    if test -z "$cwd"
        set cwd "$PWD"
    end

    set -l rid (command git -C "$cwd" config --local --get rad.id 2>/dev/null)
    if test -n "$rid"
        echo "$rid"
        return 0
    end

    if command -sq rad
        # `rad inspect` prints metadata; the first line typically starts with
        # the RID. Use --rid if available (newer rad), else fall back to head.
        set rid (command rad inspect --rid 2>/dev/null | string trim)
        if test -n "$rid"
            echo "$rid"
            return 0
        end
        set rid (command rad inspect 2>/dev/null | string match -r '^rad:[A-Za-z0-9]+' | head -n 1)
        if test -n "$rid"
            echo "$rid"
            return 0
        end
    end

    return 1
end

function __llm_rad_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: slop-radicle --help" 1>&2
        return 1
    end

    set -l rid (__llm_rad_rid_from_repo)

    while true
        echo ""
        gum style --bold --foreground 212 "Radicle access"
        if test -n "$rid"
            gum style --faint "current repo RID: $rid"
        else
            gum style --foreground 196 "no RID detected for cwd — repo-scoped actions disabled"
        end
        gum style --faint "(Esc on the menu to quit. Every action prints its equivalent CLI.)"
        echo ""

        set -l choice (gum choose \
            "Create a new identity (24h TTL)" \
            "List active identities" \
            "Bind THIS repo to an identity" \
            "Unbind THIS repo" \
            "List bindings for THIS repo" \
            "Retire expired identities" \
            "Quit")

        if test -z "$choice"
            return 0
        end

        echo ""
        switch "$choice"
            case "Create*"
                set -l name (gum input --placeholder "label (e.g. session-1)" --prompt "name › ")
                if test -z "$name"
                    continue
                end
                __llm_rad_show_cli "slop-radicle create-identity --name $name --ttl 24h"
                if gum confirm --default=true "Create identity '$name'?"
                    slop-radicle create-identity --name "$name" --ttl 24h
                end
            case "List active*"
                __llm_rad_show_cli "slop-radicle list-identities"
                slop-radicle list-identities
            case "Bind*"
                if test -z "$rid"
                    gum style --foreground 196 "no RID for cwd; cannot bind"
                    continue
                end
                set -l ident_id (gum input --placeholder "identity id (from list)" --prompt "identity-id › ")
                if test -z "$ident_id"
                    continue
                end
                set -l access (gum choose ro rw)
                if test -z "$access"
                    continue
                end
                __llm_rad_show_cli "slop-radicle here bind --identity-id $ident_id --access $access"
                if gum confirm --default=true "Bind $rid to $ident_id ($access)?"
                    slop-radicle here bind --identity-id "$ident_id" --access "$access"
                end
            case "Unbind*"
                if test -z "$rid"
                    gum style --foreground 196 "no RID for cwd; cannot unbind"
                    continue
                end
                __llm_rad_show_cli "slop-radicle here unbind --yes"
                if gum confirm --default=false "Unbind $rid for ALL identities?"
                    slop-radicle here unbind --yes
                end
            case "List bindings*"
                if test -z "$rid"
                    gum style --foreground 196 "no RID for cwd; listing all bindings instead"
                    __llm_rad_show_cli "slop-radicle list-bindings --all"
                    slop-radicle list-bindings --all
                else
                    __llm_rad_show_cli "slop-radicle here list-bindings"
                    slop-radicle here list-bindings
                end
            case "Retire expired*"
                __llm_rad_show_cli "slop-radicle retire-expired --yes"
                if gum confirm --default=true "Retire all expired identities?"
                    slop-radicle retire-expired --yes
                end
            case "Quit"
                return 0
        end
    end
end

function __llm_rad_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function slop-radicle --description "Manage local ephemeral Radicle identities and repo bindings"
    if test (count $argv) -eq 0
        __llm_rad_usage
        return 1
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    if test "$cmd" = "-h"; or test "$cmd" = "--help"
        __llm_rad_usage
        return 0
    end

    if test "$cmd" = "tui"
        __llm_rad_tui $argv
        return $status
    end

    # `here` infers --rid from the cwd's Radicle metadata and falls through to
    # the normal dispatcher. The `info` subcommand is local sugar that just
    # prints the inferred RID and exits.
    if test "$cmd" = "here"
        if test (count $argv) -eq 0
            echo "Error: 'here' requires a subcommand." 1>&2
            echo "" 1>&2
            echo "Available 'here' subcommands:" 1>&2
            echo "  info, bind, unbind, list-bindings" 1>&2
            return 1
        end

        set cmd "$argv[1]"
        set -e argv[1]

        set -l inferred_rid (__llm_rad_rid_from_repo)
        if test -z "$inferred_rid"
            echo "Error: could not infer Radicle RID from $PWD." 1>&2
            echo "  Tried: git config --local rad.id, then 'rad inspect'." 1>&2
            echo "  Workaround: invoke the underlying subcommand with --rid <rad:...>." 1>&2
            return 1
        end

        switch "$cmd"
            case info
                echo "$inferred_rid"
                return 0
            case bind
                set cmd bind-repo
                set argv --rid "$inferred_rid" $argv
            case unbind
                set cmd unbind-repo
                set argv --rid "$inferred_rid" $argv
            case list-bindings
                set argv --rid "$inferred_rid" $argv
            case '*'
                echo "Error: unknown 'here' subcommand: $cmd" 1>&2
                echo "Available: info, bind, unbind, list-bindings" 1>&2
                return 1
        end
    end

    set -l name ""
    set -l ttl "$LLM_RADICLE_TTL"
    set -l ident_id ""
    set -l rid ""
    set -l access ""
    set -l note ""
    set -l yes "false"
    set -l show_all "false"
    set -l force "false"

    while test (count $argv) -gt 0
        switch "$argv[1]"
            case --name
                set name "$argv[2]"
                set -e argv[1..2]
            case --ttl
                set ttl "$argv[2]"
                set -e argv[1..2]
            case --id --identity-id
                set ident_id "$argv[2]"
                set -e argv[1..2]
            case --rid
                set rid "$argv[2]"
                set -e argv[1..2]
            case --access
                set access "$argv[2]"
                set -e argv[1..2]
            case --note
                set note "$argv[2]"
                set -e argv[1..2]
            case --yes
                set yes "true"
                set -e argv[1]
            case --force
                set force "true"
                set -e argv[1]
            case --all
                set show_all "true"
                set -e argv[1]
            case -h --help
                __llm_rad_usage
                return 0
            case '*'
                echo "Unknown argument: $argv[1]" 1>&2
                return 1
        end
    end

    switch "$cmd"
        case create-identity
            __llm_rad_require_tools; or return 1
            if test -z "$name"
                echo "create-identity requires --name" 1>&2
                return 1
            end
            set -l expiry (__llm_rad_ttl_to_iso "$ttl")
            if test $status -ne 0
                return 1
            end

            __llm_rad_generate_identity_key "$name" "$expiry"; or return 1
            __llm_rad_append_identity "$__llm_rad_last_identity_id" "$name" "$__llm_rad_last_identity_key" "$__llm_rad_last_identity_pub" "$expiry"; or return 1

            echo "Created Radicle identity"
            echo "  id: $__llm_rad_last_identity_id"
            echo "  name: $name"
            echo "  expires: $expiry"
            echo "  private key: $__llm_rad_last_identity_key"
            echo "  public key: $__llm_rad_last_identity_pub"

        case bootstrap-config
            __llm_rad_bootstrap_config "$force"

        case list-identities
            __llm_rad_require_tools; or return 1
            __llm_rad_list_identities "$show_all"

        case retire-identity
            __llm_rad_require_tools; or return 1
            if test -z "$ident_id"
                echo "retire-identity requires --id" 1>&2
                return 1
            end
            if not __llm_rad_confirm "Retire identity $ident_id?" "$yes"
                return 1
            end
            __llm_rad_retire_identity "$ident_id"
            if test $status -ne 0
                echo "Identity not found: $ident_id" 1>&2
                return 1
            end
            echo "Retired identity: $ident_id"

        case retire-expired
            __llm_rad_require_tools; or return 1
            if not __llm_rad_confirm "Retire all expired active identities?" "$yes"
                return 1
            end
            set -l retired (__llm_rad_retire_expired)
            if test -z "$retired"
                echo "No expired active identities found."
                return 0
            end
            echo "Retired identity IDs:"
            for i in $retired
                echo "  - $i"
            end

        case bind-repo
            __llm_rad_require_tools; or return 1
            if test -z "$rid"; or test -z "$ident_id"; or test -z "$access"
                echo "bind-repo requires --rid, --identity-id, and --access" 1>&2
                return 1
            end
            __llm_rad_validate_rid "$rid"; or return 1
            __llm_rad_validate_access "$access"; or return 1
            set -l op (__llm_rad_bind_repo "$rid" "$ident_id" "$access" "$note")
            if test $status -eq 2
                echo "Identity must exist and be active: $ident_id" 1>&2
                return 1
            else if test $status -ne 0
                return 1
            end
            echo "$op binding for $rid with identity $ident_id ($access)"

        case list-bindings
            __llm_rad_require_tools; or return 1
            if test -n "$rid"
                __llm_rad_validate_rid "$rid"; or return 1
            end
            __llm_rad_list_bindings "$rid" "$show_all"

        case unbind-repo
            __llm_rad_require_tools; or return 1
            if test -z "$rid"
                echo "unbind-repo requires --rid" 1>&2
                return 1
            end
            __llm_rad_validate_rid "$rid"; or return 1
            if not __llm_rad_confirm "Retire matching bindings for $rid?" "$yes"
                return 1
            end
            set -l removed (__llm_rad_unbind_repo "$rid" "$ident_id")
            if test -z "$removed"
                echo "No active bindings matched."
                return 0
            end
            echo "Retired bindings:"
            for item in $removed
                echo "  - $item"
            end

        case print-env
            __llm_rad_require_tools; or return 1
            if test -z "$ident_id"
                echo "print-env requires --identity-id" 1>&2
                return 1
            end
            __llm_rad_print_env "$ident_id"

        case '*'
            echo "Unknown command: $cmd" 1>&2
            __llm_rad_usage
            return 1
    end
end
