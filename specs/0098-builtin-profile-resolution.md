# 0098 — Builtin profile resolution and provenance

Status: planned
Date: 2026-07-12

SCOPE: implement binary-embedded, cwd-independent launchable builtin profiles, distinct from scaffold presets, so `safeslop session create --profile pi|claude|fish|zsh --output json` and `safeslop profile show <name> --output json` work from any directory. Pin project-over-builtin precedence, invalid-project fail-closed behavior, JSON provenance, session record hash semantics, and run-time builtin reconstruction.

OFF-LIMITS: do not remove or repurpose existing scaffold presets (`profile presets`); do not silently ignore an invalid `safeslop.cue`; do not let builtin profiles override project profiles of the same name; do not require a local `safeslop.cue` for builtin profile launch; do not weaken project profile policy-byte trust/re-read behavior from specs/0072 and 0073; do not add host-tier builtin defaults in this slice; do not implement host projection or progressive grants here except as fields consumed from their own specs.

WORKTREE: `.worktrees/0096-contained-hybrid-default-profiles/`

## Design

Problem: existing embedded `profile presets` are scaffold CUE templates only; `session create --profile <name>` and `profile show <name>` require a local `safeslop.cue`. The contained-hybrid defaults need a launchable builtin registry carried by the binary and available from any cwd.

Chosen approach: add a second embedded registry, **builtin profiles**, separate from scaffold presets. Builtins are complete launchable profiles with stable names (`pi`, `claude`, `fish`, `zsh`), descriptions, canonical embedded bytes, and a hash. Resolution checks the project first, then falls back to builtins only when no project profile with that name exists.

## Contracts

### Registry

Add `internal/engine/policy/builtins.go` and, if CUE files are used, `internal/engine/policy/builtins/*.cue`:

```go
type BuiltinProfile struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Profile     Profile        `json:"profile"`
    CUE         string         `json:"cue,omitempty"`
    Hash        string         `json:"policy_hash"`
}

func BuiltinProfiles() []BuiltinProfile
func BuiltinProfileByName(name string) (BuiltinProfile, bool)
```

Implementation may embed one CUE file per builtin and validate via `policy.LoadBytes`, or use Go literals plus deterministic canonical rendering. The hash must be computed from the canonical embedded builtin bytes used for run-time reconstruction, not from a mutable host file. Builtin registry entries must be sorted by name for deterministic JSON.

### Resolution order

For any command that resolves a profile by name for inspection or session creation:

1. Call `findConfig("")` unless an explicit config path argument was provided.
2. If no `safeslop.cue` is found, resolve builtin by name.
3. If a `safeslop.cue` is found, load it for inspection. If loading/parsing fails, return the schema/io error and **do not** fall back to builtin.
4. If the loaded project config contains the name, use the project profile (and existing trust behavior for session creation).
5. If the loaded project config does not contain the name, resolve builtin by name.
6. If no builtin exists, return the existing not-found error.

Project names always win. Builtin fallback is allowed only after a project file is absent or successfully parsed and shown not to contain the profile.

### JSON provenance

Every JSON surface that returns a resolved project-or-builtin profile must include:

```json
{
  "profile_source": "project" | "builtin",
  "profile_name": "pi",
  "policy_path": "/abs/path/safeslop.cue" | "builtin:pi",
  "policy_hash": "sha256..."
}
```

For project profiles, `policy_path`/`policy_hash` are the trusted file path/hash already used by `createSessionFromProfile`. For builtin profiles, `policy_path` is the pseudo-path `builtin:<name>` and `policy_hash` is the builtin registry hash.

`profile show <name> --output json` should return the same resolved package/recipe/risk envelope as today plus provenance. `profile defaults --output json` should list builtin profiles with provenance and profile data under:

```json
{
  "profiles": [
    {
      "name": "pi",
      "description": "...",
      "profile_source": "builtin",
      "policy_path": "builtin:pi",
      "policy_hash": "...",
      "profile": { ... }
    }
  ]
}
```

`profile presets --output json` remains the scaffold-template contract and must not change its existing 5 rows unless a separate preset spec changes them.

### Session records and run-time reconstruction

Extend `internal/engine/session.Session` with profile source/hash fields while preserving backward compatibility:

```go
ProfileSource string `json:"profile_source,omitempty"` // "project" or "builtin" for profile-backed sessions
```

Use existing `PolicyPath`/`PolicyHash` for both sources:

- project: absolute canonical file path + trusted file hash;
- builtin: `builtin:<name>` + builtin registry hash.

Update `sessionData` to expose `profile_source`, `policy_path`, and `policy_hash` for profile-backed sessions. Existing legacy sessions without `ProfileSource` keep working: if `PolicyPath` is a real path, treat as project; if empty, treat as ad-hoc.

`verifySessionTrust(sess)` changes:

- if `PolicyPath == ""`: ad-hoc, keep existing no-op;
- if `PolicyPath` starts with `builtin:`: look up the builtin by `sess.Profile`, compare builtin hash to `sess.PolicyHash`, and fail closed on missing/mismatch;
- otherwise keep existing project trust check unchanged.

`sessionProfile(sess)` changes similarly:

- builtin pseudo-path: reconstruct from `policy.BuiltinProfileByName(sess.Profile)`, verify hash matches, set `Workspace` from `sess.Workspace`, and apply stored `Resolved.IdentitySet`/`BareAgent` behavior exactly like project sessions;
- project path: existing re-read/parse/hash behavior unchanged;
- ad-hoc: existing synthetic profile unchanged.

### Trust model

Builtin profiles are trusted as part of the signed binary's embedded registry; they do not require `safeslop trust` because there is no repo-authored policy file to approve. This does **not** weaken project policy trust: if a project profile is selected, existing trust gates apply. Builtin hash mismatch at run/supervise time fails closed so a session created under one builtin contract cannot silently run under another.

When host projection lands, projection contents remain live host filesystem state and are not content-pinned by `policy_hash`; that distinction belongs to the projection JSON from specs/0096 T1.

## Tasks

- [x] T1 — Add builtin registry API and tests
  FILE:     `internal/engine/policy/builtins.go`, `internal/engine/policy/builtins_test.go`, optional `internal/engine/policy/builtins/*.cue`
  CHANGE:   Add `BuiltinProfile`, `BuiltinProfiles`, and `BuiltinProfileByName`. Validate embedded builtin bytes via `LoadBytes` when CUE-backed. Sort output by name. Compute stable builtin hashes from canonical embedded bytes. Add placeholder/minimal builtins only if needed for this resolution slice; the final contained-hybrid profile bodies land in specs/0096 T7 after projection and session grants exist.
  VERIFY:   `go test ./internal/engine/policy -run 'Builtin|Presets' -v`
  EXPECTED: Builtins validate, resolve, sort deterministically, carry non-empty descriptions/hashes, and existing preset tests still pass unchanged.

- [ ] T2 — Add profile defaults and profile show fallback
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_profile_test.go`
  CHANGE:   Add `profile defaults --output json`. Refactor `cmdProfileShow` through a shared resolver that returns project-or-builtin profile data plus provenance. Keep invalid project config fail-closed. Keep project-over-builtin precedence. Keep `profile presets` unchanged.
  VERIFY:   `go test ./internal/cli -run 'Profile(Default|Show|Presets|List)' -v`
  EXPECTED: `profile defaults` lists builtins with `profile_source:"builtin"`; `profile show pi` works from a temp dir with no `safeslop.cue`; a project `pi` overrides builtin `pi`; an invalid local `safeslop.cue` blocks fallback; `profile presets` still returns the existing scaffold rows.

- [ ] T3 — Add builtin-aware session create
  FILE:     `internal/cli/cli.go`, `internal/cli/cli_session_test.go`, `internal/cli/cli_trust_session_test.go`
  CHANGE:   Refactor `createSessionFromProfile` into project/builtin resolution. Project path keeps trust status check and records absolute `PolicyPath` + file hash. Builtin path skips `safeslop.cue` trust, records `ProfileSource:"builtin"`, `PolicyPath:"builtin:<name>"`, and builtin hash. Workspace defaults to current cwd for builtins with no workspace. Resolved packages/recipe metadata are recorded exactly as for project profiles.
  VERIFY:   `go test ./internal/cli -run 'SessionCreate.*(Builtin|Profile)|TrustSession|SessionCreateFromProfile' -v`
  EXPECTED: `session create --profile pi --output json` succeeds from a temp dir with no `safeslop.cue`; project `pi` still requires trust and overrides builtin; invalid project config blocks fallback; JSON includes profile provenance/path/hash; unresolved builtin package errors surface as invalid-argument, not panic.

- [ ] T4 — Reconstruct builtin sessions at run/supervise time and fail closed on drift
  FILE:     `internal/cli/cli.go`, `internal/cli/supervise.go`, `internal/cli/cli_session_profile_test.go`, `internal/engine/session/session.go`, `internal/engine/session/session_test.go`
  CHANGE:   Extend session record/source fields, `sessionData`, `verifySessionTrust`, and `sessionProfile` for `builtin:<name>`. Preserve existing project re-read/hash/trust behavior. Preserve legacy/ad-hoc sessions. Add a test seam to simulate builtin hash drift and prove run-time fail-closed behavior.
  VERIFY:   `go test ./internal/cli ./internal/engine/session -run 'Builtin|SessionProfile|VerifySessionTrust|SessionData|Legacy' -v`
  EXPECTED: Builtin sessions reconstruct from embedded registry; missing/mismatched builtin hash fails closed at run/supervise; project sessions still re-read trusted policy bytes; legacy records without `ProfileSource` still load.

- [ ] T5 — Docs, help, and final verification
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `internal/cli/cli_help_iw3_test.go`, `specs/0098-builtin-profile-resolution.md`
  CHANGE:   Document builtin defaults vs scaffold presets, project-over-builtin precedence, cwd-independent launch examples, invalid-config fail-closed behavior, and provenance fields. Update CLI help tests for `profile defaults` and any wording change.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Docs/help match real commands; full repository checks and build pass.
