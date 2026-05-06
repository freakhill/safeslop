# Tart Brew Sandbox Template

This document defines the assumptions for `scripts/slop-brew-vm.fish`.

## Requirements

- macOS host with [Tart](https://tart.run) installed
- Guest VM image with:
  - SSH enabled
  - Homebrew installed
  - user account matching `BREW_VM_SSH_USER` (default: `admin`)

## Default variables used by `slop-brew-vm.fish`

- `BREW_VM_SOURCE_IMAGE=ghcr.io/cirruslabs/macos-sonoma-base:latest`
- `BREW_VM_BASE_TEMPLATE=brew-sandbox-base`
- `BREW_VM_SESSION_NAME=brew-sandbox-session`
- `BREW_VM_SSH_USER=admin`

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

Then stop the VM, keep it as your trusted base template, and run disposable clones for each suspicious formula evaluation.

## Optional proxy enforcement in guest

To route guest traffic through an allowlist proxy, export before `brew-vm-*` commands:

```fish
set -x HTTP_PROXY http://<proxy-host>:3128
set -x HTTPS_PROXY http://<proxy-host>:3128
```

`slop-brew-vm.fish` itself does not mutate guest network settings; enforce host firewall rules and proxy policy separately.
