# safeslop library

This directory contains static assets and reference material used by the Go
engine and documentation.

- `layer/container/` — canonical container assets copied into the Go embed tree
  by `make sync-container-assets` and checked by `make check`.
- `layer/policy/` — reference policy fixtures and generated examples kept for
  compatibility with the design history.
- `task/` — explanatory recipes for isolation and agent workflows. Current
  runtime behavior is implemented by the `safeslop` Go binary.

For day-to-day use, prefer `safeslop validate`, `safeslop run`, `safeslop doctor`,
and `safeslop down`.
