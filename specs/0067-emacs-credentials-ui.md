# 0067 — Emacs credentials UI (ephemeral creds, legible before launch)

**Goal:** Give the Emacs cockpit a third surface — **Credentials** — that makes a
workspace's credential posture *legible and verifiable before launch*: for every
profile in the active `safeslop.cue`, show which secrets/credentials it will stage,
from which source ref, whether they are **ephemeral** (minted per-session) or
**ref-backed** (resolved from `op://`/`env:`), and — for the ref-backed ones — whether
they **resolve right now**. This directly serves safeslop's north star: work safely on
projects using *ephemeral credentials* and *limited file/network access*. You should be
able to answer "are my creds wired and safe?" at a glance, without launching and without
ever seeing a secret value.

**North-star tie-in:** ephemeral-first. The surface's job is to make the *ephemerality*
and *resolvability* of credentials visible; it is not a secret vault or a value editor.

## Non-goals (explicitly deferred)

- **No secret-value entry or display.** The UI edits/reads *refs* (`op://…`, `env:NAME`),
  never values. Value custody stays in 1Password / the environment. (Frozen law, below.)
- **No standalone mint/revoke.** Ephemeral deploy keys (ssh/forgejo) are minted at
  session/run start into a per-session stage dir and revoked+wiped on stop — they have no
  meaningful life outside a session, so there is no "mint a key now" button. The surface
  *shows* that they are ephemeral and their declared scope; the lifecycle stays owned by
  `run`/`session`.
- **No in-UI CUE rewrite.** Authoring stays CUE-canonical (specs/0029), mirroring the
  Profiles surface: `e` opens `safeslop.cue` anchored at the credentials block; validation
  is on save. No machine rewrite of the guard.
- **Dynamic ok/deny on access** (interactive egress/file approval, specs/0048) is future.
- **No new engine credential providers.** This is a *view* over the existing
  `internal/engine/creds` + `internal/engine/secrets` model.

## Security posture (committed decision note — read before executing)

This feature is additive UI over an already-frozen security boundary; it introduces no new
hard/irreversible design call, so it does **not** require an ayo→FLO. It does make three
boundary commitments explicit, recorded here as the decision note:

1. **Refs, not values (frozen).** The credential model already stores tokens/PATs as secret
   *refs* (`op://`/`env:`), never values (`policy.SshCreds.Pat`, `ForgejoCreds.Token/Pat`,
   `PnpmRegistry.Token`, `Profile.Secrets`). The UI upholds this: it displays and edits refs
   only. Choosing the conservative side of an already-settled law — not a new call.

2. **Resolvability probe discards the value.** Status is computed by attempting resolution
   (`secrets.Resolve`) and keeping only `err == nil`/`!= nil` — **the resolved value is
   discarded immediately and never returned, logged, printed, or emitted in any envelope.**
   The engine `Inspect` API returns a status enum, not a value; there is no code path from a
   resolved value to output. This is the load-bearing redaction boundary of this feature.

3. **op-state fan-out is cheap and value-free.** When `op` is unavailable/signed-out we
   report `op-unavailable`/`op-signed-out` *without* attempting per-ref resolution (no `op`
   calls, no values touched). Only `env:` refs and — when `op` is ready — `op://` refs are
   probed.

The client debug log already only records allowlisted non-secret fields
(`safeslop-client.el`), and safeslop never passes secret values as argv, so the CLI surface
of this feature is safe to log.

## Architecture

```
policy.Load(safeslop.cue)  ─┐
                            ├─▶ creds.Inspect(ctx, cfg, Prober)  ── pure enumeration + value-free probe
secrets.{OpAvailable,       │        │  []CredRow{Profile,Kind,Name,Scope,Ref,Status}
  OpSignedIn,Resolve} ──────┘        ▼
                             cli `creds list|show --output json`  ── jsoncontract.OK(data)
                                     │  envelope (existing v1 codes; no new codes)
                                     ▼
                             safeslop-credentials.el  ── Credentials "C" surface
                                     │  tabulated-list (Profile│Kind│Name│Source│Status) + detail
                                     ▼
                             registry row in safeslop-surface--order  (Sessions│Profiles│Credentials)
```

**Tech stack:** Go (`internal/engine/creds`, `internal/cli`, `internal/jsoncontract`),
Emacs Lisp (`emacs/*.el`), CUE (`safeslop.cue` unchanged in shape). No new deps, no `.proto`
change, no new error codes.

**Base branch:** `feat/emacs-credentials-ui` off `main` (@ `d787655`). Never push `main`.

## Status semantics

Each declared credential becomes one row. `Status` is one of:

| Status | Meaning | Applies to |
|---|---|---|
| `resolvable`     | ref resolves now (value discarded)          | `env:`/`op://` refs (secrets, pnpm.token, ssh.pat, forgejo.token/pat) |
| `missing`        | `env:` var unset, or `op://` item not found | ref-backed |
| `op-signed-out`  | `op://` ref but 1Password not signed in     | `op://` refs |
| `op-unavailable` | `op://` ref but `op` CLI not installed      | `op://` refs |
| `ephemeral`      | minted per-session; no static ref to probe  | ssh/forgejo deploy-key mode |
| `ambient`        | uses host ambient auth (SSO/ADC/cloud)      | aws, gcp, kube |

`ephemeral`/`ambient` are *honest non-probes* — we don't fake a resolvability check for a
key that doesn't exist yet or for cloud auth that's validated only at stage time.

## Wire shape (`creds list`)

```json
{ "schema": 1, "ok": true, "data": {
    "config": "/abs/path/safeslop.cue",
    "op": { "available": true, "signedIn": true },
    "credentials": [
      {"profile":"pi","kind":"secret","name":"ANTHROPIC_API_KEY","scope":"","ref":"op://vault/ai/key","status":"resolvable"},
      {"profile":"pi","kind":"ssh","name":"origin","scope":"deploy-key ro","ref":"","status":"ephemeral"},
      {"profile":"ci","kind":"pnpm","name":"npm.pkg.github.com","scope":"@acme","ref":"env:NPM_TOKEN","status":"missing"},
      {"profile":"ci","kind":"aws","name":"acme-sso","scope":"eu-west-1","ref":"","status":"ambient"}
    ] } }
```

`creds show <profile>` returns the same `credentials[]` filtered to one profile plus the
raw declared shape (already-safe: refs only) for the detail view. Errors use existing codes:
`NOT_FOUND` (no config / no profile), `SCHEMA_VIOLATION` (bad cue), `INVALID_ARGUMENT`.

## DAG (8 nodes, 6 waves)

- **W1 S** — this spec (done on write) + decision note above.
- **W2 CON** — golden fixtures + `safeslop-contract.el` mirror for the `creds` envelope shape.
- **W2 ENG** — `internal/engine/creds/inspect.go`: `Prober`, `CredRow`, `Inspect(ctx,cfg,Prober)`;
  value-free probe; hermetic tests with a fake prober.
- **W3 CLI** — `safeslop creds list|show --output json` (parent `cmdCreds()` sibling to
  `cmdProfile()`), wired to `creds.Inspect`, emitting the envelope; CLI tests.
- **W4 SURF** — `emacs/safeslop-credentials.el`: `safeslop-credentials-mode`, buffer,
  tabulated-list (Profile│Kind│Name│Source│Status, Status colored by class), header/legend,
  async fetch over `creds list`; register `Credentials "C"` in `safeslop-surface--order`
  (+ `safeslop-surface--current-sym`).
- **W5 ACT** — keybindings: `RET`/`i` inspect detail (`creds show`), `e` edit (jump to the
  `credentials:` block in `safeslop.cue`), `g` refresh, `s` re-probe status; doom/evil
  bindings in `safeslop-doom.el`; empty-state guidance.
- **W6 DOC** — README credentials section + skill(s) under `skills/`; **TST** — full
  `make check` + `make build`, ERT for the surface, golden contract tests.

## Design decisions

1. **Read + status + jump-to-edit, not a vault.** Mirrors the Profiles surface exactly
   (CUE-canonical authoring, async fetch, shared surface engine). The one novel capability is
   the **value-free resolvability probe**, which is the feature's real payload.

2. **Enumerate across all profiles.** A workspace's credentials live inside profiles; the
   surface aggregates them (Profile column) rather than inventing a global store. `show`
   scopes to one profile for the detail view.

3. **Prober is injected.** `Inspect` takes a `Prober` (op-available/op-signed-in/lookup-env/
   resolve-op funcs) so tests are hermetic (no `op`, no live env). Production wires it to
   `secrets.*`. Mirrors the codebase's function-seam style (`sessionRevokeCredentials`).

4. **op state computed once.** `OpAvailable`/`OpSignedIn` are evaluated once per `Inspect`;
   per-ref probing short-circuits on op-down. Keeps the surface fast and value-free when op
   is unavailable.

5. **No new error codes / no `.proto` change.** The append-only registry is untouched;
   existing `NOT_FOUND`/`SCHEMA_VIOLATION`/`INVALID_ARGUMENT`/`AUTH_REQUIRED` cover it.

## Verification (what "done" means)

- `make check` + `make build` green on the branch.
- `creds.Inspect` unit tests: env-ref resolvable/missing; op-unavailable and op-signed-out
  short-circuit (no resolve attempt); ssh/forgejo deploy-key → `ephemeral`; aws/gcp/kube →
  `ambient`; **a resolved value never appears in any `CredRow`**.
- `creds list|show` golden contract tests pass on both Go and Emacs sides.
- ERT: `Credentials "C"` appears in the surface order and tab strip; the surface builds rows
  from a fixture envelope; `e` resolves the `credentials:` block location.
- Live smoke (documented, not asserted in CI): against a real `safeslop.cue`, `creds list`
  shows `resolvable`/`missing`/`ephemeral`/`ambient` correctly and reveals no values.

## Deliberately deferred

- Standalone ephemeral-key mint/revoke UI (doesn't fit the session-scoped lifecycle).
- In-UI secret-value or 1Password-item creation (would move value custody into the UI — a
  real security-boundary change that *would* need an ayo→FLO first).
- Dynamic per-access ok/deny (egress/file approval, specs/0048).
- Live session credential introspection (what's currently staged/active for a *running*
  session) — a natural second wave once this read-only view lands.
