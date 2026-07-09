# 0090 — Credential connection + repository picker

Status: planned
Date: 2026-07-09
Follows: `specs/0087-product-activation.md` track 2; builds on `specs/0067-emacs-credentials-ui.md`, `specs/0068-forge-account-ephemeral-creds-flo.md`, and `specs/0069-forge-account-creds-p1.md`.

SCOPE: make credential setup actionable from the CLI/Emacs cockpit: value-free account-link status, operator-driven GitHub/Forgejo linking, and a repository/scope picker that writes `credentials.github` or `credentials.forgejo` into an existing profile without hand-editing CUE.

OFF-LIMITS: no secret-value entry/display/storage; no live forge calls in tests; no sandbox-side mint/renew; no standalone credential mint UI; no GitHub live repository discovery in this slice because listing installation repositories requires an installation token outside the session-owned lifecycle settled by `specs/0068`; no Forgejo account-wide API staging; no weakening `network: "deny"`, trust gates, host consent, or one-forge-per-profile staging.

WORKTREE: `.worktrees/0090-credential-connection-repo-picker/`

## Problem

The Credentials surface can show declared credential posture, but activation still requires leaving Emacs and hand-writing CUE for the two hardest steps: linking forge accounts and declaring which repositories receive read/write credentials.

## Success criteria

- Emacs shows linked GitHub/Forgejo accounts and their value-free readiness without exposing secret values.
- Emacs can link/unlink GitHub App and Forgejo account refs by shelling out to existing CLI verbs; prompts collect refs/ids only, never token/key values.
- A profile can be updated through a structured repo picker: choose GitHub or Forgejo, choose explicit `owner/repo` entries and per-repo read/write, or choose the existing origin-inference mode.
- The write path goes through a tested CLI contract, not ad-hoc Emacs CUE edits.
- Existing profile fields are preserved; setting one forge clears the other forge only, because staging currently supports one forge per profile.
- All new tests are hermetic: fake CLI envelopes / fake forge HTTP seams / no real GitHub, Forgejo, 1Password, Docker, or network.
- Documentation and `skills/` match the new command/UI surface.

## Chosen design

Recommended approach: **CLI-owned profile credential mutation + Emacs orchestration**.

- The CLI owns CUE mutation and validation via a new `profile credentials set|clear` command group.
- Emacs owns the interaction loop: account link prompts, account status display, and checkbox-like repo/scope selection.
- Repository candidates are conservative in this slice: existing declared repos, an explicit `origin inference` option, and manual `owner/repo` additions. Live forge repository discovery is deferred until there is an explicit design for host-side discovery tokens that does not violate `specs/0068`'s session-owned lifecycle.

Rejected alternatives:

1. **Only add Emacs wrappers around `creds link` and keep repo editing manual.** Too small; it leaves the activation blocker intact.
2. **Have Emacs rewrite `safeslop.cue` directly.** Duplicates renderer/schema rules and risks corrupting hand-edited policy; CUE remains canonical but mutation should be engine-owned.
3. **Live repo discovery now.** GitHub listing requires a minted installation token; Forgejo listing uses the account-wide token. That may be product-worthy later, but it is a credential lifecycle decision and does not belong in this activation slice.

## Contract

### Account status JSON

Add an Emacs-friendly shared contract form for account links:

```text
safeslop creds status --output json
```

Response envelope data:

```json
{
  "links": [
    {"forge":"github", "host":"github.com", "owner":"acme", "appID":123, "installationID":456, "probe":"ok", "ttl":"1h-renewable"},
    {"forge":"forgejo", "host":"forgejo.example.com", "owner":"acme", "sshPort":2222, "probe":"secret-unresolved", "ttl":"account-wide token"}
  ]
}
```

Rules:

- `probe` is a value-free class only: `ok`, `secret-unresolved`, `unreachable`, `denied`, or `error`.
- No `privateKeyRef`, `tokenRef`, token value, key value, staged path, or HTTP body is emitted.
- Keep the existing human `creds status` output and the current raw `--json` output unchanged for compatibility; new Emacs code must use `--output json`.

### Profile credential mutation CLI

Add:

```text
safeslop profile credentials set <profile> [safeslop.cue] --provider github [--use-origin] [--repo owner/name ...] [--write-repo owner/name ...] --output json
safeslop profile credentials set <profile> [safeslop.cue] --provider forgejo [--url https://host] [--ssh-port N] [--use-origin] [--repo owner/name ...] [--write-repo owner/name ...] --output json
safeslop profile credentials clear <profile> [safeslop.cue] --output json
```

Semantics:

- At least one of `--use-origin`, `--repo`, or `--write-repo` is required.
- `--use-origin` writes no `repos` entries and preserves the existing stage-time origin inference semantics.
- `--repo` writes read-only entries; `--write-repo` writes entries with `write: true`.
- Duplicate repos are rejected unless the access is identical; conflicting read/write declarations fail closed.
- Repo strings must pass the existing `[A-Za-z0-9._-]+/[A-Za-z0-9._-]+` validation before any CUE is rendered.
- GitHub writes `credentials.github` in app mode only; PAT authoring remains manual in this slice.
- Forgejo with explicit repos requires `--url`; origin-inference mode may omit it and use existing remote inference at stage time.
- Setting GitHub clears `credentials.forgejo`; setting Forgejo clears `credentials.github`; other credential providers (`pnpm`, `aws`, `gcp`, `kube`) and `secrets` are preserved.
- `clear` removes only `credentials.github` and `credentials.forgejo`, preserving other credential providers and deleting the `credentials` object if it becomes empty.
- The response is an envelope containing at least `path`, `name`, `profile`, and `credential_scopes` for the updated profile.

### Emacs UX

Extend `C-c s K` / `safeslop-credentials`:

- Header or side section shows linked accounts from `creds status --output json`.
- Keys:
  - `a` — link account: choose GitHub or Forgejo, prompt for non-secret ids/refs, run `creds link ...`, refresh.
  - `u` — unlink account: choose `host/owner`, confirm, run `creds unlink`, refresh.
  - `p` — pick repositories for a profile: choose profile and provider, edit the repo list/access, run `profile credentials set`, refresh Credentials and Profiles surfaces.
- Repo picker must show a value-free save summary before writing: profile, provider, origin-inference vs explicit repos, and any write repos highlighted.
- Empty-state guidance should direct users toward `a` for linking and `p` for declaring profile repo scopes.

## Tasks

- [ ] T1 — Add account-status JSON contract for Emacs
  FILE:     `internal/cli/creds_link.go`, `internal/cli/*test.go`, `emacs/safeslop-contract.el`, `emacs/test/safeslop-contract-test.el`
  CHANGE:   Add `creds status --output json` as a shared envelope whose `data.links` rows contain only forge, host, owner, non-secret ids, value-free probe class, ssh port, and TTL model. Preserve human output and the current raw `--json` output; Emacs must not depend on the raw form.
  VERIFY:   `go test ./internal/cli -run 'Creds(Status|Link|Unlink)' -v && emacs --batch -L emacs -l ert -l emacs/test/safeslop-contract-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-.*creds.*status")'`
  EXPECTED: command exits 0; JSON contract tests show `links: []` when empty, probe failures are per-row, and a fake token/key string never appears in output.

- [ ] T2 — Add profile forge-credential mutation CLI
  FILE:     `internal/cli/cli.go` or `internal/cli/profile_credentials.go`, `internal/cli/*profile*test.go`, `internal/engine/policy/policy.go` if pure helpers are needed
  CHANGE:   Add `safeslop profile credentials set|clear` with the contract above. Implement pure repo parsing/merge helpers; preserve all unrelated profile fields; enforce one forge per profile; reject malformed/duplicate/conflicting repos before rendering CUE; return an enveloped updated profile plus `credential_scopes`.
  VERIFY:   `go test ./internal/cli -run 'ProfileCredentials|CredentialScopes' -v`
  EXPECTED: command exits 0; tests cover github origin inference, github explicit ro/rw repos, forgejo explicit repos requiring URL, clear preserving pnpm/aws/gcp/kube/secrets, one-forge clearing, duplicate/conflict failures, and no secret/ref values in `credential_scopes`.

- [ ] T3 — Surface account links in Emacs Credentials
  FILE:     `emacs/safeslop-credentials.el`, `emacs/test/safeslop-credentials-test.el`, `emacs/test/safeslop-test.el`
  CHANGE:   Fetch `creds status --output json` alongside `creds list --output json` and render a value-free account-link section/header. Add `a` account-link and `u` unlink actions that call the existing CLI verbs with refs/ids only, show result envelopes, and refresh in place.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-credentials-.*\\(account\\|link\\|unlink\\)")'`
  EXPECTED: command exits 0; tests prove account rows render without refs/values, link/unlink argv are exact, fake token/key material is absent, and failed status fetch degrades without hiding existing credential rows.

- [ ] T4 — Add Emacs repository/scope picker
  FILE:     `emacs/safeslop-credentials.el`, `emacs/safeslop-profiles.el` if shared helpers are needed, `emacs/test/safeslop-credentials-test.el`, `emacs/test/safeslop-profiles-test.el`
  CHANGE:   Add `p` picker flow: choose profile, choose provider, choose origin inference or explicit repos, add/remove repos manually, toggle read/write, confirm a value-free summary, call `profile credentials set`, then refresh Credentials and Profiles buffers in place.
  VERIFY:   `emacs --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el --eval '(ert-run-tests-batch-and-exit "safeslop-test-credentials-.*\\(repo\\|picker\\|profile-credentials\\)")'`
  EXPECTED: command exits 0; tests prove exact argv for github/forgejo/origin/ro/rw cases, confirmation highlights write repos, cancellation aborts before CLI, and saved profiles refresh without popping an unrelated buffer.

- [ ] T5 — Docs and skills sync
  FILE:     `README.md`, `emacs/README.md`, `skills/agent-key-lifecycle/SKILL.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0087-product-activation.md`, `specs/0090-credential-connection-repo-picker.md`
  CHANGE:   Document account-link UI, `creds status --output json`, `profile credentials set|clear`, repo picker limitations, and the deliberate deferral of live forge repo discovery. Mark only this 0090 implementation complete after verification.
  VERIFY:   `rg -n 'profile credentials|creds status --output json|repo picker|GitHub App|Forgejo|account link|live repo discovery|0090' README.md emacs/README.md skills/agent-key-lifecycle/SKILL.md skills/agent-sandbox-ops/SKILL.md specs/0087-product-activation.md specs/0090-credential-connection-repo-picker.md`
  EXPECTED: output shows the new commands/UI and states that live repository discovery is deferred rather than implied.

- [ ] T6 — Full verification
  FILE:     whole repo
  CHANGE:   Run repository gates after code/docs are in sync.
  VERIFY:   `make check && make build`
  EXPECTED: command exits 0; Go tests, Emacs ERT suite, shell denylist gates, byte-compile, and build pass.

## Execution notes

Use TDD for T1–T4. Keep every test hermetic: CLI tests use fake account stores and forge HTTP seams; Emacs tests use fake envelopes/async stubs. Never call live GitHub, Forgejo, 1Password, Docker, or credential APIs in unit tests. Before implementation, if live repo discovery is reintroduced, stop and run a separate design decision because it changes credential mint/probe semantics outside session-owned lifecycle.
