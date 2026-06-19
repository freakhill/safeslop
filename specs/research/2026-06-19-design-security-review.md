# 2026-06-19 — safeslop design security review (cross-model `ayo`, verified)

A *refresh* of the same-day promise-vs-pain pass (`2026-06-19-design-promise-vs-pain.md`),
re-framed as a pure **architectural security review**: *is the design itself vulnerable?* —
plus a goals-vs-delivery refresh against current `main`.

What makes this pass different from the earlier one: every high-severity claim was
**checked against live code and, where cheap, reproduced empirically** on this host. Three
external-lane claims were *refuted* by that step; two were *confirmed* in code. Findings carry
a **STATUS** = `VERIFIED-OPEN` / `VERIFIED-FIXED` / `REFUTED` / `UNVERIFIED` so the next session
acts on facts, not lane opinion.

Provenance tags: **[C]** cross-family consensus · **[G]** Gemini-unique · **[Z]** GLM(Zhipu)-unique
· **[H]** Host-unique/empirical.

---

## Headline (load-bearing)

1. **The architecture's single load-bearing wall is the gRPC control socket, and right now it
   is drywall. [C, VERIFIED-OPEN]** Three independent facts compose into one reachable
   sandbox-escape: (a) the gRPC `Launch` path is **not** trust-gated (only the CLI `run` path
   got specs/0022); (b) peer auth is **uid-only** `LOCAL_PEERCRED` (`peerauth.go:33`),
   codesign deferred; (c) a `network:allow` sandbox **can reach** `~/.safeslop/s.sock`
   (empirically confirmed; `network:deny` cannot). So a network-enabled sandboxed agent — or
   any same-uid host malware — can rewrite `safeslop.cue` to `environment:"host"`, `connect()`
   the socket, call `Launch`, and step *out* of the cage with secrets. Every "isolated" tier
   that allows network is **one Launch call from `host`.** This is the canonical confused
   deputy (Hardy 1988) that Chrome's broker and macOS XPC both solve by verifying the peer's
   *audit token + code signature*, not its uid.

2. **The sandbox inherits the full host environment unscrubbed — so "host's permanent creds
   never cross the boundary" is false for the two tiers people actually use. [C, VERIFIED-OPEN]**
   `run` and cockpit both launch with `append(append(os.Environ(), secretEnv...), pathEnv...)`
   (`cli.go:461/469` and runProfile `:33-37`). `AWS_*`, `OP_SESSION_*`, `GITHUB_TOKEN`,
   `SSH_AUTH_SOCK`, `HISTFILE`, anything exported in the user's shell — all ride into the
   `sandbox` and `host` tiers. (Container/vm are clean: they pass only `secretEnv`.) The
   ephemeral-credential machinery is bypassed by ambient authority sitting one `os.Environ()`
   away. Capability systems' first law is *no ambient authority*; safeslop ships with the
   host's intact.

3. **Goals-vs-delivery is now mostly honest, but two product claims still outrun delivery:**
   *decay* is sold as exfil protection (it bounds retention, not the first outbound request),
   and the *default sandbox + network:allow* combination is the path of least resistance and
   has no egress control at all. The earlier pass's tier-labeling fix (H1, specs/0023) and the
   CLI trust gate (H2, specs/0022) are real, landed progress — but both stop exactly at the
   GUI/gRPC boundary, which is the surface Audience A lives on.

The corrections from verification matter as much as the findings:
- **File confinement actually works.** A first hypothesis — that the profile is allow-by-default
  because it has no `(deny default)` — was **refuted**: `system.sb` + sandbox-exec establish a
  deny baseline; out-of-workspace reads *and* writes are denied (tested). Don't "fix" this.
- **The clipboard/Mach exfil finding [G+Z] is REFUTED.** `pbpaste` under the default profile
  returned nothing — `system.sb` denies the pasteboard mach service. `(allow signal (target
  self))` likewise blocks the sandboxed agent from `kill`-ing its parent to skip teardown. The
  Mach-IPC class is far more mitigated than the lanes assumed; only `host` tier is exposed.

---

## Triaged findings

### HIGH — verified, act on these

**S1. Gate the gRPC `Launch`/`OpenSession` path on the same trust check, and verify the peer's
code signature, not its uid. [C, VERIFIED-OPEN]** · Surface 6+4 · Q1 crit
- EVIDENCE: `grep trust. internal/engine/control/` → nothing. `peerauth.go:33` rejects only
  cross-uid. Socket reachable from `network:allow` sandbox (tested). Chrome broker + XPC verify
  `audit_token` → `SecCodeCheckValidity`; the earlier pass already noted this is doable
  **CGO-free** via a `csops`/`codesign` shellout, so the "needs Security.framework/CGO"
  deferral reason (`peerauth.go:31`) is itself questionable.
- DO: (a) call `trust.Store.Check` in the `Launch` handler (the deferred fast-follow in
  specs/0022 — promote it to *now*, it is the GUI's only integrity gate); (b) audit-token +
  codesign peer verification; (c) **hard-refuse** any peer whose pid is inside a safeslop-spawned
  process tree / sandbox (defense in depth even if network:deny already blocks the socket).
- **REALIZED (specs/0024):** S1a trust-gates `resolveSession`/`OpenSession` (fail-closed, same
  `enforceTrust` as CLI `run`); S1b refuses peers inside a safeslop-spawned process tree
  (`LOCAL_PEERPID` + `kern.proc.pid` parent-walk, strict-descendant). **Correction:** the `Launch`
  RPC was *already* transitively gated — `launchProfile` shells out to `safeslop run`, which gates —
  so `OpenSession` was the only exposed launch path. Remaining follow-on: codesign/audit-token peer
  verification (closes arbitrary same-uid host malware that is *not* a descendant).

**S2. Scrub the child environment to an allowlist before entering `sandbox`/`host`. [C,
VERIFIED-OPEN]** · Surface 1+3 · Q1 crit
- EVIDENCE: `cli.go:461/469`, runProfile `:33/:37` pass `os.Environ()` through. No
  `cleanEnv`/scrub anywhere in `internal/`. `SSH_AUTH_SOCK` passthrough is the sharpest case —
  under `host` or `network:allow` the agent drives the host's real `ssh-agent` (the very
  raw-socket tier the SSH FLO decision banned, reintroduced by inheritance).
- DO: build the child env from `taggingEnv` + `secretEnv` + `pathEnv` + a small allowlist
  (`PATH`, `HOME`→stage, `TERM`, `LANG`), never the parent's. This is the highest
  value-per-line fix in the review.
- **REALIZED (specs/0024):** `childEnv` strict allowlist — the `sandbox`/`host` children no longer
  inherit `os.Environ()`; ambient `AWS_*`/`OP_SESSION`/`SSH_AUTH_SOCK`/`GITHUB_TOKEN`/`ANTHROPIC_API_KEY`
  are dropped. Agents declare any needed key in `secrets:` (jojo chose the strict, no-carve variant).

**S3. Exclude `.git` (and other host-auto-executed paths) from the agent-writable mount, or the
repo write *is* host code-exec. [G, VERIFIED reachable]** · Surface 1 · Q1 high
- EVIDENCE: sandbox makes the whole workspace `file-write*` (`sandbox.go:73`); workspace =
  repo root, so `.git/hooks/*`, `.git/config` (`core.hooksPath`, `core.fsmonitor`, aliases),
  `.envrc`, `.vscode/tasks.json` are all writable. The host later runs `git commit`/opens the
  editor *outside* the cage and executes them. Classic devcontainer escape; applies to **all
  tiers** (the repo is writable in every one). The agent must write code (the point), so the
  defense is path-class, not blanket-RO.
- DO: mount `.git` read-only (or via a worktree whose `.git` lives outside the writable tree);
  document the `direnv`/editor-task class; consider a commit shim. At minimum, surface it
  loudly — it defeats the "guards mistakes" claim when a *mistake* the agent makes is letting
  injected content write `.git/hooks`.
- **PARTIALLY REALIZED (specs/0025):** detect-and-warn shipped — `internal/engine/gitguard`
  fingerprints `.git/config` + executable `.git/hooks` before launch and `runProfile` warns on
  exit if the exec-surface changed (all tiers, non-breaking). *Prevention* (worktree-isolated
  gitdir or sandbox `.git/hooks` write-deny) and the cockpit-path wiring + `.envrc`/editor-task
  class remain follow-ons.

**S4. Treat the squid allowlist as a parser surface and label "network-enforced" honestly. [C,
UNVERIFIED — needs the squid config audit]** · Surface 2 · Q1 high
- EVIDENCE: both lanes list the standard bypass classes — `CONNECT host:port` to arbitrary
  ports, SNI/Host vs routed-IP asymmetry (domain fronting without TLS termination),
  percent-encoded/back-slash path tricks, IDN homograph, and **DNS rebinding** (first A inside
  allowlist, second inside container). TUF's lesson: the boundary is the path from name to
  bytes, not the URL string.
- DO: audit `library/.../squid` (and the Go port): deny `CONNECT` by default, pin DNS, match on
  `(normalized-host, port)` via a real parser, terminate TLS or accept that it's SNI-trust. Keep
  it (real value vs `curl|sh` + accidental beaconing) but the "network-enforced" tier label
  should read "egress-allowlisted (SNI-trust)" until bypasses are closed.

**S5. Build the child env / stage secrets so the daemon never *holds* reusable secret material,
and so same-uid `ps`/`docker inspect` can't read it. [C, partial by design]** · Surface 3 · Q1 high
- EVIDENCE: secrets are delivered as env (`secretEnv`), which the design (0001 §7.5) knowingly
  routes "out of docker inspect/ps". Same-uid `ps eww`/`docker inspect` reads it; a child daemon
  the agent spawns keeps the staged file open across the on-exit wipe. The bigger one: if any
  gRPC method can trigger `op read` on an attacker-supplied item ref, or read the staged file
  before sandbox entry, decay-first collapses to "any same-uid peer reads any secret" — which is
  exactly why S1's peer check is load-bearing.
- DO: prefer a short-lived `credential_process`/file the SDK calls over raw env (the earlier
  pass's H5 actionable 5); `FD_CLOEXEC` + explicit FD scrub on every host-only fd before spawn
  ([G] fd-leak class); never return secret bytes over the socket.

### MEDIUM — actionable, secondary, or needs design

- **M1. Refresh-token reachability undoes "no permanent cred crosses." [G+Z, partial]** STS/ADP
  short creds are minted from a 90-day SSO refresh token that stays host-side and reachable; for
  Audience A's zero-config it may be cached. "Generator reachable" = standing privilege. Keep the
  refresh token in a separate per-launch helper that exits, never the daemon's address space.
  Surface 3, Q1.
- **M2. Socket-squatting: agent (or any same-uid proc) `rm ~/.safeslop/s.sock` and binds its own
  listener; the GUI then talks to the agent. [G, VERIFIED-OPEN for host tier / mitigated in
  sandbox]** The sandbox can't write `~/.safeslop` (path not allowed), but `host` tier and any
  same-uid process can. The GUI must verify the *server's* identity too (mutual), and the engine
  should refuse to start if the socket path is attacker-writable. Surface 6, Q1.
- **M3. Installer verify→move TOCTOU + `mise install` is repo-controlled supply-chain ingress.
  [G+Z, UNVERIFIED]** safeslop pins the *installer* (mise/tart, sha256+minisign) but `mise`
  then installs *repo-`mise.toml`-controlled* toolchain versions from un-end-to-end-signed
  upstreams — the pin covers the top of the closure, not the closure (TUF's whole point). And a
  download→verify→`mv` window lets a same-uid proc swap the binary post-verify (use atomic
  rename in a protected dir / `fexecve`). Surface 5, Q1.
- **M4. WARP is not an exfil wall; SSH/DNS/split-tunnel/AF_UNIX bypass it. [G+Z, Q2 honesty]**
  WARP intercepts public HTTPS; git-over-SSH, DNS TXT tunneling, RFC-1918 split-tunnel routes,
  and local sockets all bypass it. Don't let docs imply WARP makes exfil impossible — "DLP on
  public HTTPS to corp-monitored destinations" is the honest claim. Surface 7, Q2.
- **M5. Don't trust the WARP CA *inside* the agent's TLS store. [Z, UNVERIFIED]** The keychain
  CA needed so `safeslop`'s own downloads work (a real point in the single-binary's favor) must
  not become a MITM root the agent can append its own CA to, or use to strip tokens from its own
  subprocesses. Carry safeslop's installer pin set independent of the system keychain. Surface 7+5.
- **M6. CUE "billion-laughs" DoS on the trust-dialog parse. [G, UNVERIFIED]** A repo-supplied
  `safeslop.cue` with pathological nesting could OOM the daemon when the GUI parses it to render
  the trust prompt — fail-closed *and* bounded (eval timeout / depth cap) before display. Also
  specify: failure to *positively resolve* a profile = refuse launch, never silent fallback to a
  permissive default. Surface 4, Q1.
- **M7. Tart "disposable" VM is snapshot/clone-persistable. [Z, UNVERIFIED]** A prompt-injected
  agent asks the user to "snapshot before this risky step"; the snapshot captures staged creds at
  full validity. Disposability holds only if the VM can't be saved with live creds — derive the
  disk key from a daemon-held per-session secret so a clone is unbootable. Surface 1(vm), Q1.
- **M8. GUI approval dialogs habituate to click-through for Audience A. [Z, Q2]** Permission-prompt
  literature is unambiguous; make the privileged actions (launch-unsandboxed, grant-write-creds)
  require a non-default action (TouchID / type the profile name — the earlier pass's M7), batch
  per-project not per-launch. Surface 6, Q2.

### REFUTED / corrected by verification (do NOT act)

- **Clipboard/Mach-IPC exfil [G+Z].** `pbpaste` blocked under the default profile; `system.sb`
  denies the pasteboard service and `(allow signal (target self))` blocks parent-kill. Only the
  `host` tier (no profile) is exposed. Down-graded from "high open" to "host-tier only."
- **"Seatbelt file-read is broadly permissive, secrets readable" [G].** Refuted on this host —
  out-of-workspace reads are denied. The honest "mistake-guard" label is *accurate* for fs.
- **"No `(deny default)` ⇒ allow-by-default" [H, self-refuted].** The empirical test killed this
  before it became a false report; the import-of-`system.sb` baseline is deny.

---

## What landed since the earlier pass (verified against `git log` + code)

- **H1 tier labels → SHIPPED** (specs/0023; `policy.EnvTier`, run/doctor banners, README table).
- **H2 policy integrity → SHIPPED for CLI `run`** (specs/0022; `~/.config/safeslop/trust.json`,
  fail-closed sha256). **Gap: gRPC `Launch` still ungated** — this is S1 above and is the whole
  ballgame now.
- **H6 installer trust chain → recorded** (notarized-pin chain documented; sha256+minisign
  verify landed). Upstream-maintainer-signature + freshness delay (H6/H8) still open (M3).
- Still open from the earlier pass: H3 (control-plane codesign — S1), H4 (WARP toolchain TLS),
  H5 (scope-first creds — M1/S5), H7 (strong zero-authoring default).

---

## Actionables (ranked)

1. **S1 — gate gRPC `Launch` on `trust.Check` + codesign/audit-token peer auth + refuse
   in-sandbox peers.** The one finding that, unfixed, makes every network tier escapable. → Surface 6+4.
2. **S2 — env allowlist for sandbox/host child launch.** Highest value-per-line; closes the
   ambient-authority leak. → Surface 1+3.
3. **S3 — `.git`-class read-only mount + document the host-auto-exec write class.** → Surface 1.
4. **S4 — squid bypass-class audit + honest "egress-allowlisted (SNI-trust)" label.** → Surface 2.
5. **S5/M1 — credential_process over env, FD_CLOEXEC scrub, refresh-token out of daemon memory.** → Surface 3.
6. **M2 — mutual socket identity + refuse attacker-writable socket path.** → Surface 6.
7. **M3 — installer atomic verify+place; bring `mise.toml`-driven installs under the trust
   surface or VM-only.** → Surface 5.
8. **M6 — bounded/fail-closed CUE eval; positive-resolution-or-refuse.** → Surface 4.
9. **M4/M5 — WARP honesty in docs; isolate the installer's trust store from the agent's.** → Surface 7.
10. **M7/M8 — VM clone-binding; non-default-action gating on privileged GUI verbs.** → Surfaces 1+6.

The FLO hand-off from the earlier pass (bounded write-creds for unattended agents vs read-only
default) still stands — S2's `SSH_AUTH_SOCK` scrub interacts with it (scrubbing the agent socket
*forces* the ephemeral-key path, which is the intended design).

---

## Net

safeslop's design is **sound at the tier model and honest at the labels** — the earlier pass's
reframes held up and the fixes (tiers, CLI trust gate, signed installer) are real. It is **not
security theatre** for its stated threat (agent *mistakes* + accidental exfil) in the default
`network:deny` sandbox, which genuinely confines the filesystem and can't reach its own jailer.
But the design **is vulnerable** at three verified points, all clustered at the boundary the GUI
audience uses: the **ungated, uid-authed control socket** (escape to `host` from any network
tier — the load-bearing hole), the **unscrubbed inherited environment** (ambient host creds in
the sandbox), and **repo-write-to-host-exec** via `.git`. None is structural; all three are
surgical. Fix S1 and S2 and the "isolation" claim becomes true instead of conditional; the rest
is hardening and honest labeling. The single sentence: **the cage is real, but it currently
ships with the key inside it (the socket) and the locks left open (the env).**

---

## Method footer

Cross-family `ayo`, refresh of the same-day promise-vs-pain pass. Lanes: **Host** (Anthropic,
Opus 4.8 — own mining + all empirical verification on the target host), **Gemini 3.1 Pro**
(Google, via `ai-router` OpenRouter, ZDR enforced), **GLM-5.1** (Zhipu, via the z.ai Coding Plan,
direct). **Kimi K2.7 (Moonshot) was unavailable** — `kimi_status` healthy but `kimi_analyze`
timed out on the whole-repo read; per session policy a timed-out Kimi is treated as down and not
retried (same outcome as the earlier pass). Lanes were blind (identical brief, none saw another's
output); the Host lane alone compiled, **verified against live code**, and triaged. Verification
commands run on `darwin` against real `sandbox-exec` (file confinement, socket reachability under
deny/allow, pbpaste/clipboard, env-construction code paths). Source of truth: `internal/engine/*`,
`internal/cli/cli.go` @ `main` (df40e28), `specs/0001`/`0008`/`0011`/`0022`/`0023`, and the
earlier `2026-06-19-design-promise-vs-pain.md`.
