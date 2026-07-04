# 0057 — Remove the VM isolation tier (container-only)

Status: done
Follows specs/0053 (removed the macOS Seatbelt `sandbox` tier).

## Decision

safeslop is now **container-only**. The isolation `#Environment` is **`host` | `container`**.
The disposable Tart/Lima micro-VM tier (`vm`) is removed.

## Why

The VM was an *honest* strong boundary — unlike the Seatbelt `sandbox` tier removed
in 0053 (which overpromised), this is a cost/benefit call, not a dishonesty fix.

- **The security delta is real but narrow.** A container shares the **host kernel**;
  a VM does not. So the VM uniquely defended against a **kernel / container-runtime
  escape** (a Linux-kernel or runc/containerd 0-day, a docker-socket or host-bridge
  misconfig) turning a compromised agent into *host* compromise — in a VM that only
  reaches the guest kernel, then needs a far-harder hypervisor escape.
- **But for safeslop's actual threat model it added little.** The realistic risk is a
  misbehaving / prompt-injected / supply-chain-poisoned agent running the user's own
  claude/pi on the user's own repo — and that routes through the **same squid egress
  allowlist and the same writable workspace mount** the container already has. A VM
  does not stop exfil to an *allowed* domain, damage to the mounted repo, or misuse of
  a staged credential. Kernel-escape defense was VM-only; everything else ≈ container.
- **Its non-security value didn't outweigh the cost.** The VM could run what the
  read-only container can't (`toolchain:nix`'s writable `/nix` store; real init) and
  sidestepped the container-misconfig class — but at the cost of a full VM boot (GBs,
  Tart LRU-prunes at 100GB), extra config surface (`SAFESLOP_VM_PROXY_URL`, SSH keys),
  and macOS-specific machinery, for a tier rarely exercised by the captive coworker
  audience. The container (egress-allowlist + read-only + cap-drop) is the right default;
  the niche cases (truly-untrusted agent, nix) did not justify carrying the tier.

## What was removed

- `internal/engine/vm/` (the whole package: vm/launch/ssh/sshkey + tests + the Tart
  idempotency integration test).
- `"vm"` from `#Environment` (schema), `policy.EnvTier`, the `session create`/`run`
  validation + `--environment` help, `agentArgv`'s shell-tier `vm` case, and the Emacs
  environment picker + portal tier legend.
- The `claude-vm-disposable` preset; the `vm` risk/consent/lint handling; the per-tier
  `config.vm` SSH config + `.gitconfig.vm` PAT staging in `creds/`.
- `tart` from the install plan (`DesiredState`) + `doctor` probe; the `make
  test-integration` (real-Tart) target.
- `SAFESLOP_VM_*` env vars; `down`'s VM teardown; README/AGENTS/CLAUDE.md tier docs.

## What stays (do not confuse with the VM tier)

- **lima / nerdctl** — the rootless *container* backend (an alternative to host docker;
  specs/0043/0044). It boots a Linux VM to host containers, but it is the **container**
  tier's engine, not the removed `vm` environment. `limactl` stays in the install plan.
- The `host` and `container` tiers, with honest `EnvTier` labels (now host/container
  only). `environment` remains required, with no default (specs/0053).

## Method footer

- Mechanical removal delegated to a subagent against a `make check` + `make build` gate,
  then host-reviewed (every diff audited: the creds/install/lint changes are legitimate
  per-tier removals; a `doctorTiers["vm"]`-must-not-reappear regression guard was added,
  mirroring the 0053 sandbox guard).
- **Load-bearing decision (do not re-litigate):** container-only. The VM's kernel-escape
  defense did not justify its cost for safeslop's threat model; the container is the
  default and only isolation tier (plus `host`).
