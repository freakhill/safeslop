# 2026-07-14 — Profile safety evaluation decision (FLO)

Status: decision accepted for `specs/0101-profile-safety-evaluation.md`
Implementation: contract, compatibility projection, context, UI, and docs landed through T5; final T6 repository verification and roadmap closure pending
Score: **97.25 / 100** (no deterministic LAW override)
Inputs: `specs/research/2026-07-14-profile-safety-evaluation-ayo.md`, `agent/tmp/flo-runs/0101-profile-safety-evaluation/inputs/{goal,rubric,packet}.md`

## Verdict

Add an engine-owned, additive `data.evaluation` object to `profile show` and `profile create --dry-run`. It has three structurally separate sections, always rendered in this order:

1. **Authority — what a compromised run can reach.** Purely derived from the decoded profile.
2. **Trust — whether this exact saved policy or embedded builtin provenance is approved.** A launch gate; never an authority modifier.
3. **Readiness — whether local, safeslop-controlled launch prerequisites are available now.** Point-in-time state; never an authority modifier.

There is no combined verdict, numeric score, letter grade, weighted roll-up, or single red/green status. A missing helper or untrusted policy can block launch, but it cannot suppress, downgrade, reorder, or recolor the profile's authority findings.

Keep existing `risk` and `risk_axes` fields for compatibility. New clients prefer `evaluation`; old clients continue to decode. `risk.level` remains a compatibility color band, not an overall safety judgment.

## Pinned v1 contract

```go
type Evaluation struct {
    SchemaVersion int                 `json:"schema_version"` // exactly 1
    Authority     AuthorityEvaluation `json:"authority"`
    Trust         TrustEvaluation     `json:"trust"`
    Readiness     ReadinessEvaluation `json:"readiness"`
}

type AuthorityEvaluation struct {
    Findings         []Finding         `json:"findings"`
    CredentialScopes []CredentialScope `json:"credential_scopes"`
}

type TrustEvaluation struct {
    State     string     `json:"state"`
    Basis     string     `json:"basis"`
    CheckedAt *time.Time `json:"checked_at"`
    Findings  []Finding  `json:"findings"`
}

type ReadinessEvaluation struct {
    State     string     `json:"state"`
    CheckedAt *time.Time `json:"checked_at"`
    Findings  []Finding  `json:"findings"`
}

type Finding struct {
    RuleID      string       `json:"rule_id"`
    Axis        string       `json:"axis"`
    Outcome     string       `json:"outcome"`
    Severity    string       `json:"severity"`
    Title       string       `json:"title"`
    Consequence string       `json:"consequence"`
    ScopeIDs    []string     `json:"scope_ids"`
    Remediation *Remediation `json:"remediation"`
}

type Remediation struct {
    Kind     string `json:"kind"`
    ActionID string `json:"action_id"`
    Summary  string `json:"summary"`
    DocsRef  string `json:"docs_ref"`
}

type CredentialScope struct {
    ScopeID    string `json:"scope_id"`
    Provider   string `json:"provider"`
    Target     string `json:"target"`
    Access     string `json:"access"`
    Lifetime   string `json:"lifetime"`
    Basis      string `json:"basis"`
}
```

Every object/array is emitted; arrays are `[]`, not `null`. Nullable timestamps and remediation objects are explicit `null`. One injected clock read supplies a snapshot's UTC RFC3339 timestamp. Object member order is non-semantic; array order is deterministic.

### Closed enums

- `Finding.axis`: `network | files | projection | secrets | credentials | trust | readiness`
- `Finding.outcome`: `concern | bounded | pass | fail | unknown | not_applicable`
- `Finding.severity`: `critical | high | medium | info`
- trust state: `trusted | untrusted | changed | unknown | not_applicable`
- trust basis: `project_exact_bytes | embedded_builtin | unsaved | unknown`
- readiness state: `ready | blocked | unknown | not_applicable`
- remediation kind: `policy_change | operator_workflow | install_helper | repair_helper_resolution | review_and_trust | retry_check`
- credential provider: `pnpm | aws | gcp | kube | github | forgejo`
- credential access: `read_only | read_write | scoped_api | external_policy | provider_default | unknown`
- credential lifetime: `short_lived | persistent | unknown`
- credential basis: `declared | resolved_at_launch | provider_default`

Unknown schema versions/enums render as `UNKNOWN — update required`; they never map to pass, bounded, or contained.

### Value-free scope rules

`CredentialScope.target` may contain already-approved non-secret target metadata such as `owner/repo`, `origin`, registry host, cloud profile/role label, API-scope label, or cluster label. It must never contain a secret value/ref, private-key ref, account-link ref, staged path, workspace/host path, inline policy body, or credential material. `ScopeID` is an engine symbol and never embeds user target text.

GitHub/Forgejo effective write is `provider.Write || RepoCred.Write`; declared mixed access becomes separate read/write rows. This closes the current under-reporting. Forgejo must be included. Unknown/provider-default scope stays `unknown`/`provider_default`, never assumed read-only.

## Computation boundaries

### Static authority

`policy.EvaluateAuthority(Profile)` is pure: no clock, filesystem, environment, trust store, helper execution, account store, credential API, secret provider, or network. It consumes a shared normalized authority-facts model also used by legacy `RiskSummary`, `RiskAxes`, lint predicates, and session credential-scope projection.

For identical decoded profile input, authority findings are byte-for-byte stable across dry-run, show, and prelaunch. Core axes are always present: network, files, projection, secrets, credentials. Bounded/absent axes remain visible so omission cannot look green.

Initial required meanings include:

- host network and whole-account files: `concern/critical`;
- container open egress: `concern/high`;
- container deny allowlist: `bounded/info`, while naming allowlisted destinations as possible exfiltration channels;
- workspace read-write reach: concrete consequence, not “safe”;
- live builtin projection: readable instruction/config authority, not hash-pinned content;
- each secret/credential provider: count/scope/lifetime consequence without material;
- GitHub and Forgejo effective write + open/unrestricted egress: critical combination finding;
- ignored `profile.egress`: concern that configured domains do not constrain actual reach;
- unknown/future authority: loud `unknown`, conservatively naming wider assumed reach.

### Trust

- saved project exact hash match: `trusted / project_exact_bytes`, pass;
- no approval: `untrusted / project_exact_bytes`, fail and launch remains blocked;
- changed bytes: `changed / project_exact_bytes`, fail and launch remains blocked;
- known embedded builtin: `trusted / embedded_builtin`, pass; do not claim a runtime signature verification the engine does not perform;
- builtin registry/integrity inconsistency: `unknown / embedded_builtin`, unknown/high and fail closed;
- unsaved compose dry-run: `not_applicable / unsaved`, never “trusted”;
- check failure: `unknown / unknown`, never pass.

Trust remediation opens the existing explicit review/trust flow and never approves automatically.

### Readiness

Readiness checks only local, safeslop-controlled prerequisites already represented by existing seams: workspace availability without returning its path; selected container runtime availability/identity/shadow state; required host agent/helper availability through sanitized PATH; required toolchain helper; and presence of required value-free account-link metadata. It does not stage credentials, contact forges/clouds/clusters/registries, resolve or expose secret values, or promise remote authentication.

- any required check fails: `blocked`;
- otherwise any check unknown: `unknown`;
- all required checks pass: `ready`;
- context deliberately unavailable: `not_applicable`, with an explicit not-collected finding and null timestamp.

`profile show` and compose dry-run may collect this local snapshot without granting authority. Immediately before launch, the engine recomputes through the same functions; cached/displayed evaluation is never an authorization token, and existing launch gates remain authoritative.

## Finding and rule-ID law

- IDs are lowercase stable symbols, owned by a Go registry; existing `egress-ignored` and `github-write-open-egress` retain their IDs where meaning is unchanged.
- IDs never contain profile names, targets, paths, hashes, timestamps, secret names/refs, or credential material.
- The registry pins ID, axis, allowed outcome/severity, consequence builder, remediation policy, docs anchor, and deterministic ordinal.
- Wording may improve without changing ID. A material predicate/security-meaning change gets a new ID; retired IDs are never reused.
- Tests reject duplicates, unregistered emission, invalid enum combinations, unstable ordering, or a concern/fail/unknown finding without remediation.
- Clients tolerate unknown rule IDs by rendering supplied structured fields; they never discard or treat them as pass.

Initial families: `authority.network.*`, `authority.files.*`, `authority.projection.*`, `authority.secrets.*`, `authority.credentials.*`, `egress-ignored`, `github-write-open-egress`, `forgejo-write-open-egress`, `trust.project.*`, `trust.builtin.*`, `trust.unsaved`, `readiness.workspace`, `readiness.container-runtime`, `readiness.agent`, `readiness.secret-provider`, `readiness.toolchain.*`, `readiness.github-account`, `readiness.forgejo-account`, and `readiness.not-collected`.

## UI contract

Emacs renders engine order and never parses prose:

1. **Authority — what it can reach**: fixed axis order network, files, projection, secrets, credentials.
2. **Trust — is this exact policy approved?**
3. **Readiness — can this host launch it now?** with visible point-in-time timestamp and remote-validity caveat.

Every row prints the explicit outcome word (`CONCERN`, `BOUNDED`, `PASS`, `FAIL`, `UNKNOWN`, `N/A`); color only reinforces. Unknown and N/A are never green. Consequence is primary text; remediation is selected only from `kind`, `action_id`, and `docs_ref`. V1 actions show/open engine-owned guidance or existing trust/setup flows; they never auto-edit CUE or execute shell text.

There is no combined safety banner. A compact summary may show three unblended facts such as `Authority: 3 concerns · Trust: trusted · Readiness: blocked`; it must not assign one color or verdict to that line.

Remediation display is deterministic: section order above; authority axis order above; within an axis concerns/unknown before bounded, then severity `critical > high > medium > info`, then registry ordinal. Trust/readiness retain registry order. Duplicate `action_id` buttons are collapsed to the first occurrence while every finding remains visible.

When `evaluation` is absent, render `Legacy safety summary — trust and readiness unavailable` plus existing `risk`/`risk_axes`. Unsupported v1 content renders loud unknown, not a legacy green fallback. With supported v1 present, Emacs does not prominently render or color by legacy `risk.level`.

## Compatibility

- `risk` and `risk_axes` retain the same names and JSON types for an additive, open-ended v1 compatibility period.
- `risk_axes` remains network/files only; new axes live in `evaluation`.
- Legacy risk serialization becomes a projection of shared authority facts, preventing two arbiters.
- Compatibility corrections for per-repository GitHub write and Forgejo value-free summaries are isolated in their own verified task/commit before public evaluation wiring.
- New fields/enums are additive within v1. Removing/renaming fields or changing enum meaning requires `schema_version: 2`.

## Rejected / deferred

Rejected: scalar/letter/weighted scores; one flat finding stream; combined red/green verdict; trust/readiness modifiers on authority severity; client-derived rules or prose parsing; raw CUE patches or shell commands in remediation; automatic trust; removing/expanding legacy axes in place; remote credential probes during inspection; probabilistic compromise claims.

Deferred: custom mount authoring, forge credential P2, arbitrary action execution, live IAM/RBAC inference, a daemon, and any change to settled trust/network/host-consent/session-grant/file-sharing laws.

## Implementation anchors

1. Pure authority domain, closed enums, registry, credential scopes, and deterministic/value-free tests.
2. Isolated legacy compatibility corrections and projection from shared authority facts.
3. Trust/readiness adapters with fixed clock and fake helper/runtime/account/workspace seams; additive JSON wiring for show/dry-run/prelaunch.
4. Emacs inspect/compose renderer, unsupported/legacy fallback, typed remediation dispatch, and ERT.
5. Docs, goldens, security-regression gates, `make check`, and `make build`.

## Implementation status

The accepted split now drives resolved profile JSON and Emacs Profile
inspect/compose/launch review: Authority stays static, exact-byte/builtin Trust is
a separate gate, and Readiness is a point-in-time local snapshot with no remote
validity claim. There is no aggregate score or combined verdict. Credential
targets remain value-free, and absent evaluation uses the labeled legacy fallback
while malformed/unsupported evaluation is loud unknown. T6 still owns final
repository gates and completion; custom mounts, forge credential P2, live remote
permission inference, and arbitrary action execution remain deferred.

## Scoring

Locked weights: C1 safety/laws 30%, C2 public contract 25%, C3 actionability 20%, C4 architecture 15%, C5 phaseability 10%.

| Candidate | C1 | C2 | C3 | C4 | C5 | Weighted |
|---|---:|---:|---:|---:|---:|---:|
| additive compatibility | 10.0 | 9.5 | 9.5 | 10.0 | 9.5 | **97.25** |
| explicit sectioned command | 9.5 | 9.0 | 9.0 | 9.0 | 9.5 | **92.00** |

No deterministic LAW override fired. The additive candidate won because it achieves the same section separation without a duplicate command and keeps current inspection/authoring flow intact.

Host applied evaluator clarifications without re-evaluation: explicit unknown builtin handling; deterministic remediation ordering/deduplication; isolated legacy-correction checkpoint. Host also narrowed an overclaim from `builtin_signature` to `embedded_builtin` and retained existing value-free non-secret credential targets for operator actionability. These tighten truth/legibility without changing the selected architecture or weakening a LAW.

## Method

- Expansion read current `risk.go`, `lint.go`, trust store, profile show/dry-run wiring, Emacs Profiles UI, specs 0029/0032/0087/0091, and current docs.
- AYO: Gemini, DeepSeek, Opus, and GLM blind lanes; all succeeded. Compiled note: `specs/research/2026-07-14-profile-safety-evaluation-ayo.md`.
- FLO: two blind `flo-worker` candidates; each independently scored by `flo-evaluator-kimi-thinking` against the locked rubric. Host computed weighted totals and checked deterministic LAWs.
