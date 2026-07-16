# Pi OAuth staging security decision

Date: 2026-07-16 · Status: **locked for MVP implementation**

## Verdict

Choose an **immutable access-only snapshot** for one exact Pi provider/model. Reject raw host auth projection, refresh-token staging, a host broker/socket/helper, and generic secret-file/startup-code mechanisms.

```cue
credentials: pi: {
	provider: "openai-codex"
	model:    "gpt-5.6-luna"
}
```

This field is authority-bearing and is honored only for an exact-byte-trusted **project** profile with `agent:"pi"`, `environment:"container"`, and `network:"deny"`. Builtins never set or infer it. MVP validates the two literal values above; every other provider/model/auth kind is unsupported.

## Authority statement

The sandbox receives one existing OpenAI Codex OAuth **access bearer**, represented to Pi as an API-key entry solely to disable Pi's refresh path. It receives no refresh token, account metadata, other provider entry, host path/reference, or host callable capability.

The bearer is replayable provider-default authority until issuer expiry/revocation. `--model gpt-5.6-luna` is client selection, not cryptographic model/audience/account/spend downscope. Container isolation and exact progressive egress reduce reach but do not make a stolen bearer non-replayable. Local wipe is not issuer revocation.

## Host read contract

- Source is fixed to the invoking user's default `~/.pi/agent/auth.json`; no policy path and no `PI_CODING_AGENT_DIR` override in MVP.
- Open from a retained home root. Before and after reading, `.pi`, `.pi/agent`, and `auth.json` must remain non-symlink, same-identity nodes. Parents must be current-user directories without group/other write; file must be current-user regular, link-count one, exactly 0600, and at most 1 MiB.
- If `auth.json.lock` exists, retry. Accept bytes only when lock is absent before/after, descriptor stat is unchanged, pathname still names the same file, and bounded JSON is stable. Ten attempts, 50 ms spacing; never remove/repair Pi's lock or file.
- Reject duplicate keys/trailing JSON. Read only `openai-codex`; require `type:"oauth"`, bounded nonempty ASCII access token without whitespace/control, and integral epoch-millisecond `expires`.
- Require strictly more than 15 minutes remaining both at extraction and immediately before publication. Safeslop does not validate token signature/audience and performs no network probe.
- Skip/zero raw source and token buffers best-effort. Never wrap raw OS/JSON errors into public output.

A stale lock remediation is exactly: run `pi --list-models gpt-5.6-luna` on the host, let Pi's own proper-lockfile implementation complete, then retry. Safeslop never deletes the lock.

## Staged artifact and invocation

Atomically publish one file under the existing private stage:

```text
pi/openai-codex/auth.json   mode 0600
```

Canonical content:

```json
{"openai-codex":{"type":"api_key","key":"<access>"}}
```

The existing read-only `/safeslop/runtime` mount carries it. Fixed entrypoint code copies it atomically to `$HOME/.pi/agent/auth.json` in the already-established tmpfs home before agent start, with parent 0700 and file 0600. No new mount, env value, `NODE_OPTIONS`, `PI_CODING_AGENT_DIR`, settings file, listener, or workspace artifact is introduced.

Engine argv is exactly:

```text
pi --provider openai-codex --model gpt-5.6-luna
```

The normal stage remains until teardown; all failed-launch/stop/reconcile/remove paths already wipe it and container removal destroys tmpfs home. No running-session renewal/re-read occurs. After issuer expiry/provider rejection, the operator refreshes with host Pi and creates a new session.

`chatgpt.com` is deliberately **not** added to static Pi egress. The container starts deny; the first request becomes a value-free observation and requires the normal explicit exact session grant.

## Public/value-free surfaces

Reuse existing contracts rather than add a new secret/status schema:

- session credential scope: `kind:"pi-oauth"`, `name:"openai-codex/gpt-5.6-luna"`, `scope:"access snapshot, short-lived"`;
- profile Authority scope: provider/model target, access `provider_default`, lifetime `short_lived`, basis `host_snapshot`;
- profile Readiness may check only local file safety/shape/expiry without exposing values/paths and without network calls;
- launch failures implement the existing engine `Failure()` contract with fixed classes such as source missing/unsafe/busy/malformed, provider missing, auth kind unsupported, expired/near-expiry, stage/handoff/cleanup failure.

No exact expiry, remaining duration, account ID, fingerprint, source/ref/path, raw parse fragment, or token appears in policy evaluation, credential inspection, session records, receipts, logs, or errors.

## Hard laws

Any violation rejects implementation:

1. Never project/copy host Pi auth wholesale.
2. Never expose or consume host refresh authority.
3. Exact-byte-trusted opt-in project policy only; no builtin ambient auth.
4. No values/refs/private paths in argv, env, Compose, inspect, logs, JSON, receipts, or workspace.
5. Private 0700/0600 stage, tmpfs-home copy only, full failed-launch/teardown/reconcile/remove wipe.
6. Unsafe/malformed/unsupported/expired/near-expiry source fails before agent start.
7. Public surfaces remain value-free and honest about provider-default bearer authority.
8. No host listener, forwarded helper/agent, metadata endpoint, or generic startup-code/file injection.

## Verification contract

Hermetic tests must cover: policy shape and builtin absence; disabled/untrusted policy proving zero host-auth reads; fixed-root parent/file symlink, owner, mode, type, link-count, and size rejection; lock-held, in-place mutation, replacement, bounded retry, and stable-read acceptance; duplicate/trailing/missing/wrong-type/expired/exact-headroom JSON; sentinel refresh/other-provider/source-path values absent from staged bytes, argv, environment, generated Compose, failures, evaluation, session JSON, receipts, and workspace; canonical synthetic bytes and 0700/0600 modes; entrypoint copy into tmpfs home before Pi; every injected stage/handoff failure cleanup; stop/reconcile/remove wipe and orphan cleanup. Fixtures and fake clocks only—no live provider calls.

The human-gated live E2E must use an already-authenticated host Pi account and the real signed image: host Luna marker; trusted profile launch; initial Codex request denied and observed as `chatgpt.com:443`; exact session grant; real Luna marker; revoke and denial; stop/remove; host auth bytes unchanged; container/stage/session/temp state absent. Output retains no secret, fingerprint, exact expiry, or private path.

## Rejected/deferred

- **Raw `auth.json`: rejected** — unrelated secrets plus rotating refresh authority and host/container races.
- **Host broker/socket/helper: rejected for MVP** — standing callable host authority and a new protocol/daemon.
- **Generic secret file or bootstrap: rejected** — arbitrary destination/startup-code capability.
- **Refresh/re-snapshot/live reload: deferred** — no Pi reload contract and creates renewal custody.
- **Other providers/models/API keys/custom Pi home: deferred** — new source and authority decisions.
- **Active session kill-at-expiry guard: deferred** — issuer already expires the access-only bearer; adding another init/signal owner is not required for MVP safety.

## FLO method and score

Locked rubric: Security/capability boundary 35 · OAuth lifecycle/race correctness 25 · Pi compatibility/usability 15 · Fail-closed observability/verifiability 15 · Implementation fit/YAGNI 10. Deterministic laws above override scores.

One isolated worker drafted from the expansion+ayo packet. Blind cross-family Kimi (original order) and DeepSeek (reversed order) evaluators found no law violation or fatal flaw. Baseline scores averaged by criterion: 10 / 10 / 9.5 / 10 / 10, weighted **99.25/100**. Forced host fixes: spell the CUE/trust interaction explicitly; make stale-lock remediation executable; require tests to scan sentinel values/raw errors; avoid claiming host Pi binary/version itself is credential authority. The active-expiry supervisor was then removed as unnecessary new signal complexity while retaining access-only/no-refresh/issuer-expiry safety; this narrows authority and implementation surface rather than weakening a law. DeepSeek re-evaluated the narrowed note at 10 / 10 / 9.5 / 8.5 / 10, weighted **97.0/100**, with no blocker or law violation; its forced test/remediation specificity is incorporated above.
