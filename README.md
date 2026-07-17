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
safeslop profile delete <name> [safeslop.cue] --output json  delete one validated project profile
safeslop profile show <name> --output json         profile + recipe + three-section safety evaluation
safeslop profile credentials set <profile> [safeslop.cue] --provider <github|forgejo> [--use-origin|--repo owner/name ...] [--write-repo owner/name ...] --output json
safeslop profile credentials clear <profile> [safeslop.cue] --output json
safeslop creds link|unlink|status                 manage value-free forge account links
safeslop creds gc --host H --repo owner/repo ... [--dry-run|--yes] [--output json]  narrow Forgejo deploy-key cleanup
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
safeslop session egress dismiss --session-id <id> --host example.com --port 443 --output json
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

All four builtins are contained-hybrid defaults: `environment: "container"`,
`network: "deny"`, and the buildable `personal` bundle (plus the agent's own
default bundle where applicable). Personal binary artifacts are pinned by version,
per-architecture URL, and SHA256; `python3` is pinned to the immutable signed
Debian snapshot. Image handlers verify the selected artifact before installation,
so profile inspection fails closed if a package lacks a reviewed build path.

Builtin host projection is read-only and allowlist-only. Pi/Claude receive pi
instructions and skills; Fish receives only demand-loaded functions/completions;
Zsh receives Zsh and Starship configuration. The builtin Fish profile deliberately
does not copy or execute host `config.fish` or `conf.d` startup scripts: arbitrary
host startup assumptions are not portable into the contained OS/tool set. Normal
container-owned Fish startup remains active. Create a fresh Fish session after
this builtin contract update; exact-byte hash fidelity rejects old created records
instead of silently changing them. On macOS and Linux, sources are walked from a
pinned home descriptor and copied into a private per-session `0700` snapshot;
Compose mounts only those completed snapshots under opaque paths, never the live
source. This accepts ordinary relative in-home layouts such as
`~/.config -> dotfiles/files/.config` and exact-spelling absolute source links
whose raw target is a proper descendant of the same approved root. The absolute
target is converted to components and walked only from the pinned root descriptor;
it is never canonicalized or reopened as a pathname. Fish's retained optional
`*.fish` globs select only physical regular files: terminal symlinks, directories,
and special-file matches are never followed or opened and produce one aggregate
`skipped-nonregular` manifest status while safe siblings continue. This is
selection, not new read authority. Links that leave the approved root, use an
alternate case/Unicode/alias spelling or ambiguous dot/empty components, or enter
an excluded credential/cache root still fail closed, as do internal recursive-tree
links, required-glob non-regular matches, loops, mount crossings, source races,
and unsupported descriptor/mount-identity platforms; there is no pathname
fallback. This resolver-only refinement does not change builtin CUE bytes or
policy hashes. The workspace remains the only read-write host
mount. All of `$HOME`, raw Git configuration, `.ssh`, cloud/Kubernetes/Docker
config, npm/cargo credentials, browser/keychain data, and safeslop state is never
projected. Network authority still starts denied and can be expanded only through
the explicit session-scoped grant commands below.

`session create --profile`, `session status`, and `session list` include
value-free credential scope in the JSON contract as `credential_scopes` for
profile-backed sessions. Each row names only the credential kind, non-secret
target, and access/scope; it does not include token values, source references,
or staged file paths. Ad-hoc sessions and profiles without credentials omit it
or return an empty array. Session record store failures are likewise typed and
value-free: corrupt records do not disappear or yield partial lists, stale
mutations are rejected with retry semantics instead of overwriting newer state,
and commit uncertainty is reported as uncertainty rather than success.

### Progressive session egress

There is no `network: "progressive"` policy value. Progressive egress is an
operator-invoked review surface available only to `environment: "container"` +
`network: "deny"` sessions. `observations` returns value-free denied FQDN:port
rows; it never grants traffic. `grant`/`revoke` control a session-scoped overlay
only, so a grant is removed with the session and never mutates `profile.egress`
or `safeslop.cue`. `dismiss` records a value-free **Keep denied** acknowledgement
for the observed destination: it grants nothing, suppresses only observations at
or before that acknowledgement, and a later denial is visible again. On a running
session, a grant that adds a new session-grant row or a revoke that removes one
force-replaces the proxy and succeeds only after the running proxy
acknowledges the exact grant revision and overlay hash by label plus the mounted
`session-grants.conf` sha256, so revocation drops old tunnels instead of relying
on in-place reload. Widening persists its upper bound before activation; narrowing
activates and acknowledges the smaller set before committing it. Created sessions
persist the reviewed set for launch without a live replacement; dismiss is
record-only. If runtime authority or a commit outcome cannot be proven, safeslop
attempts full boundary teardown and records the fixed value-free code
`network_authority_uncertain` rather than guessing. If teardown itself cannot be
proven, further egress operations stay blocked until explicit stop/reap. Before
agent start, safeslop requires both a valid Squid configuration and a live proxy
listener.
Failure tears down the partial stack and records the value-free structured code
`network_proxy_unavailable`; raw runtime output is never persisted. Operators
can run the opt-in real Docker gate with `make test-progressive-egress-smoke`.

For a deliberately reviewed future-session rule, use the separate typed policy
field — never legacy `egress`:

```cue
persistentEgress: [{fqdn: "api.example.com", port: 443}]
```

It is accepted only on container-deny profiles and each entry is one normalized,
exact FQDN on port 80 or 443. Review the value-free logical delta and source hash
first, then make the explicit hash-checked write:

```bash
safeslop profile egress preview review safeslop.cue --host api.example.com --port 443 --expected-policy-hash HASH --output json
safeslop profile egress add     review safeslop.cue --host api.example.com --port 443 --expected-policy-hash HASH --output json
safeslop profile egress remove  review safeslop.cue --host api.example.com --port 443 --expected-policy-hash HASH --output json
```

Preview never writes. Add/remove fail closed on a stale hash, validate and
atomically render the complete policy, and change policy bytes; review and run
`safeslop trust` before a **new** session can use the rule. They cannot alter a
running session. Persistent rules identify `profile-persistent / future sessions`;
dynamic grants identify `session-grant / this session`.

IP literals, private or link-local addresses, localhost/metadata, credential
broker or mint endpoints, wildcards, suffixes, raw URLs, and other ports are
non-grantable. Host-tier and `network: "allow"` sessions are not enforceably
isolated and reject these commands. Observations are non-modal: agent traffic
never opens a prompt, focuses a review buffer, edits CUE, or changes network
authority. In Emacs, compose labels container deny as **Deny (progressive
review)** (not an authorization); session detail shows a passive pending count
and `v` opens review. There, `a` is Allow now, `k` is Keep denied, and `A` first
shows the hash/CUE delta before a separate explicit durable add.

`session status` and `session list` reconcile liveness: a session still marked
`running` whose recorded process is gone — or whose PID now names a different
process identity after reuse — is reported and persisted as `stopped`, so status
never lies about a session that is no longer executing. Container sessions are
additionally reaped by their `safeslop.session=<id>` labels during
stop/reconcile, and the reconstructed host stage dir is wiped, so teardown does
not depend on a still-readable session record.

`session stop` tears the boundary down rather than just killing the wrapper:
credentials are revoked before process termination when `--revoke-credentials` is
requested, stop reconciles the recorded PID/process identity before signalling
(so a stale detached PGID is not targeted), and the labelled container boundary
is reaped; staged secrets are wiped by run teardown or the stop/reconcile cleanup
path. Closing a coupled terminal sends `SIGHUP` to its coupled CLI and triggers its
deferred teardown. Closing an attach buffer only disconnects from a detached
supervisor: the session keeps running until explicit stop. Explicit detached stop
first sends `SIGTERM`, then (for a token-verified process identity) `SIGKILL`
after the bounded grace period if needed, followed by label reap, socket removal,
and stage wipe. A tokenless legacy record gets the full `SIGTERM` grace but never
an unverified `SIGKILL` escalation. Interactive `Ctrl-C`
(`SIGINT`) is left for the agent and does not tear the session down.

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
`SIGTERM`, then token-revalidated `SIGKILL`) so the boundary tree is torn down and
the socket removed; a supervisor that dies uncleanly has its stale socket and host stage dir
swept on the next `session status`/`list` reconcile.

Detaching is a deliberate trade-off: a detached agent holds its staged secrets
(`secrets.env`, deploy keys, kubeconfig) for its whole — possibly long — life,
where a coupled run bounds them to the buffer's lifetime. `stop
--revoke-credentials` still revokes before the kill, and process-identity
liveness reconcile plus the stale-resource/stage-dir sweep bound the leak if the
supervisor dies uncleanly.

An exited session stays listed as `stopped` rather than vanishing. Projection
preparation failures are persisted atomically as versioned, value-free
`last_failure` data (stable code, engine-owned summary/action, builtin label, and
`~`-spelled source), with a bounded `last_error` compatibility summary. Session
status/list return both. Emacs shows the structured reason directly in failed
rows, opens a durable summary/action detail view after a fast terminal exit,
refreshes the portal, and deduplicates the notification; it never displays raw
resolver paths, OS errors, command output, or secret values. `session rm
--session-id <id>` removes one stopped record and `session prune` removes every
stopped record in one call, so the session list does not accumulate dead-session
corpses. Both
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
legible without opening session details. Persistent safety chrome in each live
buffer's mode-line repeats the literal environment/network posture and a
value-free credential count, with color as reinforcement only; its tooltip
expands the honest tier/network notes and safe scope names. The portal Status
posture tooltip carries the same facts alongside lifecycle state.

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
supply-chain review boundary. Buildable npm catalog entries (`claude-code`, `pi`,
`pnpm`) additionally have one reviewed package-lock project each, with transitive
SRI required for every registry package, a closed package→binary→script-policy
registry, and recipe hashes over the selected lock bytes. Image build contexts
materialize only the selected lock projects plus reviewed Dockerfiles and never
include credential staging. The proxy image is not a moving tag at runtime: the
reviewed `ubuntu/squid` OCI index digest and linux/amd64+linux/arm64 manifest
digests are locked in `proxy-image.lock.json`/`proxy-image.index.json`; Compose
uses the digest reference and runs Squid with `cap_drop: ALL`, no-new-privileges,
read-only root, PID bounds, live-required tmpfs paths, and the reviewed non-root
service user. The package-version selection and bump policy that governs which
pin lands is canonized in `specs/research/2026-06-30-version-policy-flo.md`.

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
uses `net/http`. Networked image builds are integrity-pinned — URL/SHA256 for
binary artifacts, Debian snapshot pins for apt, package-lock/SRI for npm, and
OCI-index/manifests for Squid — but they are not hermetic or bit-reproducible
because the builder still contacts the selected registries/upstreams. Add
`--output json` for the enveloped machine contract.

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
with built-in `claude`, `fish`, `pi`, and `zsh` defaults alongside project rows. The Source column labels the signed-binary defaults; a same-named project profile wins. Builtins are launchable/inspectable without `safeslop trust`, but immutable; create a project profile to customize one. The surface has ergonomic CRUD and launch keys: `RET`/`i` inspect a profile's resolved
packages, egress, image recipe, and three-section evaluation (read-only, no file
edit); `r` fetches and displays the engine evaluation before offering to launch a
session from the selected profile; `e` opens
the CUE file jumped to that profile's block; `c` opens `*safeslop profile
compose*`; `C` clones the row at point (only a new name is required); `D` asks
for confirmation, then deletes the selected project profile through the validated
CLI and refreshes in place; `g` refreshes.

The compose buffer defaults to a container/deny profile and uses the catalog
bundle defaults (`data.defaults`) to show inherited packages as selected and
locked. Its Name, Agent, Environment, Network, and Workspace rows are `RET`-editable;
agent changes recompute default-package inheritance before preview. `L` means a row
is included by its displayed source and cannot be partly toggled. `RET` toggles
unlocked bundle/package rows while retaining the logical row and each showing
window's scroll position; `g` refreshes catalog data with the same context
preservation. An `Automatic agent bundle` control is the deliberate
all-or-nothing opt-out for an agent default: disabling it emits
`--no-default-bundle`, retains explicit selections, and can leave the agent without
its runtime so launch may fail. It does not relax the container, network, or
workspace-only file boundary. `?` shows row help, `C-c C-c` asks the engine for
`profile create --dry-run --output json` and shows the returned
Authority/Trust/Readiness findings, resolved packages, and image recipe before a
final write, and `q` cancels without
writing. Project marker suggestions (`go.mod`, `package.json`, `pyproject.toml`,
`Cargo.toml`) are visible suggestions rather than automatic authority expansion.
File reach is workspace-only in this slice; arbitrary custom host mounts are
deferred until a mount capability model is specified. Compose authors new profiles
only; it does not partially overwrite existing profiles with fields outside its UI.
Creating still routes through `profile create`, while CUE stays the stored source of truth. This repo also
dogfoods a checked-in `safeslop.cue` with `default`, `pi`, and `shell` profiles so
the Profiles surface has useful local rows immediately.

### Profile safety evaluation

`profile show <name> --output json` and `profile create --dry-run --output json`
return an additive v1 `evaluation` beside the compatibility `risk` and
`risk_axes` fields. The evaluation keeps three questions structurally separate
and always presents them in this order:

1. **Authority — what it can reach.** Static consequences derived only from the
   decoded profile: network, writable files, live host-config projection, direct
   secrets, and credential authority.
2. **Trust — is this exact policy approved?** Saved project profiles report
   exact-byte approval, builtins report embedded-builtin provenance, and an
   unsaved compose preview reports trust as not applicable.
3. **Readiness — can this host launch it now?** Local workspace, sanitized helper,
   container-runtime, toolchain, and required account-link metadata checks.

There is no aggregate score, grade, combined safety color, or overall red/green
verdict. A trust failure or blocked Readiness can stop launch, but it never
suppresses or lowers Authority. For the same decoded profile, Authority is stable
across show, compose preview, and launch review; Trust and Readiness are context
snapshots.

Credential rows expose only value-free targets such as `owner/repo`, a registry
host, cloud role/profile label, API-scope label, or cluster label, together with
access, lifetime, and basis. They never include values, secret/private-key/account
refs, staged paths, or private host paths. Unknown scope is explicit and is not
assumed read-only.

Readiness is a point-in-time local snapshot, timestamped when collected. It does
not contact forges, clouds, clusters, registries, or credential APIs, does not
resolve secret values, and does not promise remote authentication or authorization.
It is not an authorization token: launch-time CLI trust, host-consent, runtime,
network, and credential gates remain authoritative.

In Emacs Profiles, `RET`/`i` Inspect renders Authority → Trust → Readiness from a
fresh `profile show`; compose `C-c C-c` renders the same sections from the exact
unsaved dry-run before save; and `r` fetches and displays the named profile's
engine evaluation before the final launch confirmation. Outcomes are printed as
words, with color only as reinforcement, and remediation buttons dispatch only
typed engine guidance—never automatic CUE edits or trust. If an older engine
omits `evaluation`, Emacs shows **Legacy safety summary — trust and readiness
unavailable** using `risk`/`risk_axes`. A present but malformed or unsupported
evaluation instead renders loud `UNKNOWN — update required` and does not fall
back to a reassuring legacy level.

Custom host-mount authoring, live remote permission inference, and arbitrary
remediation execution remain deferred.

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

### Workspace and runtime ownership

For saved policies, a non-empty relative `workspace` is policy-relative: it is
resolved from the directory containing the trusted `safeslop.cue`, not from the
operator's current shell. An empty ad-hoc workspace uses the invocation directory.
Before launch, safeslop requires exactly one canonical workspace: an absolute,
existing directory after symlink resolution. The private runtime stage is separate
from that workspace, and workspace↔stage containment in either direction fails
closed so the read-only stage cannot be reached through `/workspace`.

Hostile but valid path spelling is supported. Spaces, colons, quotes, Unicode,
literal `$`, and Compose-looking text are carried through typed long-form binds
with `create_host_path:false` and escaped YAML/JSON scalars, so they do not create
extra structure or interpolation. Invalid UTF-8, NUL/control/format separators,
missing paths, non-directories, unsupported bind source types, and overlapping
workspace/stage paths are rejected before launch. Public JSON stays value-free:
failures identify stable codes and engine-owned summaries/actions, not private
resolver paths or raw runtime output.

Direct `safeslop run <profile>` invocations get a fresh `run-<32 lowercase hex>`
identity from 128 bits of `crypto/rand` after approval and before staging. That
single owner labels the stage, Compose project, marker, cleanup, and dead-run reap,
so concurrent direct runs of the same profile cannot share or remove each other's
boundary. New session records use the random session id as layout-2 runtime
identity. Legacy records without runtime-layout fields reconstruct their stage
from `session-<id>` plus the canonical-workspace hash, normalize the historical
`system` backend to Docker, and remain untouched on read. Before their first live
egress mutation, running legacy sessions install and ACK their unchanged durable
generation; bootstrap uncertainty tears the boundary down. These paths
keep deployed sessions reconstructably stoppable without an automatic rewrite.

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

- Pi OAuth is an explicit **project-profile-only** access snapshot for exactly
  `openai-codex/gpt-5.6-luna`; no builtin enables it. Declare the literal block
  below on a Pi/container/deny profile, review the whole policy, and run
  `safeslop trust` before creating the session:

  ```cue
  profiles: luna: {
    agent:       "pi"
    environment: "container"
    network:     "deny"
    credentials: pi: {
      provider: "openai-codex"
      model:    "gpt-5.6-luna"
    }
  }
  ```

  At launch safeslop safely reads the default host Pi store
  (`~/.pi/agent/auth.json`). Its fixed source may traverse relative same-HOME
  links or exact-spelling absolute links whose raw target is a proper descendant
  of that same HOME, including links at `.pi`, `agent`, or the final leaf. HOME
  and every reached directory must be current-user-owned with `mode & 0022 == 0`,
  so ordinary `0755` ancestry is accepted; the ultimate file remains an exact
  regular `0600`, current-user-owned, single-link, bounded leaf on the same mount.
  The lexical sibling lock is checked before and after descriptor reading, then a
  fresh full proof must match. Outside/prefix/ambiguous targets, loops, dangling
  links, writable or wrong-owner ancestry, mount crossings, and source races fail
  closed without exposing the resolved topology. Safeslop requires more than
  15 minutes of remaining access lifetime, and stages only a synthetic `{type:"api_key", key:<access>}` entry
  into the container's tmpfs home. It never copies the refresh token, account
  metadata, another provider, or the host file. There is no renewal, listener,
  broker, or startup-code injection. This bearer remains **provider-default
  replay authority**: selecting Luna and granting one hostname constrain the
  workflow but do not cryptographically downscope the token. If Pi's lock remains
  busy, let host Pi finish or run `pi --list-models gpt-5.6-luna`, then retry a
  new session. `chatgpt.com` is intentionally not a static allowlist entry; after
  reviewing a denied observation, grant only the current session and revoke it
  when finished:

  ```bash
  safeslop session egress grant --session-id ID --host chatgpt.com --port 443 --output json
  safeslop session egress revoke --session-id ID --grant-id G --output json
  ```

  Stop/remove/reconcile wipes host staging and the container tmpfs copy. That is
  local deletion, **not issuer revocation**; the copied access bearer remains
  valid until its upstream expiry.
- GitHub uses `credentials.github`. In the default `app` mode safeslop mints an
  ephemeral, repo-scoped GitHub App installation token and stages it as a
  git-over-HTTPS credential — no deploy keys, no `gh` CLI. Each owner needs an
  account link (`safeslop creds link github`); repos are partitioned by `write` so
  a read-only repo never gets a write token. Owner/repo names, whether declared or
  inferred from `origin`, must match `[A-Za-z0-9._-]+` before any git config is
  staged. The **host-owned** lease renews App-token batches atomically; containers
  receive files only and cannot mint, renew, or revoke. `ttl` defaults to `"1h"`;
  a positive Go duration caps future staging/renewal from initial staging, while
  explicit `""` leaves the lease until normal teardown. A horizon never
  retroactively invalidates an already-issued token. PAT mode (`mode: "pat"`,
  `pat: <ref>`) stages one existing fine-grained token instead and is not renewed.
- Set `credentials.github.api` only with App mode and nonempty unique
  `permissions: ["permission:read"|"permission:write", ...]`. Safeslop stages
  API tokens in canonical 0600 files: one partition exposes
  `SAFESLOP_GITHUB_TOKEN_FILE`; multiple partitions expose
  `SAFESLOP_GITHUB_TOKEN_DIR` plus `SAFESLOP_GITHUB_TOKEN_MANIFEST`. It never
  injects `GITHUB_TOKEN`: any copied compatibility value would be stale after a
  host renewal. GitHub API egress (`api.github.com`) is added only for this
  explicit API opt-in; normal Git/LFS staging adds only `github.com`,
  `codeload.github.com`, and `objects.githubusercontent.com`.
- Forgejo/Gitea uses `credentials.forgejo` (deploy keys, one per repo, with
  per-repo SSH host aliases + git URL rewrites). The account token that registers
  each key comes from `~/.config/safeslop/accounts.cue`
  (`safeslop creds link forgejo`), never from `safeslop.cue`. Origin-inferred or
  declared owner/repo names must match `[A-Za-z0-9._-]+`; malformed remotes fail
  closed before `.gitconfig`/`.ssh/config` are rendered. Forgejo account tokens
  are account-wide — prefer a dedicated bot account. `ttl` follows the same
  horizon rules; at a bounded horizon safeslop removes an opted-in Forgejo API
  file and attempts best-effort deploy-key cleanup.
- Set `credentials.forgejo.api.enabled: true` only with
  `ackAccountWide: true`, an HTTPS `url`, and default port 443. The staged API
  token is file-only (`SAFESLOP_FORGEJO_TOKEN_FILE`); its provider scope is
  operator-provisioned, unverified, and may be account-wide. Its exact hostname is
  added to deny-tier egress only for this opt-in.
- Account links live in `~/.config/safeslop/accounts.cue` (0600, host-only): they
  hold non-secret ids + secret *refs* only, never a token or key value, and are
  never serialized into a container or stage dir. Manage them with `safeslop creds
  link|unlink|status`; UI clients use `safeslop creds status --output json`, whose
  `data.links` rows expose only forge, host, owner, non-secret ids, value-free
  probe class, SSH port, and TTL model.
- `safeslop creds gc --host H --repo owner/repo ...` is a narrow Forgejo deploy-key
  cleanup. It defaults to discovery-only; deletion requires `--yes` (which cannot
  be combined with `--dry-run`). It discovers every requested repository before
  deleting, matches only exact `safeslop-<owner>-<repo>` titles, rechecks each
  candidate, and treats HTTP 404 as already absent. It never discovers or deletes
  outside the explicitly named repositories.

### Inspecting credential posture (Emacs `C-c s K`, `safeslop creds`)

The Emacs Credentials surface (`C-c s K`, "Keys") makes a workspace's credential
posture legible *before* launch. For every profile it lists each declared secret
and credential with its **source ref** (`op://…`/`env:NAME` — a reference, never a
value), whether it is **ephemeral** (a deploy key minted per session and wiped on
exit) or **ref-backed**, and — for the ref-backed ones — a value-free **readiness
status**: `resolvable`, `missing`, `op-signed-out`, `op-unavailable`, `ephemeral`,
or `ambient` (host SSO/ADC or a launch-validated Pi OAuth snapshot). A Pi row
shows only `openai-codex/gpt-5.6-luna`, short-lived access-snapshot lifetime, and
provider-default authority — never its host path, exact expiry, account metadata,
or bearer. The header also shows linked GitHub App / Forgejo account links from
`creds status --output json` without token/key refs or values.
Universal raw/Evil keys are: `RET`/`i` inspect, `A` link a GitHub App or Forgejo
account using refs/ids only, `U` unlink an account, `R` configure profile
repository scopes, `X` clear only a profile's GitHub/Forgejo scopes, and `e` open
the `safeslop.cue` credentials block. Refresh is `g` in raw Emacs and `gr` in
Evil normal state. Lowercase `a`/`u`/`p` remain raw-Emacs compatibility aliases.

For first-time setup, create or clone a **project** profile first (builtins are
immutable), then press `A` to link the host account and `R` to assign origin or
explicit read/write repositories. Account identity is reviewed value-free before
linking. `R` loads all project profiles even when the credential table is empty,
prefills existing provider/mode/read/write scopes, and confirms a before/after
full replacement; changing provider clears only the other forge declaration.
`X` removes profile forge scopes while retaining reusable account links and
unrelated credential providers. Failed account/scope writes retain value-free
drafts: return with `K`, then press `A` or `R` to correct/retry. Successful scope
changes alter policy bytes, so review and re-trust before launching a new session.
Unlink warns that unchanged profile scopes will fail staging until relinked or
cleared with `X`.

The surface is backed by `safeslop creds list [safeslop.cue] --output json`,
`safeslop creds show <profile> --output json`, `safeslop creds status --output
json`, and `safeslop profile credentials set|clear ... --output json` for
structured profile credential mutation. The repo picker can choose origin
inference or manually entered `owner/repo` rows with read/write access and writes
`credentials.github` or `credentials.forgejo` while preserving other credential
providers; setting one forge clears the other. Live repo discovery is
deliberately deferred: GitHub listing would require an installation token and
Forgejo listing would use an account-wide token outside this slice's
session-owned lifecycle. The readiness probe resolves each ref only to keep the
pass/fail result and **discards the value**, so no secret is ever read into the UI
or the envelope. There is no in-UI mint/revoke — ephemeral keys live and die with
a session (`run`/`session`), so the surface is account linking + repo scope
selection, not a secret vault. Pi OAuth is likewise inspection-only in Emacs MVP:
add its literal CUE block manually, review/re-trust the changed exact bytes, and
create a new session.

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

- container asset drift check (`make check-assets`)
- npm package-lock/SRI and closed-script-policy check (`make check-npm-locks`)
- digest-pinned Squid lock check (`make check-proxy-image-lock`)
- active docs/workflow drift check for removed VM and obsolete image surfaces (`make check-active-surface-drift`)
- catalog render drift check (`make check-catalog-sync`)
- specs/0049 pivot denylist, host-helper exec denylist, and hostpath import gates
- `go vet ./...`
- `gofmt` verification for `cmd` and `internal`
- `go test ./...`
- strict Emacs package smoke/contract/session tests via `make test-emacs`

Useful targeted tests:

```bash
go test ./internal/engine/creds/ -v
go test ./internal/engine/policy/ -run 'Pinned|Latest' -v
go test ./internal/cli/ -v
go test ./internal/engine/session ./internal/jsoncontract -v
make test-emacs EMACS=/absolute/path/to/emacs
make test-emacs-ui-matrix
make test-container-images          # opt-in Docker image build gate
make test-progressive-egress-smoke  # opt-in real Docker grant/revoke smoke
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
