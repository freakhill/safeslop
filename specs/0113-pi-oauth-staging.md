# 0113 — First-class access-only Pi OAuth staging

Status: in progress

SCOPE: implement the locked access-only Pi OAuth snapshot for exactly `openai-codex/gpt-5.6-luna`: explicit trusted project policy, safe/stable host Pi auth extraction, synthetic non-refreshing auth file in container tmpfs, engine-owned model argv, value-free evaluation/inspection/session failures/scopes, full teardown wipe, and real deny→grant→Luna→revoke acceptance on the 0112 runtime foundation.

OFF-LIMITS: no builtin ambient auth; no host `auth.json` projection/copy; no refresh/account/other-provider export; no host auth write/lock removal/refresh; no broker/socket/helper/metadata endpoint; no generic secret-file, user destination, `NODE_OPTIONS`, startup-code, settings, or `PI_CODING_AGENT_DIR` surface; no other provider/model/API-key; no static `chatgpt.com` allowlist; no issuer-revocation claim; no exact expiry/value/ref/private path/fingerprint in output.

WORKTREE: `.worktrees/0113-pi-oauth-staging/`

Decision notes: `specs/research/2026-07-16-pi-oauth-staging-ayo.md`, `specs/research/2026-07-16-pi-oauth-staging-flo.md`.

- [x] Add RED policy, authority, argv, and session-scope tests
  FILE:     `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy_test.go`, `internal/engine/policy/evaluation_test.go`, `internal/cli/cli_agentargv_test.go`, `internal/cli/cli_session_test.go`
  CHANGE:   Test the ideal `credentials.pi {provider,model}` contract: only Pi/container/deny plus literal `openai-codex/gpt-5.6-luna`; builtins absent; Authority reports honest provider-default short-lived host snapshot; Pi argv includes the exact provider/model pair; session create persists only `pi-oauth|openai-codex/gpt-5.6-luna|access snapshot, short-lived`.
  VERIFY:   `! go test ./internal/engine/policy ./internal/cli -run 'PiOAuth|Pi.*Luna|AgentArgvPi' -count=1 -v`
  EXPECTED: Tests fail on unknown schema fields/missing authority scope/plain `pi` argv/missing session scope, not fixture plumbing.

- [x] Implement the narrow policy and value-free public contract
  FILE:     `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy.go`, `internal/engine/policy/policy_test.go`, `internal/engine/policy/evaluation.go`, `internal/engine/policy/evaluation_test.go`, `internal/cli/cli.go`, `internal/cli/cli_agentargv_test.go`, `internal/cli/cli_session_test.go`
  CHANGE:   Add `PiCreds` and strict profile validation; keep all builtins unchanged. Add policy/session credential scopes and engine-owned `pi --provider openai-codex --model gpt-5.6-luna`. Reuse existing JSON structs; add no value-bearing or exact-expiry field.
  VERIFY:   `go test ./internal/engine/policy ./internal/cli -run 'PiOAuth|Pi.*Luna|AgentArgvPi' -count=1 -v`
  EXPECTED: Valid profile decodes and reports exact value-free authority/session scope/argv; every unsupported boundary fails validation; builtins remain unauthenticated.

- [x] Add RED safe-source and synthetic-stage tests
  FILE:     `internal/engine/creds/pi.go`, `internal/engine/creds/pi_test.go`, `internal/cli/cli_stage_test.go`
  CHANGE:   Specify the provider extractor/stager with temp HOME fixtures and fake clock/sleeper: canonical access-only artifact; 0700/0600 modes; default fixed source; refresh/other-provider sentinels absent; missing/parent-or-file symlink/owner-mode-type-link-size/lock/unstable/duplicate/trailing/wrong-type/expired/15-minute-boundary failures; fixed value-free error classes; no source mutation; stage cleanup on failure.
  VERIFY:   `! go test ./internal/engine/creds ./internal/cli -run 'PiOAuth|StagePi' -count=1 -v`
  EXPECTED: Tests fail because no provider-specific safe reader/stager or stageProfile integration exists.

- [x] Implement stable host extraction and access-only staging
  FILE:     `internal/engine/creds/pi.go`, `internal/engine/creds/pi_test.go`, `internal/cli/cli.go`, `internal/cli/cli_stage_test.go`, `internal/engine/container/runtime_failure.go`
  CHANGE:   Read only default `~/.pi/agent/auth.json` from a retained approved-home root; pre/post reject unsafe parent/file identity/mode/owner/link/size and Pi lock; bounded stable retries; duplicate/trailing JSON rejection; literal provider OAuth access+expiry extraction; >15-minute headroom twice; best-effort buffer zeroing. Atomically write only synthetic `type:api_key` artifact under stageDir and integrate it into `stageProfile`. Return engine-owned fixed `pi_oauth_*` failures without wrapped details.
  VERIFY:   `go test ./internal/engine/creds ./internal/cli -run 'PiOAuth|StagePi' -count=1 -v`
  EXPECTED: All source/race/expiry/leak/mode/cleanup tests pass; no refresh/account/other provider/source path crosses or serializes.

- [x] Copy Pi auth into tmpfs before agent start and prove teardown
  FILE:     `internal/engine/container/assets/entrypoint.sh`, `internal/engine/container/compose_test.go`, `internal/engine/container/launch_test.go`, `internal/cli/cli_runprofile_test.go`, `internal/cli/cli_session_test.go`
  CHANGE:   Add fixed conditional entrypoint handling for `/safeslop/runtime/pi/openai-codex/auth.json`: reject unsafe staged shape, create tmpfs Pi dirs 0700, copy atomically as 0600 before exec. Add sentinel scans proving no token/ref/path in argv/env/Compose/inspect-facing config/log/status/receipt/workspace and injected launch/stop/reconcile/remove cleanup coverage.
  VERIFY:   `go test ./internal/engine/container ./internal/cli -run 'PiOAuth|PiAuth|RunProfile.*Pi|Session.*Pi' -count=1 -v`
  EXPECTED: Synthetic auth reaches only tmpfs home before Pi; failure prevents agent start; all local/container copies disappear on every teardown path without claiming issuer revocation.

- [ ] Synchronize inspection, docs, and operator workflow
  FILE:     `internal/engine/creds/inspect.go`, `internal/engine/creds/inspect_test.go`, `README.md`, `emacs/README.md`, `skills/agent-key-lifecycle/SKILL.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0113-pi-oauth-staging.md`
  CHANGE:   Make credential inspection value-free for Pi OAuth and document explicit policy/trust, default host source, provider-default replay authority, no refresh/renewal, 15-minute launch headroom, stale-lock command `pi --list-models gpt-5.6-luna`, progressive `chatgpt.com:443` grant, and local-wipe-not-revocation semantics. No new Emacs mutation action in MVP.
  VERIFY:   `go test ./internal/engine/creds -run 'Inspect.*PiOAuth' -count=1 -v && git diff --check && rg -n 'credentials.*pi|gpt-5.6-luna|access snapshot|provider-default|15 minutes|chatgpt.com' README.md emacs/README.md skills/agent-key-lifecycle/SKILL.md skills/agent-sandbox-ops/SKILL.md`
  EXPECTED: Inspection/docs expose only provider/model/lifetime/readiness and executable workflow; no values/refs/private paths or downscope/revocation overclaim.

- [ ] Run full gates, live Pi acceptance, deploy, and clean up
  FILE:     whole repo, `specs/0112-progressive-runtime-readiness.md`, `specs/0113-pi-oauth-staging.md`
  CHANGE:   Run focused suites and 0112 progressive smoke; build the new Pi image; with explicit live opt-in run host Luna marker and trusted project session: deny observation of `chatgpt.com:443`, exact grant, real Luna marker, revoke/deny, stop/remove. Compare host auth bytes before/after without outputting them; prove no test state remains. Then run UI/check/build, mark both specs complete, merge/push both remotes, install matching binary/Emacs files, and remove both worktrees/branches.
  VERIFY:   `git diff --check && make test-progressive-egress-smoke && make test-emacs-ui-matrix && make check && make build`
  EXPECTED: Hermetic and live gates pass; real access-only Luna works only after the session grant; host auth is unchanged; staged/container/session/temp state is absent; installed version and both remotes match.
