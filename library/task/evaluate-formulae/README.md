# task/evaluate-formulae/

Audit a Homebrew formula in a disposable [Tart](https://tart.run) macOS VM, then throw the VM away. Useful when a formula's tap is unfamiliar or its install script does too much.

## What it composes

- [`../../layer/vm/`](../../layer/vm/) — VM scaffolding (today: pointer to this recipe).
- [`scripts/slop-brew-vm.fish`](../../../scripts/slop-brew-vm.fish) — the actual runner. Subcommands: `create-base`, `init`, `install`, `run`, `shell`, `verify-network`, `copy-in`, `copy-out`, `destroy`.
- (Optional) [`../../layer/container/squid.conf`](../../layer/container/squid.conf) — proxy ACL if you want to constrain the guest's egress.

## Requirements

- macOS host with [Tart](https://tart.run) installed.
- Guest VM image with:
  - SSH enabled.
  - [Homebrew](https://brew.sh) installed (or install it once in the base template — see below).
  - User account matching `BREW_VM_SSH_USER` (default: `admin`).

## Default variables

| Var | Default | Meaning |
|---|---|---|
| `BREW_VM_SOURCE_IMAGE`     | `ghcr.io/cirruslabs/macos-sonoma-base:latest` | Where to pull the base image from. |
| `BREW_VM_BASE_TEMPLATE`    | `brew-sandbox-base`        | Trusted clone source you keep around. |
| `BREW_VM_SESSION_NAME`     | `brew-sandbox-session`     | Disposable per-evaluation clone. |
| `BREW_VM_SSH_USER`         | `admin`                    | Guest SSH account. |
| `BREW_VM_NETWORK_POLICY`   | `strict-egress`            | Set to `proxy-only` or `off` to relax. |
| `BREW_VM_PROXY_URL`        | (unset)                    | If set, plumbed into the guest as `HTTP_PROXY`/`HTTPS_PROXY`. |

## One-time setup

```fish
source scripts/slop-brew-vm.fish
slop-brew-vm create-base
slop-brew-vm init
slop-brew-vm run brew --version
slop-brew-vm destroy
```

If the base image does not include Homebrew, install it once in the base template:

```fish
source scripts/slop-brew-vm.fish
slop-brew-vm init
slop-brew-vm run /bin/bash -lc 'NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"'
slop-brew-vm run brew --version
```

Then stop the VM, keep it as your trusted base template, and clone-disposable for each suspicious formula evaluation.

## Per-formula evaluation

```fish
slop-brew-vm install <formula>
slop-brew-vm run brew info <formula>
slop-brew-vm shell                     # poke around manually if anything looks off
slop-brew-vm destroy                   # discard the VM
```

## Optional proxy enforcement

```fish
set -x HTTP_PROXY  http://<proxy-host>:3128
set -x HTTPS_PROXY http://<proxy-host>:3128
slop-brew-vm install <formula>
slop-brew-vm verify-network            # confirms allowed/blocked behavior matches the policy
```

`slop-brew-vm.fish` does not mutate guest network settings beyond plumbing those env vars; enforce host firewall rules and proxy policy separately ([LuLu](https://objective-see.org/products/lulu.html) / [pf](https://www.openbsd.org/faq/pf/)).

## Failure modes

- `tart` not installed → install with `brew install cirruslabs/cli/tart`.
- Pull fails → check the registry path; tap private images via `tart login` first.
- The VM survives unexpectedly → `slop-brew-vm destroy` is idempotent and explicit; nothing in the runner keeps a session beyond `BREW_VM_KEEP_SESSION=true`.

## Cleanup

`slop-brew-vm destroy` deletes the disposable clone. The trusted base template stays — re-run `slop-brew-vm create-base` to refresh it, or `tart delete <name>` if you want to remove it entirely.
