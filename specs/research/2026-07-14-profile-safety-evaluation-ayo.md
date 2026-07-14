# 2026-07-14 — Profile safety evaluation prior art (AYO)

Status: compiled for `specs/0101`
Target: evolve safeslop's coarse `risk`/`risk_axes` preview into an actionable profile evaluation without a misleading aggregate score.

## Corpus

- AWS IAM Access Analyzer policy validation: [finding API](https://docs.aws.amazon.com/access-analyzer/latest/APIReference/API_ValidatePolicyFinding.html), [validation guide](https://docs.aws.amazon.com/IAM/latest/UserGuide/access-analyzer-policy-validation.html)
- Kubernetes Pod Security Admission: [concept](https://kubernetes.io/docs/concepts/security/pod-security-admission/), [PSP migration](https://kubernetes.io/docs/tasks/configure-pod-container/migrate-from-psp/)
- OASIS SARIF 2.1: [specification](https://docs.oasis-open.org/sarif/sarif/v2.1.0/os/sarif-v2.1.0-os.html)
- VS Code Workspace Trust: [operator guide](https://code.visualstudio.com/docs/editing/workspaces/workspace-trust), [extension guide](https://code.visualstudio.com/api/extension-guides/workspace-trust)
- OpenSSF Scorecard: [README and aggregate-score warning](https://github.com/ossf/scorecard/blob/main/README.md), [check documentation](https://github.com/ossf/scorecard/blob/main/docs/checks.md)
- NIST SP 800-30 Rev. 1: [Guide for Conducting Risk Assessments](https://csrc.nist.gov/pubs/sp/800/30/r1/final)

## HIGH — carry into the decision

1. **Separate authority, readiness, and trust/provenance.** AWS separates nonfunctional-policy errors from overly permissive policy; Kubernetes separates policy level from enforcement mode; VS Code models trust as an execution gate. A missing or shadowed helper means “cannot launch now,” not “smaller blast radius.” Trust state gates launch but does not change what a compromised trusted run could reach.

2. **Use individually addressable findings with stable engine-owned IDs.** SARIF and IAM Access Analyzer make rule/result identity machine-readable while treating prose as replaceable presentation. Safeslop findings need stable IDs, category/axis, status/severity, concrete consequence, and a documentation anchor. Emacs must not parse prose to decide grouping, color, or actions.

3. **Never aggregate unlike authorities into a score.** OpenSSF explicitly warns that aggregate scores hide which behaviors are present or absent, permit very different profiles to reach the same number, and drift as heuristics change. Keep the frozen `specs/0029` law: concrete consequences, no numeric or letter-grade safety score.

4. **Make unknown and not-applicable first-class.** NIST requires uncertainty and rationale; AWS reports policy errors rather than silently passing ambiguity. Unknown scope/readiness must be loud and must never resolve to green/contained. Not-applicable must be distinguishable from pass.

5. **Keep evaluation content stable across preview/show/launch.** Kubernetes warn/audit/enforce modes evaluate the same policy and differ only in response. Safeslop's policy-derived authority findings should be identical for the same profile bytes in `profile create --dry-run`, `profile show`, and prelaunch. Context enrichment may differ but must live in separate readiness/trust sections.

6. **Every concern needs concrete, value-free remediation.** IAM findings include guidance and learn-more links; SARIF can carry structured fixes. Safeslop should state the smallest narrowing action without embedding secret values/refs, account-link refs, private paths, or executable free-form patches. Initial actions should be typed hints/command capabilities, not client-invented CUE edits.

7. **Show both bounded and open axes.** Kubernetes' coarse policy levels cannot express every fine-grained constraint. Keep file/network consequences first-class and add credentials/secrets/projection rather than stretching one global level. A green-looking omission is a dark pattern.

8. **Document rationale and model limits.** Static profile analysis establishes authorized reach, not probability of compromise. Consequence text should name the triggering policy fact and what the agent can do if compromised; it must not claim calibrated likelihood.

## MEDIUM — phase carefully

- Reuse current stable-ish lint codes where their meaning is unchanged, but define a registry/test that prevents accidental duplicate or unstable IDs.
- Preserve the existing additive `risk` and `risk_axes` wire fields during migration. New consumers can use the richer evaluation; old consumers must not break.
- Keep remediation typed and reviewable. One-click narrowing may later call engine-owned mutation commands, but automatic patch application is not required for the first slice.
- Expose point-in-time readiness only where the command has enough context. An unsaved compose dry-run has no meaningful policy trust state; report `not_applicable`, not `untrusted`.

## DEFERRED / rejected for this slice

- A scalar score, weighted grade, or “security percentage.”
- Probabilistic likelihood claims from static config.
- Replacing safeslop's whole-policy trust gate with per-capability trust semantics. VS Code's extension-specific model is useful prior art, but safeslop's exact-byte launch gate is settled.
- Auto-applying arbitrary CUE patches from finding text.
- Live forge/credential probes in evaluation tests.
- Custom mount authoring; current evaluation can describe workspace and builtin projection authority only.

## Contested HIGH choices for FLO

1. **Legacy roll-up:** retain `risk.headline/level` only as a compatibility projection, or remove/deprecate it immediately because even a coarse color band acts like a score.
2. **Wire shape:** one flat finding stream with dimensions, or separate `authority`, `readiness`, and `trust` sections sharing a common finding schema.
3. **Remediation contract:** prose + docs only, or typed action metadata in the first version.
4. **Readiness timing:** enrich `profile show` directly, or add an explicit evaluation/preflight command so normal inspection stays deterministic and cheap.

## Method

Four blind lanes (`ayo-research-gemini`, `ayo-research-deepseek`, `ayo-research-opus`, `ayo-research-glm`) received the same brief and source packet; all returned eight lessons. Host synthesis marked consensus, resolved duplicates, and kept the four contract choices above for adversarial FLO. No lane selected the final design.
