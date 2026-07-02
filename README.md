# safeslop

> **Practice safe safeslop** — launch coding agents behind stronger isolation defaults.

`safeslop` is a single signed Go binary that launches coding agents under a
per-repo `safeslop.cue` policy. One file declares the agent, isolation tier,
network posture, toolchain, secrets, and ephemeral credentials; the binary
validates the policy, provisions what is needed, launches the session, and tears
staged state down on exit.

## What it provides

- **Honest isolation tiers**: `host` and `container`, with the active tier
  printed by `run`/`doctor`. `environment` is required — there is no default, so a
  profile always states its isolation explicitly.
- **Fail-closed policy trust**: `safeslop run` refuses an unapproved or changed
  `safeslop.cue` until you review and trust it.
- **Scrubbed child environments**: ambient host credentials are not inherited;
  only policy-declared secrets/credentials cross the boundary.
- **Ephemeral credentials**: staged deploy keys, registry tokens, cloud tokens,
  and Kubernetes configs are scoped to the run and wiped on exit.
- **Pinned tooling checks**: `make check` includes a Go pinning gate that rejects
  unpinned `latest` references in policy/build config inputs.

## Quick start

Create `safeslop.cue` at your repo root:

```cue
package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {agent: "claude", environment: "container", network: "deny"}
		pair:   {agent: "pi", environment: "container", network: "deny"}
	}
}
```

Then validate, approve, and run:

```bash
safeslop validate
safeslop list
safeslop trust
safeslop run review
```

Inspect without launching:

```bash
safeslop run review --dry-run
safeslop doctor
```

## CLI

```text
safeslop validate [safeslop.cue]    check against the embedded schema
safeslop list [safeslop.cue]        list profiles and resolved tiers
safeslop catalog list [--bundles] --output json   curated package catalog for UIs
safeslop catalog bump <pkg> --to V [--security]   bump a pin: resolve all-arch digests, enforce the policy, write a plan sheet
safeslop catalog propose-version <pkg>            list upstream candidates + would-be digests + blast radius (read-only)
safeslop catalog add <pkg> --kind K --version V   add a pinned entry (channel ban + full validate)
safeslop catalog audit                           report staleness, yanked/unmaintained advisories, suggested lane (read-only)
safeslop bundle add|remove <name> <pkg>...       mutate bundle membership (re-validates references)
safeslop bundle list --output json               curated bundles for UIs
safeslop profile list|presets --output json       profiles + preset library as the JSON contract
safeslop profile create --name N --agent A --environment E [--bundle B] [--package P] --output json
safeslop profile show <name> --output json         profile + resolved packages + image recipe
safeslop lock [profile] --output json              write repo-root safeslop.lock.json
safeslop trust [safeslop.cue]       approve this policy's exact bytes
safeslop run <profile> [--dry-run]  launch a trusted profile
safeslop session create --profile <name> [--name <label>] --output json
safeslop session create --agent <claude|pi|fish|zsh> --environment <host|container> --workspace <dir> [--name <label>] --output json
safeslop session run --session-id <id> [--detach]
safeslop session attach --session-id <id>
safeslop session status --session-id <id> --output <json|jsonl>
safeslop session stop --session-id <id> --revoke-credentials --output json
safeslop session rm --session-id <id> --output json        remove one stopped session record
safeslop session prune --output json                       remove all stopped session records
safeslop session rename --session-id <id> --name <label> --output json   set or clear a session's display name
safeslop doctor                     report available tools and isolation tiers
safeslop down                       tear down safeslop-managed container stacks
safeslop gc [--until <age>] [--keep <N>]   remove unreferenced safeslop-managed images
safeslop launch <profile>           open a terminal running a profile
```

Add `--json` to commands for machine-readable output where supported. Emacs-facing
commands emit the shared versioned JSON contract envelope (`schema_version`,
`ok`, `data`, `warnings`, `errors`). Session status also supports `--output
jsonl` for a line-delimited monitor stream.

`session status` and `session list` reconcile liveness: a session still marked
`running` whose run process is gone (crash, kill, host sleep) is reported — and
persisted — as `stopped`, so status never lies about a session that is no longer
executing. Container sessions are additionally reaped by their
`safeslop.session=<id>` labels during stop/reconcile, so teardown does not depend
on a still-readable session record.

`session stop` (and a terminal/buffer close) tears the boundary down rather than
just killing the wrapper: credentials are revoked before process termination when
`--revoke-credentials` is requested, the run receives `SIGTERM`/`SIGHUP`, and the
labelled container boundary is reaped; staged secrets are wiped by run teardown
or the reap path. Interactive `Ctrl-C` (`SIGINT`) is left for the agent and does
not tear the session down.

`session run` is an interactive attach and needs a controlling terminal — Emacs
supplies one via `make-term`. Invoked without a usable TTY (a pipe, cron, a
headless shell), it emits the `PTY_UNAVAILABLE` contract error
(`details.fallback = "status-jsonl"`) and exits non-zero **without** marking the
session running, so nothing is left as a phantom. The Emacs client switches to a
read-only `--output jsonl` status monitor on that code.

`session run --detach` gives a session a life independent of the Emacs buffer that
started it: it launches a per-session **supervisor** that owns the agent and its
PTY — which, for a host session, is made the agent's controlling
terminal so it behaves like a real interactive run (`/dev/tty`, terminal signals,
hangup) — and
serves that PTY over a per-session unix socket
(`$SAFESLOP_STATE_DIR/sessions/<id>.sock`, surfaced as the session's `socket`
field; when that path would exceed the `sun_path` limit it is transparently
relocated to a short private runtime dir), and returns immediately. `session attach --session-id <id>` rejoins the
running agent over that socket — bridging the local terminal, forwarding
window-size changes, and exiting with the agent's code — with at most one client
attached at a time. Attaching when no supervisor is serving the socket reports
`SESSION_NOT_RUNNING` rather than the more specific `SESSION_STOPPED`. `session
stop` then signals the supervisor's whole process
group (graceful `SIGTERM`, then `SIGKILL`) so the boundary tree is torn down and
the socket removed; a supervisor that dies uncleanly has its stale socket swept on
the next `session status`/`list` reconcile.

Detaching is a deliberate trade-off: a detached agent holds its staged secrets
(`secrets.env`, deploy keys, kubeconfig) for its whole — possibly long — life,
where a coupled run bounds them to the buffer's lifetime. `stop
--revoke-credentials` still revokes before the kill, and liveness reconcile plus
the stale-resource sweep bound the leak if the supervisor dies uncleanly.

An exited session stays listed as `stopped` (with its exit code and last error)
rather than vanishing, so its outcome is inspectable. `session rm --session-id
<id>` removes one such record and `session prune` removes every stopped record in
one call, so the session list does not accumulate dead-session corpses. Both
refuse a still-running session — stop it first — and revoke any still-live staged
credentials before deleting a record, so a removal can never orphan secrets on
disk. `prune` first runs the liveness reconcile, so a crashed session (marked
`running` but whose process is gone) is persisted as `stopped` and swept in the
same pass. The Emacs portal exposes these as `x` (remove one) and `X` (prune).

A session can carry an optional human display name. Set it at creation with
`session create --name <label>` (combinable with `--profile`), or later — in any
status — with `session rename --session-id <id> --name <label>`; an empty
`--name` clears it. The name is validated (control, format, and bidi characters
are rejected so it cannot break the JSONL line protocol or spoof a status) and,
when set, is surfaced in the session's `status`/`list` envelope and the Emacs
portal.

`safeslop down` removes safeslop-managed host-container stacks by label. Container
startup also sweeps managed, record-less orphan boundaries on the detected
container runtime. `safeslop gc` only removes safeslop-managed images after
protecting the current image recipe for resolving profiles, the repo
`safeslop.lock.json`, and running session image references; `--until` and `--keep`
apply only to the unreferenced remainder.

## Container runtime

The `container` tier runs on an **ambient, user-provided** container runtime — safeslop
detects one and drives it; it never installs, upgrades, or manages one. Have one of these
present:

- **docker** — Docker Desktop, OrbStack, or any docker-compatible CLI on PATH (today's
  behaviour). The only runtime egress-verified for the `network: deny` tier.
- **podman** — `podman` plus a working `podman compose`.
- **lima** — a user-managed lima instance on a containerd/nerdctl template (`lima nerdctl`).

Selection order: `SAFESLOP_CONTAINER_RUNTIME=docker|podman|lima` (an explicit override — that
runtime is used or the command fails closed, never a silent fallback to another), else
auto-detect in the fixed precedence **docker → podman → lima** (first whose CLI *and* working
compose capability is present wins). With none present/working, the command fails closed and
names all three.

**Deny-tier fail-closed:** a `network: deny` profile must place the agent on a network with no
default route. Only docker/OrbStack are egress-verified for this today, so launching a `deny`
profile on **podman or lima is refused** unless you set `SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1`
to accept the (still-unverified) risk. Teardown — `down`, the startup sweep, session reap — is
never gated, so cleanup works on any detected runtime, verified or not.

## `safeslop.cue` reference

A representative profile:

```cue
package safeslop

safeslop: {
	version: 1
	profiles: {
		work: {
			agent:       "claude"          // "claude" | "claude-code" | "pi" | "fish" | "zsh"
			environment: "container"       // "host" | "container"  (required)
			network:     "deny"            // "deny" | "allow"
			workspace:   "."
			bundles:     ["base-tools"]
			packages:    ["pnpm"]
			egress:      [".internal.example.com"]

			secrets: {
				ANTHROPIC_API_KEY: "op://Private/Anthropic/credential"
			}

			credentials: {
				// GitHub deploy keys: omit repos to infer the current origin, or declare
				// repos for one deploy key per repo.
				ssh: {
					mode:  "deploy-key"
					repos: [{repo: "owner/web"}, {repo: "owner/api", write: true}]
				}

				// GitHub PAT opt-in: one existing fine-grained HTTPS token for repos.
				// The token is staged in a wipe-on-exit file, not embedded in config.
				// ssh: {mode: "pat", pat: "env:GITHUB_FINE_GRAINED_PAT", repos: [{repo: "owner/web"}]}

				// Forgejo/Gitea deploy keys or PATs. url is required for multi-repo/PAT.
				forgejo: {
					mode:       "deploy-key"
					url:        "https://forgejo.example.com"
					token:      "env:FORGEJO_ADMIN_TOKEN"
					"ssh-port": 2222
					repos:      [{repo: "owner/web"}]
				}

				aws:  {profile: "my-sso-profile"}
				gcp:  {}
				pnpm: [{host: "registry.npmjs.org", token: "op://Private/npm/token"}]
			}

			toolchain: {kind: "mise"} // "mise" | "nix" | "none"
		}
	}
}
```

`agent: "claude-code"` is accepted as a user-facing alias for Claude Code and is
normalized to the canonical `claude` engine value, so it launches the same
`claude` binary and is reported as `claude` in status output.

`bundles` and `packages` select build-time packages from the curated catalog. Use
`safeslop catalog list --output json` or `safeslop catalog list --bundles --output
json` to inspect available entries; `profile show` reports the resolved package
set and dry-run image `recipeID` without building. `safeslop lock [profile]
--output json` writes the repo-root `safeslop.lock.json` for review/commit.

The curated bundles (`catalog list --bundles`) cover common toolchains. Declaring a
bundle pulls in its packages' `requires`-closure in topological install order and
unions each package's runtime egress into the squid allowlist (never relaxing
default-deny):

| bundle | packages | for |
|---|---|---|
| `base-tools` | ripgrep, fd, bat, eza, fzf, zoxide | everyday CLI ergonomics |
| `claude` | node, claude-code | the `claude` agent (its default) |
| `go` | go | Go toolchain (module proxy + checksum DB egress) |
| `node` | node, pnpm, bun | JS/TS work |
| `personal` | CLI ergonomics + node/python/go/rust toolchains + hyperfine/tokei/sccache | daily-driver multi-language set |
| `pi` | node, pi | the `pi` agent (its default) |
| `python` | python3, uv, ruff | Python work |
| `rust` | rust + cargo-nextest/audit/deny/expand/make/watch + sccache | Rust toolchain + cargo subcommands |
| `rust-embedded` | rust, cargo-binutils, flip-link | no_std / embedded targets |
| `web` | node, pnpm, typescript, vite, eslint, prettier, web-ext | JS/TS web development |

Catalog packages and bundles are safeslop-owned, version- and (for `binary` kinds)
per-arch sha256-pinned; extending the catalog is a code edit + review, which is the
supply-chain review boundary. The package-version selection and bump policy that
governs which pin lands is canonized in `specs/research/2026-06-30-version-policy-flo.md`.

### Catalog version tooling

The catalog source of truth is the authored `internal/engine/policy/catalog.cue`, rendered
to an embedded `catalog.json` (`make render-catalog`; a `make check` sync gate fails on
drift). `catalog bump`/`add` and `bundle add`/`remove` mutate it: they load the catalog,
run the engine, and re-emit **both** `catalog.cue` and `catalog.json` in lockstep, then
print a reviewable plan sheet. Run them from the repo root (or pass `--catalog-dir`):

```text
safeslop catalog bump ripgrep --to 14.2.0         # resolve all-arch digests, enforce LAWs, write + plan sheet
safeslop catalog bump ripgrep --to 14.2.0 --security   # waive the soak window only (never a LAW)
safeslop catalog propose-version ripgrep          # newest-first upstream candidates + would-be shas + blast radius
safeslop catalog add mytool --kind binary --version 1.0.0 --sha256 amd64=… --sha256 arm64=…
safeslop catalog audit                           # versions-behind, yanked/unmaintained, suggested lane
safeslop bundle add personal jq                  # add a package to a bundle (re-validates)
```

Every bump enforces the four hard LAWs: **A** atomic all-arch real digest (no
`sha256Unresolved` survives), **B** stable channel only (rejects rc/beta/nightly/…),
**C** apt bumps coordinate the Debian-snapshot timestamp, **D** one version per name —
plus the monotonic floor (never roll back) and a SemVer-aware soak window (`--security`
waives soak, never a LAW). Non-semver kinds (apt, calver) return candidates flagged
`requires-human-confirm`. Live fetch is hermetic in tests (a fixture seam); production
uses `net/http`. Add `--output json` for the enveloped machine contract.

`safeslop session create --profile <name> --output json` creates an Emacs-visible
session from an existing `safeslop.cue` profile: it uses the profile's agent,
environment, network, and workspace, resolves its catalog package set, and includes
`recipeID`/`image` metadata for the portal Recipe/Image columns. In Emacs, this
profile-backed path opens `*safeslop session progress*` so slow first-use image
build logs stream live and end with the subprocess exit status. The ad-hoc
`--agent` form remains available for one-off sessions.

### Starter profiles (presets)

safeslop ships a small library of known-good starting points so you don't write a
`safeslop.cue` from scratch. List them as the JSON contract:

```bash
safeslop profile presets --output json
```

Each preset is a complete, validated `safeslop.cue` with a one-line description:

| preset | what it gives you |
|---|---|
| `claude-container-allowlist` | Claude Code in a container, default-allowlist egress (the safe default). |
| `claude-subscription-container` | Same, but authenticated with your Claude **subscription** token (not an API key). |
| `claude-host-unconfined` | Claude Code on the host — **no isolation**; convenient, not contained. |
| `pi-container-allowlist` | The `pi` agent in a container, default-allowlist egress. |
| `shell-container` | A plain `fish` shell in a container — a sandboxed shell, no coding agent. |

The Emacs Profiles surface (`C-c s F`) is a list of your `safeslop.cue` profiles
with ergonomic CRUD and launch keys: `RET`/`i` inspect a profile's resolved
packages, egress, and image recipe (read-only, no file edit); `x` launches a
session from the selected profile after an isolation/network summary; `e` opens
the CUE file jumped to that profile's block; `n` creates one with structured
prompts (the name is validated and overwriting an existing profile is confirmed);
`c` clones the row at point (only a new name is required); `D` guides deletion
(pick the target, confirm, then remove the block by hand); `g` refreshes. Creating
is backed by the catalog/bundle lists and routes through `profile create`, while
CUE stays the stored source of truth. This repo also dogfoods a checked-in
`safeslop.cue` with `default`, `pi`, and `shell` profiles so the Profiles surface
has useful local rows immediately.

### Trust model

`validate`, `list`, and `run --dry-run` are inspection commands and do not require
trust. A real launch requires the exact current policy bytes to be trusted:

```bash
safeslop trust
safeslop run work
```

If an agent or editor changes the policy, launch is blocked until you review and
trust the new bytes.

### Isolation tiers

`environment` is required — there is no default (specs/0053 removed the macOS
Seatbelt `sandbox` tier: it could not run the network-bound, home-installed agents
safeslop launches, only confined accidents).

| environment | label | summary |
|---|---|---|
| `host` | none | No isolation boundary; the agent runs as you. |
| `container` | egress-allowlisted | An ambient docker/podman/lima container plus proxy topology for per-domain egress control. |

Use `container` for routine agent sessions (network-bound agents belong here),
and `host` only when you accept no isolation.

## Credentials

Credentials are staged under the run's runtime directory and wiped on exit.
Deploy-key revocation is best-effort; private-key deletion is the decay-first
safety guarantee.

- GitHub deploy keys use `credentials.ssh`.
- Forgejo/Gitea deploy keys use `credentials.forgejo` with an API token ref.
- Multi-repo deploy-key mode creates one key per repo and uses per-repo SSH host
  aliases plus git URL rewrites so git chooses the correct key.
- PAT mode (`mode: "pat"`) stages one existing fine-grained token for explicit
  repos. safeslop does not mint or revoke account PATs; rotate/revoke them at
  the forge.

### Inspecting credential posture (Emacs `C-c s K`, `safeslop creds`)

The Emacs Credentials surface (`C-c s K`, "Keys") makes a workspace's credential
posture legible *before* launch. For every profile it lists each declared secret
and credential with its **source ref** (`op://…`/`env:NAME` — a reference, never a
value), whether it is **ephemeral** (a deploy key minted per session and wiped on
exit) or **ref-backed**, and — for the ref-backed ones — a value-free **readiness
status**: `resolvable`, `missing`, `op-signed-out`, `op-unavailable`, `ephemeral`,
or `ambient` (host SSO/ADC). `RET`/`i` inspect a profile's credentials, `e` opens
the `safeslop.cue` credentials block (authoring stays CUE-canonical — you edit
refs, not values), and `g` re-probes.

The surface is backed by `safeslop creds list [safeslop.cue] --output json` and
`safeslop creds show <profile> --output json`. The readiness probe resolves each
ref only to keep the pass/fail result and **discards the value**, so no secret is
ever read into the UI or the envelope. There is no in-UI mint/revoke — ephemeral
keys live and die with a session (`run`/`session`), so the surface is read +
status + jump-to-edit, never a secret vault.

## Development

Requirements for engine work:

```bash
make check
make build
```

Local developer install:

```bash
make install       # ~/.local/bin/safeslop + ~/.local/share/safeslop/emacs
make install-emacs # only sync the Emacs package under ~/.local/share/safeslop/emacs
```

If a future safeslop MCP server package is added under `cmd/*mcp*`, `make install`
will also build and install it into `~/.local/bin`.

`make check` performs:

- container asset drift check
- specs/0049 pivot denylist check
- `go vet ./...`
- `gofmt` verification for `cmd` and `internal`
- `go test ./...`
- Emacs package smoke/contract/session tests via `make test-emacs`

Useful targeted tests:

```bash
go test ./internal/engine/creds/ -v
go test ./internal/engine/policy/ -run 'Pinned|Latest' -v
go test ./internal/cli/ -v
go test ./internal/engine/session ./internal/jsoncontract -v
make test-emacs EMACS=/absolute/path/to/emacs
```

The checked Emacs package consumes the Go golden JSON fixtures directly from
`internal/jsoncontract/testdata/*.golden.json`; there is no copied fixture set.
CI scaffolding for a pinned Emacs 32.1 source build lives under `ci/emacs32/`.

Active development happens on the Forgejo remote. Use branches and Forgejo PRs;
GitHub is a release mirror.

## Release builds

Build local binaries with:

```bash
make build
make dist
```
