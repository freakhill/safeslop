# 0047 — graduate fish: complete the Go migration + multi-repo creds Implementation Plan

**Goal:** Retire the entire fish + Python(`uv`) + Textual toolkit and make the signed
`safeslop` Go binary the sole runtime and entrypoint. Close the two real capability gaps that
block deletion — **Forgejo ephemeral deploy keys** and **multi-repo credentials** — then delete
`scripts/`, `tests/*.fish`, the fish CI gates, and the `slop` fish entrypoint, and rewrite the
docs to Go-only.

**Why now:** The Go binary is already runtime-independent of the fish/Python stack — nothing in
`cmd/safeslop` or `internal/**` shells out to a `scripts/*.fish` or `scripts/_py/*.py` file (the
only fish references are `tools.go` listing fish as an *installable* tool and `hostenv` reading
`~/.config/fish/config.fish` read-only; both stay). Deletion is gated by **capability parity**,
not coupling. We are pre-live: breaking changes are acceptable.

**Architecture:** Credentials grow from a single repo-scoped GitHub deploy key (`creds/ssh.go`,
keyed off the `origin` remote) to a **per-forge, per-repo provider** that mints N ephemeral deploy
keys (default) or one fine-grained PAT (opt-in), for both **GitHub and Forgejo**, and stages them
with distinct SSH `Host <host>-<slug>` aliases plus git `insteadOf` URL rewrites so git selects the
right credential per repo. The orchestrator's launch path (`run`) drives provisioning and on-exit
revocation; the staged dir wipe remains the decay-first guarantee.

**Tech stack:** Go stdlib + existing deps (cobra, embedded CUE via `cuelang.org/go`). No new
runtime deps. `gh` / `tea` CLIs remain the API transport for key/token lifecycle (already how
`ssh.go` works). TDD on every new Go unit.

**Decisions locked (this session):**
- **Multi-repo:** deploy-key multi-alias is the **default**; **PAT opt-in** per profile (`mode: "pat"`).
- **GitHub manual key CLI** (`list`/`revoke`/`revoke-by-title`/`install-ssh-config`/`here`): **dropped.**
  `run` auto-stages on launch and revokes on exit; the only retained manual verb is a new
  `safeslop creds gc` orphan sweep.
- **Host Envoy/CoreDNS/notifier isolation** (`isolation.py` + `envoy_notifier.py` + `slop-isolate.fish`):
  **deleted with the rest.** The Go tiers model (host=none / sandbox=Seatbelt / container=Docker+squid /
  vm=adversary-grade, specs/0023) already supersedes it. What a Go-native host egress-approval UX
  should be is a **separate premium-FLO follow-up** (→ `specs/0048-host-egress-approval-flo.md`),
  which graduation does **not** wait on. Accept a temporary lapse of the interactive
  `approve --once/--always` egress flow.
- **Entrypoint:** hard cut. `safeslop` binary is the sole entrypoint; remove `slop.fish`, `./install`,
  `slop-install.fish`, and the fish conf.d snippet. No `slop` shim.

**Scope:** Everything under `scripts/`, `tests/*.fish`, the four fish CI pipelines (Woodpecker +
GitHub mirror), the fish entrypoint/installer, and the README "Legacy fish toolkit" section. **Plus**
two net-new Go capabilities (Forgejo provider, multi-repo creds) and two small parity ports (the
`:latest` pinning gate → Go; agent config seeding → confirm/port).

**Base branch:** `graduate-fish` off `main`. Develop on Forgejo (`forgejo` remote), open the PR in
the Forgejo web UI. **Never push `main`.**

---

## Phase 1 — Forgejo ephemeral deploy-key provider (Go)

Mirror `creds/ssh.go` for Forgejo/Gitea. Multi-instance: HostName + API base resolved per instance
(github's is hardcoded `github.com`; Forgejo/Codeberg/self-hosted vary), reusing the `tea` CLI as
transport. This unblocks `credentials: forgejo:` profiles, which today have **zero** Go support.

**File structure:**
- Create: `internal/engine/creds/forgejo.go` — `StageForgejo` / `RevokeForgejo`, argv builders, parsers.
- Create: `internal/engine/creds/forgejo_test.go` — TDD on argv builders, owner/repo + host parsing, revoke-info round-trip.
- Modify: `internal/engine/policy/policy.go` — `Credentials.Forgejo` provider type (see Phase 2 schema).
- Reference: `scripts/slop-forgejo-key.fish` (897) + `scripts/_py/llm_forgejo_keys.py` (259) for the
  instance-config shape and API payloads — port the behavior, not the fish.

### Task 1.1: Forgejo argv + parser units (red → green)

- [ ] **Step 1: failing test** `internal/engine/creds/forgejo_test.go` — assert:
  - `forgejoRegisterArgv(host, owner, repo, title, pubkey, write)` produces the `tea` (or
    `gh`-equivalent REST) call against `/api/v1/repos/<owner>/<repo>/keys` with `read_only`.
  - `forgejoRevokeArgv(host, owner, repo, id)` is the DELETE form.
  - `parseForgejoRemote(url)` extracts `(host, owner, repo)` from ssh/scp/https Forgejo remotes and
    rejects a `github.com` URL.
  - `resolveForgejoHost` reads the instance's HostName/API base from the user config (port the
    `forgejo-instances.json` lookup from `llm_forgejo_keys.py`).
- [ ] **Step 2: implement** `forgejo.go` until green: `go test ./internal/engine/creds/ -run Forgejo -v`.

### Task 1.2: `StageForgejo` / `RevokeForgejo`

- [ ] Mirror `StageSSH`: keygen → register deploy key → stage 0600 private key + pinned `known_hosts`
      (per-instance host key, not hardcoded github) + `revoke-info` → return `GIT_SSH_COMMAND` host path.
- [ ] `RevokeForgejo` best-effort, reads `revoke-info`, swallows errors (decay-first wipe is the real cleanup).
- [ ] Wire into the `run` staging path alongside `StageSSH`. Test: `go test ./internal/engine/creds/ -v`.

---

## Phase 2 — Multi-repo credentials (deploy-key default + PAT opt-in)

The feature. Generalize from one repo-scoped key off `origin` to **N keys across named repos**, per
forge, with a PAT alternative. Solves the "multiple deploy keys, one host" problem via per-repo SSH
aliases + git `insteadOf` rewrites in the staged config.

**Proposed schema** (`library/layer/policy/schema/schema.cue`) — replaces the single enum (we can
break; not live):

```cue
#RepoAccess: "ro" | "rw"
#RepoCred: {repo: string, access: #RepoAccess | *"ro"}   // repo = "owner/name"

#ForgeCredential: {
	mode: *"deploy-key" | "pat"
	// deploy-key mode: one ephemeral key per entry. Omit `repos` to infer the
	// single repo from the cwd origin (back-compat with the old single-repo flow).
	repos?: [...#RepoCred]
	// pat mode (opt-in): one fine-grained token scoped to `repos`.
}
#Credentials: {
	github?:  #ForgeCredential
	forgejo?: #ForgeCredential
}
```

**Staging mechanics** (`creds/`): for each `RepoCred`, mint a key and append an SSH block

```
Host github.com-<slug>          # slug = owner-name
  HostName github.com
  IdentityFile <staged>/id_<slug>
  IdentitiesOnly yes
```

plus a git rewrite so unmodified remotes route to the right key:

```
[url "git@github.com-<slug>:<owner>/<name>.git"]
    insteadOf = git@github.com:<owner>/<name>.git
```

PAT mode stages one token as a non-secret-path env (token in the staged dir, wiped on exit) and an
`http.extraheader`/credential-helper rewrite instead of SSH aliases.

**File structure:**
- Modify: `schema.cue` (above), `policy/policy.go` (provider structs), `creds/ssh.go` +
  `creds/forgejo.go` (loop over repos; emit aliases + insteadOf), and add `creds/pat.go` (PAT mode).
- Create: `creds/multirepo_test.go` — 2-repo fixture: assert two distinct keys staged, two `Host`
  aliases, two `insteadOf` rewrites, and (PAT) one token + one rewrite.
- Modify: `library/layer/policy/samples/slop/slop.cue` — show a multi-repo profile.

### Task 2.1: schema + policy types (red → green)

- [ ] Failing CUE/policy test: a multi-repo `#Credentials` validates; a bare-string `github: "ephemeral-rw"`
      now fails (document the migration in the sample). `go test ./internal/engine/policy/ -v`.
- [ ] Implement provider structs + CUE; update `cli list`/`validate` rendering if it prints creds.

### Task 2.2: multi-key staging + insteadOf (red → green)

- [ ] Failing `creds/multirepo_test.go` (2-repo fixture, both forges) asserting aliases + rewrites + key count.
- [ ] Implement the per-repo loop in `ssh.go`/`forgejo.go`; factor the alias/insteadOf writer into a shared helper.
- [ ] On-exit: revoke **every** staged key id (extend `revoke-info` to N lines). `go test ./internal/engine/creds/ -v`.

### Task 2.3: PAT opt-in mode

- [x] `creds/pat.go`: stage an existing fine-grained PAT scoped to `repos` as HTTPS credentials for GitHub/Forgejo.
      safeslop intentionally does not mint or revoke account PATs; rotate/revoke PATs at the forge.
- [x] Test PAT staging for 2-repo GitHub + Forgejo fixtures; token value lives only in a 0600 staged file, never in git config/env.

---

## Phase 3 — `safeslop creds gc` (drop the rest of the manual key CLI)

- [ ] Add `safeslop creds gc [--forge github|forgejo] [--dry-run]` — list deploy keys whose title
      matches the `safeslop-*` prefix and are past TTL, revoke them. Sweeps orphans from crashed runs.
- [ ] Test in `internal/cli/cli_creds_test.go`. Do **not** port `list`/`revoke`/`revoke-by-title`/
      `install-ssh-config`/`here` — `run` auto-stages + revokes.

---

## Phase 4 — Parity ports before deletion

Two capabilities die with fish unless ported.

### Task 4.1: `:latest` pinning gate → Go

`slop-pinning.fish` scans every `*.cue` and the four agent-tools build-config files for `:latest"`,
`@latest"`, `==latest`. `policy/lint.go` does **not** cover this today.

- [x] Add a dedicated Go pinning gate that fails on `:latest`/`@latest`/`==latest` in `*.cue` + the build-config files.
- [x] Wire into `make check` via `go test ./...`. Test: `go test ./internal/engine/policy/ -run 'Pinned|Latest' -v`.

### Task 4.2: agent config seeding parity

`slop-agents seed` writes bundled agent defaults non-clobbering; Go `launch.go` is only a ctty
terminal-spawn.

- [x] Port non-clobbering Claude/OpenCode default seeding into Go launch/session paths using embedded fixtures.

---

## Phase 5 — Entrypoint hard cut

- [ ] Delete `scripts/slop.fish`, `./install`, `scripts/slop-install.fish`, and the conf.d snippet generation.
- [ ] Confirm `safeslop` exposes every user-facing verb the `slop` wrapper did (`run`/`validate`/`list`/`down`
      already present). No `slop` alias.
- [ ] Remove README "Install fish command shims" + "Cleanup of legacy installs" sections.

---

## Phase 6 — Delete the fish/Python stack + CI

Only after Phases 1–5 are green.

- [ ] Delete `scripts/*.fish` (18 files) and `scripts/_py/*.py` (7 files), including the superseded host
      isolation stack (`isolation.py`, `envoy_notifier.py`, `slop-isolate.fish`), the dev tooling
      (`slop-sandboxctl`, `slop-safe-uv/npm`, `slop-skills-install`, `slop-sync-help`, `slop-pinning`,
      `script-template`, `brew-sandbox`), and the now-ported key scripts.
- [ ] Delete `tests/*.fish`, `tests/run.fish`, `tests/helpers.fish`, `tests/README.md`.
- [ ] Delete the four fish Woodpecker pipelines (`.woodpecker/{help-sync,pinning,tests,script-doc-sync}.yml`)
      and the four GitHub mirror workflows (`.github/workflows/{help-sync-check,pinning-check,tests,script-doc-sync-check}.yml`).
      **Keep** `.woodpecker/go.yml` and `.github/workflows/go.yml`.
- [ ] Rewrite `CLAUDE.md`, `AGENTS.md`, `README.md`, `scripts/CONVENTIONS.md`, `CONTRIBUTING.md` to Go-only:
      drop the fish landmines, the four fish gates, the uv/Textual/PEP-723 sections, and the strangler framing.
- [ ] Spin out `specs/0048-host-egress-approval-flo.md` stub referencing this decision (premium-FLO to design
      the Go-native replacement for the dropped `approve --once/--always` UX).

---

## Phase 7 — Verification (gates & done-checklist)

```bash
make check                 # vet + gofmt + go test ./... (incl. new pinning + creds tests)
make build                 # static binary
go test ./internal/engine/creds/ ./internal/engine/policy/ ./internal/cli/ -v
```

Binary smoke (real, in a scratch repo):
- [ ] `safeslop run <profile>` — single GitHub deploy key (back-compat path) stages + revokes.
- [ ] Forgejo profile stages + revokes against a self-hosted/Codeberg instance.
- [ ] **Multi-repo** profile: two repos → two keys, two SSH aliases, two `insteadOf` rewrites; git
      operations to both repos succeed inside the agent; both keys revoked on exit.
- [ ] PAT opt-in profile: token staged + rewrite + revoke.
- [ ] `safeslop creds gc --dry-run` lists orphaned `safeslop-*` keys.

Graduation gate — prove fish-free:
- [ ] `grep -rIl -E '\.fish|_py/|uv run|fish tests' --include='*.go' --include='*.yml' --include='*.md' .`
      returns only the **allowed** residue (the `tools.go` installable-tool entry + `hostenv`
      `config.fish` read + any historical spec text). No runtime invocations remain.
- [ ] `scripts/` and `tests/*.fish` no longer exist; `make check` + the Go CI are the only gates.

---

## Deferred (explicitly out of scope)

- The Go-native host egress-approval UX (→ `specs/0048`, premium-FLO).
- Re-syncing the GitHub `origin` mirror + resuming GitHub CI (release-time, per CLAUDE.md).
- Any new isolation tier or adapter beyond the existing host/sandbox/container/vm model.
