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
- **Fail-closed policy trust and host consent**: every launch lane — `safeslop
  run`, `safeslop session run`, and the Emacs client — refuses an unapproved or
  changed `safeslop.cue` until you review and trust it; an ad-hoc host session
  requires an explicit `--trust-host` acknowledgement, and every host-tier launch
  requires a per-launch yes/no comprehension gate before the agent starts.
- **Scrubbed child environments**: ambient host credentials are not inherited;
  only policy-declared secrets/credentials cross the boundary.
- **Ephemeral credentials**: staged deploy keys, registry tokens, Kubernetes
  configs, and short-lived cloud env credentials are scoped to the run; on-disk
  staged state is wiped on exit.
- **Pinned tooling checks**: `make check` includes Go gates that reject unpinned
  `latest` references and raw protected host-helper exec regressions.
- **Hardened host helpers**: safeslop-owned helper CLIs (`op`, cloud CLIs, git/ssh
  helpers, container runtimes) resolve through safeslop's sanitized host PATH;
  shadowed security-critical helpers fail closed instead of warning through.

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
safeslop catalog list [--bundles] --output json   curated package catalog for UIs (bundle JSON includes defaults)
safeslop catalog bump <pkg> --to V [--security]   bump a pin: resolve all-arch digests, enforce the policy, write a plan sheet
safeslop catalog propose-version <pkg>            list upstream candidates + would-be digests + blast radius (read-only)
safeslop catalog add <pkg> --kind K --version V   add a pinned entry (channel ban + full validate)
safeslop catalog audit                           report staleness, yanked/unmaintained advisories, suggested lane (read-only)
safeslop bundle add|remove <name> <pkg>...       mutate bundle membership (re-validates references)
safeslop bundle list --output json               curated bundles for UIs
safeslop profile list|presets|defaults --output json profiles, scaffold presets, or builtin defaults
safeslop profile create --name N --agent A --environment E [--bundle B] [--package P] [--no-default-bundle] [--dry-run] --output json
safeslop profile show <name> --output json         profile + resolved packages + image recipe
safeslop profile credentials set <profile> [safeslop.cue] --provider <github|forgejo> [--use-origin|--repo owner/name ...] [--write-repo owner/name ...] --output json
safeslop profile credentials clear <profile> [safeslop.cue] --output json
safeslop creds link|unlink|status                 manage value-free forge account links
safeslop creds status --output json               account-link status envelope for UIs
safeslop lock [profile] --output json              write repo-root safeslop.lock.json
safeslop trust [safeslop.cue]       approve this policy's exact bytes
safeslop untrust [safeslop.cue]     remove approval so launches must be re-trusted
safeslop run <profile> [--dry-run]  launch a trusted profile
safeslop session create --profile <name> [--name <label>] --output json
safeslop session create --agent <claude|pi|fish|zsh> --environment <host|container> --workspace <dir> [--name <label>] [--trust-host] --output json
safeslop session run --session-id <id> [--detach]
safeslop session attach --session-id <id>
safeslop session status --session-id <id> --output <json|jsonl>
safeslop session egress observations --session-id <id> --output json
safeslop session egress grants --session-id <id> --output json
safeslop session egress grant --session-id <id> --host example.com --port 443 --output json
safeslop session egress revoke --session-id <id> --grant-id G --output json
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

`profile defaults --output json` lists the signed-binary builtin profiles
(`claude`, `fish`, `pi`, and `zsh`), while `profile presets` remains the separate
scaffold-template library. `profile show pi --output json` and `session create
--profile pi --output json` work without a local `safeslop.cue`. A valid project
profile of the same name takes precedence and retains its normal trust gate; an
invalid local policy fails closed rather than falling back. Resolved project and
builtin JSON includes `profile_source`, `profile_name`, `policy_path`, and
`policy_hash`; builtin paths are `builtin:<name>` and their hashes pin the
embedded profile used when the session runs.

`session create --profile`, `session status`, and `session list` include
value-free credential scope in the JSON contract as `credential_scopes` for
profile-backed sessions. Each row names only the credential kind, non-secret
target, and access/scope; it does not include token values, source references,
or staged file paths. Ad-hoc sessions and profiles without credentials omit it
or return an empty array.

### Progressive session egress

There is no `network: "progressive"` policy value. Progressive egress is an
operator-invoked, session-scoped overlay available only to
`environment: "container"` + `network: "deny"` sessions. Use `observations` to
inspect denied, value-free FQDN:port destinations, then explicitly `grant` or
`revoke` one exact destination. A grant never mutates `profile.egress` or
`safeslop.cue`; it is removed with the session. Proxy overlay/reload failure
keeps the prior, more-restrictive deny state.

Only exact FQDNs on port 80 or 443 are eligible. IP literals, private or
link-local addresses, localhost/metadata, credential broker or mint endpoints,
wildcards, suffixes, raw URLs, and other ports are non-grantable. Host-tier and
`network: "allow"` sessions are not enforceably isolated and reject these
commands. Observations are non-modal: agent traffic never opens a prompt or
changes network authority. In Emacs session detail, `o` shows observations, `G`
lists grants, `+` grants an operator-entered FQDN:port, and `-` revokes a grant.

`session status` and `session list` reconcile liveness: a session still marked
`running` whose recorded process is gone — or whose PID now names a different
process identity after reuse — is reported and persisted as `stopped`, so status
never lies about a session that is no longer executing. Container sessions are
additionally reaped by their `safeslop.session=<id>` labels during
stop/reconcile, and the reconstructed host stage dir is wiped, so teardown does
not depend on a still-readable session record.

`session stop` (and a terminal/buffer close) tears the boundary down rather than
just killing the wrapper: credentials are revoked before process termination when
`--revoke-credentials` is requested, stop reconciles the recorded PID/process
identity before signalling (so a stale detached PGID is not targeted), the run
receives `SIGTERM`/`SIGHUP`, and the labelled container boundary is reaped;
staged secrets are wiped by run teardown or the stop/reconcile cleanup path.
Interactive `Ctrl-C` (`SIGINT`) is left for the agent and does not tear the
session down.

`session run` is an interactive attach and needs a controlling terminal — Emacs
supplies one via `make-term`. Invoked without a usable TTY (a pipe, cron, a
headless shell), it emits the `PTY_UNAVAILABLE` contract error
(`details.fallback = "status-jsonl"`) and exits non-zero **without** marking the
session running, so nothing is left as a phantom. The Emacs client switches to a
read-only `--output jsonl` status monitor on that code.

Before Emacs starts a coupled or detached container session, it performs a
best-effort runtime preflight with `safeslop doctor --json`. Same-file Docker
aliases are reported as `alias_paths` and do not block launch; genuinely distinct
helpers appear in `shadowed_paths`, which makes Emacs abort before opening the
launch subprocess and show the selected and shadowed paths. If doctor fails or
emits older JSON, launch continues and the CLI remains authoritative. Reattaching to an already-detached session uses the existing
supervisor socket and does not preflight Docker.

`session run --detach` gives a session a life independent of the Emacs buffer that
started it: after the host consent gate passes for host-tier sessions, it launches
a per-session **supervisor** that owns the agent and its PTY — which, for a host
session, is made the agent's controlling terminal so it behaves like a real
interactive run (`/dev/tty`, terminal signals, hangup) — and
serves that PTY over a per-session unix socket
(`$SAFESLOP_STATE_DIR/sessions/<id>.sock`, surfaced as the session's `socket`
field; when that path would exceed the `sun_path` limit it is transparently
relocated to a short private runtime dir), and returns immediately. `session attach --session-id <id>` rejoins the
running agent over that socket — bridging the local terminal, forwarding
window-size changes, and exiting with the agent's code — with at most one client
attached at a time. Attaching when no supervisor is serving the socket reports
`SESSION_NOT_RUNNING` rather than the more specific `SESSION_STOPPED`. `session
stop` first verifies that the recorded supervisor PID still matches the stored
process identity, then signals the supervisor's whole process group (graceful
`SIGTERM`, then `SIGKILL`) so the boundary tree is torn down and the socket
removed; a supervisor that dies uncleanly has its stale socket and host stage dir
swept on the next `session status`/`list` reconcile.

Detaching is a deliberate trade-off: a detached agent holds its staged secrets
(`secrets.env`, deploy keys, kubeconfig) for its whole — possibly long — life,
where a coupled run bounds them to the buffer's lifetime. `stop
--revoke-credentials` still revokes before the kill, and process-identity
liveness reconcile plus the stale-resource/stage-dir sweep bound the leak if the
supervisor dies uncleanly.

An exited session stays listed as `stopped` (with its exit code and last error)
rather than vanishing, so its outcome is inspectable. `session rm --session-id
<id>` removes one such record and `session prune` removes every stopped record in
one call, so the session list does not accumulate dead-session corpses. Both
refuse a still-running session — stop it first — and revoke any still-live staged
credentials before deleting a record, so a removal can never orphan secrets on
disk. `rm`/`prune` also wipe the reconstructed host stage dir. `prune` first runs
the liveness reconcile, so a crashed session (marked `running` but whose process
is gone or reused) is persisted as `stopped` and swept in the same pass. The
Emacs portal exposes these as `x` (remove one) and `X` (prune).

A session can carry an optional human display name. Set it at creation with
`session create --name <label>` (combinable with `--profile`), or later — in any
status — with `session rename --session-id <id> --name <label>`; an empty
`--name` clears it. The name is validated (control, format, and bidi characters
are rejected so it cannot break the JSONL line protocol or spoof a status) and,
when set, is surfaced in the session's `status`/`list` envelope and the Emacs
portal. Live Emacs buffers are also named and annotated with profile/project,
tier/net, and value-free credential scope, so a running or attached terminal is
legible without opening session details.

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
compose capability is present wins). Runtime CLIs are resolved once through safeslop's sanitized
host PATH and carried as absolute paths into later commands, so detection and execution cannot
drift. Same-file PATH aliases (for example, two OrbStack `docker` symlinks) count as one
helper; genuinely distinct runtime binaries fail closed. This identity check is point-in-time,
not a descriptor pin, so the existing validate-to-exec TOCTOU caveat remains. With none
present/working, the command fails closed and names all three.

**Deny-tier fail-closed:** a `network: deny` profile must place the agent on a network with no
default route. HTTP(S) egress is a domain allowlist: numeric IP-literal destinations are denied
before domain matching, and Squid reverse-DNS matching is disabled for the allowlist. Docker's
embedded DNS is pinned to the container loopback for external lookups, so deny-tier DNS cannot
forward to the host resolver while local service names such as `proxy` still work. Agent launches
are hard-set to uid/gid 1000 in Compose, matching the image user and tmpfs home owner. Only
docker/OrbStack are egress-verified for this today, so launching a `deny` profile on **podman or
lima is refused** unless you set `SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1` to accept the
(still-unverified) risk. Teardown — `down`, the startup sweep, session reap — is never gated, so
cleanup works on any detected runtime, verified or not.

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
				// GitHub App tokens over HTTPS: omit repos to infer the current origin, or
				// declare repos (one token per owner, partitioned by write). Inferred and
				// declared owner/repo components must match [A-Za-z0-9._-]+. Each owner needs
				// a link: safeslop creds link github --app-id N --installation-id N --key-ref op://…
				github: {
					repos: [{repo: "owner/web"}, {repo: "owner/api", write: true}]
				}

				// GitHub PAT fallback: one existing fine-grained HTTPS token for repos.
				// The token is staged in a wipe-on-exit file, not embedded in config.
				// github: {mode: "pat", pat: "env:GITHUB_FINE_GRAINED_PAT", repos: [{repo: "owner/web"}]}

				// Forgejo/Gitea deploy keys. url is required for multi-repo. The registration
				// token comes from accounts.cue: safeslop creds link forgejo --host H --owner O --token-ref op://…
				forgejo: {
					url:        "https://forgejo.example.com"
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
json` to inspect available entries; the bundle-list envelope includes
`data.defaults` (agent -> default bundle) so UIs can inherit defaults without
hardcoding policy internals. `profile show` reports the resolved package set and
dry-run image `recipeID` without building. `safeslop lock [profile] --output
json` writes the repo-root `safeslop.lock.json` for review/commit.

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
`recipeID`/`image` metadata for the portal Recipe/Image columns. Its create/list/status
JSON includes `credential_scopes` for profile-backed sessions: a value-free summary of
credential kind, non-secret target, and access/scope only. In Emacs, this
profile-backed path opens `*safeslop session progress*` so slow first-use image
build logs stream live and end with the subprocess exit status. The Sessions portal
shows the same value-free credential scope in its `Creds` column, and live buffers
use profile/project plus `[tier/net]` names with headers that repeat the value-free
scope. The ad-hoc `--agent` form remains available for one-off sessions. In
Emacs, choosing `host` for an ad-hoc session asks an explicit yes/no host
acknowledgement before adding `--trust-host` to `session create`; declining
aborts before the CLI is called. If the CLI still returns host `TRUST_REQUIRED`
without a policy path, Emacs offers one retry with `--trust-host` instead of
routing you to `safeslop trust`.

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
packages, egress, and image recipe (read-only, no file edit); `r` launches a
session from the selected profile after an isolation/network summary; `e` opens
the CUE file jumped to that profile's block; `c` opens `*safeslop profile
compose*`; `C` clones the row at point (only a new name is required); `D` guides
deletion (pick the target, confirm, then remove the block by hand); `g` refreshes.

The compose buffer defaults to a container/deny profile and uses the catalog
bundle defaults (`data.defaults`) to show inherited packages as selected and
locked. `L` means a row is included by its displayed source and cannot be partly
toggled. `RET` toggles unlocked bundle/package rows while retaining the logical row
and each showing window's scroll position; `g` refreshes catalog data with the same
context preservation. An `Automatic agent bundle` control is the deliberate
all-or-nothing opt-out for an agent default: disabling it emits
`--no-default-bundle`, retains explicit selections, and can leave the agent without
its runtime so launch may fail. It does not relax the container, network, or
workspace-only file boundary. `?` shows row help, `C-c C-c` asks the engine for
`profile create --dry-run --output json` and shows the returned risk
lines/resolved packages/image recipe before a final write, and `q` cancels without
writing. Project marker suggestions (`go.mod`, `package.json`, `pyproject.toml`,
`Cargo.toml`) are visible suggestions rather than automatic authority expansion.
File reach is workspace-only in this slice; arbitrary custom host mounts are
deferred until a mount capability model is specified. Creating still routes through
`profile create`, while CUE stays the stored source of truth. This repo also
dogfoods a checked-in `safeslop.cue` with `default`, `pi`, and `shell` profiles so
the Profiles surface has useful local rows immediately.

### Trust model

`validate`, `list`, and `run --dry-run` are inspection commands and do not require
trust. Every real launch — `safeslop run`, `safeslop session run`, and the Emacs
client (which launches only through sessions) — requires the exact current policy
bytes to be trusted:

```bash
safeslop trust
safeslop run work
safeslop untrust   # remove approval; future launches fail closed until re-trusted
```

For sessions the approval is checked when the session is created from a profile and
recorded on the session, then re-verified before the agent starts; if an agent or
editor changes the policy — or `safeslop untrust [safeslop.cue]` revokes the host
approval — in between, launch is blocked until you review and trust the new bytes.
An ad-hoc host session (`session create --agent
… --environment host`) has no `safeslop.cue` to approve and instead requires an
explicit `--trust-host` acknowledgement that the agent runs unconfined with your
host credentials.

Every real host-tier launch (`run`, coupled `session run`, and `session run
--detach` before the supervisor starts) also asks a per-launch comprehension gate:
the CLI prints the host blast radius and live scope, then requires matching yes/no
answers to engine-authored true/false statements. Consent is not persisted; rerun
the command to draw a fresh gate.

Staged credentials never live under the agent-writable workspace: they are written
to a host-only stage dir under your user cache dir (`~/Library/Caches/safeslop` on
macOS, `~/.cache/safeslop` on Linux) and bind-mounted read-only into the container.

### Isolation tiers

`environment` is required — there is no default (specs/0053 removed the macOS
Seatbelt `sandbox` tier: it could not run the network-bound, home-installed agents
safeslop launches, only confined accidents).

| environment | label | summary |
|---|---|---|
| `host` | none | No isolation boundary; the agent runs as you after a per-launch comprehension gate. |
| `container` | egress-allowlisted | An ambient docker/podman/lima container plus proxy topology for per-domain egress control; deny-tier HTTP(S) rejects IP literals, DNS forwarding is loopback-pinned, and the agent launch is hard-set to uid/gid 1000. |

Use `container` for routine agent sessions (network-bound agents belong here),
and `host` only when you accept no isolation and can pass the per-launch consent
check.

## Credentials

Credentials are staged under the run's runtime directory and wiped on exit.
Token revocation is best-effort; deletion of the staged token/key on exit is the
decay-first safety guarantee.

- GitHub uses `credentials.github`. In the default `app` mode safeslop mints an
  ephemeral, repo-scoped GitHub App installation token (contents + metadata) and
  stages it as a git-over-HTTPS credential — no deploy keys, no `gh` CLI. Each
  owner needs an account link (`safeslop creds link github`); repos are
  partitioned by `write` so a read-only repo never gets a write token. Owner/repo
  names, whether declared or inferred from `origin`, must match `[A-Za-z0-9._-]+`
  before any git config is staged. The P1 token lifetime is capped at ~1h (no
  renewal yet; renewal lands in P2). PAT mode (`mode: "pat"`, `pat: <ref>`)
  stages one existing fine-grained token instead.
- Forgejo/Gitea uses `credentials.forgejo` (deploy keys, one per repo, with
  per-repo SSH host aliases + git URL rewrites). The account token that registers
  each key comes from `~/.config/safeslop/accounts.cue`
  (`safeslop creds link forgejo`), never from `safeslop.cue`. Origin-inferred or
  declared owner/repo names must match `[A-Za-z0-9._-]+`; malformed remotes fail
  closed before `.gitconfig`/`.ssh/config` are rendered. Forgejo account tokens
  are account-wide — prefer a dedicated bot account.
- Account links live in `~/.config/safeslop/accounts.cue` (0600, host-only): they
  hold non-secret ids + secret *refs* only, never a token or key value, and are
  never serialized into a container or stage dir. Manage them with `safeslop creds
  link|unlink|status`; UI clients use `safeslop creds status --output json`, whose
  `data.links` rows expose only forge, host, owner, non-secret ids, value-free
  probe class, SSH port, and TTL model.
- When github creds are staged on a `network: "deny"` profile, the egress
  allowlist gains `github.com`, `codeload.github.com`, and
  `objects.githubusercontent.com` (clone + LFS). `api.github.com` is not added in
  P1 (API-token staging is P2).

### Inspecting credential posture (Emacs `C-c s K`, `safeslop creds`)

The Emacs Credentials surface (`C-c s K`, "Keys") makes a workspace's credential
posture legible *before* launch. For every profile it lists each declared secret
and credential with its **source ref** (`op://…`/`env:NAME` — a reference, never a
value), whether it is **ephemeral** (a deploy key minted per session and wiped on
exit) or **ref-backed**, and — for the ref-backed ones — a value-free **readiness
status**: `resolvable`, `missing`, `op-signed-out`, `op-unavailable`, `ephemeral`,
or `ambient` (host SSO/ADC). The header also shows linked GitHub App / Forgejo
account links from `creds status --output json` without token/key refs or values.
Keys: `RET`/`i` inspect, `a` link a GitHub App or Forgejo account using refs/ids
only, `u` unlink an account, `p` open the repo picker, `e` opens the
`safeslop.cue` credentials block, and `g` re-probes.

The surface is backed by `safeslop creds list [safeslop.cue] --output json`,
`safeslop creds show <profile> --output json`, `safeslop creds status --output
json`, and `safeslop profile credentials set|clear ... --output json` for
structured profile credential mutation. The repo picker can choose origin
inference or manually entered `owner/repo` rows with read/write access and writes
`credentials.github` or `credentials.forgejo` while preserving other credential
providers; setting one forge clears the other. live repo discovery is
deliberately deferred: GitHub listing would require an installation token and
Forgejo listing would use an account-wide token outside this slice's
session-owned lifecycle. The readiness probe resolves each ref only to keep the
pass/fail result and **discards the value**, so no secret is ever read into the UI
or the envelope. There is no in-UI mint/revoke — ephemeral keys live and die with
a session (`run`/`session`), so the surface is account linking + repo scope
selection, not a secret vault.

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
make test-emacs-ui-matrix
```

`make test-emacs-ui-matrix` is the local Emacs compatibility gate: raw Emacs and
Doom-shim slots always run; real Evil/Doom+Evil slots run when a local Evil build
is auto-detected or `SAFESLOP_EVIL_LOAD_PATH` supplies colon-separated load dirs.
A personal config probe is opt-in via `SAFESLOP_UI_PERSONAL_CMD`, and
`SAFESLOP_UI_REQUIRE_PERSONAL=1` makes that slot mandatory for your local run.
`make check` stays hermetic and does not require private Doom/Evil state.

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
