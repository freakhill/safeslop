# library/layer/host/

macOS host-side scaffolding. There is no static file in this directory — the substantive material is generated:

- **`sandbox-exec` profiles** are compiled by `slop-isolate` from a CUE preset and emitted at [`../policy/fixtures/<adapter>/<adapter>.sb`](../policy/fixtures/). Pre-compiled fixtures cover `any-agent`, `claude-code`, `opencode`, `crewai`, `pydantic-ai`, `ag2`, `openclaw`, `zeroclaw`, `nous-hermes-local`, `nous-hermes-remote`.
- **[pf](https://www.openbsd.org/faq/pf/) rules** are also a `slop-isolate` adapter output (per-flow). pf is the kernel firewall built into macOS; rules are not committed to this tree because they need root to load.
- **[LuLu](https://objective-see.org/products/lulu.html)** is configured per-binary via its own GUI; the repo's role is to provide the deny-by-default mindset and the agent allowlist domains under [`../container/allowlist.domains`](../container/allowlist.domains).

Driven by [`slop-macos-sandbox`](../../../scripts/slop-macos-sandbox.fish) (run, shell, print-profile) and [`slop-isolate`](../../../scripts/slop-isolate.fish) (compile + apply).
