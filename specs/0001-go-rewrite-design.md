# 0001 — `slop` Go rewrite: architecture design

Status: **approved** (design gate passed 2026-06-16)
Author: brainstormed with jojo, FLO-hardened (Gemini + Kimi cross-family evaluation)
Scope: strategic design for the whole program. Each sub-project (SP0–SP4) gets its
own implementation plan under `specs/`.

---

## 1. Why

Coworkers need to use `slop`, but the current stack blocks them:

- It is **fish-first** (19 scripts sourced as `conf.d` modules); coworkers are not on fish.
- It runs **Python via `uv`** and shells out to **`cue`**; both are package-manager installs
  that fail unreliably behind the corporate **Cloudflare WARP** TLS-intercepting proxy.

jojo holds an Apple Developer cert and can codesign/notarize. The fix is to ship a **single
signed Go binary** with zero runtime dependencies — no fish, no `uv`, no external `cue`.

Alongside the rewrite, four product changes (drip-fed during brainstorming):

1. **Scrap Radicle** support entirely.
2. **Promote `sandbox-exec`** (macOS Seatbelt) to a first-class isolation path.
3. **Restructure the README**: lead with the real use cases (Claude Code, or a sandboxed
   shell for `pnpm`/`uv`), move the capability matrix to the bottom as reference.
4. Add a **pnpm/npm registry-token helper** and **1Password CLI integration** for
   secrets and SSH.

## 2. What `slop` is

A macOS-focused toolkit for sandboxing coding agents. ~19K LOC today: fish CLI (~5.8K),
Python-via-`uv` (Textual TUI 1,580; orchestrator 1,380; CUE isolation compiler 923),
CUE policy (~760), fish tests (~5K). Fundamentally a **subprocess orchestrator**: it reads
a per-repo `slop.cue` policy and launches an agent under host / container / vm isolation
with **ephemeral credentials revoked on exit**, shelling out to `docker`, `cue`, `gh`,
`ssh`, `tart`, `sandbox-exec`, `op`, `git`, `claude`.

## 3. Locked decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language | **Go** | Embeds the CUE engine (`cuelang.org/go`) → the external `cue` binary disappears; single signed static binary immune to the WARP/uv problem; easiest read for a mixed team; the canonical language for a subprocess orchestrator. |
| Shape | **Engine library + thin CLI emitting JSON** | A later GUI for non-technical users drives the engine with zero engine logic of its own. |
| GUI | **Deferred, tech TBD; dual role** | Not only a launch portal but also a **safe bootstrapper/installer** for non-technical users — installs agent CLIs (Claude Code …), version/tool managers (mise, nyx), runtime deps (Docker/OrbStack, `op`), and slop itself, all pinned/verified via the repo's safe-install machinery. When needed: native SwiftUI `.app` driving the binary, or a Go/Wails app — both sign with the Apple cert; neither touches the engine. |
| Terminal TUI | **Dropped (2026-06-17)** | CLI-first covers coworkers + Claude-Code/shell launching; the *second* surface is the **GUI** (SP7), not a terminal TUI. Net direction: **CLI or GUI**. |
| Rollout | **Strangler — core path first** | Usable coworker binary fastest; old stack stays green during transition; Radicle is scrapped for free by never porting it. |
| Container env | **Ships in v1 (built last)** | sandbox-exec-only is an interim alpha; the first *stable* release has both boundaries (sandbox + container URL-allowlist). The old fish container path bridges jojo during transition. |

## 4. Verified facts (checked, not assumed)

- **CUE is a Go library** (`cuelang.org/go`). `//go:embed` the schema + presets,
  `cuecontext.New()`, `CompileString`/`load.Instances` (with an `Overlay` for the user's
  on-disk `slop.cue`), `Unify` + `Validate`, `Value.Decode` into Go structs. Friendly
  validation errors via `cue/errors.Details`. → embed the engine, delete the external
  `cue` dependency *and* the 923-line `isolation.py`. (Context7, cuelang docs.)
- **npm classic tokens are permanently dead** (revoked 2025-12-09). Only **granular access
  tokens** remain: `npm token create/list/revoke`, expiry (write tokens ≤ 90 days),
  stored in `.npmrc` as `_authToken`, revocable by id. → the pnpm helper mints a short-TTL
  granular token and revokes on exit, mirroring `slop-gh-key`. *Minting requires prior
  auth* (see §7.2). (npm docs, GitHub changelog.)
- **1Password CLI**: `op read` / `op inject` / `op run` with `op://vault/item/field`
  references; SSH via the **1Password SSH agent** socket (`SSH_AUTH_SOCK`). Caveat: the
  SSH-agent path needs the **desktop app running**; the `op` CLI alone covers
  `read`/`inject` of secrets. (1Password developer docs.)

## 5. FLO hardening (provenance)

The architecture was run through a right-sized FLO loop: Kimi K2.7 (Moonshot) and Gemini
3.1 Pro (Google) as independent cross-family evaluators, Opus as orchestrator. Scores hit
the ceiling (Gemini 10/10/10; Kimi ~7–8 on coverage, no major holes), so the loop stopped
after one generation + triangulation — the value was the *converging critique*, not more
generations. Both judges independently flagged the same items, now folded into §6–§8:

- **ctty is the #1 technical risk** → two explicit code paths + a spike (§6.2).
- **pnpm chicken-and-egg** → bootstrap credential from 1Password (§7.2).
- **1Password SSH-agent socket pass-through** into container/sandbox (§7.1).
- **Environment selection** made explicit (§6.3).
- CUE friendly-error formatting + schema-version/back-compat risk (§4, §9).

## 6. Target architecture

Single Go binary `slop` = reusable engine library + thin CLI.

```
cmd/slop/main.go            # cobra command tree → engine calls only
internal/engine/
  policy/      # embedded CUE: go:embed schema+presets; load user slop.cue via Overlay;
               #   Unify+Validate; Decode→structs; errors.Details for friendly errors.
               #   (replaces isolation.py + every `cue export` subprocess)
  orchestrator/# run lifecycle: provision → stage → launch → on-exit hooks; .slop/state.json
  isolation/   # CUE policy → adapter configs (.sb seatbelt, docker-compose, squid)
  sandbox/     # sandbox-exec (Seatbelt): FIRST-CLASS launch path
  container/   # docker compose + squid URL-allowlist
  creds/       # CredentialProvider interface + gh / forgejo / pnpm / onepassword
  secrets/     # 1Password resolver (op read/inject) + SSH-agent wiring
  exec/        # subprocess + ctty/PTY handoff (replaces _spawn_with_ctty)
  state/       # .slop/state.json (per-repo, gitignored)
schema/        # embedded CUE schema + presets (go:embed)
```

The CLI emits `--json` on every command so a future GUI needs no engine logic.

### 6.1 CUE embedded, not shelled (the central win)

`go:embed` the schema + presets into the binary; load the user's `slop.cue` from disk via
`load.Config.Overlay` so the embedded module and the real file unify in-process. Validate,
decode to typed structs. This kills the external `cue` binary dependency and deletes
`isolation.py` (923 LOC). Surface `cue/errors.Details` so validation errors read as nicely
as `cue vet` does today.

### 6.2 exec/ctty — the #1 risk (two paths + a spike)

Interactive children (claude, vi, `$SHELL`) must own the terminal foreground.

- **Direct host launch** (the headline): `os/exec` with
  `SysProcAttr{Setpgid: true, Foreground: true, Ctty: <fd>}`. Go's `exec_unix.go` performs
  the `tcsetpgrp` handoff internally, so this is the correct primitive — the child inherits
  the real tty; **no PTY needed**.
- **Container / `docker exec` interactive** (and any path where we wrap child I/O): allocate
  a **PTY** via `github.com/creack/pty`, proxy stdin/stdout, forward **SIGWINCH** and set
  raw mode.

**SP1 begins with a ctty spike** that proves the direct-host handoff on the target macOS
versions before any other SP1 work is built. The PTY path is the architectural fallback if
the `SysProcAttr` primitive proves insufficient on a given macOS release. A static guard
test (mirroring today's `tests/test_slop.fish` ctty guard) prevents regressions.

### 6.3 Environment selection

`slop.cue`'s existing `environment:` field selects `sandbox` | `container` | `vm`. **When
unspecified, the default is `sandbox`** (the promotion in requirement 3). `slop run` resolves
the environment, compiles the policy to that adapter, and launches.

## 7. New capabilities

### 7.1 1Password integration (the backbone)

- `slop.cue` may declare `secrets: { ENV_NAME: "op://vault/item/field" }`. At launch the
  engine materializes them via `op read` / `op inject` into the ephemeral stage + the agent
  env, **wiped on exit**, **values never logged**.
- **SSH/Git auth** is delivered as a per-run, repo-scoped **ephemeral deploy key** — the 1Password
  SSH agent socket is **never** passed across the boundary (FLO-decided 2026-06-18, superseding the
  earlier socket-pass-through design; rationale + scores in
  `specs/research/2026-06-18-ssh-auth-flo-decision.md`). The host mints the key
  (`slop-gh-key create-pair` under Touch-ID/1Password, or `op read` of a pre-provisioned scoped key
  via `OP_SERVICE_ACCOUNT_TOKEN` for headless/CI), **read-only by default**. Only the `0600` private
  key + a pinned `known_hosts` are staged into the per-run stage, exposed via
  `GIT_SSH_COMMAND="ssh -i <key> -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=yes -o UserKnownHostsFile=<kh>"`
  at the boundary-correct path (real stage in sandbox, `/slop/runtime` in container,
  `~/.slop-runtime` in the disposable vm — the same per-env channel as `KUBECONFIG`/`.npmrc`).
  `write:true` is opt-in and lint-gated on `network:deny` + a forge-only egress allowlist (an
  exfiltrated write key is then useless off-host); TTL ≤ 60m. There is **no raw-socket tier and no
  in-boundary signing broker** — a caged key file is a strictly smaller attack surface than a live
  signing oracle. Teardown is decay-first across three independent layers (best-effort on-exit
  revoke, stage wipe / vm teardown, host-side reaper), none relied on alone. Honest residual: a
  compromise can reuse the key for git ops to that **one repo** until decay — bounded blast radius,
  not single-use.
- `slop doctor` reports `op` presence + signed-in state, and (for the SSH path) the key's
  read-only/write flag, resolved TTL, and — for write profiles — that the egress allowlist is
  forge-only.

### 7.2 npm/pnpm registry-token helper

A `CredentialProvider` (not a pnpm feature — pnpm has no minting command; this drives npm's
registry token API / `npm token`):

- **Bootstrap auth** (resolves the chicken-and-egg both judges flagged): the credential
  needed to *mint* comes from **1Password** (`op read` of a stored npm token) or a one-time
  `npm login`. This is why 1Password is the backbone.
- **Provision**: mint a short-TTL granular token scoped to the needed registry/packages.
- **Stage**: write a scoped `.npmrc` (`//registry.npmjs.org/:_authToken=…`; also supports
  GitHub Packages / private registries) into the ephemeral stage.
- **Revoke**: revoke the token by id and wipe the `.npmrc` on exit.

Net: a sandboxed `pnpm install` against a private registry works without ever exposing the
user's permanent token to the agent.

### 7.3 gh + forgejo

Ported to the same `CredentialProvider` interface — cleaner than today's special-cased
Python branches.

### 7.4 CredentialProvider interface

```go
type Provider interface {
    Provision(ctx context.Context, p Profile) (Snapshot, error) // create ephemeral creds, capture ids
    Stage(ctx context.Context, s Snapshot, stageDir string) error // write artifacts (ssh config, .npmrc)
    Revoke(ctx context.Context, s Snapshot) error                 // tear down by captured ids
}
```

A registry of providers replaces today's per-family `if cred == ...` branches in
`slop_orchestrator.py`. `Snapshot` is persisted in `.slop/state.json` so on-exit revoke can
target creds by captured id.

### 7.5 AWS / GCP cloud credentials — decay-first (specs/0009)

The biggest gap for the target user (a Rust/TS/Java startup on AWS/GCP), per the cross-model
research (`specs/research/2026-06-17-startup-usecase-prior-art.md`):

- **AWS** = IAM Identity Center / SSO. `credentials: {aws: {profile: "<sso-profile>"}}`.
  `aws configure export-credentials --profile P --format process` resolves SSO → short-lived role
  creds; staged as the standard `AWS_ACCESS_KEY_ID`/`SECRET`/`SESSION_TOKEN` env vars (env, not a
  `~/.aws/credentials` file — the same values then work uniformly in host/sandbox/container/vm with
  no path remapping, riding the existing `secretEnv` channel out of `docker inspect`/`ps`).
- **GCP** = Application Default Credentials. `credentials: {gcp: {}}`. `gcloud auth
  application-default print-access-token` mints a short-lived access token, delivered only via
  `CLOUDSDK_AUTH_ACCESS_TOKEN` (the long-lived `refresh_token` is never read or written).
- **Decay-first** (the load-bearing lesson): short TTL is the *primary* control; there is **no
  revoke step**, because `SIGKILL`/force-quit can skip on-exit hooks. Cleanup = the `stageDir` wipe.
- The host's `~/.aws/credentials` / `~/.config/gcloud` **never** cross a boundary (pinned by a
  no-host-cloud-config-mount test); `slop doctor` reports `aws`/`gcloud`.
- A keyless **federation/OIDC** provider shape (AWS OIDC role-assumption, GCP Workload Identity
  Federation) is reserved for later — the best ephemeral cred is one that never exists as a file.
- **kubectl** (EKS/GKE) is implemented as a composing provider (specs/0010): the host pre-mints a
  short-lived k8s bearer token (`aws eks get-token` / `gke-gcloud-auth-plugin`) and stages a scoped
  one-cluster kubeconfig (token inside, `0600`), so the agent's `kubectl` needs neither cloud creds
  nor the cloud CLI inside the boundary. `KUBECONFIG` rides the per-environment path channel like
  `.npmrc` (host path for host/sandbox; the `/slop/runtime` bind mount for container); `slop doctor`
  reports `gke-gcloud-auth-plugin`. vm support is deferred behind a guard.

## 8. Distribution, CLI, tests

- **Build/sign**: `CGO_ENABLED=0` static binary, cross-compiled `darwin/arm64` + `darwin/amd64`
  (+ `linux` for CI/containers). `codesign` → `notarytool submit --wait` → `xcrun stapler
  staple`. Ship via a **Homebrew tap** + GitHub Releases. One signed file, no fish/uv/cue →
  this *is* the WARP fix.
- **CLI**: cobra owns `--help`. `slop gen-docs` regenerates the README command reference
  (replaces `slop-sync-help`). The pinning gate (`slop-pinning`) is reimplemented for
  `go.mod` + Docker base images + npm CLI versions.
- **Tests**: Go `testing` for engine units; `github.com/rogpeppe/go-internal/testscript`
  for CLI-level tests (close in spirit to today's fish tests); the existing `.sb`/compose
  fixtures become **golden files**; **no live API calls** in credential tests (today's rule).

## 9. Risks & mitigations

| Risk | Mitigation |
|---|---|
| **ctty/foreground handoff parity on macOS** (both judges' #1) | SP1 starts with a spike; `SysProcAttr` primitive for direct launch, `creack/pty` fallback for wrapped/container; static guard test. |
| CUE embedding edge cases; schema vs user-file version skew | Embed a `schemaVersion` field; `slop validate` gates; golden-file tests over the existing fixtures; `errors.Details` for legibility. |
| sandbox-exec genuine limits (Apple-deprecated; coarse network) | Honest README caveat; container (squid URL-allowlist) ships in v1 for real network control; VM for highest-risk. |
| 1Password SSH-agent needs the desktop app | `slop doctor` detects + reports; `op read`/`inject` secrets path works CLI-only as the fallback. |
| codesign/notarization friction | Scripted, pinned pipeline; notarize in CI; document the cert/Team-ID setup once. |
| Throwaway work on the old stack during transition | SP0 cleanup is cheap and keeps the old stack green; nothing else is invested in fish/python. |

## 10. Decomposition (program)

Each sub-project = its own `specs/` plan → implementation → review.

| | Sub-project | Contents | Release |
|---|---|---|---|
| **SP0** | Decommission Radicle + doc restructure | Delete Radicle (5 files + surgical edits, fully mapped); README: capability matrix → bottom, lead with Claude-Code/shell, reframe sandbox-exec first-class. Language-agnostic; old stack stays green. | v1 |
| **SP1** | Go engine foundation + headline launch | module skeleton, embedded CUE, **exec/ctty spike first**, `slop run/validate/list/down/doctor`, **sandbox environment (default)**, launch claude-code + shell, JSON output, signed build + notarize, testscript + unit tests, rewrite CLAUDE/AGENTS/CONVENTIONS for the Go stack. | v1 |
| **SP2** | Credential providers in Go | `CredentialProvider` interface + gh + forgejo + **pnpm-token** + **1Password** + secrets resolver + stage/wipe lifecycle. | v1 |
| **SP3** | Container environment | docker compose + squid URL-allowlist ported to Go (the real network boundary). Built last but **in v1**. | v1 |
| **SP4** | tart VM environment | Disposable Tart VM launch path ported to Go (`environment: vm`). | later |
| **SP5** | nyx (Nix) + mise toolchains | A `toolchain:` concept in `slop.cue` (`nix` \| `mise` \| `none`), orthogonal to `environment`, that provisions pinned tools into the chosen environment and can launch mise tasks / Nix flake apps under isolation. **nyx** = Nix flakes/NixOS (pinned flake inputs = the safe-install story); **mise** = version manager + task runner (`mise.toml`, `mise run`/`mise exec`). | later |
| ~~**SP6**~~ | ~~terminal TUI~~ — **dropped** | Bubbletea terminal-TUI port **cut 2026-06-17**: the product is **CLI + GUI**. The interactive surface for non-CLI users is the SP7 GUI portal, not a terminal menu hub. | — |
| **SP7** | GUI — portal + safe installer | Drives the engine's JSON API; native SwiftUI `.app` or Go/Wails (decided then). Two roles: **(a) launch portal** for profiles; **(b) safe bootstrapper/installer** for non-technical users — installs agent CLIs (Claude Code …), version managers (mise, nyx), runtime deps (Docker/OrbStack, `op`), and slop itself, each pinned/verified and sandbox/VM-evaluated using the repo's safe-install machinery (so a non-technical user can go from zero to a working sandboxed setup). | later |
| **SP8** | niche adapters + own egress filter | envoy / coredns / pf isolation adapters, and **our own macOS NetworkExtension egress filter à-la-LuLu** (`NEFilterDataProvider`, per-process/per-domain egress decided at the host kernel boundary — the successor to the squid allowlist, and the way to extend enforced egress to the `host`/`sandbox` environments, not just `container`). | later |

## 11. Execution order

`SP0 → SP1 → SP2 → SP3 → SP4 → SP5 → SP7 → SP8` (**SP6 dropped 2026-06-17**).

SP0–SP5 are **complete**: SP0 (`specs/0002`, PR #1), SP1 (`specs/0003`, PR #2), SP2
(`specs/0004`, PR #3), SP3 (`specs/0005`, container — network-enforced squid + leak-free
secrets), SP4 (`specs/0006`, disposable Tart VM `environment: vm`), SP5 (`specs/0007`, the
`toolchain:` concept — mise/nix across all environments, nix-in-container deferred).
SP6 (terminal TUI) was **dropped on 2026-06-17**: the direction is **CLI or GUI**, so the
second surface is the **SP7 GUI portal + safe installer**, not a Bubbletea terminal TUI. SP7
(added 2026-06-16 at the user's request) is now the next artifact.
