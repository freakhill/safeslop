# 0054 — Isolation-image matrix redesign

Status: planned
Branch: `image-matrix` (off `remove-sandbox-tier`, which carries specs/0053)
Supersedes the ad-hoc `local/agent-sandbox*:latest` image scheme.

## Why

Debugging the "create a container/VM and run a command" flow surfaced two root
bugs and a pile of image sprawl. Both bugs were reproduced live while writing
this spec.

- **Bug A — record-less orphan.** A `SIGKILL`'d run skips deferred teardown, and
  neither `session.reconcile` nor `Stop` reap the boundary. `safeslop down` is
  driven off the session *record*, so once the record is gone the orphan is
  invisible to it. Verified live: session `sess-2a99e823254d43770f79ed4a` had its
  record gone (`~/.local/state/safeslop/sessions/` held only `trust.json`) yet its
  `…-agent-run` container + `…-proxy-1` squid + two networks were still up 8h
  later; `safeslop down` did **not** reap them (it only looked at an unrelated VM
  session). Cleaned manually with `docker rm -f` + `docker network rm`.
- **Bug B — stale `:latest`.** `buildImages` skips a rebuild whenever the
  `:latest` tag already exists (`container.go:78,83`), so an image goes stale
  forever. Verified: `local/agent-sandbox-tools:latest` was a 9-day pre-pivot
  build (3.68GB; carried the now-removed default-on Python frameworks).

And the image surface is wrong for where the project went: the agent enum has a
duplicated `claude`/`claude-code`, a generic `shell` that `session create`
rejects (`cli.go:378` allows only `pi`/`claude`), no first-class `fish`/`zsh`, and
the tools image bakes in three default-on Python agent frameworks
(crewai/pydantic-ai/ag2 — the 3.68GB bulk) nobody in the claude/pi/fish/zsh world
needs.

## What we're building

A **parametrized matrix of minimalist, lazily-built images**:

```
agent {claude, pi, fish, zsh}  ×  substrate {container, vm}  ×  tool-layers {uv, pnpm, bunx, mise}
```

Each cell is a *recipe*. A recipe has a content-hash **identity**; its image is
built **lazily** (only when first needed and absent), **minimally** (slim,
digest-pinned bases; tool layers behind build-arg toggles + BuildKit cache
mounts), tagged by id (never `:latest`), labelled for **reaping** and **GC**, and
the terminal is correct at every boundary.

## Design decisions (do not re-litigate)

- **D1 — agent surface.** Canonical agent is `claude`. `claude-code` stays in the
  `#Agent` enum as a deprecated *accepted alias* (v1 JSON contract;
  `NormalizeAgent` already maps it) but is **dropped from every UI surface**. Add
  `fish` and `zsh` first-class. `policy.IsLaunchableAgent(name)` (input already
  normalized) is the shared session-create allowlist `{claude, pi, fish, zsh}`.
  The generic `shell` is intentionally **not** launchable: it stays a profile-only
  value (mapped by `agentArgv` for `safeslop run`, but rejected by
  `session create`) — this preserves `TestSessionCreateRejectsUnsupportedAgentAsContract`,
  which is also the `AGENT_UNSUPPORTED` golden's trigger. `fish`/`zsh` supersede it.
- **D2 — bases.** `node:22-bookworm-slim@sha256:…` for the node agents
  (`claude`, `pi`); `debian:bookworm-slim@sha256:…` for `fish`/`zsh`. Chosen over
  Wolfi: zero new tooling, glibc, `xterm-256color` terminfo already present,
  digest-pinnable so the recipe id is reproducible offline.
- **D3 — no `docker buildx bake`.** nerdctl/lima lacks it; bake would break the
  `runtime.Engine` seam. Build each image with `runEngine(... "build" ...)`.
- **D4 — recipe identity.** `recipeID` = short sha256 over the canonicalized build
  inputs (Dockerfile bytes + sorted build-args + base digest). Tag images
  `local/safeslop-<stage>:<id>`. `imageExists(<id-tag>)` becomes a *correct* skip:
  same inputs → same id → present → skip; changed inputs → new id → miss →
  rebuild. Kills Bug B. A per-id `withBuildLock(id)` flock (sibling of the
  existing `withRepoLock`) serializes concurrent builds of the same recipe.
- **D5 — label-based teardown (Ryuk pattern).** Compose services carry
  `safeslop.session=<id>` + `safeslop.managed=true`. `container.ReapBySession`
  (`ps -aq --filter label … | rm -f`) runs on `Stop`, on reconcile-when-PID-dead,
  on a startup sweep, and in `Down` — independent of the session record, so a
  record-less orphan is still reaped. Kills Bug A. `Session.Backend`
  (`"system"`|`"lima"`, additive) lets reap pick the right engine.
- **D6 — VM identity, not VM baking.** No per-recipe baked VMs (Tart LRU-prunes at
  100GB). One base VM image + `provisionToolchain` carrying the same `ID()` as a
  guest marker; re-provision is idempotent and skipped when the marker matches.
- **D7 — terminal.** Force `TERM=xterm-256color` + `COLORTERM=truecolor`
  (load-bearing for Ink/chalk) at *every* boundary; never export `LINES`/`COLUMNS`.
  `pty.Setsize` right after `pty.Open`. `ControllingTTY:true` on the detached
  container branch. Emacs front-end uses `eat` (optional, `term` fallback).
- **D8 — tool layers.** New `#Tool` enum + `#Profile.tools?: [...#Tool]`
  (orthogonal to the existing `#Toolchain` mise/nix field) + `policy.Profile.Tools`.
  `{uv, pnpm, bunx, mise}` are build-arg toggles with BuildKit cache mounts.
- **D9 — W0 byte-win is a flag flip, not deletion.** W0 flips
  `ENABLE_CREWAI/PYDANTICAI/AG2` defaults `true→false` (minimal, reversible; the
  3.68GB drops out of the default build). The full removal of those ARG/RUN blocks
  is folded into W4's Dockerfile rewrite. (Refines the handoff, which listed
  "delete" under W0.)

## Invariants / off-limits

- Do **not** reintroduce the macOS Seatbelt `sandbox` tier (removed in 0053).
  `environment` stays required with no default; keep honest `EnvTier` labels.
- Do **not** break the v1 JSON contract: `claude-code` stays an accepted alias;
  the `jsoncontract` golden fixtures (incl. `error-agent-unsupported.golden.json`)
  stay valid — the `AGENT_UNSUPPORTED` golden must keep firing via a *still*-
  unsupported agent (e.g. `opencode`), never via `fish`/`zsh`/`shell`.
- Do **not** change the squid egress-allowlist security model (the proxy is the
  only egress; `network: allow` vs `deny`).
- `library/layer/**` is the single source of truth for synced assets. After any
  edit to `Dockerfile.agent`, `Dockerfile.agent.tools`, `allowlist.domains`, or
  `agent-tools.env.example`, run `make sync-container-assets` so the embedded copy
  under `internal/engine/container/assets/` matches — `make check-assets` gates the
  drift. (schema.cue is **not** in the synced set; only the embedded
  `internal/engine/policy/schema/schema.cue` is authoritative.)

## Wave plan

Each wave is independently shippable and gated by `make check` + `make build`
(its own worktree). Order is by dependency: W1 realizes the byte win W0 sets up.

---

### W0 — agent surface + free-bytes (quick-win starter)

Goal: fix `AGENT_UNSUPPORTED` for shells, add `fish`/`zsh`, drop `claude-code`
from the UI, and set up the 3.68GB byte-win (realized once W1 lets the image
rebuild).

Status: implemented and verified on branch `image-matrix` (`make check` +
`make build` green; 13 files, +107/-13).

- [ ] **Add `IsLaunchableAgent` to policy**
  FILE: `internal/engine/policy/policy.go`
  CHANGE: after `NormalizeAgent` (ends line 156), add
  `func IsLaunchableAgent(name string) bool { switch name { case "claude", "pi", "fish", "zsh": return true }; return false }`.
  Doc it: input is the already-normalized canonical name; `shell` is deliberately
  excluded (profile-only, not session-creatable).
  VERIFY: `cd .worktrees/image-matrix && go build ./internal/engine/policy/`
  EXPECTED: exit 0.

- [ ] **Unit-test `IsLaunchableAgent`**
  FILE: `internal/engine/policy/policy_test.go` (add a test func; create the file
  only if absent)
  CHANGE: assert true for `claude, pi, fish, zsh`; false for `shell, claude-code,
  cursor, notanagent, ""` (caller normalizes `claude-code`→`claude` *before*
  calling). Do NOT use the `opencode`/`vscode` literals — `ci/pivot-denylist.sh`
  forbids them outside its two exempt test files.
  VERIFY: `go test ./internal/engine/policy/ -run IsLaunchableAgent -v`
  EXPECTED: PASS.

- [ ] **Add `fish`/`zsh` to the schema enum (keep `claude-code`, `shell`)**
  FILE: `internal/engine/policy/schema/schema.cue` (line 19)
  CHANGE: `#Agent: "claude" | "claude-code" | "shell" | "pi" | "fish" | "zsh"`.
  Leave the `claude-code` alias comment (lines 17-18) intact.
  VERIFY: `go test ./internal/engine/policy/ -run 'Load|Schema' -v`
  EXPECTED: PASS (schema still compiles/validates).

- [ ] **Replace the narrow allowlist at the session-create reject site**
  FILE: `internal/cli/cli.go` (lines 377-380)
  CHANGE: replace `if canonicalAgent != "pi" && canonicalAgent != "claude" {` with
  `if !policy.IsLaunchableAgent(canonicalAgent) {`. Leave the `emitContractError`
  body unchanged.
  VERIFY: `go build ./internal/cli/`
  EXPECTED: exit 0.

- [ ] **Add `fish`/`zsh` cases to `agentArgv`**
  FILE: `internal/cli/cli.go` (`agentArgv`, lines 1582-1610)
  CHANGE: add `case "fish": return []string{"fish"}, nil` and
  `case "zsh": return []string{"zsh"}, nil` before `default`. (Guest PATH resolves
  them: container has `fish`+`zsh` after the Dockerfile task below; the VM guest
  already runs `zsh -lc`. Host tier resolves via `resolveHostBinary`.)
  VERIFY: `go build ./internal/cli/`
  EXPECTED: exit 0.

- [ ] **Add `fish`/`zsh` to `seedAgentDefaults` no-op case**
  FILE: `internal/cli/agentseed.go` (line 22)
  CHANGE: `case "pi", "shell", "fish", "zsh", "":` (only `claude` seeds defaults).
  VERIFY: `go test ./internal/cli/ -run SeedAgentDefaults -v`
  EXPECTED: PASS.

- [ ] **Extend agentseed tests for `fish`/`zsh`**
  FILE: `internal/cli/agentseed_test.go`
  CHANGE: add cases asserting `seedAgentDefaults(Profile{Agent:"fish"}, ws)` and
  `…"zsh"…` return nil and write nothing. Keep the existing `opencode`/`vscode`
  rejection cases (they stay unsupported).
  VERIFY: `go test ./internal/cli/ -run SeedAgentDefaults -v`
  EXPECTED: PASS.

- [ ] **Drop `claude-code` from the CLI UI; advertise the new set**
  FILE: `internal/cli/cli.go` (lines 370 + 421)
  CHANGE: line 370 `Use:` → `create --agent <claude|pi|fish|zsh> --environment <host|container|vm> --workspace <dir> --output json`.
  Line 421 flag help → `"agent to run: claude, pi, fish, or zsh"`.
  VERIFY: `./safeslop session create --help 2>&1 | grep -q 'claude, pi, fish, or zsh'`
  (after `go build`) — and `! ./safeslop session create --help 2>&1 | grep -q claude-code`.
  EXPECTED: both exit 0.

- [ ] **Drop `claude-code` from the Emacs agent picker; add `fish`/`zsh`**
  FILE: `emacs/safeslop-session.el` (line 54)
  CHANGE: `'("claude" "pi" "fish" "zsh")` (was `'("claude" "claude-code" "pi")`),
  default stays `"claude"`.
  VERIFY: `grep -q '("claude" "pi" "fish" "zsh")' emacs/safeslop-session.el && ! grep -q 'claude-code' emacs/safeslop-session.el`
  EXPECTED: exit 0.

- [ ] **Make `zsh` real in the container image**
  FILE: `library/layer/container/Dockerfile.agent` (apt-get list, lines 7-16)
  CHANGE: add `zsh \` to the `apt-get install` package list (fish already present).
  VERIFY: `grep -q 'zsh' library/layer/container/Dockerfile.agent`
  EXPECTED: exit 0.

- [ ] **Flip the three Python frameworks off by default (byte-win)**
  FILE: `library/layer/container/Dockerfile.agent.tools` (lines 5-7)
  CHANGE: `ENABLE_CREWAI=false`, `ENABLE_PYDANTICAI=false`, `ENABLE_AG2=false`.
  Leave the ARG/RUN blocks in place (full removal is W4).
  VERIFY: `grep -E 'ENABLE_(CREWAI|PYDANTICAI|AG2)=false' library/layer/container/Dockerfile.agent.tools | wc -l | grep -q 3`
  EXPECTED: exit 0.

- [ ] **Sync assets + gate drift**
  FILE: (generated) `internal/engine/container/assets/Dockerfile.agent*`
  CHANGE: run `make sync-container-assets`.
  VERIFY: `make check-assets`
  EXPECTED: no `drift:` output, exit 0.

- [ ] **W0 gate**
  VERIFY: `make check && make build`
  EXPECTED: exit 0 (Go ./… + Emacs ERT all green; binary builds).

Off-limits in W0: `container.go` build logic (W1), the proxy/squid model, the
`#Toolchain` field, `library/layer/policy/presets/*` (the crewai/ag2/pydantic
presets stay untouched — they no longer pre-bake but that is a later cleanup).

---

### W1 — recipe identity + build fix (realizes the W0 byte-win)

Goal: id-tagged, content-addressed images; correct skip; build lock. Bug B dies.

- [ ] **Add `recipeID` (content hash of build inputs)**
  FILE: `internal/engine/container/identity.go` (new)
  CHANGE: `func recipeID(dockerfile []byte, buildArgs map[string]string) string` —
  sha256 over `dockerfile` + sorted `k=v` build-args (one per line), return first
  12 hex chars. Pure; no engine calls.
  VERIFY: `go test ./internal/engine/container/ -run RecipeID -v`
  EXPECTED: PASS (add a deterministic test: same inputs → same id; changed arg →
  different id).

- [ ] **Tag images by id; delete the `:latest` consts**
  FILE: `internal/engine/container/container.go` (lines 14-17, `buildImages`)
  CHANGE: drop `baseTag`/`toolsTag`. In `buildImages`, compute
  `baseID := recipeID(baseDockerfileBytes, nil)` →
  `baseImg := "local/safeslop-base:"+baseID`, and
  `toolsID := recipeID(toolsDockerfileBytes, buildArgs)` (buildArgs include
  `BASE=`+baseImg so a base change propagates) → `toolsImg := "local/safeslop-tools:"+toolsID`.
  `imageExists(<id-tag>)` is now a correct skip.
  VERIFY: `go build ./internal/engine/container/`
  EXPECTED: exit 0.

- [ ] **Make the tools Dockerfile's base parametrizable**
  FILE: `library/layer/container/Dockerfile.agent.tools` (line 1) + sync
  CHANGE: `ARG BASE=local/safeslop-base` then `FROM ${BASE}` (so the id-tagged base
  feeds the tools build). Run `make sync-container-assets`.
  VERIFY: `make check-assets`
  EXPECTED: exit 0.

- [ ] **Add `withBuildLock(id, fn)`**
  FILE: `internal/engine/container/lock.go` (sibling of `withRepoLock`, line 11)
  CHANGE: flock `LOCK_EX` on `<stateDir>/build/<id>.lock` (create dir 0o700). After
  acquiring, `fn` re-checks `imageExists` before building (double-checked locking)
  so a queued second builder skips. Wrap each per-image build in `buildImages`.
  VERIFY: `go build ./internal/engine/container/`
  EXPECTED: exit 0.

- [ ] **Thread `AgentImage` into compose params**
  FILE: `internal/engine/container/compose.go` (composeParams) +
  `internal/engine/container/assets/compose.yml.tmpl` line 9
  CHANGE: add `AgentImage string` to composeParams; set it to `toolsImg`; template
  `image: {{.AgentImage}}` (was `local/agent-sandbox-tools:latest`).
  NOTE: `compose.yml.tmpl` is embedded-only — it is NOT in the Makefile `SYNCED`
  set and has NO `library/layer/container/` source copy (the library carries
  `docker-compose.yml`). Edit the embedded `assets/` file directly; no
  `make sync-container-assets`/`check-assets` step applies to it.
  VERIFY: `go test ./internal/engine/container/ -run Compose -v`
  EXPECTED: PASS.

- [ ] **W1 gate (live byte-win check)**
  VERIFY: `make check && make build`; then optionally
  `docker rmi local/agent-sandbox-tools:latest local/agent-sandbox:latest` and a
  real `safeslop run` to force a rebuild under the new tags.
  EXPECTED: new `local/safeslop-tools:<id>` image is materially smaller than 3.68GB
  (frameworks gone); a second run with no changes does **not** rebuild.

Off-limits: terminal env (W2), labels/reap (W3), the Dockerfile rewrite (W4).

---

### W2 — terminal correctness

Goal: 24-bit color + correct size at every boundary; no Finder/launchd
`TERM`-stripping surprises.

- [ ] **Force TERM/COLORTERM in the container (drop the conditional)**
  FILE: `internal/engine/container/assets/compose.yml.tmpl` (lines 21-22; embedded-only, no sync)
  CHANGE: replace `{{if .Term}}      TERM: {{.Term}}\n{{end}}` with unconditional
  `      TERM: xterm-256color\n      COLORTERM: truecolor`. Drop
  `composeParams.Term` + its `os.Getenv` source in `compose.go`.
  VERIFY: `grep -q 'COLORTERM: truecolor' internal/engine/container/assets/compose.yml.tmpl && go build ./internal/engine/container/`
  EXPECTED: exit 0.

- [ ] **Inject TERM/COLORTERM into the host child env**
  FILE: `internal/cli/childenv.go` (`childEnv`, line 55)
  CHANGE: after building the allowlisted env, set `TERM=xterm-256color` +
  `COLORTERM=truecolor` explicitly (override, not passthrough — `childenv.go:17`
  only *allows* them; under launchd they are absent). Never set `LINES`/`COLUMNS`.
  VERIFY: `go test ./internal/cli/ -run ChildEnv -v`
  EXPECTED: PASS (add an assertion that both are present even with an empty host
  env).

- [ ] **Export TERM/COLORTERM in the VM remote command**
  FILE: `internal/engine/vm/ssh.go` (`remoteAgentCmd`, line 44)
  CHANGE: prepend `export TERM=xterm-256color COLORTERM=truecolor; ` to the emitted
  `zsh -lc` string.
  VERIFY: `go test ./internal/engine/vm/ -run RemoteAgentCmd -v`
  EXPECTED: PASS.

- [ ] **Size the PTY at open**
  FILE: `internal/cli/supervise.go` (after `pty.Open()`, line 68)
  CHANGE: `_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})` immediately
  after Open (the resize handler at line 263 then corrects to the client size).
  VERIFY: `go build ./internal/cli/`
  EXPECTED: exit 0.

- [ ] **Give the detached container branch a controlling TTY**
  FILE: `internal/engine/container/launch.go` (line 203, `exec.RunInTerminal`)
  CHANGE: set `ControllingTTY: true` on the detached `exec.LaunchSpec` (the host
  branch already sets it).
  VERIFY: `go build ./internal/engine/container/ && go test ./internal/engine/container/ -run Launch -v`
  EXPECTED: exit 0 / PASS.

- [ ] **(Optional) Emacs `eat` terminal with `term` fallback**
  FILE: `emacs/safeslop-session.el` (terminal open path)
  CHANGE: `(require 'eat nil t)`; use `eat` with `eat-term-name "xterm-256color"`
  when available, else fall back to `term`. Never export `LINES`/`COLUMNS`.
  VERIFY: `make test-emacs`
  EXPECTED: `safeslop emacs ok`.

- [ ] **W2 gate**
  VERIFY: `make check && make build`
  EXPECTED: exit 0.

---

### W3 — reap + GC (Bug A dies)

Goal: record-independent teardown via labels; a real `safeslop gc`.

- [ ] **Label compose services for reap**
  FILE: `internal/engine/container/assets/compose.yml.tmpl` (proxy + agent services; embedded-only, no sync)
  CHANGE: add `labels:` with `safeslop.session: "{{.SessionID}}"` +
  `safeslop.managed: "true"` to both services; add `SessionID` to composeParams.
  VERIFY: `grep -c 'safeslop.session' internal/engine/container/assets/compose.yml.tmpl | grep -q 2`
  EXPECTED: exit 0.

- [ ] **Add `container.ReapBySession`**
  FILE: `internal/engine/container/container.go`
  CHANGE: `func ReapBySession(ctx, eng runtime.Engine, sessionID string) error` —
  `ps -aq --filter label=safeslop.session=<id>`, then `rm -f` the ids (no-op on
  empty); also `network ls --filter label=… | network rm`.
  VERIFY: `go test ./internal/engine/container/ -run Reap -v` (fake engine records
  the argv)
  EXPECTED: PASS.

- [ ] **Add `Session.Backend`**
  FILE: `internal/engine/session/session.go` (`Session` struct, line 29)
  CHANGE: add `Backend string` (`"system"`|`"lima"`, additive; default `"system"`
  on create). Round-trips through `store.go` JSON automatically.
  VERIFY: `go test ./internal/engine/session/ -v`
  EXPECTED: PASS.

- [ ] **Call `ReapBySession` on Stop / reconcile-dead / Down**
  FILE: `internal/engine/session/session.go` (`Stop` line 248; `reconcile` callers
  220/237) + `internal/cli/cli.go` Down path
  CHANGE: on `Stop` and when reconcile finds the PID dead, reap by session id via
  the backend's engine; wire `safeslop down` to also sweep all
  `safeslop.managed=true` containers.
  VERIFY: `go test ./internal/engine/session/ -run 'Stop|Reconcile' -v`
  EXPECTED: PASS.

- [ ] **Startup sweep of managed orphans**
  FILE: `internal/cli/cli.go` (run/up entry)
  CHANGE: on run, sweep containers labelled `safeslop.managed=true` whose
  `safeslop.session` has no live record (reap the record-less ones — the Bug A
  case).
  VERIFY: `go test ./internal/cli/ -run Sweep -v`
  EXPECTED: PASS.

- [ ] **Add `safeslop gc`**
  FILE: `internal/cli/cli.go` (new `cmdGc`) + `library/layer/container/*` LABEL
  CHANGE: add `LABEL safeslop.managed=true` to the Dockerfiles (so prune --filter
  works; sync). `gc [--until <age>] [--keep <N>]` →
  `image prune -f --filter label=safeslop.managed=true --filter until=<age>`, then
  keep only the N most-recent managed images (LRU via `image ls` CreatedAt).
  VERIFY: `./safeslop gc --help` and `go test ./internal/cli/ -run Gc -v`
  EXPECTED: command exists; PASS.

- [ ] **W3 gate**
  VERIFY: `make check && make build`
  EXPECTED: exit 0.

---

### W4 — minimal images + tool layers

Goal: one parametrized Dockerfile; slim digest-pinned bases; tool layers behind
toggles + cache mounts. Realizes the full minimal-image design.

- [ ] **Pin slim bases by digest**
  FILE: `library/layer/container/Dockerfile.agent` + sync
  CHANGE: `node:22-bookworm-slim@sha256:…` (claude/pi) / `debian:bookworm-slim@sha256:…`
  (fish/zsh) selected by an `ARG AGENT_FAMILY`. Keep `fish`,`zsh`,terminfo present.
  VERIFY: `grep -q 'bookworm-slim@sha256' library/layer/container/Dockerfile.agent && make check-assets`
  EXPECTED: exit 0.

- [ ] **Collapse to one multi-stage parametrized Dockerfile; remove the dead
  framework blocks**
  FILE: `library/layer/container/Dockerfile.agent` (+ retire `Dockerfile.agent.tools`
  or fold it in) + sync; update `buildImages` + Makefile `SYNCED`
  CHANGE: single multi-stage build; **delete** the crewai/pydantic-ai/ag2 ARG/RUN
  blocks (the W0 flag flip is now permanent). Update the `SYNCED` list + asset sync
  if a file is removed.
  VERIFY: `make check-assets && make build`
  EXPECTED: exit 0.

- [ ] **Tool layers as build-arg toggles + cache mounts**
  FILE: `library/layer/container/Dockerfile.agent` + sync
  CHANGE: `ENABLE_UV`, `ENABLE_PNPM`, `ENABLE_BUNX`, `ENABLE_MISE` ARGs, each a
  `RUN --mount=type=cache,target=…` guarded by `if [ "$ENABLE_X" = true ]`. Builds
  must set `DOCKER_BUILDKIT=1` (and the nerdctl equivalent).
  VERIFY: `grep -q 'mount=type=cache' library/layer/container/Dockerfile.agent`
  EXPECTED: exit 0.

- [ ] **`#Tool` enum + `#Profile.tools` + `policy.Profile.Tools`**
  FILE: `internal/engine/policy/schema/schema.cue` + `internal/engine/policy/policy.go`
  CHANGE: `#Tool: "uv" | "pnpm" | "bunx" | "mise"`; `tools?: [...#Tool]` on the
  profile; `Tools []string` on `policy.Profile`. Orthogonal to `#Toolchain`. Feed
  the toggles + the recipe id (W1) from `Tools`.
  VERIFY: `go test ./internal/engine/policy/ -run 'Tool|Load' -v`
  EXPECTED: PASS.

- [ ] **W4 gate**
  VERIFY: `make check && make build`
  EXPECTED: exit 0.

---

### W5 — VM recipe identity

Goal: VM substrate joins the matrix without baking per-recipe VMs.

- [ ] **`provisionToolchain` carries a recipe marker**
  FILE: `internal/engine/vm/vm.go` (`provisionToolchain`, line 181) +
  `internal/engine/vm/launch.go` (call site line 44)
  CHANGE: write a guest marker file keyed to the same `ID()`/recipe hash; skip
  re-provision when the marker matches (idempotent). No per-recipe baked VM images.
  VERIFY: `go test ./internal/engine/vm/ -run Provision -v`
  EXPECTED: PASS.

- [ ] **W5 gate**
  VERIFY: `make check && make build`
  EXPECTED: exit 0.

---

### W6 — front-end + chrome

Goal: drive the matrix from the Emacs cockpit.

- [ ] **Recipe picker (agent × substrate × tools)**
  FILE: `emacs/safeslop-session.el` (`safeslop-session-new`)
  CHANGE: prompt agent, substrate, and a multi-select of tools; pass through to
  `session create`.
  VERIFY: `make test-emacs`
  EXPECTED: `safeslop emacs ok`.

- [ ] **Recipe + Image columns in the portal**
  FILE: `emacs/safeslop-portal.el` (`tabulated-list-format`, line 369)
  CHANGE: add `Recipe` and `Image` columns sourced from the session JSON.
  VERIFY: `make test-emacs`
  EXPECTED: ok.

- [ ] **Async build-progress buffer**
  FILE: `emacs/safeslop-session.el`
  CHANGE: reuse the `make-process` async pattern from `safeslop-install.el` to
  stream a (slow) lazy build into a progress buffer.
  VERIFY: `make test-emacs`
  EXPECTED: ok.

- [ ] **W6 gate**
  VERIFY: `make check && make build`
  EXPECTED: exit 0.

---

## Execution notes

- Build each wave in a fresh worktree off the latest main (`.worktrees/<wave>`),
  atomic scoped commits (`feat|refactor|fix|docs(0054): …`), `make check` +
  `make build` before declaring done.
- W0→W1 ordering is load-bearing: W0 sets `ENABLE_*=false`, but the 3.68GB only
  leaves disk once W1 stops the stale-`:latest` skip and the image rebuilds under
  an id tag.
- The two-copy asset-sync constraint applies to every Dockerfile/compose/allowlist
  edit: edit `library/layer/container/**`, then `make sync-container-assets`,
  then `make check-assets`.
