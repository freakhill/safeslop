#!/usr/bin/env fish

# Purpose:
# - Run Homebrew installs inside a disposable macOS VM instead of on host.
# - Keep network policy explicit (default strict egress through proxy).
# - Keep host/guest file transfer explicit (copy-in/copy-out) to avoid accidental
#   broad host exposure.
#
# Design notes:
# - "strict-egress" is default because package install/build paths are high risk.
# - No automatic host mounts: explicit transfer is easier to reason about/audit.
#
# References:
# - Tart docs: https://tart.run/
# - OpenSSH ssh/scp: https://man.openbsd.org/ssh and https://man.openbsd.org/scp
# - Homebrew install docs: https://docs.brew.sh/Installation

set -g BREW_VM_BASE_TEMPLATE "brew-sandbox-base"
set -g BREW_VM_SESSION_NAME "brew-sandbox-session"
set -g BREW_VM_SOURCE_IMAGE "ghcr.io/cirruslabs/macos-sonoma-base:latest"
set -g BREW_VM_SSH_USER "admin"
set -g BREW_VM_KEEP_SESSION "false"
set -g BREW_VM_BOOT_TIMEOUT 120
set -g BREW_VM_SSH_TIMEOUT 120
set -g BREW_VM_NETWORK_POLICY "strict-egress"
set -g BREW_VM_PROXY_URL ""
set -g BREW_VM_SHARE_DIR "/tmp/llm-share"

function __brew_vm_examples
    # BEGIN AUTOGEN: examples section="How to sandbox brew with disposable Tart VMs"
    echo 'Load VM helper:'
    echo '  source scripts/slop-brew-vm.fish'
    echo
    echo 'Create base template once:'
    echo '  slop-brew-vm create-base'
    echo
    echo 'Install formula in disposable VM session:'
    echo '  set -x BREW_VM_PROXY_URL http://<proxy-host>:3128'
    echo '  slop-brew-vm install --network-policy strict-egress <formula>'
    echo
    echo 'Optional: inspect manually in VM shell:'
    echo '  set -x BREW_VM_KEEP_SESSION true'
    echo '  slop-brew-vm install <formula>'
    echo '  slop-brew-vm shell'
    echo '  slop-brew-vm destroy'
    echo
    echo 'Share files explicitly with host:'
    echo '  slop-brew-vm copy-in ./local-file.txt /tmp/llm-share/local-file.txt'
    echo '  slop-brew-vm copy-out /tmp/llm-share/result.txt ./result.txt'
    echo
    echo 'Verify policy enforcement:'
    echo '  slop-brew-vm verify-network'
    # END AUTOGEN: examples
end

function __brew_vm_help
    echo "slop-brew-vm — disposable Tart VM wrapper for Homebrew installs"
    echo ""
    echo "Description:"
    echo "  Each session clones a trusted base VM template, runs brew install"
    echo "  inside it (strict-egress through a proxy by default), and destroys"
    echo "  the session VM afterwards. The host stays untouched; explicit"
    echo "  copy-in/copy-out is the only way to move files in/out."
    echo ""
    echo "Usage:"
    echo "  source scripts/slop-brew-vm.fish"
    echo "  slop-brew-vm create-base"
    echo "  slop-brew-vm init"
    echo "  slop-brew-vm run [options] <command...>"
    echo "  slop-brew-vm shell [options]"
    echo "  slop-brew-vm install [options] <formula>"
    echo "  slop-brew-vm verify-network [--allow-url <url>] [--block-url <url>]"
    echo "  slop-brew-vm copy-in <host-path> <guest-path>"
    echo "  slop-brew-vm copy-out <guest-path> <host-path>"
    echo "  slop-brew-vm destroy"
    echo "  slop-brew-vm tui                       (interactive launcher; requires gum)"
    echo "  slop-brew-vm help"
    echo ""
    echo "Options:"
    echo "  --network-policy strict-egress|proxy-only|off"
    echo "                                    Default: \$BREW_VM_NETWORK_POLICY ($BREW_VM_NETWORK_POLICY)."
    echo "                                    strict-egress and proxy-only require"
    echo "                                    BREW_VM_PROXY_URL to be set."
    echo ""
    echo "Examples (synced from README → 'How to sandbox brew with disposable Tart VMs'):"
    __brew_vm_examples
    echo ""
    echo "Notes:"
    echo "  - Host/guest files are NOT auto-shared. Use copy-in/copy-out explicitly."
    echo "  - Recommended guest share path: $BREW_VM_SHARE_DIR (inside the disposable VM)."
    echo "  - Set BREW_VM_KEEP_SESSION=true to retain the VM after install for inspection."
    echo "  - Boot logs are written under \$XDG_STATE_HOME/safeslop/brew-vm/."
    echo "  - Full reference: README.md → 'How to sandbox brew with disposable Tart VMs'."
end

function __brew_vm_help_to_stderr
    __brew_vm_help 1>&2
end

# Backwards-compatible alias so other scripts (e.g. slop-sandboxctl) that still
# reference __brew_vm_usage keep working during the rollout.
function __brew_vm_usage
    __brew_vm_help
end

# Keep boolean parsing centralized so policy toggles stay consistent across future
# edits and wrappers.
function __brew_vm_truthy --argument-names value
    switch (string lower -- "$value")
        case 1 true yes on
            return 0
    end
    return 1
end

# Only allow explicit policy values to avoid silent insecure fallback.
function __brew_vm_validate_policy --argument-names policy
    if not contains -- "$policy" strict-egress proxy-only off
        echo "Error: Invalid --network-policy: $policy (allowed: strict-egress, proxy-only, off)" 1>&2
        echo "" 1>&2
        __brew_vm_help_to_stderr
        return 1
    end
end

function __brew_vm_exists --argument-names name
    tart list 2>/dev/null | string match -rq "(^|[[:space:]])$name([[:space:]]|\$)"
end

# Per-user state dir for host-side artifacts (boot logs, etc.). We deliberately
# avoid /tmp/<fixed-name> because that pattern is symlink-attack-able on
# multi-user systems and lets one user clobber another's logs. XDG_STATE_HOME
# is honored when set; otherwise we fall back to ~/.local/state, mirroring the
# convention used by slop-install elsewhere in this repo.
function __brew_vm_state_dir
    set -l root "$XDG_STATE_HOME"
    if test -z "$root"
        set root "$HOME/.local/state"
    end
    set -l dir "$root/safeslop/brew-vm"
    mkdir -p "$dir"; or return 1
    # Best-effort restrictive perms; ignore errors on filesystems that do not
    # support chmod (e.g. some network mounts).
    chmod 700 "$dir" 2>/dev/null
    echo "$dir"
end

function __brew_vm_require_tools
    for tool in tart ssh scp
        if not command -sq "$tool"
            echo "Missing required tool: $tool" 1>&2
            return 1
        end
    end
end

# SSH opts are deliberately non-interactive so scripts fail fast in automation.
function __brew_vm_ssh_opts
    set -l opts \
        -o BatchMode=yes \
        -o ConnectTimeout=5 \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null

    if set -q BREW_VM_SSH_KEY
        set -a opts -i "$BREW_VM_SSH_KEY"
    end

    echo $opts
end

function __brew_vm_ip
    tart ip "$BREW_VM_SESSION_NAME" 2>/dev/null
end

function __brew_vm_proxy_prefix --argument-names policy
    # Why: proxy env var injection is the least invasive way to force package
    # tooling through a policy-enforcing egress path. We fail closed in strict
    # modes when proxy URL is missing.
    if test "$policy" = "off"
        echo ""
        return 0
    end

    if test -z "$BREW_VM_PROXY_URL"
        echo "BREW_VM_PROXY_URL is required for --network-policy $policy" 1>&2
        return 1
    end

    echo "export HTTP_PROXY='$BREW_VM_PROXY_URL' HTTPS_PROXY='$BREW_VM_PROXY_URL' http_proxy='$BREW_VM_PROXY_URL' https_proxy='$BREW_VM_PROXY_URL'; "
end

function __brew_vm_ssh
    set -l ip (__brew_vm_ip)
    if test -z "$ip"
        echo "VM is not running: $BREW_VM_SESSION_NAME" 1>&2
        return 1
    end

    set -l opts (__brew_vm_ssh_opts)
    ssh $opts "$BREW_VM_SSH_USER@$ip" -- $argv
end

function slop-brew-vm-create-base --description "Create Tart base VM template for brew sandboxing"
    __brew_vm_require_tools; or return 1

    if __brew_vm_exists "$BREW_VM_BASE_TEMPLATE"
        echo "Base VM already exists: $BREW_VM_BASE_TEMPLATE"
        return 0
    end

    echo "Cloning base image into local template: $BREW_VM_BASE_TEMPLATE"
    tart clone "$BREW_VM_SOURCE_IMAGE" "$BREW_VM_BASE_TEMPLATE"
end

function slop-brew-vm-init --description "Clone and boot disposable brew VM"
    # Why: clone from trusted base each session so potentially compromised
    # install state does not persist to host or future runs.
    __brew_vm_require_tools; or return 1

    if not __brew_vm_exists "$BREW_VM_BASE_TEMPLATE"
        echo "Missing base VM template: $BREW_VM_BASE_TEMPLATE" 1>&2
        echo "Run: slop-brew-vm create-base" 1>&2
        return 1
    end

    if not __brew_vm_exists "$BREW_VM_SESSION_NAME"
        echo "Cloning disposable session VM: $BREW_VM_SESSION_NAME"
        tart clone "$BREW_VM_BASE_TEMPLATE" "$BREW_VM_SESSION_NAME"; or return 1
    end

    if test -z "(__brew_vm_ip)"
        echo "Booting VM: $BREW_VM_SESSION_NAME"
        set -l state_dir (__brew_vm_state_dir)
        if test -z "$state_dir"
            echo "Failed to prepare slop-brew-vm state directory" 1>&2
            return 1
        end
        set -l log_file "$state_dir/$BREW_VM_SESSION_NAME.log"
        tart run --no-graphics "$BREW_VM_SESSION_NAME" >"$log_file" 2>&1 &
        disown
    end

    set -l boot_deadline (math (date +%s) + $BREW_VM_BOOT_TIMEOUT)
    set -l ip ""
    while true
        set ip (__brew_vm_ip)
        if test -n "$ip"
            break
        end
        if test (date +%s) -ge $boot_deadline
            echo "Timed out waiting for VM IP" 1>&2
            return 1
        end
        sleep 1
    end

    set -l opts (__brew_vm_ssh_opts)
    set -l ssh_deadline (math (date +%s) + $BREW_VM_SSH_TIMEOUT)
    while true
        if ssh $opts "$BREW_VM_SSH_USER@$ip" "true" >/dev/null 2>&1
            break
        end
        if test (date +%s) -ge $ssh_deadline
            echo "Timed out waiting for SSH on $ip as $BREW_VM_SSH_USER" 1>&2
            return 1
        end
        sleep 1
    end

    __brew_vm_ssh zsh -lc "command -v brew >/dev/null"
    if test $status -ne 0
        echo "Homebrew is not available in VM session. Install it in the base template first." 1>&2
        return 1
    end

    __brew_vm_ssh mkdir -p "$BREW_VM_SHARE_DIR" >/dev/null
    echo "VM ready: $BREW_VM_SESSION_NAME ($ip)"
end

function __brew_vm_run_with_policy --argument-names policy
    # Why: single execution path keeps policy handling and escaping consistent.
    __brew_vm_validate_policy "$policy"; or return 1
    slop-brew-vm-init >/dev/null; or return 1

    if test (count $argv) -eq 1
        echo "Usage: slop-brew-vm run <command...>" 1>&2
        return 1
    end

    set -e argv[1]
    set -l escaped (string escape -- $argv)
    set -l cmd (string join " " -- $escaped)
    set -l prefix (__brew_vm_proxy_prefix "$policy")
    __brew_vm_ssh zsh -lc "$prefix$cmd"
end

function slop-brew-vm-run --description "Run command inside disposable brew VM"
    __brew_vm_run_with_policy "$BREW_VM_NETWORK_POLICY" run $argv
end

function slop-brew-vm-shell --description "Open interactive shell inside disposable brew VM"
    set -l policy "$BREW_VM_NETWORK_POLICY"
    if test (count $argv) -ge 2; and test "$argv[1]" = "--network-policy"
        set policy "$argv[2]"
    end

    __brew_vm_validate_policy "$policy"; or return 1
    slop-brew-vm-init >/dev/null; or return 1
    set -l prefix (__brew_vm_proxy_prefix "$policy")
    __brew_vm_ssh zsh -lc "$prefix exec zsh -l"
end

function slop-brew-vm-install --description "Audit and install formula in disposable brew VM"
    set -l policy "$BREW_VM_NETWORK_POLICY"
    if test (count $argv) -ge 2; and test "$argv[1]" = "--network-policy"
        set policy "$argv[2]"
        set -e argv[1..2]
    end

    if test (count $argv) -ne 1
        echo "Usage: slop-brew-vm install [--network-policy strict-egress|proxy-only|off] <formula>" 1>&2
        return 1
    end

    set -l formula "$argv[1]"
    slop-brew-vm-destroy >/dev/null 2>&1
    slop-brew-vm-init; or return 1

    echo "[1/3] Reviewing formula metadata"
    __brew_vm_run_with_policy "$policy" run brew info "$formula"; or return 1

    echo "[2/3] Dry-run install"
    __brew_vm_run_with_policy "$policy" run brew install --dry-run "$formula"; or return 1

    echo "[3/3] Installing in disposable VM"
    __brew_vm_run_with_policy "$policy" run brew install "$formula"; or return 1

    if __brew_vm_truthy "$BREW_VM_KEEP_SESSION"
        echo "Keeping VM for inspection: $BREW_VM_SESSION_NAME"
    else
        echo "Destroying disposable VM session"
        slop-brew-vm-destroy
    end
end

function slop-brew-vm-copy-in --description "Copy a host file/dir into VM"
    # Why: explicit transfer boundary is easier to audit than broad mounts.
    if test (count $argv) -ne 2
        echo "Usage: slop-brew-vm copy-in <host-path> <guest-path>" 1>&2
        return 1
    end

    set -l src "$argv[1]"
    set -l dst "$argv[2]"
    if not test -e "$src"
        echo "Host path does not exist: $src" 1>&2
        return 1
    end

    slop-brew-vm-init >/dev/null; or return 1
    set -l ip (__brew_vm_ip)
    set -l opts (__brew_vm_ssh_opts)
    scp $opts -r "$src" "$BREW_VM_SSH_USER@$ip:$dst"
end

function slop-brew-vm-copy-out --description "Copy a VM file/dir to host"
    # Why: explicit host writes reduce accidental data leakage from guest.
    if test (count $argv) -ne 2
        echo "Usage: slop-brew-vm copy-out <guest-path> <host-path>" 1>&2
        return 1
    end

    set -l src "$argv[1]"
    set -l dst "$argv[2]"

    slop-brew-vm-init >/dev/null; or return 1
    set -l ip (__brew_vm_ip)
    set -l opts (__brew_vm_ssh_opts)
    scp $opts -r "$BREW_VM_SSH_USER@$ip:$src" "$dst"
end

function slop-brew-vm-verify-network --description "Verify allowed and blocked network behavior"
    # Quick policy check: one expected-allow URL + one expected-block URL.
    set -l allow_url "https://registry.npmjs.org"
    set -l block_url "https://example.com"

    while test (count $argv) -gt 0
        switch "$argv[1]"
            case --allow-url
                set allow_url "$argv[2]"
                set -e argv[1..2]
            case --block-url
                set block_url "$argv[2]"
                set -e argv[1..2]
            case '*'
                echo "Unknown argument: $argv[1]" 1>&2
                return 1
        end
    end

    echo "Checking allowlisted URL: $allow_url"
    __brew_vm_run_with_policy "$BREW_VM_NETWORK_POLICY" run curl -I "$allow_url"; or return 1
    echo "Checking blocked URL: $block_url"
    __brew_vm_run_with_policy "$BREW_VM_NETWORK_POLICY" run sh -lc "curl -I '$block_url' >/dev/null 2>&1 && exit 1 || exit 0"; or return 1
    echo "Network verification passed for current policy: $BREW_VM_NETWORK_POLICY"
end

function slop-brew-vm-destroy --description "Stop and delete disposable brew VM"
    __brew_vm_require_tools; or return 1

    if not __brew_vm_exists "$BREW_VM_SESSION_NAME"
        echo "No VM session to destroy: $BREW_VM_SESSION_NAME"
        return 0
    end

    tart stop "$BREW_VM_SESSION_NAME" >/dev/null 2>&1
    tart delete "$BREW_VM_SESSION_NAME"
end

function __brew_vm_show_cli --argument-names cmd
    gum style --faint "Equivalent CLI:"
    echo "  $cmd"
    echo ""
end

function __brew_vm_tui
    if not command -sq gum
        echo "Error: 'gum' is required for the interactive TUI (soft dependency)." 1>&2
        echo "" 1>&2
        echo "Install:" 1>&2
        echo "  brew install gum" 1>&2
        echo "  https://github.com/charmbracelet/gum#installation" 1>&2
        echo "" 1>&2
        echo "Or use the CLI directly. See: slop-brew-vm help" 1>&2
        return 1
    end

    while true
        echo ""
        gum style --bold --foreground 212 "slop-brew-vm — disposable Tart VM for Homebrew installs"
        gum style --faint "session VM: $BREW_VM_SESSION_NAME    base template: $BREW_VM_BASE_TEMPLATE"
        gum style --faint "network policy: $BREW_VM_NETWORK_POLICY    proxy: "(test -n "$BREW_VM_PROXY_URL"; and echo $BREW_VM_PROXY_URL; or echo "(unset)")
        gum style --faint "(Esc on the menu to quit. Every action prints its equivalent CLI.)"
        echo ""

        set -l choice (gum choose \
            "Create base template (one-time)" \
            "Install a formula (audit + install in disposable VM)" \
            "Run a one-off command in the session VM" \
            "Open an SSH shell in the session VM" \
            "Verify network policy enforcement" \
            "Copy a file INTO the VM" \
            "Copy a file OUT of the VM" \
            "Destroy the session VM" \
            "Quit")

        if test -z "$choice"
            return 0
        end

        echo ""
        switch "$choice"
            case "Create base*"
                __brew_vm_show_cli "slop-brew-vm create-base"
                if gum confirm --default=true "Run slop-brew-vm create-base now?"
                    slop-brew-vm create-base
                end
            case "Install a formula*"
                set -l formula (gum input --placeholder "formula name (e.g. wget)" --prompt "formula › ")
                if test -z "$formula"
                    continue
                end
                __brew_vm_show_cli "slop-brew-vm install --network-policy $BREW_VM_NETWORK_POLICY $formula"
                if gum confirm --default=true "Install '$formula' in disposable VM?"
                    slop-brew-vm install --network-policy "$BREW_VM_NETWORK_POLICY" "$formula"
                end
            case "Run a one-off*"
                set -l cmd (gum input --placeholder "command (e.g. brew info wget)" --prompt "command › ")
                if test -z "$cmd"
                    continue
                end
                __brew_vm_show_cli "slop-brew-vm run --network-policy $BREW_VM_NETWORK_POLICY $cmd"
                if gum confirm --default=true "Run '$cmd' in VM?"
                    slop-brew-vm run --network-policy "$BREW_VM_NETWORK_POLICY" $cmd
                end
            case "Open an SSH*"
                __brew_vm_show_cli "slop-brew-vm shell --network-policy $BREW_VM_NETWORK_POLICY"
                if gum confirm --default=true "Open SSH shell in VM?"
                    slop-brew-vm shell --network-policy "$BREW_VM_NETWORK_POLICY"
                end
            case "Verify network*"
                __brew_vm_show_cli "slop-brew-vm verify-network"
                slop-brew-vm verify-network
            case "Copy a file INTO*"
                set -l src (gum input --placeholder "host path" --prompt "host › ")
                if test -z "$src"
                    continue
                end
                set -l dst (gum input --placeholder "guest path" --prompt "guest › " --value="$BREW_VM_SHARE_DIR/")
                if test -z "$dst"
                    continue
                end
                __brew_vm_show_cli "slop-brew-vm copy-in $src $dst"
                if gum confirm --default=true "Copy $src → VM:$dst ?"
                    slop-brew-vm copy-in "$src" "$dst"
                end
            case "Copy a file OUT*"
                set -l src (gum input --placeholder "guest path" --prompt "guest › " --value="$BREW_VM_SHARE_DIR/")
                if test -z "$src"
                    continue
                end
                set -l dst (gum input --placeholder "host path" --prompt "host › ")
                if test -z "$dst"
                    continue
                end
                __brew_vm_show_cli "slop-brew-vm copy-out $src $dst"
                if gum confirm --default=true "Copy VM:$src → $dst ?"
                    slop-brew-vm copy-out "$src" "$dst"
                end
            case "Destroy*"
                gum style --foreground 196 --bold "DESTRUCTIVE: stops + deletes the session VM '$BREW_VM_SESSION_NAME'."
                __brew_vm_show_cli "slop-brew-vm destroy"
                if gum confirm --default=false "Really destroy session VM?"
                    slop-brew-vm destroy
                end
            case "Quit"
                return 0
        end
    end
end

function slop-brew-vm --description "Unified wrapper for brew VM sandbox operations"
    if test (count $argv) -eq 0
        __brew_vm_help
        return 0
    end

    set -l cmd "$argv[1]"
    set -e argv[1]

    switch "$cmd"
        case help --help -h
            __brew_vm_help
        case tui
            __brew_vm_tui
            return $status
        case create-base
            slop-brew-vm-create-base
        case init
            slop-brew-vm-init
        case run
            set -l policy "$BREW_VM_NETWORK_POLICY"
            if test (count $argv) -ge 2; and test "$argv[1]" = "--network-policy"
                set policy "$argv[2]"
                set -e argv[1..2]
            end
            __brew_vm_run_with_policy "$policy" run $argv
        case shell
            slop-brew-vm-shell $argv
        case install
            slop-brew-vm-install $argv
        case verify-network
            slop-brew-vm-verify-network $argv
        case copy-in
            slop-brew-vm-copy-in $argv
        case copy-out
            slop-brew-vm-copy-out $argv
        case destroy
            slop-brew-vm-destroy
        case '*'
            echo "Error: Unknown command: $cmd" 1>&2
            echo "" 1>&2
            __brew_vm_help_to_stderr
            return 1
    end
end
