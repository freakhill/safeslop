# 0072 — Session-lane trust gate + stage-dir relocation (0070 B1/B2)

**Status:** implemented and verified **Date:** 2026-07-03 (impl 2026-07-04)
Fixes the two release blockers from `specs/0070-security-review.md`. Must merge
BEFORE `specs/0069` execution: 0069 T4 stages GitHub App tokens into the stage
dir, which today sits inside the agent-writable workspace (B2), and the Emacs
cockpit launches agents with no trust gate at all (B1).

## Scope

**In:** B1 (session-lane trust bypass, incl. the Emacs create/run surface) and
B2 (stage dir relocation out of the workspace). The 0070 fix text for B1
("persist the approved hash … re-verify at run time") also closes B3.
**Out:** H1 (PATH hygiene), H2, M1–M7, L1–L5 — tracked in 0070, unchanged.

## F1 — B1: trust gate on the session lane

**Files:** `internal/cli/cli.go` (createSessionFromProfile ~586, cmdSessionCreate
~443, cmdSessionRun ~803, sessionProfile ~641, enforceTrust ~1174),
`internal/cli/supervise.go` (Supervise), `internal/engine/session/` (Session
struct), `internal/engine/jsoncontract/` (new error code),
`emacs/safeslop-session.el` (+ ERT), tests beside each.

Design, per 0070's fix paragraph:

1. **Profile sessions (create).** `createSessionFromProfile` calls
   `enforceTrust(path, false)` right after `findConfig`. Failure emits a new
   machine-readable contract error `CodeTrustRequired` with
   `{path, hint: "run: safeslop trust <path>"}` so clients can branch. No
   `--trust` flag on `session create` — approval stays an explicit separate act
   (`safeslop trust`), preserving the specs/0022 comprehension gate.
2. **Persist the approved hash.** Add `PolicyPath string` + `PolicyHash string`
   (json `policy_path`/`policy_hash`, omitempty) to `Session`. Factor the
   canonical-bytes hash out of `enforceTrust` so create records exactly what the
   trust store approved.
3. **Re-verify at run/supervise.** `cmdSessionRun` and `Supervise` (both build
   the profile from the session record, never re-reading the cue) verify, for
   profile sessions, that the trust store still holds an approval for
   `(sess.PolicyPath, sess.PolicyHash)`. Mismatch/absence = fail-closed
   `CodeTrustRequired`. This closes the create→run drift (0070 B3) without
   re-reading policy bytes at run time.
4. **Ad-hoc sessions (`--agent`).** No policy file exists; every parameter is
   explicit in argv, so there are no hidden bytes to approve. Container ad-hoc
   sessions stay ungated. **Host** ad-hoc sessions require a new
   `--trust-host` ack flag on `session create`, mirroring the host-launch
   comprehension gate `safeslop run` applies (implementer: mirror cmdRun's
   exact host-gate semantics, specs/0030); absent flag = fail-closed error
   naming it.
5. **Emacs surface.** Create flow handles `CodeTrustRequired`: show path +
   short hash, `y-or-n-p` "Trust this safeslop.cue …?", run
   `safeslop trust <path>` (new pure argv builder + ERT), retry create once.
   Ad-hoc host creation shows the comprehension prompt and passes
   `--trust-host`. Argv builders stay pure and ERT-tested (house style).

**Tests:** Go — untrusted cue: create fails `CodeTrustRequired`; after
`safeslop trust`: create succeeds and records hash; hash drift (retrust a
changed file → old session's hash stale): run fails; ad-hoc host without flag
fails / with flag succeeds; ad-hoc container unaffected. ERT — new argv
builders; trust-prompt flow with a fake contract error.
**Done when:** every `session`-lane launch path calls the gate;
`go test ./internal/cli/...` + ERT green.

## F2 — B2: stage dir out of the workspace

**Files:** `internal/cli/supervise.go` (runProfileCtx stage construction),
`internal/cli/cli.go` (sessionRevokeCredentials ~324), tests; grep for
`.safeslop/runtime` in docs/skills.

1. New helper `stageDirFor(name, ws string) (string, error)`:
   `os.UserCacheDir()/safeslop/runtime/<name>-<8-hex fnv(ws)>` (base MkdirAll
   0700). The ws-hash disambiguates concurrent coupled runs of the same profile
   name in different workspaces (previously separated by being under each ws).
   `os.UserCacheDir` error = fail-closed (no tmp fallback: predictable path +
   0700 ancestry are part of the boundary).
2. `runProfileCtx` uses the helper; the deferred `os.RemoveAll` wipe and the
   pre-wipe revoke defers are unchanged. `sessionRevokeCredentials`
   reconstructs the identical path via the same helper
   (`"session-"+sess.ID`, `sess.Workspace`) — deterministic, nothing persisted.
3. Container side unchanged: `containerLaunch` already receives `stageDir` and
   mounts it at `/safeslop/runtime:ro`; only the host source moves. This also
   moves the rendered `squid.conf`/`allowlist.domains` (compose.go: StageDir ==
   RuntimeDir) out of agent-writable space — an egress-config tamper hole 0070
   folded into B2. Lima/Colima file sharing is safe: the new base is under
   `$HOME` exactly like the workspaces already mounted rw.
4. `.container` gitconfig variants and in-boundary env (`/safeslop/runtime/...`)
   are container-absolute and need no change; host-path env (KUBECONFIG etc.)
   follows stageDir automatically.

**Tests:** helper unit test (outside ws, deterministic, distinct for distinct
ws, 0700 base); revoke-path reconstruction equals launch-path dir; repo grep
`filepath.Join(ws, ".safeslop"` / `Join(sess.Workspace, ".safeslop"` → zero
hits; existing staging tests keep passing with relocated dirs.
**Done when:** no staged byte lands under any workspace; `go test ./...` green.

## F3 — Docs/skills sync + gate

- README: trust section now states ALL launch lanes are gated (run + session +
  Emacs); stage-dir location note (`~/Library/Caches` / `~/.cache`).
- Grep skills/ + README for `.safeslop/runtime` workspace paths and the old
  claim "`safeslop run` refuses…" → update to cover the session lane.
- `make check` && `make build` pass.

## Execution notes

- Worktree `.worktrees/security-blockers-0072`, branch
  `security-blockers-0072`; merge to main before 0069 work starts.
- TDD where behavior is testable; hermetic tests (no docker, no network).
- Order: F2 (mechanical, unblocks everything) → F1 (gate + Emacs) → F3.
