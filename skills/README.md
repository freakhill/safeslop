# safeslop skills

Repo-local skills summarize common safeslop operating workflows.

Before using a skill, read:

1. `AGENTS.md`
2. `CONTRIBUTING.md`
3. `README.md`
4. Relevant specs under `specs/`

Current skills:

- `agent-key-lifecycle` — ephemeral GitHub/Forgejo credential staging and cleanup.
- `agent-sandbox-ops` — safe operation of host/container isolation profiles,
  canonical workspace boundaries, progressive egress, and pinned container builds.

When command behavior changes, update the affected skill, `README.md`,
`emacs/README.md` when UI behavior is visible, and Go/Elisp tests in the same
change. Active skills must not revive removed VM commands or obsolete legacy
image surfaces; historical specs may retain that context only when clearly
labelled as historical.
