# task/isolate-network/

Force an agent's outbound traffic through a deny-by-default proxy with an explicit URL/domain allowlist. Defense-in-depth on top of the OS-level controls — URL allowlists do not stop a `bash -c "curl ..."` or a raw socket; only the network namespace + proxy-only routing actually does.

## What it composes

- [`../../layer/container/Dockerfile.agent`](../../layer/container/Dockerfile.agent) and [`Dockerfile.agent.tools`](../../layer/container/Dockerfile.agent.tools) — minimal images.
- [`../../layer/container/docker-compose.yml`](../../layer/container/docker-compose.yml) — `agent` and `agent-tools` on the `sandbox_internal` network with no direct internet route; `proxy` on both `sandbox_internal` and `sandbox_egress`.
- [`../../layer/container/squid.conf`](../../layer/container/squid.conf) — [Squid](https://www.squid-cache.org/) config: deny-by-default, allow only the listed domains.
- [`../../layer/container/allowlist.domains`](../../layer/container/allowlist.domains) — the domains the proxy lets through (registries + git).
- (Optional, for SNI/MITM enforcement) the [Envoy](https://www.envoyproxy.io/) + [CoreDNS](https://coredns.io/) + [terminal-notifier](https://github.com/julienXX/terminal-notifier) stack emitted by `slop-isolate` — see [`scripts/slop-isolate.fish`](../../../scripts/slop-isolate.fish).

## Recipe

```fish
source scripts/slop-agent-sandbox-tools.fish

# Build the base + tools images (the FROM-dep is handled automatically)
slop-agent-sandbox-tools up

# Drop into the container — outbound limited to the allowlist
slop-agent-sandbox-tools shell
```

Or for a one-off command:

```fish
slop-agent-sandbox-tools run npm view @anthropic-ai/claude-code dist.integrity
```

## Tightening it further

Add the [Envoy](https://www.envoyproxy.io/) MITM stack:

```fish
source scripts/slop-isolate.fish
slop-isolate proxy start --mitm
```

Stack-specific approve-on-the-fly flow:

```fish
slop-isolate approve --once api.example.com   # 10-min TTL
slop-isolate denials --since 10m              # tail recent blocks
slop-isolate proxy stop
```

## Failure modes

- `pull access denied for local/agent-sandbox` from docker.io → the base image hasn't been built yet. Run `slop-agent-sandbox-tools up` first; it builds both `agent` and `agent-tools` so the FROM-dep resolves.
- Squid blocks a domain you actually need → either add it to `allowlist.domains` and rebuild, or `slop-isolate approve --once <host>` if the Envoy MITM stack is running.

## Cleanup

```fish
slop-agent-sandbox-tools down    # stop and remove the stack
slop-isolate proxy stop          # if the Envoy stack is up
```
