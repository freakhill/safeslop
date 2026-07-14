# 0087 — Product activation roadmap

Status: planned
Date: 2026-07-09

SCOPE: turn the operator-reported "still basically unusable" gaps into ordered, reviewable product tracks. This is an umbrella roadmap, not an implementation spec.

OFF-LIMITS: do not weaken safeslop's trust model, network defaults, host-helper shadow refusal, credential value-free guarantees, or policy-byte binding. Do not make hard capability-boundary decisions here; network authority and credential authoring models get their own ayo-flo/specs before implementation.

WORKTREE: none for this umbrella; implementation slices use their own worktrees.

## Problem

After `specs/0086` and local install, safeslop is safer and more legible but still fails the activation path for real use: profile creation is too manual, credential setup/repo selection has no ergonomic UI, container/host launch failures are surfaced late and unactionably, network authority is too blunt, and open session safety chrome is still too weak.

## Activation success criteria

- A new operator can create a useful default container profile without hand-writing CUE.
- The UI can connect/check GitHub and Forgejo readiness and choose repositories/scopes without exposing secret values.
- Bundle/package selection is checkbox-driven, documented inline, and makes inherited packages obvious.
- Network authority supports more than allow/deny only after an ayo-flo decision on the capability model.
- Profile authoring supports mounts and shows a safety evaluation before save/run.
- The UI blocks or explains runtime helper failures before a session is created/launched.
- Host ad-hoc creation in Emacs offers the explicit `--trust-host` acknowledgement instead of dead-ending on `TRUST_REQUIRED`.
- Open sessions have persistent chrome that certifies environment, network, and credential posture.

## Tracks

1. **0088 — Host trust + runtime preflight UI (immediate blocker).**
   Fix the two observed dead ends: host ad-hoc creation from Emacs must offer the explicit `--trust-host` acknowledgement, and container launches must preflight/report shadowed runtime helpers before the operator creates/runs a doomed session.

2. **Credential connection + repository picker.**
   UI flows for GitHub and Forgejo account links, readiness, repository selection with checkboxes, and read/write scope toggles. Values/refs stay outside rows unless explicitly in the existing Credentials readiness surface; session/profile records remain value-free.

3. **Profile authoring cockpit.**
   Default profiles, checkbox bundle/package picker with `?` help, inherited bundle packages shown selected/locked, dynamic LSP bundle generation, mounts UI, and safety summary before save.

4. **Network authority model (ayo-flo required).**
   Decide the policy/UX model for deny, allowlist domains/IPs, progressive ask-per-destination, temporary grants, and mixed allowlist+prompt. This is a security/capability boundary and must not be improvised in code.

5. **Session safety chrome.**
   Persistent, redundant visual chrome across terminal buffers/portal/modeline/tab that certifies tier/net/credential scope at a glance. Builds on `specs/0086` labels and headers.

6. **Profile safety evaluation.**
   Rubric and UI that scores/explains profile risk: host vs container, network posture, mounts, credential write scopes, helper readiness, and trust state. Must be actionable, not a magic number.

## Recommended order

1. Ship 0088 first because it fixes current launch blockers without changing the policy schema.
2. Run ayo-flo for network authority before coding any progressive network grant model.
3. Design credential picker and profile authoring together enough to avoid duplicate CUE-writing machinery, then implement in small slices.
4. Add safety chrome/evaluation as soon as profile/session metadata is available, rather than waiting for every authoring feature.

## Status

- [x] 0088 host trust + runtime preflight UI implemented.
- [x] Credential connection + repo picker implemented (`specs/0090-credential-connection-repo-picker.md`): account-link status/UI, `profile credentials set|clear`, and manual repo picker shipped; live repo discovery remains deferred.
- [x] Profile authoring cockpit implemented (`specs/0091-profile-authoring-cockpit.md`): compose buffer, checkbox/help catalog selection, dry-run safety preview, catalog defaults inheritance, and custom mounts deferred.
- [x] Network authority ayo-flo decision landed (`specs/0089-network-authority-ayo-flo.md`).
- [x] Session safety chrome implemented (`specs/0100-session-safety-chrome.md`): live Emacs terminals carry persistent, color-redundant tier/network/credential posture in the mode-line, mirrored in portal Status help.
- [ ] Profile safety evaluation spec written.
