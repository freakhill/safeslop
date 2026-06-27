# safeslop

> **Practice safe safeslop** — launch coding agents behind stronger isolation defaults.

`safeslop` is a single signed Go binary that launches coding agents under a
per-repo `safeslop.cue` policy. One file declares the agent, isolation tier,
network posture, toolchain, secrets, and ephemeral credentials; the binary
validates the policy, provisions what is needed, launches the session, and tears
staged state down on exit.

## What it provides

- **Honest isolation tiers**: `host`, `sandbox`, `container`, and `vm`, with the
  active tier printed by `run`/`doctor`.
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
		review: {agent: "claude", environment: "sandbox", network: "deny"}
		pair:   {agent: "pi", environment: "sandbox", network: "deny"}
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
safeslop trust [safeslop.cue]       approve this policy's exact bytes
safeslop run <profile> [--dry-run]  launch a trusted profile
safeslop session create --agent <pi|claude|claude-code> --workspace <dir> --output json
safeslop session run --session-id <id> [--detach]
safeslop session attach --session-id <id>
safeslop session status --session-id <id> --output <json|jsonl>
safeslop session stop --session-id <id> --revoke-credentials --output json
safeslop doctor                     report available tools and isolation tiers
safeslop down                       tear down container/VM sessions
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
the agent and runs the deferred cleanup — disposable VM destroyed, container
removed, staged secrets wiped, ephemeral credentials revoked. Interactive
`Ctrl-C` (`SIGINT`) is left for the agent and does not tear the session down.

`session run` is an interactive attach and needs a controlling terminal — Emacs
supplies one via `make-term`. Invoked without a usable TTY (a pipe, cron, a
headless shell), it emits the `PTY_UNAVAILABLE` contract error
(`details.fallback = "status-jsonl"`) and exits non-zero **without** marking the
session running, so nothing is left as a phantom. The Emacs client switches to a
read-only `--output jsonl` status monitor on that code.

`session run --detach` gives a session a life independent of the Emacs buffer that
started it: it launches a per-session **supervisor** that owns the agent and its
PTY — which, for a host or sandbox session, is made the agent's controlling
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
			agent:       "claude"          // "claude" | "claude-code" | "pi"
			environment: "container"       // "sandbox" | "container" | "vm" | "host"
			network:     "deny"            // "deny" | "allow"
			workspace:   "."
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

| environment | label | summary |
|---|---|---|
| `host` | none | No isolation boundary; the agent runs as you. |
| `sandbox` | mistake-guard | macOS Seatbelt file/exec boundary for everyday agent work. |
| `container` | egress-allowlisted | Docker/Lima container plus proxy topology for per-domain egress control. |
| `vm` | adversary-grade | Disposable Tart VM; strongest boundary and heaviest workflow. |

Use `sandbox` for routine local agent sessions, `container` when URL-level egress
control matters, and `vm` for untrusted code.

The `vm` tier reaches the disposable VM over SSH with the key its base image
authorizes. Provide that key one of two ways: set `SAFESLOP_VM_SSH_KEY` to a private
key file path, or set `SAFESLOP_VM_SSH_KEY_OP` to a 1Password reference and safeslop
reads the key just-in-time (into a transient, wiped-on-exit file) so no key lives on
disk. Request the OpenSSH format in the reference — op's default is PKCS#8, which ssh
cannot use:

```sh
export SAFESLOP_VM_SSH_KEY_OP='op://homelab-infra/safeslop-base-vm/private key?ssh-format=openssh'
```

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
