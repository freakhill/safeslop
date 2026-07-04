# safeslop skills

Repo-local skills summarize common safeslop operating workflows.

Before using a skill, read:

1. `AGENTS.md`
2. `CONTRIBUTING.md`
3. `README.md`
4. Relevant specs under `specs/`

Current skills:

- `agent-key-lifecycle` — ephemeral GitHub/Forgejo credential staging and cleanup.
- `agent-sandbox-ops` — safe operation of host/container/VM isolation profiles.

When command behavior changes, update the affected skill, `README.md`, and Go
tests in the same change.
