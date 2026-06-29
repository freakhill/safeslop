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
safeslop profile list|presets --output json       profiles + preset library as the JSON contract
safeslop profile create --name N --agent A --environment E [--bundle B] [--package P] --output json
safeslop profile show <name> --output json         profile + resolved packages + image recipe
safeslop lock [profile] --output json              write repo-root safeslop.lock.json
safeslop trust [safeslop.cue]       approve this policy's exact bytes
safeslop run <profile> [--dry-run]  launch a trusted profile
safeslop session create --profile <name> --output json
safeslop session create --agent <claude|pi|fish|zsh> --environment <host|container> --workspace <dir> --output json
safeslop session run --session-id <id> [--detach]
safeslop session attach --session-id <id>
safeslop session status --session-id <id> --output <json|jsonl>
safeslop session stop --session-id <id> --revoke-credentials --output json
safeslop doctor                     report available tools and isolation tiers
safeslop down                       tear down container sessions
safeslop install                    inventory and install pinned toolchains/runtimes
safeslop uninstall                  receipt-driven removal of installed tools
safeslop launch <profile>           open a terminal running a profile
```

Add `--json` to commands for machine-readable output where supported. Emacs-facing
commands emit the shared versioned JSON contract envelope (`schema_version`,
`ok`, `data`, `warnings`, `errors`). Session status also supports `--output
jsonl` for a line-delimited monitor stream.

`session status` and `session list` reconcile liveness: a session still marked
`running` whose run process is gone (crash, kill, host sleep) is reported — and
persisted — as `stopped`, so status never lies about a session that is no longer
executing.

`session stop` (and a terminal/buffer close) tears the boundary down rather than
just killing the wrapper: the run receives `SIGTERM`/`SIGHUP`, which tears down
the agent and runs the deferred cleanup — container removed, staged secrets
wiped, ephemeral credentials revoked. Interactive
`Ctrl-C` (`SIGINT`) is left for the agent and does not tear the session down.

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

`safeslop session create --profile <name> --output json` creates an Emacs-visible
session from an existing `safeslop.cue` profile: it uses the profile's agent,
environment, network, and workspace, resolves its catalog package set, and includes
`recipeID`/`image` metadata for the portal Recipe/Image columns. In Emacs, this
profile-backed path opens `*safeslop session progress*` so slow first-use image
build logs stream live and end with the subprocess exit status. The ad-hoc
`--agent` form remains available for one-off sessions.

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
| `container` | egress-allowlisted | Docker/Lima container plus proxy topology for per-domain egress control. |

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
