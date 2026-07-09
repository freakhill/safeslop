---
name: agent-sandbox-ops
description: >
  Operate safeslop isolation profiles safely: host, container, and VM.
---

# Agent Sandbox Ops Skill

Use this skill whenever tasks involve runtime isolation, network limiting, or
file transfer between host and sandboxed runtimes.

## Required pre-read

1. `CONTRIBUTING.md`
2. `AGENTS.md`
3. `README.md`
4. Relevant specs under `specs/`

## Command map

- `safeslop validate` — validate a policy against the embedded schema.
- `safeslop list` — list available profiles.
- `safeslop catalog list [--bundles] --output json` — list curated package catalog entries/bundles for profile creation UIs; the bundle-list envelope includes `data.defaults` (agent -> default bundle) for UI inheritance.
- `safeslop catalog bump <pkg> --to V [--security]` — bump a pin: resolve all-arch digests, enforce the version policy (LAW-A/B/C/D + monotonic floor + soak), write `catalog.cue`+`catalog.json`, print a plan sheet. `--security` waives the soak window only, never a LAW.
- `safeslop catalog propose-version <pkg>` — list upstream candidates newest-first with would-be digests + blast radius (read-only).
- `safeslop catalog add <pkg> --kind K --version V [--sha256 arch=hex]...` — add a pinned entry (channel ban + full validate).
- `safeslop catalog audit` — report staleness (versions-behind), yanked/unmaintained advisories, suggested lane (read-only).
- `safeslop bundle add|remove <name> <pkg>...` — mutate bundle membership, re-validating references.
- `safeslop bundle list --output json` — list curated bundles.
- `safeslop profile create --name N --agent A --environment E [--bundle B] [--package P] [--dry-run] --output json` — create or update a `safeslop.cue` profile; `--dry-run` resolves packages/recipe and returns engine risk data without writing.
- `safeslop profile credentials set <profile> [safeslop.cue] --provider github|forgejo [--use-origin] [--repo owner/name] [--write-repo owner/name] --output json` — engine-owned CUE mutation for GitHub/Forgejo repo scopes; preserves other credential providers/secrets and clears only the opposite forge.
- `safeslop profile credentials clear <profile> [safeslop.cue] --output json` — remove only `credentials.github`/`credentials.forgejo`, deleting the `credentials` object if it becomes empty.
- `safeslop creds list|show [<profile>] --output json` — inspect the credential posture of `safeslop.cue` profiles (declared creds + value-free readiness status); read-only, never reveals secret values.
- `safeslop creds link|unlink|status` — manage host-only account links in `~/.config/safeslop/accounts.cue` (refs + non-secret ids only); `creds status --output json` is the Emacs account-link status envelope.
- `safeslop profile show <name> --output json` — inspect a profile with resolved package set and dry-run image recipe.
- `safeslop lock [profile] --output json` — write repo-root `safeslop.lock.json` for the selected profile's recipe identity.
- `safeslop trust` — approve a policy's exact bytes for launch. Required by every launch lane: `safeslop run <profile>`, `session create --profile`, and the Emacs client all share this gate (specs/0072); an untrusted or changed `safeslop.cue` is refused with a `TRUST_REQUIRED` envelope.
- `safeslop untrust [safeslop.cue]` — remove that host approval; future launches fail closed until the current bytes are reviewed and trusted again.
- `safeslop run <profile>` — launch a trusted profile; host-tier profiles require a per-launch yes/no comprehension gate before the agent starts.
- `safeslop session create --profile <name> [--name <label>] --output json` — create an Emacs-visible session from an existing profile; create/list/status JSON includes value-free `credential_scopes` (credential kind, non-secret target, and access/scope only), the record includes resolved recipe/image metadata for the portal, and the Emacs client streams slow first-use image-build output into `*safeslop session progress*` with the final exit status. `--name` sets an optional display name and is combinable with `--profile`.
- `safeslop session create --agent <claude|pi|fish|zsh> --environment <host|container> --workspace <dir> [--name <label>] [--trust-host] --output json` — create an ad-hoc Emacs-visible session record (`--environment` is required). A host ad-hoc session runs the agent unconfined with your host credentials, so it requires an explicit `--trust-host` acknowledgement (specs/0072); container ad-hoc sessions do not. The interactive Emacs new-session flow prompts for that host acknowledgement before appending `--trust-host`, and if a host ad-hoc create returns `TRUST_REQUIRED` without a policy path, it offers one retry with the flag. `--name` sets an optional display name. `claude-code` remains accepted as a compatibility alias for `claude` but is not advertised in new UI/docs.
- `safeslop session run --session-id <id> [--detach]` — run the session agent under safeslop isolation. Host-tier sessions require the per-launch yes/no comprehension gate first; for `--detach`, the gate runs before the supervisor is spawned. Coupled (default) needs a controlling terminal (Emacs supplies one via `make-term`); with no usable TTY it emits the `PTY_UNAVAILABLE` contract error and the caller switches to the `--output jsonl` status monitor. `--detach` launches a per-session supervisor that owns the agent + its PTY, serves it over a per-session unix socket, and returns immediately (the buffer is freed).
- `safeslop session attach --session-id <id>` — rejoin a detached session's agent over its socket under a controlling terminal, exiting with the agent's code; one active attach at a time. No usable TTY emits `PTY_UNAVAILABLE`.
- `safeslop session status --session-id <id> --output <json|jsonl>` — inspect or monitor session state; JSON/JSONL carries value-free credential scope for profile-backed sessions, and a running detached session also reports its `socket`.
- `safeslop session stop --session-id <id> --revoke-credentials --output json` — stop idempotently, reconciling liveness/process identity before signalling, revoking ephemeral credentials before termination when requested, terminating the process (a detached supervisor's whole process group), removing the socket, and wiping the host stage dir.
- `safeslop session rm --session-id <id> --output json` — permanently remove one stopped/created session record so the portal does not accumulate dead-session corpses. Refuses a running session (stop it first); revokes any still-live staged credentials and wipes the host stage dir before deleting, so removal never orphans secrets. Returns `data.removed` (the removed id).
- `safeslop session rename --session-id <id> --name <label> --output json` — set (or, with an empty `--name`, clear) a session's human display name. Allowed in any status (created, running, or stopped) since a label touches no boundary, credential, or process state. The name is validated (control/format/bidi characters rejected, so it cannot break the JSONL line protocol or spoof a status) and, when set, is surfaced as `data.name` in the session envelope and shown in the portal. Unknown id → `SESSION_NOT_FOUND`; a rejected name → `INVALID_ARGUMENT`.
- `safeslop session prune --output json` — remove every stopped session record in one call, leaving created and running sessions untouched. Runs the liveness/process-identity reconcile first, so a crashed session (marked `running` but whose process is gone or whose PID was reused) is persisted as `stopped`; stale sockets and host stage dirs are swept in the same pass. Returns `data.removed` (the removed ids). In Emacs these are the portal's `x` (remove one) and `X` (prune) keys.
- `safeslop doctor` — report available tools and isolation tiers.
- `safeslop down` — tear down safeslop-managed host-container stacks by label, on the detected container runtime.
- `safeslop gc [--until <age>] [--keep <N>]` — remove only unreferenced safeslop-managed images; current resolving profiles, the repo lockfile, and live sessions anchor images.

Emacs-specific session guards: coupled and detached container run actions perform
a best-effort runtime preflight via `safeslop doctor --json`; a shadowed Docker
helper aborts before launching the terminal/subprocess and lists the
selected/shadowed paths, while failed/old doctor output proceeds to the CLI.
Socket reattach does not preflight Docker because it rejoins an existing
supervisor rather than selecting a runtime.

## Container runtime

The `container` tier runs on an **ambient, user-provided** container runtime; safeslop detects
one and drives it, and never installs, upgrades, or manages one. Have one present:

- **docker** (Docker Desktop / OrbStack / any docker-compatible CLI) — the only runtime
  egress-verified for `network: deny` today.
- **podman** — `podman` plus a working `podman compose`.
- **lima** — a user-managed lima instance on a containerd/nerdctl template (`lima nerdctl`).

Selection: `SAFESLOP_CONTAINER_RUNTIME=docker|podman|lima` forces one (used or fail closed — no
silent fallback); otherwise auto-detect **docker → podman → lima** (first with a working compose
wins); none present fails closed naming all three. Runtime CLIs are resolved once through safeslop's
sanitized host PATH and carried as absolute paths into later commands; shadowed runtime CLIs fail
closed. A `network: deny` profile is **refused on podman/lima** (not yet egress-verified) unless
`SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1` is set; teardown (`down`, the startup sweep, session reap) is
never gated.

## Default policy

- `environment` is required (`host` | `container` | `vm`) — there is no default
  tier (specs/0053 removed the macOS Seatbelt `sandbox` tier).
- Prefer `environment: "container"` with `network: "deny"` for everyday agent work:
  network-bound agents (claude, pi) need their runtime + egress inside the boundary.
- Use `environment: "vm"` for untrusted code or maximum isolation.
- Use `environment: "host"` only when you accept no isolation and can pass the per-launch consent gate.
- Do not mount or expose host credential directories to agents.

## Common workflows

### Create and inspect a profile

```bash
safeslop catalog list --bundles --output json
safeslop profile create --name review --agent claude --environment container --network deny --output json
safeslop profile show review --output json
safeslop lock review --output json
safeslop session create --profile review --output json
safeslop validate
safeslop list
safeslop run review --dry-run
```

In Emacs, `C-c s F` opens the Profiles surface. Use `RET`/`i` to inspect a
profile's resolved packages/egress/recipe, `r` to launch a session from the row
after an isolation/network summary, `e` to edit the CUE at that profile's block,
`c` to open `*safeslop profile compose*`, `C` to clone, `D` for guided manual
deletion, and `g` to refresh. The compose buffer shows catalog defaults as
selected/locked inherited rows, marks local project-language suggestions, and uses
`RET` to toggle unlocked rows, `?` for bundle/package help, `g` to refresh,
`C-c C-c` to request the engine `profile create --dry-run` safety preview before
the final write, and `q` to cancel. File reach is workspace-only here; arbitrary
custom host mounts are deferred until a mount capability model is specified.
`C-c s K` opens the Credentials surface: `a` links GitHub App / Forgejo accounts
using refs/ids only, `u` unlinks, and `p` opens the repo picker that writes
through `profile credentials set` (origin inference or manual `owner/repo` rows;
live repo discovery is deferred).

`C-c s P` opens the Sessions portal. The tab strip shows each surface's direct
switch key (`P` Sessions, `F` Profiles, `K` Credentials); `TAB`/`S-TAB` or
`[`/`]` cycle between them, and the strip is mouse-clickable. Portal rows include a
value-free `Creds` column sourced from `credential_scopes`, showing only credential
kind, non-secret target, and access/scope. Portal row keys: `RET`/`o`
state-aware open, `r` run, `R` run detached, `A` reattach, `i` details, `s`
stop/revoke, `x` remove one stopped session, `X` prune all stopped sessions, `c`
new, `g` refresh, `a` pause/resume auto-refresh. Live buffers opened from the
portal are named and annotated with profile/project, tier/net, and value-free
credential scope. Each
in-place refresh keeps point on the same session and preserves window scroll, so
it never jumps the cursor out from under a row action key; session-mutating row
actions refresh the portal in place instead of popping a JSON result buffer over
the dashboard.

### Maintain the catalog (bump / propose / add / audit)

The catalog source of truth is `internal/engine/policy/catalog.cue` (rendered to embedded
`catalog.json`; `make check` fails on drift). `bump`/`add` and `bundle add`/`remove`
re-emit **both** files in lockstep and print a reviewable plan sheet. Run from the repo
root (or pass `--catalog-dir`); add `--output json` for the machine contract.

```bash
safeslop catalog propose-version ripgrep          # survey candidates first (read-only)
safeslop catalog bump ripgrep --to 14.2.0          # enforce LAWs, write cue+json, plan sheet
safeslop catalog bump ripgrep --to 2.0.0 --security   # CVE lane: waives soak, never a LAW
safeslop catalog audit                            # staleness + advisory lanes
safeslop catalog add mytool --kind binary --version 1.0.0 --sha256 amd64=$(…) --sha256 arm64=$(…)
safeslop bundle add personal jq                   # re-validates the bundle
make check                                        # proves cue↔json sync, vet, tests
```

Bumps enforce: **A** atomic all-arch real digest, **B** stable channel only,
**C** apt coordinates the Debian-snapshot timestamp, **D** one version per name — plus
the monotonic floor and a SemVer-aware soak window. Non-semver kinds (apt/calver) are
flagged `requires-human-confirm`. The policy is canonized in
`specs/research/2026-06-30-version-policy-flo.md`.

### Trust and launch

```bash
safeslop trust
safeslop run review
safeslop untrust   # revoke approval when this repo should no longer launch without review
```

### Container profile

```cue
profiles: container_review: {
	agent:       "claude"
	environment: "container"
	network:     "deny"
	egress:      [".internal.example.com"]
}
```

The container tier enforces egress by topology: the agent sits on an internal
network and reaches HTTP(S) through the proxy allowlist. In `network: deny`, the
proxy allowlist is domain-only: numeric IP-literal destinations are denied before
matching, reverse-DNS lookups are disabled for the domain ACL, and Docker's
external DNS forwarding is pinned to the container loopback (local service names
such as `proxy` still resolve). Agent launches are hard-set to uid/gid 1000 in
Compose, matching the image user and writable tmpfs home.

### VM profile

```cue
profiles: vm_review: {
	agent:       "pi"
	environment: "vm"
	network:     "deny"
}
```

The VM tier is disposable. Use explicit staged state and copy boundaries; do not
rely on broad host mounts.

## Safety checklist

- Keep network allowlists narrow and documented.
- Prefer read-only credentials; use write credentials only for explicit workflows.
- Verify `safeslop doctor` output before depending on a tier; shadowed protected helpers are unsafe
  and must be removed/fixed rather than ignored.
- Run `safeslop down` to clean up safeslop-managed host-container stacks after interrupted work.
- Run `safeslop gc --keep 2` only when you want to reclaim unreferenced managed images; it preserves profile/lock/live-session anchors.

## Verification

```bash
go test ./internal/engine/container/ ./internal/engine/vm/ -v
make check
```

For Emacs surface, Doom, or Evil binding changes, also run the local UI matrix:
`make test-emacs-ui-matrix`.  It keeps `make check` hermetic while covering raw
Emacs, a Doom `map!` shim, locally installed Evil, Doom+Evil, and an opt-in
personal command via `SAFESLOP_UI_PERSONAL_CMD` (`SAFESLOP_UI_REQUIRE_PERSONAL=1`
makes that personal slot mandatory locally).
