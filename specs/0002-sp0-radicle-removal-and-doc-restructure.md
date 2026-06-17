# SP0 — Radicle removal + README restructure — Implementation Plan

**Goal:** Scrap all Radicle support and restructure the README to lead with the real use
cases (Claude Code; sandboxed shell for `pnpm`/`uv`), reframe `sandbox-exec` as a
first-class local boundary, and move the capability matrix to the bottom — all on the
current fish/Python stack, keeping every CI gate green.

**Architecture:** Pure deletion + editorial work, no new behavior. Radicle is removed by
deleting its 5 dedicated files and surgically excising every shared-file reference, then
the README is reorganized. The old stack stays runnable throughout (this is the
decks-clearing sub-project of the larger Go rewrite — see `specs/0001-go-rewrite-design.md`).

**Tech stack:** fish, Python-via-`uv`, CUE, Textual; the repo's own gates (`fish -n`,
`tests/run.fish`, `slop-sync-help`, `slop-pinning`).

**CI gates that must stay green** (`.github/workflows/`): `tests.yml`,
`script-doc-sync-check.yml` (any `scripts/*.fish`|`scripts/_py/*.py` change in the PR must
be accompanied by a `README.md` **and** a `skills/*/SKILL.md` **and** a `tests/*.fish`
change — SP0 does all three), `help-sync-check.yml` (`slop-sync-help check`),
`pinning-check.yml`, `sandbox-images-check.yml` (untouched by SP0).

**File structure:**

Delete:
- `scripts/slop-radicle.fish` — Radicle identity lifecycle (gone).
- `scripts/_py/llm_radicle_access.py` — Radicle state helper (gone).
- `scripts/completions/slop-radicle.fish` — completions (gone).
- `tests/test_slop_radicle.fish` — Radicle test suite (gone).
- `library/layer/policy/radicle-access-policy.example.json` — example state (gone).

Modify (Radicle excision):
- `library/layer/policy/schema/schema.cue` — drop `#RadicleCredential` + `radicle?:` field.
- `scripts/_py/slop_orchestrator.py` — drop the radicle const/provision/stage/dry-run/revoke.
- `scripts/_py/slop_tui.py` — drop `build_radicle_actions()` + its menu entry.
- `scripts/slop.fish` — drop the `slop-radicle tui` help line.
- `scripts/slop-install.fish` — drop radicle from module list + legacy cleanup list.
- `scripts/slop-sandboxctl.fish` — drop radicle dispatch/topic/tutorial-autogen/tool-map/comments.
- `scripts/completions/slop-sandboxctl.fish` — drop radicle topic completions.
- `tests/test_slop_orchestrator.fish` — drop `test_credential_staging_handles_radicle_keypair`.
- `tests/test_slop.fish` — drop the radicle TUI-audit assertion.
- `tests/test_slop_sandboxctl.fish` — drop the `radicle-access` topic assertion.
- `tests/test_py_helpers.fish` — drop the radicle helper test functions.
- `README.md`, `CLAUDE.md`, `RELEASE.md`, `library/layer/README.md`, `library/README.md`,
  `library/task/restrictive-flows/openclaw.md`, `library/task/restrictive-flows/zeroclaw.md`,
  `skills/agent-key-lifecycle/SKILL.md` — drop radicle prose (RELEASE.md keeps history +
  gets a removal note).

Modify (README restructure):
- `README.md` — add "Common use cases" lead; reframe `sandbox-exec` first-class; move the
  capability matrix to a bottom "Reference appendix".

---

## Part A — Radicle removal

### Task 1: Delete the five dedicated Radicle files

**Files:** Delete the 5 listed under "Delete" above.

- [ ] **Step 1: Delete the files**

```bash
cd /Users/jojo/workspace/safeslop
git rm scripts/slop-radicle.fish \
       scripts/_py/llm_radicle_access.py \
       scripts/completions/slop-radicle.fish \
       tests/test_slop_radicle.fish \
       library/layer/policy/radicle-access-policy.example.json
```

- [ ] **Step 2: Verify fish syntax still parses (the deleted script can't break siblings)**

```bash
fish -n scripts/*.fish
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git commit -m "sp0: delete dedicated Radicle files (script, helper, completions, test, fixture)"
```

### Task 2: Remove Radicle from the CUE schema

**Files:** Modify `library/layer/policy/schema/schema.cue`

- [ ] **Step 1: Remove the type definition.** Delete the line:

```cue
#RadicleCredential:  *"none" | "ephemeral"
```

- [ ] **Step 2: Remove the struct field.** In `#Credentials`, delete the line:

```cue
	radicle?: #RadicleCredential
```

- [ ] **Step 3: Fix the nearby comment.** Find the comment referencing the radicle flow
  (it reads `matching \`slop-<host>-key here ...\` (or \`slop-radicle ...\`) flow on`) and
  remove the `(or \`slop-radicle ...\`)` clause so it reads `matching \`slop-<host>-key
  here ...\` flow on`.

- [ ] **Step 4: Verify the schema + orchestrator schema tests still pass**

```bash
fish tests/run.fish test_slop_orchestrator_schema.fish
```
Expected: PASS (no `radicle` field expected anywhere).

- [ ] **Step 5: Commit**

```bash
git add library/layer/policy/schema/schema.cue
git commit -m "sp0: drop radicle credential from CUE schema"
```

### Task 3: Remove Radicle from the Python orchestrator + its test

**Files:** Modify `scripts/_py/slop_orchestrator.py`, `tests/test_slop_orchestrator.fish`

- [ ] **Step 1: Excise every radicle construct in `slop_orchestrator.py`.** Grep first, then
  remove each block. The constructs (26 references) are:
  - the constant `SLOP_RADICLE = SOURCE_REPO_ROOT / "scripts" / "slop-radicle.fish"`;
  - in `_provision_credentials`, the block beginning `rad = profile.credentials.get("radicle")`
    through its `snapshot["radicle"] = {...}` (the `create-identity` call);
  - in `_stage_credentials`, the radicle staging block (the `if rad != "none":` block that
    copies `llm_agent_radicle_*` keys and writes the `RAD_KEYS_PATH` SSH-config comment),
    plus the `rad = ...` mode extraction and the `radicle` mention in the family comment;
  - the `radicle` entries in the two dry-run family lists (host staging + VM staging);
  - in the revoke-credentials hook, the block `if "radicle" in state.credentials:` running
    `retire-expired --yes`.

```bash
grep -n -i radicle scripts/_py/slop_orchestrator.py    # should print nothing after edits
```

- [ ] **Step 2: Remove the radicle staging test.** In `tests/test_slop_orchestrator.fish`,
  delete the whole `test_credential_staging_handles_radicle_keypair` function and any line
  that registers/calls it in the test runner list.

```bash
grep -n -i radicle tests/test_slop_orchestrator.fish    # should print nothing after edits
```

- [ ] **Step 3: Run the orchestrator suite**

```bash
fish tests/run.fish test_slop_orchestrator.fish
```
Expected: PASS, no radicle references.

- [ ] **Step 4: Commit**

```bash
git add scripts/_py/slop_orchestrator.py tests/test_slop_orchestrator.fish
git commit -m "sp0: remove radicle provisioning/staging/revoke from orchestrator + test"
```

### Task 4: Remove Radicle from the Textual TUI + its audit assertion

**Files:** Modify `scripts/_py/slop_tui.py`, `tests/test_slop.fish`

- [ ] **Step 1: Remove `build_radicle_actions()`** entirely from `slop_tui.py`.

- [ ] **Step 2: Remove the menu entry.** In `build_top_actions()`, delete the `Action(key="r",
  label="Radicle access", ... submenu=build_radicle_actions(), ...)` item. (The key `r`
  becomes free for future use; do not renumber other keys.)

```bash
grep -n -i radicle scripts/_py/slop_tui.py    # should print nothing after edits
```

- [ ] **Step 3: Remove the audit assertion.** In `tests/test_slop.fish`, delete the assertion
  that checks for a `slop-radicle tui` shell-out in the TUI python (the radicle line in the
  audit test).

```bash
grep -n -i radicle tests/test_slop.fish    # should print nothing after edits
```

- [ ] **Step 4: Run the TUI audit + mount-check + the slop test**

```bash
env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
    uv run --script --quiet scripts/_py/slop_tui.py --audit
env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
    uv run --script --quiet scripts/_py/slop_tui.py --mount-check
fish tests/run.fish test_slop.fish
```
Expected: audit OK (no legacy shellouts, no leftover placeholders), mount-check OK, PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/_py/slop_tui.py tests/test_slop.fish
git commit -m "sp0: remove Radicle access submenu from Textual TUI + audit assertion"
```

### Task 5: Remove Radicle from fish dispatch/install + completions + topic test

**Files:** Modify `scripts/slop.fish`, `scripts/slop-install.fish`,
`scripts/slop-sandboxctl.fish`, `scripts/completions/slop-sandboxctl.fish`,
`tests/test_slop_sandboxctl.fish`

- [ ] **Step 1: `scripts/slop.fish`** — remove the help-text line mentioning `slop-radicle tui`.

- [ ] **Step 2: `scripts/slop-install.fish`** — remove `slop-radicle` from the module-scripts
  list (`__ift_module_scripts`) and from the legacy-bin cleanup list (`__ift_legacy_bin_cmds`).

- [ ] **Step 3: `scripts/slop-sandboxctl.fish`** — remove: the radicle dispatcher mapping,
  the `radicle-access` topic entry, the `__sandboxctl_tutorial_radicle_access` function
  (including its `# BEGIN/END AUTOGEN` block), the radicle tool-map entry, the radicle
  comment(s), and the `radicle-access` switch/dispatch cases.

- [ ] **Step 4: `scripts/completions/slop-sandboxctl.fish`** — remove the `radicle-access`
  topic from the completion candidates.

- [ ] **Step 5: `tests/test_slop_sandboxctl.fish`** — remove `radicle-access` from the
  known-topics assertion loop.

```bash
grep -n -i radicle scripts/slop.fish scripts/slop-install.fish \
     scripts/slop-sandboxctl.fish scripts/completions/slop-sandboxctl.fish \
     tests/test_slop_sandboxctl.fish    # should print nothing after edits
```

- [ ] **Step 6: Verify syntax + the affected suites**

```bash
fish -n scripts/*.fish
fish tests/run.fish test_slop_sandboxctl.fish test_slop_install.fish
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add scripts/slop.fish scripts/slop-install.fish scripts/slop-sandboxctl.fish \
        scripts/completions/slop-sandboxctl.fish tests/test_slop_sandboxctl.fish
git commit -m "sp0: remove radicle from fish dispatch, install, completions, topic test"
```

### Task 6: Remove Radicle from the Python-helper tests

**Files:** Modify `tests/test_py_helpers.fish`

- [ ] **Step 1: Delete every radicle helper test function** (the `test_radicle_*` family:
  `test_radicle_uuid8_format`, `test_radicle_identity_lifecycle`,
  `test_radicle_retire_expired`, `test_radicle_bind_repo_idempotent_upgrade`,
  `test_radicle_bind_repo_inactive_identity_rejected`, `test_radicle_get_active_key`) and
  any runner registration that invokes them.

```bash
grep -n -i radicle tests/test_py_helpers.fish    # should print nothing after edits
```

- [ ] **Step 2: Run the helper suite**

```bash
fish tests/run.fish test_py_helpers.fish
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add tests/test_py_helpers.fish
git commit -m "sp0: remove radicle python-helper tests"
```

### Task 7: Remove Radicle from docs + skills; resync help

**Files:** Modify `README.md`, `CLAUDE.md`, `RELEASE.md`, `library/layer/README.md`,
`library/README.md`, `library/task/restrictive-flows/openclaw.md`,
`library/task/restrictive-flows/zeroclaw.md`, `skills/agent-key-lifecycle/SKILL.md`

- [ ] **Step 1: `README.md`** — remove `slop-radicle` from the feature bullet (the
  "ephemeral key/identity lifecycle helpers" line becomes `(\`slop-gh-key\`,
  \`slop-forgejo-key\`)`); delete the entire `### How to manage ephemeral Radicle identities
  across many repos` section (heading through its `Reference state format: ...` line and the
  following `---` divider); remove the two radicle mentions in the tool-inventory section.

- [ ] **Step 2: `CLAUDE.md`** — remove the `slop-radicle.fish` line from the credential-
  lifecycle list and the radicle staging explanation (RAD_KEYS_PATH / `rad://` / "no SSH
  alias") paragraph.

- [ ] **Step 3: `RELEASE.md`** — KEEP the two historical release entries (they were accurate
  at the time; do not rewrite shipped history). Add a new top entry:

```markdown
## Unreleased

- Removed: Radicle identity support (`slop-radicle`, `llm_radicle_access.py`, the
  `radicle` credential in `slop.cue`, and the Radicle TUI submenu). Radicle is no longer
  supported.
```

- [ ] **Step 4: `library/layer/README.md` + `library/README.md`** — remove the line(s)
  referencing `radicle-access-policy.example.json` / radicle.

- [ ] **Step 5: `library/task/restrictive-flows/openclaw.md` + `zeroclaw.md`** — remove the
  sentence recommending `scripts/slop-radicle.fish` for repos the agent touches.

- [ ] **Step 6: `skills/agent-key-lifecycle/SKILL.md`** — remove the radicle command-map
  entry, the entire "Radicle identities across multiple repos" workflow section, and the
  radicle sync-requirement line (the one naming `tests/test_slop_radicle.fish`).

- [ ] **Step 7: Resync AUTOGEN help blocks and confirm no drift**

```bash
fish scripts/slop-sync-help.fish sync
fish scripts/slop-sync-help.fish check
```
Expected: `sync` rewrites nothing radicle-related (the markers are gone); `check` exits 0.

- [ ] **Step 8: Confirm radicle is gone everywhere except the RELEASE.md history/removal note**

```bash
grep -rniE 'radicle|rad://|RAD_KEYS_PATH|llm_radicle|slop-radicle' . \
  --exclude-dir=.git --exclude-dir=specs
```
Expected: only `RELEASE.md` lines (historical entries + the new removal note). Nothing else.

- [ ] **Step 9: Full suite + commit**

```bash
fish tests/run.fish
git add README.md CLAUDE.md RELEASE.md library/layer/README.md library/README.md \
        library/task/restrictive-flows/openclaw.md \
        library/task/restrictive-flows/zeroclaw.md \
        skills/agent-key-lifecycle/SKILL.md scripts/
git commit -m "sp0: remove radicle from docs + skills; resync help"
```

---

## Part B — README restructure

> Part B is README-only (no `scripts/` change), so the doc-sync gate does not trigger; only
> `help-sync-check` (`slop-sync-help check`) matters. Run `slop-sync-help sync` after each
> task in case a reframed section feeds an AUTOGEN block.

### Task 8: Lead with the real use cases

**Files:** Modify `README.md`

- [ ] **Step 1: Insert a "Common use cases" section** immediately after the `## Quick start`
  block (before `## Install fish command shims`). Use this exact content:

```markdown
## Common use cases

The two everyday workflows this repo is built for:

- **Sandboxed Claude Code** — drop into Claude Code with file, SSH, network, and installer
  boundaries already applied:

  ```fish
  slop-agents seed all     # one-time: write .claude/settings.json at repo root
  slop-agents claude       # launch Claude Code from the seeded cwd
  ```

- **A sandboxed shell for package work (`pnpm`/`uv`)** — run installs and builds under a
  local `sandbox-exec` boundary so lifecycle scripts can't read `~/.ssh` or phone home:

  ```fish
  slop-macos-sandbox run --repo-root-access -- /usr/bin/env pnpm install
  slop-macos-sandbox run --repo-root-access -- /usr/bin/env uv sync
  ```

Everything below is reference and how-to detail for these and the heavier
container/VM boundaries. See the **Reference appendix** at the end for the full
per-framework capability matrix.
```

- [ ] **Step 2: Verify + commit**

```bash
fish scripts/slop-sync-help.fish check
git add README.md
git commit -m "sp0(readme): lead with the Claude-Code and sandboxed-shell use cases"
```

### Task 9: Reframe `sandbox-exec` as first-class (with honest caveats)

**Files:** Modify `README.md`

- [ ] **Step 1: Rewrite the `### macOS isolation reality in 2026` bullets.** Replace the
  current bullet list + the two paragraphs after it (the `sandbox-exec is deprecated...`
  bullet through the `Optional exception: ...` paragraph) with:

```markdown
- `sandbox-exec` (macOS Seatbelt) is the **first-class lightweight local boundary** for the
  common case — launching Claude Code or a shell for package work with a file boundary and
  strict egress deny. It is built in to macOS, needs no daemon, and starts instantly.
- Honest caveats: Apple has deprecated the `sandbox-exec` CLI (still present and working on
  current macOS), and its network control is coarse (allow/deny, not a URL allowlist).
- For untrusted code or real URL-allowlisting, step up to the container boundary
  (Docker network namespace + a `squid` proxy allowlist) or a disposable VM
  ([Tart](https://tart.run) / [VZ.framework](https://developer.apple.com/documentation/virtualization)).
- Per-process outbound controls are best done with a Network Extension firewall
  ([LuLu](https://objective-see.org/products/lulu.html)) as defense-in-depth.

Practical consequence: use `sandbox-exec` as the default local boundary for everyday agent
and package work; escalate to container/VM when you need URL-level network control or are
running untrusted code.
```

- [ ] **Step 2: Reframe the how-to section.** Change the heading
  `### How to use optional local \`sandbox-exec\` layer on macOS` to
  `### How to run a command under the \`sandbox-exec\` boundary (macOS)`, and replace the
  line `Use this only when full container/VM flows are not practical.` with:

```markdown
This is the default local boundary for everyday agent and package work. Step up to
container/VM (below) when you need URL-level network control or are running untrusted code.
```

- [ ] **Step 3: Update the "Notes" tail of that how-to section** — replace the line
  `- Prefer Docker/VM workflows for untrusted execution` with
  `- Escalate to Docker/VM workflows for untrusted execution or URL-level network control`.

- [ ] **Step 4: Verify + commit**

```bash
fish scripts/slop-sync-help.fish sync
fish scripts/slop-sync-help.fish check
git add README.md scripts/
git commit -m "sp0(readme): promote sandbox-exec to first-class local boundary (honest caveats kept)"
```

### Task 10: Move the capability matrix to a bottom "Reference appendix"

**Files:** Modify `README.md`

- [ ] **Step 1: Cut the matrix subsection.** Remove the entire `### Capability matrix (macOS)`
  subsection — from its heading through the end of the "Process visibility" follow-up note
  (the block ending `...add \`(deny process-info*)\` and \`(deny mach-lookup)\` to the
  profile.`), i.e. everything up to but not including `### Default best-practice
  recommendations per framework`. Hold this text aside.

- [ ] **Step 2: Repoint the Diataxis intro.** In the `## LLM Agent Sandboxing on macOS`
  intro list, change `- Reference: capability matrix and copy/paste config snippets` to
  `- Reference: copy/paste config snippets (the full capability matrix is in the Reference
  appendix at the end)`.

- [ ] **Step 3: Paste the matrix at the bottom.** Immediately before `## Verification
  checklist`, add a new top-level section and paste the cut text under it:

```markdown
## Reference appendix

### Capability matrix (macOS)

<the subsection cut in Step 1, pasted verbatim>

---
```

- [ ] **Step 4: Verify the matrix now appears exactly once, near the bottom**

```bash
grep -n '### Capability matrix (macOS)' README.md         # exactly one hit, near EOF
grep -n '## Reference appendix' README.md                 # one hit, just before Verification checklist
```

- [ ] **Step 5: Run all gates**

```bash
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
fish -n scripts/*.fish
fish tests/run.fish
```
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "sp0(readme): move capability matrix to a bottom Reference appendix"
```

---

## Task 11: Final verification gate (whole SP0)

- [ ] **Step 1: Run every gate CI runs**

```bash
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green.

- [ ] **Step 2: Confirm radicle is fully gone (except RELEASE.md history/note)**

```bash
grep -rniE 'radicle|rad://|RAD_KEYS_PATH|llm_radicle|slop-radicle' . \
  --exclude-dir=.git --exclude-dir=specs
```
Expected: only the `RELEASE.md` historical entries + the removal note.

- [ ] **Step 3: Confirm README structure** — capability matrix appears once under
  `## Reference appendix` near EOF; a `## Common use cases` section sits right after Quick
  start; `sandbox-exec` is described as first-class with caveats.

- [ ] **Step 4: Open the PR** (do not push to `main` directly).

```bash
git push -u origin go-rewrite    # if the user authorizes pushing; otherwise leave commits local
```
