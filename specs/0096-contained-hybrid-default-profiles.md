# 0096 — Contained-hybrid builtin default profiles

Status: complete
Date: 2026-07-12

SCOPE: deliver binary-embedded, cwd-independent default profiles named `pi`, `claude`, `fish`, and `zsh` that launch as contained-hybrid container sessions: curated devtools, deny-by-default/progressive network authority, and read-only allowlist-style host config/home projection. This spec is the umbrella DAG: it pins the product contract, sequences the required safety/capability gates, and makes builtin profile delivery the last mile after mount and network contracts exist.

OFF-LIMITS: do not mount all of `$HOME`; do not expose host credential directories or credential-bearing config (`~/.ssh`, cloud/kube/docker/npm credentials, browser/keychain/cookie state, safeslop account links/stage/cache dirs); do not weaken `network: "deny"`, IP-literal/private/metadata hard denies, policy-byte trust, host consent, host-helper shadow refusal, or credential value-free guarantees; do not represent host networking as enforceably isolated; do not add agent-triggered modal network prompts; do not mutate `profile.egress` for temporary/session network grants; do not make builtin profiles shadow project profiles silently without provenance.

WORKTREE: `.worktrees/0096-contained-hybrid-default-profiles/`

## Confirmed direction

- Routing is **contained-hybrid**, not host-unconfined.
- Safe host file access is **allowlist-style projection**, not "mount all home minus unsafe".
- Progressive network follows `specs/0089-network-authority-ayo-flo.md`: container `network:"deny"` plus operator-invoked, session-scoped exact FQDN:port grants. There is no new `network:"progressive"` schema value in this slice.
- Builtin defaults must be embedded in the signed Go binary and usable from any working directory.

## Product contract

### Builtin defaults

Builtin default names are reserved fallback names, not hard overrides:

| name | agent | environment | network | tools | projected host config |
|---|---|---|---|---|---|
| `pi` | `pi` | `container` | `deny` + session grants | `personal` bundle + agent default bundle | pi config projection + safe home projection |
| `claude` | `claude` | `container` | `deny` + session grants | `personal` bundle + agent default bundle | pi/agent config projection + safe home projection |
| `fish` | `fish` | `container` | `deny` + session grants | `personal` bundle | shell config projection + safe home projection |
| `zsh` | `zsh` | `container` | `deny` + session grants | `personal` bundle | shell config projection + safe home projection |

`personal` is the existing daily-driver bundle (`ripgrep`, `fd`, `bat`, `eza`, `fzf`, `zoxide`, `yq`, Node/Python/Go/Rust toolchains, `hyperfine`, `tokei`, `sccache`). Its binary inputs are pinned by version, per-architecture artifact URL, and SHA256; its apt leaf is exact and inherits the immutable signed Debian snapshot. Explicit image handlers verify every selected artifact before installation. If a later catalog decision adds a `devtools` bundle, these defaults may migrate only through a separate catalog/spec update.

### Cwd-independent resolution

For commands that resolve a profile for inspection or session creation:

1. If a project `safeslop.cue` is found and contains the requested profile, use the project profile.
2. Otherwise, if the requested name exists in the embedded builtin registry, use that builtin profile.
3. Otherwise, return the existing not-found error.

Every JSON surface that can return a resolved profile must include provenance:

```json
{
  "profile_source": "project" | "builtin",
  "profile_name": "pi",
  "policy_path": "/path/to/safeslop.cue" | "builtin:pi",
  "policy_hash": "..."
}
```

Builtin profile hashes are computed from the canonical embedded builtin bytes and recorded on the session. Builtin sessions must be reconstructed from the embedded registry at `session run`/`session supervise`, and must fail closed if the embedded builtin hash no longer matches the create-time hash. Project profiles keep the existing file trust/re-read behavior from `specs/0073`.

Project profile names win over builtins so repos can override `pi`/`claude`/`fish`/`zsh` deliberately. Provenance must be visible so the operator can tell which one launched.

### Safe host projection contract (validated by T1)

The projection is read-only and allowlist-only. Spec 0107 supersedes the MVP's direct source mounts: on macOS/Linux the engine walks from a pinned approved-root descriptor, accepts only relative symlinks that remain inside that root, verifies exclusions/type/identity/mount identity, and copies bytes into an atomically published private per-session `0700` snapshot. Only snapshot files are mapped at `/safeslop/projected/<id>:ro`, recorded in `/safeslop/runtime/projection.json`, and copied into `/home/agent` tmpfs. The live original/resolved host pathname is never mounted or reopened after validation. Absolute or escaping links, excluded targets, nested links, loops, special files, mount crossings, and concurrent changes fail closed; unsupported platforms have no pathname fallback. Workspace remains the only read-write host mount. See `specs/research/2026-07-12-safe-home-projection-flo.md` and the superseding `specs/research/2026-07-16-symlinked-projection-flo.md`.

Initial allowlist:

- pi/agent config:
  - `~/.pi/agent/AGENTS.md` (required file)
  - `~/.pi/agent/skills/` (optional directory; shared instruction/code corpus)
- fish config:
  - `~/.config/fish/config.fish` (optional file)
  - `~/.config/fish/conf.d/*.fish` (optional glob)
  - `~/.config/fish/functions/*.fish` (optional glob)
  - `~/.config/fish/completions/*.fish` (optional glob)
- zsh/shell config:
  - `~/.zshrc` (optional file)
  - `~/.zprofile` (optional file)
  - `~/.zshenv` (optional file)
  - `~/.config/starship.toml` (optional file)

Explicit MVP exclusions:

- all of `$HOME` as a broad mount;
- `~/.ssh/`, `~/.aws/`, `~/.kube/`, `~/.docker/`, `~/.gnupg/`, `~/.config/gcloud/`, `~/.config/safeslop/`;
- `~/.npmrc`, `~/.pypirc`, `~/.cargo/credentials*`;
- `~/.config/fish/fish_variables` (mutable fish state that may carry surprising values);
- browser/cookie/keychain dirs;
- `~/Library/Caches/safeslop/`, `~/.cache/safeslop/`;
- raw `.gitconfig` / `.config/git` projection in MVP; future support must synthesize a safeslop-owned safe gitconfig subset.

### Progressive network contract

Builtin defaults keep `network:"deny"`. Progressive network is session overlay state from `specs/0089`, not profile policy:

- grants are exact proxy-observed FQDN:port values;
- grants are operator-invoked, non-modal, and session-scoped;
- grants never mutate `profile.egress`;
- proxy reload/update failure preserves the more restrictive state;
- IP literals/private/metadata/broker/mint destinations are non-grantable.

## DAG

```text
A  Contract checkpoint (this spec)
├─ B  Safe host projection ayo-FLO + decision note
├─ C  Progressive network/session grants implementation spec from 0089
└─ D  Embedded builtin profile resolution spec details
B,C,D → E  Implement mount + network + builtin resolution contracts/tests
E     → F  Add pi/claude/fish/zsh builtin defaults
F     → G  Docs/skills/tests + make check/build
```

## Tasks

- [x] T1 — Run ayo-FLO for safe host projection and freeze the mount capability model
  FILE:     `specs/research/2026-07-12-safe-home-projection-ayo.md`, `specs/research/2026-07-12-safe-home-projection-flo.md`, `specs/0096-contained-hybrid-default-profiles.md`
  CHANGE:   Mine prior art and adversarially evaluate the allowlist-style host projection model. The decision must explicitly accept/reject: read-only projection, source allowlist, opaque non-home staging paths, copy-only entrypoint strategy, absent/unreadable-path handling, macOS/Linux path differences, `.gitconfig`/git config exclusion, builtin-profile trust/provenance implications, and whether `~/.pi/agent/skills` is safe to project into `claude` as well as `pi`. Update this spec's contract if the decision changes field names, allowlist contents, or sequencing.
  VERIFY:   `test -s specs/research/2026-07-12-safe-home-projection-flo.md && rg -n "Verdict|Rejected|MVP|allowlist|credential" specs/research/2026-07-12-safe-home-projection-flo.md`
  EXPECTED: The FLO note exists, contains a clear verdict, names rejected alternatives, and either approves this spec's allowlist-projection MVP or blocks implementation with explicit required changes.

- [x] T2 — Write the progressive network/session-grants implementation spec from 0089
  FILE:     `specs/0097-progressive-network-session-grants.md`, `specs/0089-network-authority-ayo-flo.md`
  CHANGE:   Turn 0089's decision into an implementation spec naming typed grant data, session storage/audit fields, proxy overlay materialization, deny-observation channel, CLI JSON contracts, Emacs/UI hooks, and hermetic tests. Keep `#Network` as `"deny" | "allow"`; do not add `network:"ask"` or `network:"progressive"`.
  VERIFY:   `test -s specs/0097-progressive-network-session-grants.md && rg -n "exact FQDN:port|session-scoped|profile.egress|fail closed|IP literal|VERIFY" specs/0097-progressive-network-session-grants.md`
  EXPECTED: The spec exists and explicitly carries forward 0089's hard laws, exact-destination grant model, fail-closed overlay behavior, no profile mutation, and real verification commands.

- [x] T3 — Specify embedded builtin profile resolution and provenance
  FILE:     `specs/0098-builtin-profile-resolution.md`, `internal/cli/cli.go`, `internal/engine/policy/presets.go`, `internal/engine/session/session.go`
  CHANGE:   Write a focused implementation spec for binary-embedded launchable defaults distinct from scaffold presets. It must pin project-over-builtin precedence, cwd-independent `session create --profile <builtin>`, `profile show <builtin>`, an introspection command (`profile defaults --output json` unless the spec chooses to extend `profile presets`), session record provenance/hash fields, and run-time builtin reconstruction/fail-closed behavior.
  VERIFY:   `test -s specs/0098-builtin-profile-resolution.md && rg -n "project.*builtin|profile_source|builtin:pi|session create --profile pi|profile defaults|fail closed|VERIFY" specs/0098-builtin-profile-resolution.md`
  EXPECTED: The spec exists, pins the JSON/provenance contract, and names exact CLI/session/policy files plus tests before implementation starts.

- [x] T4 — Implement policy/schema/session support for safe host projection
  FILE:     `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy.go`, `internal/engine/policy/policy_test.go`, `internal/engine/container/compose.go`, `internal/engine/container/assets/compose.yml.tmpl`, `internal/engine/container/assets/entrypoint.sh`, `internal/engine/container/compose_test.go`, `internal/engine/container/launch_test.go`, `internal/engine/policy/risk.go`
  CHANGE:   After T1 approves the model, add the exact profile fields and runtime projection plumbing chosen by T1. Render only allowlisted read-only host sources under opaque `/safeslop/projected/<id>` staging paths, preserve `/home/agent` tmpfs from `specs/0064`, copy approved config into the ephemeral home at entrypoint time, surface the added file authority in risk summaries, and hard-reject broad home/credential-dir projection attempts.
  VERIFY:   `go test ./internal/engine/policy ./internal/engine/container -run 'Projection|HostProjection|SafeHome|Risk' -v && make check-assets`
  EXPECTED: Unit tests prove schema decode/rejects, credential-dir exclusions, read-only mount rendering, absent-source behavior, tmpfs-home preservation, and risk-axis text; mirrored container assets stay in sync.

- [x] T5 — Implement progressive session grants
  FILE:     `internal/engine/container/policy.go`, `internal/engine/container/compose.go`, `internal/engine/container/assets/squid.conf.tmpl`, `internal/engine/session/session.go`, `internal/cli/cli.go`, `internal/cli/cli_session_test.go`, `emacs/safeslop-session.el`, `emacs/test/safeslop-test.el`
  CHANGE:   Execute `specs/0097-progressive-network-session-grants.md`: store value-free session grant records, materialize grants into the proxy allow overlay, expose list/grant/revoke/status JSON, preserve fail-closed behavior on overlay write/reload failure, and add UI hooks without agent-triggered modal prompts.
  VERIFY:   `go test ./internal/engine/container ./internal/engine/session ./internal/cli -run 'SessionGrant|Progressive|EgressGrant|IPLiteral|ProfileEgress' -v && make test-emacs EMACS=$(command -v emacs)`
  EXPECTED: Tests prove exact FQDN:port grants work only for container deny sessions, IP/private/metadata destinations remain denied and non-grantable, grants are session-scoped and revocable, profile CUE is not mutated, failure preserves deny, and Emacs surfaces non-modal controls.

- [x] T6 — Implement binary-embedded builtin defaults and cwd-independent profile fallback
  FILE:     `internal/engine/policy/builtins.go`, `internal/engine/policy/builtins_test.go`, `internal/cli/cli.go`, `internal/cli/cli_profile_test.go`, `internal/cli/cli_session_test.go`, `internal/engine/session/session.go`, `internal/engine/session/session_test.go`
  CHANGE:   Execute `specs/0098-builtin-profile-resolution.md`: add embedded launchable builtin profiles for `pi`, `claude`, `fish`, and `zsh`; resolve project profiles before builtins; allow `session create --profile <builtin> --output json` from a directory with no `safeslop.cue`; include provenance/hash in JSON and session records; reconstruct builtin profiles at run/supervise time and fail closed if the builtin hash changed.
  VERIFY:   `go test ./internal/engine/policy ./internal/engine/session ./internal/cli -run 'Builtin|ProfileDefaults|SessionCreate.*Profile|ProfileShow' -v`
  EXPECTED: Tests prove cwd-independent builtin launch, project-over-builtin precedence, JSON provenance, session hash recording, run-time reconstruction, changed-hash fail-closed behavior, and no regression to existing `profile presets` scaffold contract.

- [x] T7 — Add the four contained-hybrid defaults
  FILE:     `internal/engine/policy/builtins.go` or `internal/engine/policy/builtins/*.cue`, `internal/engine/policy/builtins_test.go`, `README.md`, `skills/agent-sandbox-ops/SKILL.md`
  CHANGE:   Define builtins `pi`, `claude`, `fish`, `zsh` with `environment:"container"`, `network:"deny"`, `bundles:["personal"]`, default agent bundle enabled, progressive-session-grant capability enabled by the T5 contract, and safe projection selections approved by T1. Document that these are launchable defaults, distinct from scaffold presets, and that project `safeslop.cue` profiles with the same names override them.
  VERIFY:   `~/.local/bin/safeslop profile defaults --output json >/tmp/safeslop-defaults.json && python3 - <<'PY'
import json
j=json.load(open('/tmp/safeslop-defaults.json'))
rows={r['name']:r for r in j['data']['profiles']}
for n,a in [('pi','pi'),('claude','claude'),('fish','fish'),('zsh','zsh')]:
    r=rows[n]
    assert r['profile']['agent']==a
    assert r['profile']['environment']=='container'
    assert r['profile']['network']=='deny'
    assert 'personal' in r['profile'].get('bundles', [])
print('ok')
PY`
  EXPECTED: Installed CLI lists all four builtin defaults with the expected agent/container/deny/personal contract and provenance.

- [x] T8 — Docs, skills, and final verification
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0096-contained-hybrid-default-profiles.md`, related `specs/0097*`/`0098*` implementation specs
  CHANGE:   Update operator docs and skills to describe builtin defaults, project override precedence, safe projection boundaries, progressive grant controls, and exact commands from any directory. Mark specs complete only after their stated verification passes.
  VERIFY:   `git diff --check && make check && make build`
  EXPECTED: Whitespace check, full repository gates, and build all pass; docs/skills match real CLI behavior and safety defaults.
