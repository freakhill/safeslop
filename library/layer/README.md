# Isolation layers

Layer assets document the boundaries that the Go engine can launch:

- `container/` — Docker/Lima container assets and proxy allowlist inputs.
- `host/` — macOS host/sandbox reference material.
- `vm/` — disposable VM reference material.
- `policy/` — schema-adjacent reference fixtures.

The supported entrypoint for all layers is the `safeslop` binary. Use
`safeslop doctor` to inspect available tools and `safeslop run <profile>` to
launch a policy profile.
