# 0074 — Container teardown follow-ups (detached-stop reap + `safeslop down`)

Status: complete
Follows: 0073 (session profile fidelity), 0069 P1 (forge creds), 0051 (detached supervisor), 0055 (record-independent reap), 0066 (down/detect semantics)

## Context

Two parked container-teardown bugs surfaced during live container smoke after the
0069/0073 forge-creds work landed. Both are teardown-family and are fixed together here.

## Bug 1 — detached `session stop` leaks its containers

### Symptom
After `session stop` on a **detached** lane, the boundary containers
(`session-<id>-...-agent-run-*`, `-proxy-1`) survive and must be `docker rm -f`'d by hand.

### Root cause (verified from source)
The container's reap label is stamped by the launch path as

```
safeslop.session = SessionIDFromStageDir(stageDir)
                 = TrimPrefix(base(stageDir), "session-")
```

where `stageDir = stageDirFor("session-"+sess.ID, ws)` and `stageDirFor` appends an
`-%08x` fnv(ws) suffix (`internal/cli/cli.go` `stageDirFor`). So for session `sess-XXXX`
the stage dir base is `session-sess-XXXX-<hash>` and the **label value is
`sess-XXXX-<hash>`**.

But `sessionReapBoundary` reaps with the **bare** `sess.ID` (`sess-XXXX`):

```go
return container.ReapBySession(ctx, engineForSession(sess), sess.ID)
```

`ReapBySession` uses an **exact-match** engine filter
(`label=safeslop.session=sess-XXXX`), which never matches the hash-suffixed label, so
the reap is a silent no-op.

Why only detached leaks: a **coupled** run's container is a foreground
`compose run --rm`, so the engine removes it on agent exit regardless of the label
mismatch. A **detached** supervisor is signalled (`kill(-pgid)`) and its container's only
cleanup is the label reap — which misses — so the container survives until the next run's
`SweepManagedOrphans` (which reads the *real* label off the container and thus does match).

### Fix
Reap by the exact label the launch path stamped. `sessionRevokeCredentials` already
reconstructs the deterministic stage dir the same way (`stageDirFor("session-"+sess.ID,
sess.Workspace)`); factor that into a shared helper and derive the reap key from it:

```go
// sessionStageDir reconstructs the deterministic host stage dir a session's run staged
// under, so teardown paths (credential revoke, boundary reap) address the exact tree/label
// the launch path used (mirrors runProfileCtx's stageDirFor("session-"+id, ws)).
func sessionStageDir(sess engsession.Session) (string, error) {
	return stageDirFor("session-"+sess.ID, sess.Workspace)
}

func sessionReapBoundary(sess engsession.Session) error {
	if sess.Environment != "container" {
		return nil
	}
	key, err := sessionReapKey(sess)
	if err != nil {
		return err
	}
	return container.ReapBySession(context.Background(), engineForSession(sess), key)
}
```

`sessionReapKey(sess) = container.SessionIDFromStageDir(stageDir)`. This is the same value
the boundary carries, so the exact-match filter now hits. It is deterministic and needs no
persisted state (consistent with the revoke/wipe reconstruction).

## Bug 2 — `safeslop down` errors before it tears anything down

### Symptom
`safeslop down` fails: `validating compose.yml: services.agent.image must be a string`.

### Root cause (verified from source)
`cmdDown` renders a throwaway compose via `container.ComposeForDown()`, which calls
`materializeRun(composeParams{... SessionID:"down"}, false)` with **no `AgentImage`**. The
template line `image: {{.AgentImage}}` therefore renders blank, and `container.Down`'s
`compose -f <file> down` fails compose **schema validation**. Because that call is
sequenced *before* `ReapManaged`:

```go
if err := container.Down(ctx, eng, composeFile); err != nil {
	return err            // <- down bails here; ReapManaged never runs
}
return container.ReapManaged(ctx, eng)
```

`down` returns the error and never reaches the real teardown. The `compose down` step was
also vestigial: the throwaway project name can never match a live session's project, so it
could only ever act on an empty ephemeral project. `ReapManaged` (label sweep of all
`safeslop.managed=true` containers + networks) is the actual, working teardown.

### Fix
Drop the vestigial, actively-breaking `ComposeForDown` + `Down` from the down path; let
`ReapManaged` do the teardown:

```go
RunE: func(_ *cobra.Command, _ []string) error {
	ctx := context.Background()
	eng, err := runtimepkg.Detect(runtimepkg.PolicyAllow)
	if err != nil {
		return nil // no ambient runtime -> nothing safeslop could have started (0066 D5)
	}
	return container.ReapManaged(ctx, eng)
},
```

The now-unused `container.ComposeForDown` (`launch.go`) and `container.Down`
(`container.go`) have been removed so the dead compose-down path cannot be reintroduced
by accident.

## Tests (hermetic)

- `internal/engine/container/reap_managed_test.go`:
  `TestReapManagedRemovesAllManagedContainersAndNetworks` — fake `ReapEngine` asserts the
  `down` teardown path issues the managed-label `ps`/`rm -f` and `network ls`/`network rm`
  sweep (the sole teardown `down` now relies on). `ReapManaged` was previously untested.
- `internal/cli/reap_boundary_test.go`:
  `TestSessionReapKeyMatchesLaunchLabel` — asserts `sessionReapKey(sess)` equals the label
  the launch path stamps (`container.SessionIDFromStageDir(stageDirFor("session-"+id,
  ws))`) and, as a regression guard, that it is **not** the bare `sess.ID` (the exact bug).
  Mirrors the existing hermetic `stage_dir_test.go`.

## Done checklist (AGENTS.md)

1. [x] CLI help paths unchanged (`down` surface/behavior preserved: tears down managed stacks).
2. [x] README/skill `down` examples still match (behavior unchanged).
3. [x] Skill workflows unaffected.
4. [x] Tests cover the new reap-key derivation and the `ReapManaged` down path.
5. [x] `make check` passes.
6. [x] `make build` passes.
