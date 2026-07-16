# 2026-07-16 — Credentials panel journey baseline

Status: baseline captured; repair planned in spec 0111

## Method

A disposable batch harness opened the real `safeslop-credentials-mode`, resolved each displayed key through the active keymaps, and called the resolved interactive command with fake/value-free inputs. It ran in raw Emacs and the locally installed Evil/Doom shim. No safeslop CLI, secret manager, forge, account file, or network endpoint was called. Three isolated persona reviews then evaluated the trace against task completion, key truthfulness, context continuity, safety clarity, input burden, and recovery.

Journeys:

1. First-time raw Emacs user: valid project profile, no credentials; link GitHub App ref, assign origin, inspect.
2. Doom/Evil keyboard user: same journey from normal state.
3. Existing operator: change explicit read/write scopes, clear profile forge scopes, unlink obsolete account, recover from failure.

## Baseline trace

```text
TRACE scenario=raw-link-account key=a binding=safeslop-credentials-link-account outcome=task-action
TRACE scenario=raw-pick-repositories key=p binding=safeslop-credentials-pick-repositories outcome=task-action
TRACE scenario=raw-unlink-account key=u binding=safeslop-credentials-unlink-account outcome=task-action
TRACE scenario=empty-profile-candidates candidates=nil outcome=blocked
TRACE scenario=empty-guidance has-link=nil has-repos=nil
TRACE scenario=evil-link-account key=a binding=evil-append outcome=wrong-command
TRACE scenario=evil-pick-repositories key=p binding=evil-paste-after outcome=read-only-error
TRACE scenario=evil-unlink-account key=u binding=evil-undo outcome=read-only-error
```

## Review

| Severity | Journey defect | Functional/ergonomic consequence |
|---|---|---|
| Blocker | Profile candidates come only from credential rows. | A valid profile with no credentials—the first-run case—cannot be scoped through the picker. Linking an account does not create a row, so the advertised workflow remains blocked. |
| Blocker | Static legend advertises raw `a/p/u`, but Evil resolves them to edit commands. | Doom/Evil users cannot link, scope, or unlink from normal state; two keys fail against the read-only buffer. |
| High | Empty guidance advertises only manual CUE edit and refresh. | It hides the safer CLI-owned `link → repository scope` workflow required by spec 0090. |
| High | Scope editing starts blank/default-GitHub and replaces the whole forge declaration. | Existing untouched repos can be silently dropped; switching provider clears the other forge without showing the prior state. |
| High | No UI calls existing `profile credentials clear`. | Users may confuse account unlink with profile-scope removal, leaving broken declarations or affecting other profiles. |
| High | Account link mutates immediately and opens a generic success envelope. | Typos get no final value-free review, and success removes the user from the refreshed next-step context. |
| Medium | Failure loses the scope draft. | Correcting one malformed/conflicting repo requires repeating the whole prompt sequence. |
| Functional | Credential inspection renders a profile-level write flag for every repo instead of each `RepoCred.Write`. | Mixed read/write declarations can be displayed incorrectly and cannot safely seed an edit flow. |

Baseline completion scores (0 impossible, 1 severe workaround, 2 high friction, 3 workable, 4 clear, 5 fluent): raw first-run **1/5**, Evil first-run **0/5**, existing scope change/removal **1/5**, failure recovery **1/5**.

## Chosen repair

Use a vertical, trace-driven repair rather than rewriting the panel:

- universal visible keys `A` link, `U` unlink, `R` repository scopes, `X` clear profile forge scopes; retain raw lowercase aliases for compatibility; Evil binds the universal keys explicitly; show `g` vs `gr` truthfully;
- fetch existing project profiles through the existing `profile list --output json` contract when a profile action begins, rather than deriving candidates from credential rows or changing a public wire format;
- fetch `creds show` before scope prompts, prefill provider/mode/read/write rows, show a before/after replacement warning, and retain a value-free failed draft for retry;
- expose confirmed `profile credentials clear` separately from account unlink;
- add value-free account-link confirmation and keep successful mutations in the Credentials surface;
- fix per-repository access inspection in Go before using those rows as edit input;
- replay the same key journeys in raw, Evil, and Doom/Evil matrix slots.

Rejected: a new form/wizard framework (too large), key/docs-only repair (leaves first-run/removal/data bugs), adding profile names to the creds JSON wire (unnecessary public-surface expansion), live repository discovery (credential-lifecycle expansion), and live account/provider tests.

## Post-fix replay

The same raw/Evil key harness produced:

```text
TRACE scenario=raw-link-account key=A binding=safeslop-credentials-link-account outcome=task-action
TRACE scenario=raw-pick-repositories key=R binding=safeslop-credentials-pick-repositories outcome=task-action
TRACE scenario=raw-unlink-account key=U binding=safeslop-credentials-unlink-account outcome=task-action
TRACE scenario=raw-clear-profile-forge key=X binding=safeslop-credentials-clear-profile-forge outcome=task-action
TRACE scenario=raw-guidance link=true repos=true profile=true refresh=true
TRACE scenario=evil-link-account key=A binding=safeslop-credentials-link-account outcome=task-action
TRACE scenario=evil-pick-repositories key=R binding=safeslop-credentials-pick-repositories outcome=task-action
TRACE scenario=evil-unlink-account key=U binding=safeslop-credentials-unlink-account outcome=task-action
TRACE scenario=evil-clear-profile-forge key=X binding=safeslop-credentials-clear-profile-forge outcome=task-action
TRACE scenario=evil-guidance refresh=true
```

Seven hermetic journey tests then passed: first-run profile discovery with no credential rows, account confirmation/context, existing mixed-scope prefill and replacement warning, profile-only forge clear, value-free failed-draft retry, guidance, and universal key dispatch. The raw/Evil/Doom-Evil UI matrix resolves every displayed action. Mixed GitHub/Forgejo inspection now reports each repository's own read/write access.

Post-fix scores: raw first-run **5/5**, Evil first-run **5/5**, existing scope change/removal **4/5** (manual repo text remains because live discovery is deliberately deferred), failure recovery **4/5** (draft is retained; correction still reopens `R`). No live account, forge, 1Password, secret, network, or session state was touched.
