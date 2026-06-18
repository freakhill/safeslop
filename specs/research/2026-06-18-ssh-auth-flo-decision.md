# SSH/Git auth into the boundary — FLO decision record (2026-06-18)

**Status:** Decided. Supersedes the 1Password SSH-agent socket pass-through in `specs/0001` §7.1.
**Method:** `feedback-loop-optimization` (FLO), P=6, host=Opus 4.8, cross-family evaluation.
**Resolves:** the contested decision flagged in `specs/research/2026-06-17-startup-usecase-prior-art.md`
("1Password SSH-agent socket pass-through" → do NOT silently keep it).

## The question

How should `slop` deliver SSH/Git authentication **into** an isolation boundary (sandbox /
container / vm) so the agent can `git push`, given the human authenticates via the 1Password SSH
agent (Touch-ID) on the host? The prior-art pass surfaced three options: **(a)** keep socket
pass-through (+ per-use confirmation), **(b)** mint ephemeral single-use key material via `op`,
**(c)** per-profile choice.

The flaw in (a): after the first Touch-ID approval, subsequent requests from the same process are
often **silent**, so any code in the boundary can sign/authenticate with **any** configured key.

## Method

Six candidate designs evolved over 3 generations, each scored by an **isolated** evaluator (no
evaluator saw another design — anti-sycophancy via worker/evaluator separation). Rubric (locked):
Security 35 / Cross-env correctness 25 / Usability 20 / Philosophy-fit & honesty 20, judged against
three fixtures (happy `git push`, headless/CI without the desktop app, and an **adversarial**
case where convenience must not buy a security score). Gen-0 seeded with the status-quo socket
design as the baseline, so the loop had to *beat* it. Final design cross-family validated with
**Kimi K2.7 + GLM-5.1** (independent model families, reversed criterion order to cancel order bias).

## Result

| Design | Weighted (0–100) |
|---|---|
| (a) Socket pass-through (status quo, baseline) | **43** |
| Brokered single-key agent socket (best salvage of (a)) | 75 |
| **Agent-free ephemeral repo-scoped deploy key (winner)** | **~81** (Kimi 83.5 / GLM 78.5; Δ5.0, uncontested) |

Every socket-touching variant lost. Even a well-engineered broker that pins one key + forces per-op
Touch-ID only reached 75 — a live signing oracle, `socat`/TCP vm plumbing, and headless
silent-signing cost more than they bought. The two independent families **agreed within 5 points**
and named the **same** irreducible residual.

## Decision

Adopt **(b)**, pushed past the prior-art's "default (b)": there is **no socket/agent tier at all**,
not even gated. Option **(c)**'s per-profile idea survives only as `read-only | write | none`
granularity, never as an a-vs-b choice.

**Design:**
- Host mints a repo-scoped SSH deploy key (`slop-gh-key create-pair` under Touch-ID/1Password;
  `op read` of a pre-provisioned scoped key via `OP_SERVICE_ACCOUNT_TOKEN` for headless/CI),
  **read-only by default**. The 1Password agent socket never enters the boundary.
- Stage **only** the `0600` private key + a pinned `known_hosts`; expose via
  `GIT_SSH_COMMAND="ssh -i <key> -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=yes -o UserKnownHostsFile=<kh>"`.
  Per-env path mirrors `KUBECONFIG`/`.npmrc`: real stage (sandbox), `/slop/runtime` (container),
  `~/.slop-runtime` (disposable vm).
- `credentials.ssh: ephemeral | none` (default `ephemeral`; `none` disables push, stated plainly).
  `write:true` is opt-in and **lint-gated** on `network:deny` + a forge-only egress allowlist
  (`ssh.write && network:allow` → hard error; non-forge host in the allowlist → hard error), so an
  exfiltrated write key is useless off-host. TTL ≤ 60m.
- **3-layer decay** (GitHub deploy keys have no native TTL): best-effort on-exit revoke (not relied
  upon — SIGKILL skips hooks) + stage wipe / vm teardown + a host-side reaper of orphaned
  `slop-*`/`llm_agent_<host>_*` keys. `slop doctor` reports the key's read-only/write flag, TTL, and
  forge-only egress assertion for write profiles.

**Why no broker (deliberate omission):** a brokered signing socket *would* remove the one residual
below, but it puts a continuously-exploitable live signing oracle in/beside the boundary for the
whole run — a larger attack surface than a caged file — for marginal benefit. Read-only-default +
egress-caging removes the *motivation* (write is rare and caged) more cheaply than a confirm path
removes the *capability*. It scored lower (75 vs ~81) and is rejected.

## Honest residual risk

A file-based key cannot eliminate **in-run reuse**: a compromised boundary process can reuse the
staged key for git ops against **that one repo** until decay (for `write` profiles, that includes
pushes). Both evaluators confirmed this is the irreducible ceiling for any file-delivered
credential. Bounded blast radius — one repo, ≤60m, read-only unless explicitly opted in,
forge-only egress for write — **is** the security property; total non-reuse is not. Exfil defense
holds only while the squid egress allowlist is uncircumvented and truly forge-only.

## Implementation consequence (follow-on, not done here)

The current Go container path **does** bind-mount the agent socket
(`SSHAuthSock` in `internal/engine/container/assets/compose.yml.tmpl` →
`/slop/ssh-agent.sock`). Adopting this decision means **removing that bind-mount** and building an
`ssh` credential provider (`internal/engine/creds/ssh.go`) mirroring the kube/aws/gcp providers,
wired into `runProfile` with the `read-only`/`write` schema + lint rules above. Tracked as a future
`specs/00NN-ssh-ephemeral-key` plan.
