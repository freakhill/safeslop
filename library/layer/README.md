# library/layer/

Building blocks grouped by *technical layer* — the substrate they operate on. Pick the pieces you need; nothing here forces a particular composition. For end-to-end recipes that compose multiple layers, see [`../task/`](../task/).

## [`container/`](container/)

Everything for running an agent in a [Docker](https://www.docker.com)-style container behind a deny-by-default [Squid](https://www.squid-cache.org/) URL allowlist proxy.

- `Dockerfile.agent` — minimal sandbox image (alpine + git + ssh).
- `Dockerfile.agent.tools` — same plus pinned Claude Code / OpenCode / CrewAI / PydanticAI / AG2.
- `docker-compose.yml` — `agent`, `agent-tools`, and `proxy` services on isolated networks (sandbox_internal + sandbox_egress).
- `squid.conf` — proxy config with strict-egress defaults.
- `allowlist.domains` — the registry/git domains the proxy lets through.
- `agent-tools.env.example` — version pins for the tools image. Copy to `agent-tools.env` (gitignored) for local overrides.

Driven by [`slop-agent-sandbox`](../../../scripts/slop-agent-sandbox.fish) and [`slop-agent-sandbox-tools`](../../../scripts/slop-agent-sandbox-tools.fish).

## [`host/`](host/)

macOS host-side scaffolding. Today the substantive material is `sandbox-exec` profiles compiled by `slop-isolate` from CUE — those land under [`../policy/fixtures/<adapter>/<adapter>.sb`](policy/fixtures/). Driven by [`slop-macos-sandbox`](../../../scripts/slop-macos-sandbox.fish).

## [`vm/`](vm/)

Disposable VM scaffolding. Today the substantive material is the [Tart](https://tart.run) brew-VM recipe at [`../task/evaluate-formulae/`](../task/evaluate-formulae/) and the runtime under [`slop-brew-vm`](../../../scripts/slop-brew-vm.fish).

## [`policy/`](policy/)

What an agent is allowed to do — schemas, presets, golden fixtures, and per-app drop-in settings.

- `schema/schema.cue` — CUE schema defining `#Isolation` (network / filesystem / process / adapters).
- `presets/*.cue` — ten built-in presets (`any-agent`, `claude-code`, `opencode`, `crewai`, `pydantic-ai`, `ag2`, `openclaw`, `zeroclaw`, `nous-hermes-local`, `nous-hermes-remote`).
- `fixtures/<adapter>/` — golden compile output per adapter. Used by tests; safe to copy verbatim into a target host.
- `samples/isolation/user-config.cue` — a worked example of extending an `#Isolation` preset via `extras`.
- `samples/slop/slop.cue` — a worked example of the orchestrator schema (`#Slop` / `#Profile`); shows multi-profile + `default`, host/container envs, ephemeral creds, and on-exit hooks.
- `cue.mod/module.cue` — the CUE module declaration (`module: "slop.dev/isolation"`). Imports inside this tree use that module name, so the directory is movable without breaking imports.
- `claude-code.settings.json` — drop-in defaults for Claude Code, also referenced by [`slop-agents seed claude`](../../../scripts/slop-agents.fish).
- `opencode.restrictive.json` — strict OpenCode permission policy.
- `forgejo-instances.example.json` — instance-profile template for `slop-forgejo-key`.
- `radicle-access-policy.example.json` — bindings template for `slop-radicle`.

Compiled by [`slop-isolate`](../../../scripts/slop-isolate.fish).
