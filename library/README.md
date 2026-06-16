# library/

Reusable building blocks for agent isolation. Two top-level subtrees, each with its own README:

- **[`layer/`](layer/)** — building blocks grouped by technical layer. Pick the pieces you need; nothing forces a particular composition.
  - [`layer/container/`](layer/container/) — Dockerfiles, compose, [Squid](https://www.squid-cache.org/) proxy, allowlist domains, env templates.
  - [`layer/host/`](layer/host/) — macOS host-side scaffolding (today: pointer to compiled `sandbox-exec` profiles under `policy/`).
  - [`layer/vm/`](layer/vm/) — disposable VM scaffolding (today: pointer to the [`slop-brew-vm`](../scripts/slop-brew-vm.fish) recipe under `task/evaluate-formulae/`).
  - [`layer/policy/`](layer/policy/) — [CUE](https://cuelang.org/) schema, presets, fixtures, and per-app drop-in settings (Claude Code, OpenCode, Forgejo instance map).
- **[`task/`](task/)** — end-to-end recipes. Each is a short README that *references* the relevant layer artifacts; nothing is duplicated.
  - [`task/launch-agent/`](task/launch-agent/) — drop into Claude Code or OpenCode with the bundled defaults.
  - [`task/isolate-network/`](task/isolate-network/) — route the agent through Squid + an [Envoy](https://www.envoyproxy.io/) MITM stack.
  - [`task/sandbox-mac/`](task/sandbox-mac/) — wrap the agent in a [`sandbox-exec`](https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide-v1.0.pdf) profile.
  - [`task/evaluate-formulae/`](task/evaluate-formulae/) — audit a Homebrew formula in a disposable [Tart](https://tart.run) VM.
  - [`task/restrictive-flows/`](task/restrictive-flows/) — pre-baked tight policies for OpenClaw and ZeroClaw.

If you're new, start at [`task/`](task/) — pick the recipe that matches your goal and follow the layer-artifact links from there.
