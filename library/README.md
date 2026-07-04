# safeslop library

Static assets used by the Go engine.

- `layer/container/` — canonical container assets (Dockerfiles, compose, squid
  allowlist, the agent-tools env example) copied into the Go embed tree by
  `make sync-container-assets` and verified by `make check`.

Everything the engine does at runtime is implemented by the `safeslop` Go binary —
use `safeslop validate`, `safeslop run`, `safeslop doctor`, and `safeslop down`.
The former fish/Python toolkit's policy fixtures, `.sb` Seatbelt profiles, and task
recipes were removed in specs/0054 (reference-only, superseded by the Go engine).
