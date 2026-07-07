# 0071 — Ergonomics review: FLO pipeline + Emacs cockpit (usability, learnability, scale)

**Status:** review **Date:** 2026-07-03
Scope: (1) the ayo→FLO decision pipeline as a *working ergonomic*, and (2) the Emacs
cockpit UI — learnability, day-to-day usability, and whether it scales to a ~100-repo
shop with front-end and back-end developers. Source-grounded against `emacs/*.el`, the
example/preset policies, and the credential model. Companion to the security review
(`specs/0070`).

## TL;DR

The **cockpit UI is genuinely well-built** for the single-repo, few-profiles case:
non-blocking, color-redundant, honest about danger, and consistent across surfaces. It
**does not yet scale to 100 repos**, and the reason is structural, not cosmetic: a
profile carries no repo/workspace identity you can fan out, credentials are declared
per-profile-per-repo by hand in CUE, and the dashboards have no filter/search/group. The
FLO pipeline is powerful but has sharp operator edges (broken lanes, silent scoring
pitfalls) that live in tribal memory, not in the tool.

---

## Part A — FLO / ayo-flo pipeline ergonomics

**What works.** The Expansion→ayo→FLO structure produces auditable artifacts (the 0068
ayo note + decision note), the anti-sycophancy role split (worker ≠ scorer, cross-family)
is a real quality lever, and flag-only auditors catching a fatal the scorer missed (the
git-credential-store collision in 0068) is the method paying for itself.

**Sharp edges (all cost real time this month):**
1. **Broken lanes fail opaquely.** `flo-evaluator-glm` doesn't exist (only
   `ayo-research-glm` does); `kimi-thinking` and `ayo-research-kimi` fail identically on a
   228K-vs-102K token cap. These are discovered by dispatch failure mid-run, then
   remembered in a memory file — not surfaced by the tool. *Fix:* a preflight lane-registry
   check / `subagent --list-healthy` before a wave, and repair the kimi agent config.
2. **Same-family scoring footgun.** `flo-evaluator-opus` is forbidden as scorer when the
   worker is an inline Claude — a rule that lives only in memory. *Fix:* encode
   family-conflict rejection in the dispatch layer.
3. **Worker channel unreliability.** `flo-worker`'s tool channel echoes stale content on
   sustained work, so real build work must run inline — a large, undocumented caveat.
4. **Manual score arithmetic.** The host hand-computes the weighted total and folds fixes;
   defensible for irreversibility, but the rubric/weights/fix-forcing should be a scored
   template, not prose reconstructed each run.

**Learnability:** low without a guide. The skill files are strong but the *operational*
knowledge (which lanes are alive, scoring rules, when to re-evaluate vs. deterministic-fix)
is folk knowledge. A one-page "running a decision-FLO" runbook in `skills/` would move
this from expert-only to team-usable.

---

## Part B — Cockpit UI ergonomics

### Learnability — strong
Every surface renders its own shortcut legend, a tier legend, a net legend, and (portal)
a status legend, in-buffer — you learn the keymap by reading the buffer you're in
(`safeslop-surface--legend`, `--tier-legend`, `--net-legend`). `?` is `describe-mode`
everywhere; empty/error/loading states carry guidance, not silence
(`safeslop-surface--empty-state`/`--error-banner`). Three surfaces (Sessions **P**,
Profiles **F**, Credentials **K**) with uniform `[`/`]`/TAB cycling is a small, coherent
model.

### Responsiveness — strong (this is a real strength)
Every CLI call is async through one substrate (`safeslop--call-json-async`); the
synchronous path is reserved for tests and fast must-be-sync calls
(`safeslop-client.el`). The portal auto-refresh (5s) explicitly **skips a tick** while the
minibuffer is active, while input is pending, or while a prior fetch is in flight
(`safeslop-portal--auto-refresh`) — the "refresh fights my typing" failure is designed
out. The cursor-jump-on-refresh bug is fixed in one place (`safeslop-surface-render`
capture/restore). **The editor is not laggy** for the sizes it targets; the risk at 100
repos is different (below).

### Visual security legibility — strong on posture, has one real gap
Isolation tier and network posture are **color-redundant**: the word is always present and
color only reinforces it (`safeslop-surface--env-cell`/`--net-cell`, specs/0031), with
honest help-echo ("container + default-deny egress allowlist: stops curl|sh + accidental
beaconing, **not** exfil via an allowed domain"). `host` renders in the error face
(most dangerous), `container` in success. World-changing actions (run/launch/detach/stop)
gate behind a `yes-or-no-p` carrying the same danger summary on every surface
(`safeslop-surface--danger-summary`). Credentials surface is value-free by construction
and shows readiness (`resolvable`/`missing`/`ephemeral`/`ambient`) with color-redundant
status. **This is a genuinely good honest-danger UI.**

**The gap — which credentials a running buffer actually holds is not legible at a glance.**
The portal shows agent/env/net/status/workspace and a `credentials live|revoked` fact in
help-echo, but **not which repos/tokens are staged** into a given session. When an
operator has many live sessions, "which buffer is operating on which project with which
credentials" is answerable only by opening detail (`i`) per session. The terminal buffer
itself (`*safeslop-<id>*`) carries the opaque session id, not the project or the credential
scope (see "Day-to-day usability" below and "Visual recognition" in Part C).

### Day-to-day usability — good, with friction that grows with N
- Session id in the terminal buffer name is `*safeslop-sess-23b5…*` — opaque. Rename exists
  (`N`) but names a *session*, not the buffer, and is manual.
- No way to filter/search the portal or profiles list; both are flat status-ordered tables.
  Fine at 5 sessions, unusable at 60.
- Creating a session offers a profile picker (good) or ad-hoc agent/workspace prompts
  (good), but there is no "launch this profile against *that* repo" — workspace is baked
  into the profile or prompted ad-hoc.

---

## Part C — The 100-repo use cases (the core of the request)

### Personas
- **Front-end dev**, ~15 of the 100 repos hot (a design system + apps), needs: node/pnpm
  toolchain, GitHub read/write on those repos, npm private-registry token, occasional
  `network:allow` for `pnpm install`, an API key for the agent.
- **Back-end dev**, ~20 hot repos (services + libs), needs: Go/Python toolchain, GitHub
  read/write, AWS SSO + EKS kubeconfig for a couple, a private Go/PyPI registry token.
- Both: most of the 100 repos are cold (read rarely), a handful are hot daily.

### What profiles would they need?
Minimally, per persona, a **container + deny** profile bound to the *set of hot repos* they
touch, with: the toolchain, the git credential scope (read vs write per repo), the registry
token, and (back-end) the cloud/kube creds. Realistically 2–4 profiles each (e.g.
`fe-review` read-only, `fe-dev` write + allow; `be-review`, `be-dev`, `be-infra` with
AWS/kube).

### Do they already exist? — No.
Shipped presets are **capability templates, not repo-bound profiles**:
`claude-container-allowlist`, `pi-container-allowlist`, `shell-container`,
`claude-host-unconfined`, `claude-subscription-container`. The example/dogfood
`safeslop.cue` profiles carry only `agent/environment/network/workspace` — **no repo list,
no credentials** except the one worked `work:` example. There is no front-end or back-end
starting point, and nothing repo-aware.

### Can we create them easily? — Partially, and not at scale.
- `safeslop profile create` and the Profiles `c` key create a profile from
  agent/environment/bundle/package — **but the CLI/`create` surface has no flags for repo
  sets or credentials** (README `profile create` = `--name --agent --environment --bundle
  --package`). Credentials must be hand-authored in CUE (`e` opens the file at the block).
- For multi-repo git creds, the model is `credentials: { ssh|forgejo: { repos: [ {repo,
  write}, … ] } }` — one entry per repo, per profile, by hand. For a back-end dev's 20 hot
  repos that's a 20-entry hand-maintained list, duplicated across their review/dev/infra
  profiles. **This does not scale to 100 repos or even 20 without generation.**
- 0068/0069 improve the *credential* story (account link once, mint per session) but the
  **repo-set declaration is still a hand-written per-profile list** — the account link
  authorizes an owner, but which repos a profile scopes to is still enumerated in CUE.

### What about credentials? — The hard part, and the active work.
Today GitHub piggybacks on ambient `gh` (a security finding too); after 0069 the flow is:
`safeslop creds link github` once, then profiles mint per-session repo-scoped tokens. That
is a real improvement. But: (a) the repo→profile binding is manual; (b) there's no
"give this persona write on their 20 repos" bulk affordance; (c) the Credentials surface
shows posture beautifully but **cannot author** a repo set — only jump-to-CUE.

### Is the system easy to manage at 100 repos? — Not yet.
The management model is "one `safeslop.cue` per repo root" (README: "per-repo `safeslop.cue`
policy"). For 100 repos that's **100 policy files to author, trust, and keep current**, or
one big multi-profile file with hand-maintained repo lists. Either way: no templating, no
persona inheritance, no bulk trust, no "apply this profile shape across these repos." The
trust model is per-file-bytes (good for safety) but means 100 `trust` approvals and a
re-trust on every edit.

### Is the editor slow/laggy? — No.
For the UI sizes involved (tens of sessions/profiles) the async substrate + idle-guarded
refresh keep Emacs responsive. The 100-repo problem is **information density and authoring
scale**, not frame rate: flat unfilterable tables and hand-authored CUE, not lag.

### Visual recognition of buffer security level / project / creds? — Partly.
- **Security level (tier/net):** yes — legible and color-redundant in the dashboards and
  the detail view.
- **Which project:** weak — the terminal buffer is `*safeslop-<id>*`; the workspace shows
  in the portal table but not in the live agent buffer's name or header.
- **Which credentials:** weak — not shown on the session row or in the agent buffer; only
  reachable via per-session detail, and even there it's the profile's declared posture, not
  "these 3 tokens are staged right now."

---

## Recommendations (ranked)

1. **Persona/repo scaling model.** Introduce profile *templates* with a repo-set variable
   (persona shape × repo list → concrete profiles), or a `credentials.repos` generator that
   expands an owner + glob/label into the per-repo list. Without this, 100 repos is
   hand-CUE forever. (Design-level; likely a spec of its own.)
2. **Ship front-end and back-end starter presets** (container+deny, toolchain, an
   account-linked git scope, a registry-token slot) so a new shop has a real starting point,
   not just capability stubs.
3. **Make the live agent buffer self-describing.** Name/annotate `*safeslop-<id>*` with
   profile + project + tier + net (e.g. `*safeslop:be-dev payments [container/deny]*`), and
   put a one-line credential-scope banner (repos + kinds, value-free) in the buffer header.
   Directly answers "which buffer, which project, which creds." Planned/covered by
   `specs/0086-session-legibility.md` (recommendation #3 only; the broader 100-repo
   template, filter/search/grouping, credential-authoring, and bulk-trust recommendations
   remain open).
4. **Filter/search/group in the portal and profiles** (by workspace, agent, status,
   persona) — the flat table breaks down well before 100.
5. **Authoring affordances in the Credentials surface** — add a repo/scope to a profile from
   the surface (still writing CUE underneath), not only jump-to-file.
6. **FLO operability:** a lane-health preflight + a scored rubric template + a
   decision-FLO runbook, so the pipeline isn't expert-only tribal knowledge.
7. **Bulk trust** for a reviewed multi-repo layout (a `trust` that approves a set with one
   comprehension gate), or a per-org policy overlay, to escape 100 individual approvals —
   without weakening the byte-hash binding (coordinate with `specs/0070` B1).

## Honest caveats
- The tool is **pre-alpha and single-repo-first**; "doesn't scale to 100 repos yet" is a
  roadmap statement, not a defect against its current target.
- The credential *lifecycle* work (0067/0068/0069) is the right foundation; the missing
  piece is the *authoring/scaling* layer on top of it.
