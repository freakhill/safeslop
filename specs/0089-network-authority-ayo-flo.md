# 0089 — Network authority model: ayo-FLO decision

Date: 2026-07-09
Status: decision landed (FLO score 92.0/100; 3 clarifications applied)
Follows: `specs/0087-product-activation.md` track 4; supersedes the open question in `specs/0048-host-egress-approval-flo.md`.

SCOPE: pin the product/security model for deny, allow, static allowlists, session grants, and future ask-like UX before any code changes. This is a decision note, not an implementation spec.

OFF-LIMITS: do not weaken `network: "deny"`, policy-byte trust, runtime fail-closed behavior, host-helper shadow refusal, credential value-free guarantees, or broker/mint endpoint custody. Do not represent host runs as enforceably network-isolated.

## Expansion packet

- **Current state/index:** `specs/0087-product-activation.md` names network authority as a security/capability boundary. No `docs/spec/design` index exists; this repo uses numbered `specs/` plus `specs/research/`.
- **Settled decisions honored:** current engine schema is explicit `environment: "host" | "container"`; host has no safeslop-enforced network boundary and is consent-gated only; container `network:deny` is proxy/topology enforced; `network:allow` is explicitly broad; session/profile rows stay credential value-free.
- **Frozen laws:** default deny; no silent weakening; hard metadata/private/IP-literal denies; no reverse-DNS allowlisting; sandbox/agent never mints or renews credentials; deny-tier runtimes fail closed unless explicitly overridden; no new privileged local daemon without its own spec.
- **Current contracts consumed:** `#Network: "deny" | "allow"`; `profile.egress?: [...string]` is currently a static container-deny contributor; effective allowlist is already composed from base + `AgentEgress` + package runtime egress + `CredsEgress` + profile egress; deny topology keeps the agent internal-only behind Squid.
- **Prior notes:** `specs/0046`, `0068`, `0069`, `0070`, `0079`, `0080`, and `0087`.

## Prior-art lessons carried forward

HIGH lessons from AYO lanes (Gemini, DeepSeek, GPT; Opus unavailable due expired Claude OAuth):

1. **Do not let the agent trigger modal escalation prompts.** Browser permission and app-firewall scars show prompt fatigue turns security UX into click-through habit.
2. **Only promise progressive enforcement where safeslop controls the enforcement point.** Host networking has no safeslop firewall; container deny traffic does go through the proxy/topology.
3. **Prefer structural lifetimes.** Session-scoped grants revoke with the container/session boundary; persistent grants must be explicit profile edits.
4. **Keep static policy and runtime overlays separate.** Dynamic grants must not silently mutate `profile.egress` or bypass policy-byte trust.
5. **Default to exact destination scope.** Wildcards, suffixes, all-URLs, TLD globs, and IP literals are the recurring over-broad rule scar.
6. **DNS is policy, not plumbing.** Match proxy-observed Host/SNI; do not re-enable raw agent DNS; keep IP-literal and reverse-DNS bypasses structurally closed.
7. **Update enforcement atomically and fail closed.** Dynamic proxy updates must not move the agent onto the egress network, and UI/proxy-control failure must not open traffic.
8. **Show the effective union by source.** Agent/package/credential/profile/session contributors all widen the same effective allow set and must be legible.

## Verdict

Keep the model:

```text
container + network:deny + static effective allowlist + operator-invoked session grants
```

Do **not** add an agent-triggered `network:ask` policy mode. A blocked destination may be observed and shown to the operator, but the agent's connection attempt must never open a modal permission escalation path.

Host runs remain consent/advisory only for network authority. The UI may explain that a host run has host networking, but it must not render allowlist/prompt/session-grant controls as enforceable isolation.

## Pinned model

### Enforcement posture

- `environment:container` + `network:deny` is the only restricted posture this decision treats as enforceable.
- Temporary grants update the proxy's effective allow set; they never attach a deny/prompt agent to the egress network.
- Runtime no-egress verification remains fail-closed exactly as in `specs/0066`.
- `environment:host` has no safeslop network boundary. Render it as `host network not enforced` (or equivalent), not as a denied or allowlisted network.
- `network:allow` is explicit broad egress. Do not imply per-destination enforcement or audit unless an implementation later materializes one.

### Effective allow set under container deny

For a container `network:deny` session, the enforceable allow set is the materialized union:

```text
base allowlist
∪ policy.AgentEgress(agent)
∪ package RuntimeEgress
∪ policy.CredsEgress(profile)
∪ profile.egress
∪ session egress grants
```

Every surface that summarizes network authority should show the source and lifetime of each contributor: base, agent, package, credential, profile policy, or session overlay.

Credential/package widening is destination metadata only. It must not add credential values, secret refs, or stage paths to session/profile rows beyond the existing Credentials readiness surfaces.

Broker, mint, metadata, loopback, private/link-local, and IP-literal destinations are non-promptable and non-grantable.

### Destination shape for new session grants

First implementation target:

```text
exact observed FQDN + port, matched from proxy-observed Host/SNI
```

Rules:

- Keep raw agent DNS disabled in deny posture.
- Deny IP literals structurally before any allow/prompt path.
- Do not use reverse DNS matching.
- Do not accept all-URLs, TLD globs, or broad wildcard grants.
- Suffix/wildcard broadening requires a later explicit schema/trust decision.

**Legacy compatibility clarification:** existing `profile.egress` strings keep the current Squid/`Decide` semantics until a migration spec lands; this note does not silently narrow, broaden, or reinterpret bare-host vs leading-dot entries. New typed session grants must not silently narrow or reinterpret existing `profile.egress`; UI may label those entries as `profile legacy domain` and a later spec may add lint/suggested migration.

### Session grants

Session grants are runtime overlay state, not profile policy.

Required behavior for future implementation:

- Created only by explicit operator action (`grant this exact FQDN:port for this session`), never by the agent's blocked connection attempt alone.
- Default lifetime is the current session; revoked at session end or by explicit revoke.
- Optional TTL may be added only with exact expiry semantics; it must not outlive the session or the credential lifetime that made the grant useful.
- Persistent grants require an explicit profile/CUE edit and therefore re-enter policy-byte trust.
- Proxy updates are atomic/revisioned. Reload failure preserves the previous, more restrictive state.
- Prompt/UI/control-channel failure fails closed to strict deny.
- Deny observations are non-modal: badge/list/log/event, not a blocking approval prompt.

**Observation clarification:** the implementation spec must name the observation channel (for example proxy deny logs surfaced through `session status`/JSONL or a session egress events command). If observation is unavailable, safeslop shows no suggestions; it must not open traffic or invent inferred grants.

### Operator UX contract

Pre-launch/profile/session UI should show a materialized network-authority panel:

- environment and honest enforcement posture;
- network mode;
- hard-denied classes;
- effective allowed destinations;
- source/lifetime for each destination;
- value-free credential scope/readiness only.

In-session UX should support:

- list blocked FQDN:port observations;
- grant exact destination for this session;
- revoke a session grant;
- copy/suggest a persistent CUE edit, requiring explicit profile edit and trust;
- no agent-triggered modal prompts.

## Rejected

- `network:ask` as a policy mode that launches modal prompts from first connection attempts.
- Host allowlist enforcement or host temporary grants presented as real isolation.
- Silently mutating `profile.egress` after a temporary approval.
- IP-literal allowlists, reverse-DNS matching, broad wildcards, TLD globs, or all-URLs grants.
- Moving a denied container onto the egress network to satisfy a temporary grant.
- New long-lived privileged firewall/daemon dependency in this decision.
- Letting sandboxed agents reach credential broker/mint endpoints.

## Deferred / owed specs

1. **Typed destination/schema spec:** exact FQDN+port session targets, validation/normalization, legacy `profile.egress` compatibility/lint, and trust behavior for persistent additions.
2. **Proxy/runtime overlay spec:** revisioned session overlay allowlist, hard-deny ordering, reload failure behavior, and deny-observation logs/events.
3. **CLI + Emacs UX spec:** value-free contributor table, blocked-destination list, grant/revoke actions, host non-enforcement labels.
4. **Session storage/audit spec:** value-free session grant records with source, lifetime, policy hash, profile, agent, environment, and destination.
5. **Verification spec:** hermetic tests for IP literals, reverse DNS exclusion, metadata/private denial, UI/control failure closed, host non-enforcement labeling, and no profile mutation on session grant.
6. **Future non-HTTP authority:** run a separate ayo-FLO only when a real required workflow cannot fit HTTP(S) proxy enforcement (for example generic TCP/SSH/UDP/CIDR). Until then, label the first progressive feature as HTTP(S) destination grants and do not over-claim.

## Method

- Expansion read: `CONTRIBUTING.md`, `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0046`, `0048`, `0053`, `0066`, `0068`, `0069`, `0070`, `0071`, `0087`, current policy schema, egress code, container compose/Squid templates, and launch wiring.
- AYO lanes: `ayo-research-gemini`, `ayo-research-deepseek`, `ayo-research-gpt` succeeded; `ayo-research-opus` unavailable (expired Claude OAuth). Host compiled and triaged lessons.
- FLO: worker = `flo-worker`; evaluator = `flo-evaluator-deepseek` (cross-family, rubric-locked). Host computed score and applied clarifications.
- Rubric/scores: R1 safety/laws 30% → 10; R2 architecture fit 25% → 9; R3 UX/legibility 20% → 9; R4 phaseability 15% → 9; R5 extensibility 10% → 8. Weighted total **92.0/100**. Fatal flaws: none.
- Clarifications applied from evaluator weaknesses: legacy `profile.egress` compatibility, observation-channel fail-closed requirement, and explicit trigger for future non-HTTP authority design.
