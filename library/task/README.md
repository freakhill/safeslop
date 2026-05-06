# library/task/

End-to-end recipes. Each subdirectory is a short README that walks through one job and *links* to the layer artifacts it composes — no duplication. Pick the recipe that matches your goal:

| Goal | Recipe | Composes |
|---|---|---|
| Run Claude Code or OpenCode with the repo's bundled defaults | [`launch-agent/`](launch-agent/) | [`layer/policy/`](../layer/policy/) |
| Force the agent through a deny-by-default proxy | [`isolate-network/`](isolate-network/) | [`layer/container/`](../layer/container/) |
| Wrap an agent in a `sandbox-exec` profile | [`sandbox-mac/`](sandbox-mac/) | [`layer/host/`](../layer/host/) + [`layer/policy/`](../layer/policy/) |
| Audit a Homebrew formula in a disposable VM | [`evaluate-formulae/`](evaluate-formulae/) | [`layer/vm/`](../layer/vm/) |
| Apply a pre-baked tight policy for a specific agent | [`restrictive-flows/`](restrictive-flows/) | [`layer/policy/`](../layer/policy/) |
