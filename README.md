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
		dev:    {agent: "shell", environment: "sandbox", network: "allow"}
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
safeslop doctor                     report available tools and isolation tiers
safeslop down                       tear down container/VM sessions
safeslop install                    inventory and install pinned toolchains/runtimes
safeslop uninstall                  receipt-driven removal of installed tools
safeslop launch <profile>           open a terminal running a profile
```

Add `--json` to commands for machine-readable output where supported.

## `safeslop.cue` reference

A representative profile:

```cue
package safeslop

safeslop: {
	version: 1
	profiles: {
		work: {
			agent:       "claude"          // "claude" | "shell" | "pi"
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
- Emacs package smoke/contract tests via `make test-emacs`

Useful targeted tests:

```bash
go test ./internal/engine/creds/ -v
go test ./internal/engine/policy/ -run 'Pinned|Latest' -v
go test ./internal/cli/ -v
go test ./internal/jsoncontract -v
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
