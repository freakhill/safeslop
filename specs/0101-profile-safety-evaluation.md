# 0101 — Actionable profile safety evaluation

Status: planned
Date: 2026-07-14
Decision: `specs/research/2026-07-14-profile-safety-evaluation-flo.md`
Prior art: `specs/research/2026-07-14-profile-safety-evaluation-ayo.md`
Follows: `specs/0087-product-activation.md` track 6 and `specs/0091-profile-authoring-cockpit.md`.

SCOPE: add the decision's engine-owned, additive `evaluation` contract to resolved profile inspection/authoring; cover static authority, exact-byte/builtin trust, and point-in-time local readiness; render it in Emacs Profile inspect/compose/launch flows with concrete value-free consequences and typed remediation guidance.

OFF-LIMITS: no aggregate score/grade/combined safety color; no weakening of trust, host consent, network deny/session grants, helper-shadow refusal, credential scope, or file-sharing boundaries; no client-derived safety rules; no automatic CUE patching/trust; no live forge/cloud/cluster/registry/credential APIs in inspection or tests; no secret values/refs, private-key/account-link refs, staged paths, or private host paths in evaluation; no custom mount authoring, forge credential P2, daemon, or runtime dependency.

WORKTREE: `.worktrees/0101-profile-safety-evaluation/`

## Contract

- Preserve existing `risk` and two-row `risk_axes`; add versioned `evaluation {schema_version,authority,trust,readiness}`.
- Authority is a pure function of decoded policy and remains identical for the same profile across dry-run/show/prelaunch.
- Trust and readiness are separate context snapshots. Their failure can block launch but never suppress or lower authority.
- Findings carry engine-owned `rule_id`, `axis`, `outcome`, `severity`, consequence, scope IDs, and typed remediation. Unknown/not-applicable are explicit and never green.
- Credential scopes expose only non-secret targets/access/lifetime/basis and correctly honor provider-level or per-repository write.
- Emacs renders Authority → Trust → Readiness from structured fields and never parses prose to infer severity, grouping, or action.

## Tasks

- [x] T1 — Implement the pure evaluation domain and authority registry
  FILE:     `internal/engine/policy/evaluation.go`, `internal/engine/policy/evaluation_test.go`
  CHANGE:   TDD the v1 `Evaluation`, section, finding, remediation, and credential-scope JSON types/enums from the FLO note. Implement a validated stable rule registry and pure `EvaluateAuthority(Profile)` with deterministic axis/rule ordering for host/container network and file reach, builtin projection, secret count, all credential providers, value-free non-secret targets, provider/repo effective write, ignored egress, GitHub/Forgejo write+open-egress combinations, bounded/absent rows, and loud unknown handling. Add registry checks for duplicate/unregistered IDs, enum combinations, required remediation, and forbidden material.
  VERIFY:   `go test ./internal/engine/policy/ -run 'Test(Evaluation|EvaluateAuthority|CredentialScope|FindingRegistry)' -count=1 -v`
  EXPECTED: command exits 0; authority is pure/deterministic, every core axis is present, per-repo/Forgejo scope is correct, unknown is non-green, and JSON/findings contain no forbidden secret/ref/path material.

- [x] T2 — Make legacy risk/lint a compatibility projection of shared facts
  FILE:     `internal/engine/policy/risk.go`, `internal/engine/policy/risk_test.go`, `internal/engine/policy/lint.go`, `internal/engine/policy/lint_test.go`, `internal/engine/policy/evaluation.go`
  CHANGE:   Refactor `RiskSummary`, `RiskAxes`, and lint predicates to consume the evaluation's shared normalized authority facts rather than a second arbiter. Preserve field names/types, two-axis cardinality, valid-profile headline/level mappings, and existing prose order. In an isolated compatibility checkpoint, correct GitHub write detection to include `RepoCred.Write`, add value-free Forgejo credential lines, and add `forgejo-write-open-egress` without changing settled lint-code meaning.
  VERIFY:   `go test ./internal/engine/policy/ -run 'Test(Risk|Lint|LegacyEvaluationProjection)' -count=1 -v`
  EXPECTED: command exits 0; existing risk/axis behavior stays compatible, old surfaces no longer under-report per-repo GitHub or Forgejo authority, and lint/evaluation predicates cannot drift.

- [ ] T3 — Add trust/readiness context and wire additive profile JSON
  FILE:     `internal/cli/profile_evaluation.go`, `internal/cli/profile_evaluation_test.go`, `internal/cli/cli.go`, `internal/cli/cli_profile_iw3_test.go`, `internal/cli/cli_profile_test.go`
  CHANGE:   Add narrow injectable adapters/clock for exact policy-byte trust, embedded-builtin provenance, workspace availability, sanitized helper inspection, container runtime identity/readiness, toolchain helpers, and value-free GitHub/Forgejo account-link presence. Do not call remote APIs or resolve secret values. Produce trust/readiness states/findings per the FLO note, suppress all helper/workspace/account paths, and add `evaluation` to `profileResolvedData`: project show checks current hash/trust; builtin show uses `embedded_builtin`; unsaved dry-run uses trust not-applicable; every context snapshot is timestamped once. Preserve all current envelope keys and prove static authority equality across show/dry-run inputs.
  VERIFY:   `go test ./internal/cli/ -run 'TestProfile(Evaluation|Show.*Evaluation|CreateDryRun.*Evaluation|EvaluationCompatibility)' -count=1 -v`
  EXPECTED: command exits 0; project/builtin/unsaved envelopes match the v1 contract, blocked/unknown readiness never changes authority, helper shadowing remains fail-closed, output is value-free, and old JSON fields remain decodable.

- [ ] T4 — Render structured evaluation in Emacs Profile workflows
  FILE:     `emacs/safeslop-profiles.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   TDD pure helpers that validate v1, render Authority → Trust → Readiness, print explicit outcome words with color redundancy, preserve engine order, show timestamp/remote-validity caveat, and dispatch remediation only by typed `kind/action_id/docs_ref`. Use the renderer in Profile Inspect and compose Preview; make profile launch fetch/show the engine evaluation before its final confirmation while leaving CLI launch gates authoritative. Unsupported/malformed v1 renders loud UNKNOWN; absent evaluation renders a labeled legacy fallback; supported v1 does not prominently use `risk.level`. Deduplicate remediation buttons by first `action_id` without hiding findings.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-profiles-.*evaluation")'`
  EXPECTED: command exits 0; all three questions are independently legible, unknown/N/A are non-green, prose changes do not alter UI behavior, typed remediation is value-free, launch reviews the exact engine evaluation, and legacy clients/snapshots degrade loudly.

- [ ] T5 — Synchronize docs, roadmap, and decision status
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0087-product-activation.md`, `specs/0101-profile-safety-evaluation.md`, `specs/research/2026-07-14-profile-safety-evaluation-{ayo,flo}.md`
  CHANGE:   Document the three-section non-score evaluation, local readiness snapshot caveat, trust/authority separation, value-free credential targets, legacy fallback, and Profile inspect/compose/launch UX. Mark 0087 profile safety evaluation complete only after T6 passes; keep custom mounts and forge P2 deferred.
  VERIFY:   `rg -n 'Authority|Trust|Readiness|no.*score|value-free|0101|profile safety evaluation' README.md skills/agent-sandbox-ops/SKILL.md specs/0087-product-activation.md specs/0101-profile-safety-evaluation.md specs/research/2026-07-14-profile-safety-evaluation-{ayo,flo}.md`
  EXPECTED: output shows the engine/UI contract, non-score law, roadmap closure, and explicit deferrals.

- [ ] T6 — Run compatibility, security, and repository gates
  FILE:     whole repo
  CHANGE:   Run the UI matrix, required repository checks/build, and inspect the final diff. Set this spec complete only after every gate succeeds.
  VERIFY:   `make test-emacs-ui-matrix && make check && make build`
  EXPECTED: command exits 0; raw/Doom/Evil UI slots, Go/ERT suites, denylist/sync/vet/format gates, and binary build pass with existing security refusals unchanged.

## Execution notes

Use TDD for T1–T4. Execute tasks in order because the public/context layers consume the pure registry and compatibility projection. Commit each task only after its exact VERIFY passes; stop on the first failure. Keep real HOME/PATH/accounts/trust/credential providers behind injected seams in tests.
