# task/restrictive-flows/

Pre-baked tight policies for two specific agents:

- **[`openclaw.md`](openclaw.md)** — restrictive flow for OpenClaw. SOUL.md tool allowlist, OS sandbox layer, deny-by-default egress.
- **[`zeroclaw.md`](zeroclaw.md)** — restrictive flow for ZeroClaw. Workspace-bounded, supervised-autonomy, no host PID namespace.

Both flows reference [`../../layer/policy/presets/openclaw.cue`](../../layer/policy/presets/openclaw.cue) / [`zeroclaw.cue`](../../layer/policy/presets/zeroclaw.cue) (CUE source) and the matching fixtures under [`../../layer/policy/fixtures/`](../../layer/policy/fixtures/) (compile output).

For other agents, start from [`launch-agent/`](../launch-agent/) and lean on the bundled defaults — those are already restrictive enough for most uses. The flows here exist because OpenClaw / ZeroClaw have non-default behavior worth documenting in line.
