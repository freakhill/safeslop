# 0070 — Security review (whole-system, source-grounded)

**Status:** review, findings open **Date:** 2026-07-03
Method: four adversarial source-read lanes (credential lifecycle / egress+container /
host trust+exec) plus host verification of every headline finding against `file:line`.
Cross-checked against the just-landed `specs/0068` decision and `specs/0069` P1 plan.
No files were modified. Findings are the shipped `main` tree, not the 0068/0069 futures.

## Verdict

The isolation *primitives* are sound and honestly documented — the weaknesses are in
**where the gates are wired**, not in the gates themselves. Two findings are release
blockers: the trust/comprehension gate is bypassed by the entire Emacs launch path, and
staged secrets sit in the agent-writable workspace behind an illusory read-only mount.

## Blockers

### B1 — The `session` lane launches agents with no trust gate (the Emacs cockpit is entirely on this lane)
`enforceTrust` is called in exactly two places: `cmdRun` (`internal/cli/cli.go:278`) and
`cmdTrust` (`cli.go:1211`). The `session` verbs — `createSessionFromProfile`
(`cli.go:586`), `cmdSessionRun` (`cli.go:803`), `cmdSessionSupervise`/`runProfileCtx`
(`supervise.go`) — call `policy.Load` and launch **without** `enforceTrust`.

The Emacs client drives launches **only** through this lane:
`safeslop-session.el:49` (`session create --agent …`), `:58` (`session create
--profile …`), `:216`/`:224` (`session run [--detach]`). The Profiles surface `r`
(`safeslop-profiles-launch`, `safeslop-profiles.el:543`) and the portal `RET`/`r` all
route to `session create` → `session run`. So **every launch a cockpit user performs
skips policy-byte approval and the host-launch comprehension gate** (specs/0022, 0030).
Only the bare `safeslop run <profile>` CLI verb is gated.

Attack: a freshly cloned malicious `safeslop.cue`, or a direct `session create --agent
claude --environment host --workspace .`, runs a host agent (staging your real
credentials, unconfined) with no approval of the policy bytes. This defeats the central
control safeslop advertises ("`safeslop run` refuses an unapproved or changed
`safeslop.cue`" — README).

**Fix:** call `enforceTrust(path, false)` inside `createSessionFromProfile` and before
`runProfile`/`runProfileCtx` in the run/supervise paths; persist the approved hash into
the session record at create time and re-verify it at run time (closes B3 too). Surface
the trust/comprehension prompt in the Emacs create flow.

### B2 — Staged secrets live in the agent-writable workspace; the read-only stage mount is illusory
`stageDir := filepath.Join(sess.Workspace, ".safeslop", "runtime", "session-"+sess.ID)`
(`cli.go:324`, and the same construction on the run path). The container template mounts
`{{.Workspace}}:/workspace:rw` **and** `{{.StageDir}}:/safeslop/runtime:ro`
(`internal/engine/container/assets/compose.yml.tmpl:27-28`). Because the stage dir is a
subpath of the workspace, the agent sees every staged bearer at BOTH
`/safeslop/runtime/...` (ro) **and** `/workspace/.safeslop/runtime/session-<id>/...`
(rw). Consequences:
- The `:ro` protection is defeated — the agent can rewrite staged creds via the
  `/workspace` path.
- Staged secrets (`kubeconfig` with a bearer token, `gcp-access-token`, `.npmrc`
  `_authToken`, the SSH private key, and the future git App-token files from 0069) are
  trivially readable by the agent at a predictable workspace path — and readable by a
  *later* session running in the same workspace after a SIGKILL orphan (B4).

**Fix:** stage credentials OUTSIDE the agent-writable workspace (e.g.
`os.UserCacheDir()/safeslop/session-<id>`, mounted only at `/safeslop/runtime:ro`), so
`/workspace` never contains the stage tree. This single change also fixes the ro-defeat
and shrinks the B4 orphan blast radius.

## High

### H1 — No shadowed-binary detection gates exec; host helpers resolve bare names on the raw PATH (specs/0035)
The hostenv PATH hardening (`filterPATH`: strips world-writable / `..` / `DYLD_*`) is
applied only to the agent's own `argv[0]` and the child env. Every host-side helper is
spawned by **bare name** against the unsanitized process PATH:
`git`/`ssh-keygen`/`git remote` (`creds/ssh.go:80` via `runSSHCmd`, `creds/multirepo.go`),
`ssh-keyscan` (`creds/forgejo.go`), `aws` (`creds/aws.go:62,113`), `gcloud`/kube auth
(`creds/gcp.go:33`, `kube.go:170`), `op` (`secrets/secrets.go:34,55`),
`docker`/`podman` (`container/container.go:39`, `container/runtime/engine.go:49,68,88`).
specs/0035's `Env.LookAll` exists but has no live consumer (its cockpit caller was
removed), so shadowed-binary detection ships nowhere.

Attack: a poisoned early-PATH entry (the exact Finder-launch / inherited-shell scenario
hostenv exists to defend) plants `git`/`op`/`aws` earlier in PATH; on the next launch
these run **as the user with full credential access, before any boundary exists**.

**Fix:** resolve every host helper through the sanitized PATH
(`hostenv.Reconstruct().LookPath` → absolute path, or set `cmd.Path`), and ship the
specs/0035 detection to refuse/warn on a shadowed security-critical binary.

### H2 — External-command stderr flows into user-visible error strings (`runSSHCmd`)
`runSSHCmd` (`creds/ssh.go:79-87`) wraps `exec … Output()` errors with `%w`; Go's
`*exec.ExitError` carries captured stderr. `gh api`/`ssh-keyscan`/`git remote` stderr
can surface in UI error text. Low secret-probability today, but the 0069 plan replaces
`gh` with Go-native HTTP whose error bodies could carry more — fix the pattern now.
**Fix:** wrap with a generic hint; never fold raw external stderr into the error chain.
(Moot for GitHub once 0069 deletes the `gh` shell-out; still live for Forgejo/ssh.)

## Medium

- **M1 — Trust-check TOCTOU.** **Implemented in specs/0076.** `cmdRun` parses the
  policy at `cli.go:215` (its own `os.ReadFile`) but hashes a *separate* read inside
  `enforceTrust` (`cli.go:1176`), then runs the already-parsed profile. The bytes
  validated ≠ the bytes hashed/approved. Read once, hash and parse the *same* bytes
  (`policy.LoadBytes`).
- **M2 — git-remote injection into staged config.** **Implemented in specs/0076.**
  `owner`/`repo` parsed from `git remote get-url origin` (agent-writable `.git/config`)
  are `fmt.Fprintf`'d verbatim into `.gitconfig`/`.ssh/config` bodies
  (`creds/multirepo.go:70-78`, `renderAliasSSHConfig`). A crafted remote with an
  embedded quoted newline can inject SSH directives (`ProxyCommand`) or git config.
  Validate `owner`/`repo` against `[A-Za-z0-9._-]` before writing.
- **M3 — Detached `session stop` signals a stored PID/pgid with no reuse guard.**
  **Implemented in specs/0077.** (`session.go` Stop path; `sessionKillProcess`). After a
  supervisor death + PID recycle, `kill(-pgid, …)` can hit an unrelated group. Record
  supervisor start-time/generation and verify before signalling; reconcile immediately
  before Stop.
- **M4 — SIGKILL orphans staged creds** (defers skipped). **Implemented in specs/0077.**
  Neither `reconcile` nor `sessionRevokeCredentials` (`cli.go:317`) deletes the stage
  dir, so a killed session leaves `kubeconfig`/`gcp-access-token`/`.npmrc`/SSH key at a
  known workspace path (compounds B2). Have Stop/Remove/Prune/reconcile wipe the stage
  dir; B2's relocation shrinks the window.
- **M5 — GCP token written to a dead file.** **Implemented in specs/0078.**
  `creds/gcp.go:43-48` wrote `gcp-access-token` (0600) that nothing consumed
  (delivery is via `CLOUDSDK_AUTH_ACCESS_TOKEN` env). Removed the dead file
  instead of wiring/documenting it.
- **M6 — Squid IP-literal / reverse-DNS allowlist edge.** **Implemented in specs/0079.**
  `squid.conf.tmpl` denied metadata + RFC-1918 ranges but `dstdomain` allowlisting could
  match a bare IP via reverse PTR lookup — an attacker who controlled the PTR of their own
  public IP to an allowlisted name could be matched. Strict-mode Squid now denies numeric
  IP-literal destinations before the allowlist and renders the allowlist as `dstdomain -n`.
- **M7 — Docker embedded DNS as an exfil channel.** **Implemented in specs/0080.**
  A `network:deny` agent on the internal bridge could still reach Docker's embedded
  resolver (127.0.0.11), which may forward to the host resolver — low-bandwidth DNS
  tunnelling out. Deny-tier compose now pins per-container external DNS forwarding to
  `127.0.0.1`, preserving Docker service-name resolution for `proxy` while making
  arbitrary external DNS fail inside the container.

## Low / posture

- **L1 — `user:` directive omitted from compose** (`compose.yml.tmpl`).
  **Implemented in specs/0081.** Generated and legacy Compose now hard-set
  `user: "1000:1000"` for agent launches, matching the image `USER 1000` and the
  uid/gid-owned tmpfs home.
- **L2 — Comprehension/consent gate (specs/0030) is dead code.**
  **Implemented in specs/0082.** Host-tier `safeslop run` and `safeslop session run`
  now call `HostConsentStatements`/`HostHeadlineBody`/`HostScopeLine` for a
  per-launch yes/no comprehension gate before the agent starts.
- **L3 — `trust.Store.Revoke` is unreachable (specs/0033).**
  **Implemented in specs/0083.** `safeslop untrust [path]` now removes the
  host-side policy approval using the same canonical path key as launch trust checks.
- **L4 — `.pub` files linger post-keygen** (`multirepo.go:139,208`): removed only after
  registration; a crash leaves a 0644 public key (non-secret, but violates the
  "private key only" doc claim). Remove immediately after read.
- **L5 — `assumeRoleDownscope` inherits full host env** into the `aws sts` subprocess
  (`aws.go:91-99`). Pass a minimal allowlist.

## Rejected / not-a-finding

- **"CDN egress hosts missing from `egress.go`" (auditor flag):** correct that they're
  absent, but this is unbuilt 0069 P1 work (github.com + `codeload`/`objects.github…`
  land with App-token staging), NOT a regression. Tracked in specs/0069 T7.
- **"Secret values in argv/ps":** verified NOT present — `op` refs (not values) in argv,
  values via 0600 files / entrypoint-sourced `secrets.env`, never `-e` flags.
- **"Host gateway reachable from internal net":** the `internal: true` / external
  `--internal` bridge gives the agent no default route/gateway (template comment +
  compose semantics); squid is the only egress. Left as verify-in-CI, not an open finding.

## Verified SOUND (the primitives hold)

Trust hash binding for `run` is SHA-256 over the exact policy bytes and is
non-replayable after mutation (`trust.go:76,84-91`); `canonicalPolicyPath` collapses
symlink/`/tmp` aliasing (`cli.go:307-315`); container tier passes **only** `secretEnv`
(never `os.Environ()`), and `childEnv` (`childenv.go`) drops all ambient
`AWS_*`/`OP_SESSION*`/`SSH_AUTH_SOCK`/`GITHUB_TOKEN`/`ANTHROPIC_API_KEY`; secret refs are
schema-constrained (`^(op://|env:).+`) and resolved via argv `op read` (no shell), values
never logged; SSH staging pins `known_hosts` with `StrictHostKeyChecking=yes`,
`IdentitiesOnly=yes`, `IdentityAgent=none`; revoke-info stores refs, not values; all
bearers 0600, stage dirs 0700; the container hard-sets `user: "1000:1000"`, is
`read_only: true`, `cap_drop: [ALL]`, `no-new-privileges:true`, has no docker socket,
and uses a tmpfs home; `ValidateName` rejects control
chars (JSONL/bidi defense); terminal-launch strings are shell-quoted/AppleScript-escaped
and the profile name is `[A-Za-z0-9._-]+`-constrained.

## Priority

B1 (session-lane trust bypass), B2 (staged secrets in workspace / ro defeat), H1
(PATH/shadowed-binary exec), M1 (trust TOCTOU), M2 (remote injection), M4
(orphaned stage dirs), and M3 (PID/PGID reuse guard) have shipped in follow-up
specs 0072, 0075, 0076, and 0077; M5–M7 shipped in specs 0078–0080; L1 shipped in
specs 0081; L2 shipped in specs 0082; L3 shipped in specs 0083. Remaining order:
L4–L5.
