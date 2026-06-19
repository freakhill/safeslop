# 0024 — close the two control-plane crits: env scrub + OpenSession trust gate Implementation Plan

**Goal:** Close the two `VERIFIED-OPEN` critical findings from the 2026-06-19 security review
(`specs/research/2026-06-19-design-security-review.md`): (S2) the `sandbox`/`host` tiers inherit
the **full host environment**, leaking ambient `AWS_*`/`OP_SESSION`/`SSH_AUTH_SOCK`/`GITHUB_TOKEN`
into the cage; and (S1) the **`OpenSession` gRPC path is not trust-gated** and a same-uid in-sandbox
peer is indistinguishable from the cockpit GUI — so a network-enabled sandboxed agent can rewrite
`safeslop.cue` to `environment:"host"`, `connect()` `~/.safeslop/s.sock`, call `OpenSession`, and
escape with secrets.

**Architecture:** Three surgical, pure-Go changes plus docs.
1. **S2 — strict env allowlist.** A new `childEnv(secretEnv, pathEnv)` builds the child env from a
   *default-deny allowlist* (PATH/HOME/TERM/LANG/LC_*/TZ…) instead of `os.Environ()`. Credentials
   reach the agent **only** via the policy's `secrets:`/`credentials:` blocks. This is a deliberate
   behavior change: ambient `ANTHROPIC_API_KEY` is now dropped — agents declare it in
   `secrets: { ANTHROPIC_API_KEY: "op://…" }`. (Decision locked with jojo 2026-06-19: most-secure
   strict allowlist, no credential carve.)
2. **S1a — trust-gate `OpenSession`.** `resolveSession` (the `OpenSession` resolver) calls the
   existing `enforceTrust(path, false)` before loading the policy — the same fail-closed gate the
   CLI `run` path already uses (specs/0022). (Note: the `Launch` RPC is *already* transitively gated
   — `launchProfile` shells out to `safeslop run`, which gates — so only `OpenSession` is exposed.)
3. **S1b — refuse in-sandbox peers.** `authorizePeer` additionally rejects any peer whose pid is a
   *strict descendant* of the server's own pid (the sandboxed agent reaching back to its jailer),
   using `LOCAL_PEERPID` + a `kern.proc.pid` parent-walk. The cockpit GUI (not a descendant of
   `serve`) is unaffected.

**Tech stack:** Go stdlib + `golang.org/x/sys/unix` (already a dep). No CGO, no new deps. TDD on the
two pure helpers (`childEnv`, `isProcessTreeDescendant`).

**Out of scope (honest deferral):** full XPC-grade **codesign/audit-token peer verification** — it
closes the *arbitrary same-uid host malware* confused-deputy, but needs a second signed binary to
test meaningfully and is heavier than this slice. S1b already closes the *reachable-from-sandbox*
escape; S1a closes the policy-integrity hole. Codesign is recorded as the remaining follow-on.

**Base branch:** continue on `sp-security-review` (already carries the review note) or branch
`sp-control-plane-trust` off `main`. **Never push `main`.**

**File structure:**
- `internal/cli/childenv.go` (create) — `allowlistEnv` + `childEnv`: the strict env allowlist.
- `internal/cli/childenv_test.go` (create) — asserts ambient creds dropped, staged creds + basics carried.
- `internal/cli/cli.go` (modify) — replace the 4 `append(os.Environ()…)` sites with `childEnv`; add
  the `enforceTrust` gate in `resolveSession`.
- `internal/cli/cli_resolve_test.go` (modify) — trust the policy before each `resolveSession` call
  (the new gate is fail-closed).
- `internal/engine/control/peerauth.go` (modify) — `peerPID`, `ppidOf`, `isProcessTreeDescendant`;
  extend `authorizePeer` to reject in-tree peers.
- `internal/engine/control/peerauth_test.go` (modify) — pure-logic descendant test.
- `specs/research/2026-06-19-design-security-review.md` (modify) — correct Launch→OpenSession; mark
  S1a/S1b/S2 realized.
- `README.md` (modify) — one prose line: the sandbox scrubs ambient env; declare agent keys in `secrets:`.

---

### Task 1: S2 — strict env allowlist (`childEnv`)

**Files:**
- Create: `internal/cli/childenv.go`
- Test: `internal/cli/childenv_test.go`
- Modify: `internal/cli/cli.go` (4 env-construction sites)

- [ ] **Step 1: Write the failing test**

Create `internal/cli/childenv_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestChildEnvScrubsAmbientAuthority(t *testing.T) {
	// host ambient credentials that must NOT cross into the sandbox/host tiers
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA-HOST")
	t.Setenv("OP_SESSION_my", "op-host-token")
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "ops_host")
	t.Setenv("SSH_AUTH_SOCK", "/private/tmp/com.apple.launchd/agent.sock")
	t.Setenv("GITHUB_TOKEN", "ghp_host")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-host") // dropped by design — declare in secrets:
	// safe basics + locale that must be carried
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/Users/test")
	t.Setenv("LC_CTYPE", "UTF-8")

	env := childEnv(
		[]string{"AWS_ACCESS_KEY_ID=AKIA-EPHEMERAL"},   // a staged (ephemeral) cred
		[]string{"NPM_CONFIG_USERCONFIG=/stage/.npmrc"}, // a staged path env
	)
	has := func(s string) bool {
		for _, e := range env {
			if e == s {
				return true
			}
		}
		return false
	}
	hasName := func(name string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, name+"=") {
				return true
			}
		}
		return false
	}

	for _, leaked := range []string{"OP_SESSION_my", "OP_SERVICE_ACCOUNT_TOKEN", "SSH_AUTH_SOCK", "GITHUB_TOKEN", "ANTHROPIC_API_KEY"} {
		if hasName(leaked) {
			t.Errorf("ambient %s leaked into child env (must be dropped)", leaked)
		}
	}
	if has("AWS_ACCESS_KEY_ID=AKIA-HOST") {
		t.Error("host AWS key leaked into child env")
	}
	if !has("AWS_ACCESS_KEY_ID=AKIA-EPHEMERAL") {
		t.Error("staged ephemeral AWS key must be present")
	}
	if !hasName("PATH") || !hasName("HOME") || !hasName("LC_CTYPE") {
		t.Error("PATH/HOME/LC_CTYPE must be carried")
	}
	if !has("NPM_CONFIG_USERCONFIG=/stage/.npmrc") {
		t.Error("staged pathEnv must be carried")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/cli/ -run TestChildEnvScrubsAmbientAuthority -v
```
Expected: FAIL — `undefined: childEnv`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/childenv.go`:

```go
package cli

import (
	"os"
	"strings"
)

// allowlistEnv is the set of host environment variable NAMES safe to carry into an isolated
// (sandbox/host) child. Everything else — cloud tokens, the 1Password session, the ssh-agent
// socket, forge tokens, even ANTHROPIC_API_KEY — is dropped, so host ambient authority never
// crosses the boundary (specs/0024 S2). Credentials reach the agent ONLY via the policy's
// secrets:/credentials: blocks (the secretEnv/pathEnv channels), never by inheritance.
var allowlistEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "COLORTERM": true,
	"TMPDIR": true, "LANG": true, "TZ": true,
}

// childEnv builds the environment for an isolated child (the sandbox + host tiers, which share the
// host process namespace) from the allowlist above plus the staged secretEnv/pathEnv. It must NOT
// inherit os.Environ() wholesale: that is the ambient-authority leak specs/0024 S2 closes. LC_* are
// carried by prefix (locale, not authority). secretEnv/pathEnv are appended last so a staged value
// wins over any allowlisted host value of the same name.
func childEnv(secretEnv, pathEnv []string) []string {
	out := make([]string, 0, len(secretEnv)+len(pathEnv)+16)
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		name := kv[:i]
		if allowlistEnv[name] || strings.HasPrefix(name, "LC_") {
			out = append(out, kv)
		}
	}
	out = append(out, secretEnv...)
	out = append(out, pathEnv...)
	return out
}
```

- [ ] **Step 4: Run, verify pass**

```bash
go test ./internal/cli/ -run TestChildEnvScrubsAmbientAuthority -v
```
Expected: PASS.

- [ ] **Step 5: Swap the 4 inherit-everything sites to `childEnv`**

In `internal/cli/cli.go` there are exactly **four** identical lines (in `resolveSession` ~lines 461 &
469, and in `runProfile` ~lines 804 & 807):

```go
		env := append(append(os.Environ(), secretEnv...), pathEnv...)
```

Replace **every** occurrence with:

```go
		env := childEnv(secretEnv, pathEnv)
```

(The `container` and `vm` cases already pass only `secretEnv` — leave them untouched; they don't
share the host process env.) Confirm `os` is still imported/used elsewhere in `cli.go` (it is —
`os.ReadFile`, `os.Getwd`, etc.), so no import change is needed.

- [ ] **Step 6: Run the cli suite (catch any test asserting on inherited env)**

```bash
go test ./internal/cli/ -v 2>&1 | tail -30
```
Expected: PASS. `TestResolveSessionDeliversSecretToHostEnv` still passes (the secret rides
`secretEnv`, which `childEnv` appends). If any test now fails because it expected an *inherited*
host var in `spec.Env`, that test was asserting the bug — update it to assert the var is absent.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/childenv.go internal/cli/childenv_test.go internal/cli/cli.go
git commit -m "feat(cli): strict env allowlist for sandbox/host children — no ambient host authority (specs/0024 S2)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: S1a — trust-gate the `OpenSession` (cockpit) path

**Files:**
- Modify: `internal/cli/cli.go` (`resolveSession`, ~line 410)
- Test: `internal/cli/cli_resolve_test.go`

- [ ] **Step 1: Make the existing resolve tests trust-first (they'll fail-closed otherwise)**

`resolveSession` is about to refuse untrusted policies. The tests in
`internal/cli/cli_resolve_test.go` write a fresh (untrusted) `safeslop.cue`, so they must approve it
first. Add this helper near the top of the file (after the imports):

```go
func trustPolicy(t *testing.T, path string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // isolate the trust store from the real ~/.config/safeslop
	if err := enforceTrust(path, true); err != nil {
		t.Fatalf("trust %s: %v", path, err)
	}
}
```

Then call it once per policy:

- In `writeResolverCue`, immediately before `return path`, add:
  ```go
	trustPolicy(t, path)
  ```
  (covers `TestResolveSessionHostAndSandbox` and `TestResolveSessionContainerVMErrorWhenToolingAbsent`).
- In `TestResolveSessionDeliversSecretToHostEnv`, after the `os.WriteFile(path, …)` block (before
  `t.Chdir`), add:
  ```go
	trustPolicy(t, path)
  ```
- In `TestResolveSessionRejectsSshCreds`, after the `os.WriteFile(path, …)` block, add:
  ```go
	trustPolicy(t, path)
  ```

- [ ] **Step 2: Add a failing test for the gate**

Append to `internal/cli/cli_resolve_test.go`:

```go
func TestResolveSessionRefusesUntrustedPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // empty trust store
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	// NOT trusted -> OpenSession's resolver must fail closed (the in-sandbox escape this closes).
	if _, err := resolveSession("h", path); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted policy must be refused by resolveSession, got %v", err)
	}
}
```

- [ ] **Step 3: Run it, verify it fails**

```bash
go test ./internal/cli/ -run TestResolveSessionRefusesUntrustedPolicy -v
```
Expected: FAIL — `resolveSession` currently honors any policy (err == nil).

- [ ] **Step 4: Add the gate**

In `internal/cli/cli.go`, in `resolveSession`, right after the `findConfig` error check (the
`path, err := findConfig(configPath)` block, ~line 410) and **before** `policy.Load(path)`, insert:

```go
	// Fail-closed policy trust gate, identical to the CLI `run` path (specs/0022). The cockpit's
	// in-process OpenSession data plane was the one launch chokepoint not gated, so a same-uid
	// in-sandbox peer could rewrite safeslop.cue and OpenSession its way to environment:"host"
	// (specs/0024 S1a). The GUI surfaces approval via `safeslop trust` (a wizard screen is a
	// follow-on); allowTrust stays false here (the engine never auto-approves on the agent's behalf).
	if err := enforceTrust(path, false); err != nil {
		return control.SessionSpec{}, err
	}
```

(`enforceTrust` already exists in `cli.go` from specs/0022 — no new import.)

- [ ] **Step 5: Run, verify pass (gate + the trust-first resolve tests)**

```bash
go test ./internal/cli/ -run 'TestResolveSession' -v
```
Expected: all PASS (`RefusesUntrustedPolicy` now refuses; the others approve first).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_resolve_test.go
git commit -m "feat(cli): fail-closed trust gate on the OpenSession cockpit path (specs/0024 S1a)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: S1b — refuse control-plane peers inside a safeslop-spawned sandbox

**Files:**
- Modify: `internal/engine/control/peerauth.go`
- Test: `internal/engine/control/peerauth_test.go`

- [ ] **Step 1: Write the failing pure-logic test**

Append to `internal/engine/control/peerauth_test.go`:

```go
func TestIsProcessTreeDescendant(t *testing.T) {
	// fake tree: serve(100) -> sandbox-exec(200) -> agent(300); gui(50) -> launchd(1)
	parents := map[int]int{300: 200, 200: 100, 100: 1, 50: 1}
	ppidOf := func(pid int) (int, error) { return parents[pid], nil }

	// the sandboxed agent IS a descendant of serve -> must be detected (rejected upstream)
	if d, _ := isProcessTreeDescendant(100, 300, ppidOf); !d {
		t.Error("agent (300) must be detected as a descendant of serve (100)")
	}
	// the cockpit GUI is NOT a descendant of serve -> allowed
	if d, _ := isProcessTreeDescendant(100, 50, ppidOf); d {
		t.Error("gui (50) must NOT be a descendant of serve (100)")
	}
	// serve connecting to itself is not a STRICT descendant -> allowed (the same-process test case)
	if d, _ := isProcessTreeDescendant(100, 100, ppidOf); d {
		t.Error("serve (100) must not be its own descendant")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/control/ -run TestIsProcessTreeDescendant -v
```
Expected: FAIL — `undefined: isProcessTreeDescendant`.

- [ ] **Step 3: Write the implementation**

In `internal/engine/control/peerauth.go`, add `peerPID`, `ppidOf`, and `isProcessTreeDescendant`,
and extend `authorizePeer`. The full file becomes:

```go
package control

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerUID returns the uid of the connected peer of a unix socket via LOCAL_PEERCRED (darwin).
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var xucred *unix.Xucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		xucred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if gerr != nil {
		return -1, gerr
	}
	return int(xucred.Uid), nil
}

// peerPID returns the pid of the connected peer via LOCAL_PEERPID (darwin <sys/un.h>). Unlike
// LOCAL_PEERCRED (uid only), this lets us tell the cockpit GUI apart from the sandboxed agent
// reaching back to its jailer (specs/0024 S1b).
func peerPID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var pid int
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		pid, gerr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return -1, err
	}
	return pid, gerr
}

// ppidOf returns the parent pid of pid via the kernel proc table — a sysctl, not an exec, so it
// works regardless of any fs sandbox and adds no process to the tree.
func ppidOf(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return -1, err
	}
	return int(kp.Eproc.Ppid), nil
}

// isProcessTreeDescendant reports whether pid is a STRICT descendant of ancestor — walking pid's
// parent chain reaches ancestor. ancestor == pid is NOT a descendant (so the server connecting to
// itself, as in the unit test, is allowed). The walk is bounded so a garbage/cyclic table can't spin.
func isProcessTreeDescendant(ancestor, pid int, ppidOf func(int) (int, error)) (bool, error) {
	cur := pid
	for depth := 0; depth < 64; depth++ {
		parent, err := ppidOf(cur)
		if err != nil {
			return false, err
		}
		if parent == ancestor {
			return true, nil
		}
		if parent <= 1 {
			return false, nil
		}
		cur = parent
	}
	return false, nil
}

// authorizePeer rejects (a) any peer whose uid differs from this process's uid (same-user only), and
// (b) any peer that lives inside a safeslop-spawned process tree — the sandboxed agent reaching back
// to drive its own jailer (specs/0024 S1b). A same-uid peer OUTSIDE our tree (the cockpit GUI) is
// allowed; arbitrary same-uid host malware is the residual that the codesign/audit-token follow-on
// closes (specs/0024 Deferred). Codesign-identity verification needs Security.framework — see
// specs/0012 §2.
func authorizePeer(c *net.UnixConn) error {
	uid, err := peerUID(c)
	if err != nil {
		return fmt.Errorf("peer cred check: %w", err)
	}
	if uid != os.Getuid() {
		return fmt.Errorf("peer uid %d != server uid %d — cross-user control-plane access denied", uid, os.Getuid())
	}
	pid, err := peerPID(c)
	if err != nil {
		return nil // can't determine pid: fall back to the uid gate + the policy trust gate (S1a)
	}
	desc, err := isProcessTreeDescendant(os.Getpid(), pid, ppidOf)
	if err != nil {
		return nil // can't walk the tree: don't break the legit GUI on a transient sysctl error
	}
	if desc {
		return fmt.Errorf("control-plane access from pid %d denied: peer is inside a safeslop-spawned sandbox", pid)
	}
	return nil
}
```

- [ ] **Step 4: Run, verify pass (new logic + the existing same-process peer test)**

```bash
go test ./internal/engine/control/ -run 'TestIsProcessTreeDescendant|TestPeerUIDOnUnixSocket' -v
```
Expected: both PASS. (`TestPeerUIDOnUnixSocket` dials from the same process, so the peer pid equals
the server pid — not a strict descendant — and `authorizePeer` still returns nil.)

- [ ] **Step 5: Commit**

```bash
git add internal/engine/control/peerauth.go internal/engine/control/peerauth_test.go
git commit -m "feat(control): refuse control-plane peers inside a safeslop-spawned sandbox (specs/0024 S1b)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: docs — correct the review note, mark realized, record the behavior change

**Files:**
- Modify: `specs/research/2026-06-19-design-security-review.md`
- Modify: `README.md` (the isolation section)

- [ ] **Step 1: Correct Launch→OpenSession and mark realized in the review note**

In `specs/research/2026-06-19-design-security-review.md`, in the **S1** finding and the **Headline**,
clarify that the `Launch` RPC is transitively trust-gated (it shells out to `safeslop run`) and the
ungated path was **`OpenSession`**. Append to the S1 finding:

```markdown
- **REALIZED (specs/0024):** S1a trust-gates `resolveSession`/`OpenSession`; S1b refuses peers inside
  a safeslop-spawned process tree (LOCAL_PEERPID + parent-walk). Correction: the `Launch` RPC was
  already transitively gated via `safeslop run`; `OpenSession` was the exposed path. Remaining
  follow-on: codesign/audit-token peer verification (closes arbitrary same-uid host malware).
```

And append to the **S2** finding:

```markdown
- **REALIZED (specs/0024):** `childEnv` strict allowlist — sandbox/host children no longer inherit
  os.Environ(); ambient AWS_*/OP_SESSION/SSH_AUTH_SOCK/GITHUB_TOKEN/ANTHROPIC_API_KEY are dropped.
  Agents declare keys in `secrets:`.
```

- [ ] **Step 2: Document the behavior change in the README**

In `README.md`, under the isolation section (near the tier table from specs/0023), add one prose
paragraph (prose, so it won't trip `slop-sync-help`):

```markdown
The `sandbox` and `host` environments run the agent with a **scrubbed environment**: only safe
basics (`PATH`, `HOME`, `TERM`, `LANG`, …) are carried from your shell. Ambient credentials —
`AWS_*`, `OP_SESSION_*`, `SSH_AUTH_SOCK`, `GITHUB_TOKEN`, and even `ANTHROPIC_API_KEY` — are **not**
inherited. Give the agent a credential by declaring it in your `safeslop.cue` `secrets:` block
(e.g. `secrets: { ANTHROPIC_API_KEY: "op://Private/Anthropic/api-key" }`), which is staged
ephemerally and wiped on exit.
```

- [ ] **Step 3: Full gate + build**

```bash
make check                               # go vet + gofmt + go test ./...
make build                               # static CGO_ENABLED=0 binary
fish scripts/slop-sync-help.fish check   # README ↔ --help drift (prose paragraph must not trip it)
```
Expected: `make check` all ok; static binary; help-sync passes.

- [ ] **Step 4: Commit**

```bash
git add specs/research/2026-06-19-design-security-review.md README.md
git commit -m "docs: mark specs/0024 realized (OpenSession gate, env scrub) + README scrub note

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Gates & done-checklist

```bash
make check     # go vet + gofmt + go test ./...
make build     # static CGO_ENABLED=0 binary
fish scripts/slop-sync-help.fish check
```
Branch + PR (never push `main`):

```bash
git push -u origin sp-control-plane-trust   # or sp-security-review
gh pr create --fill
```

## Manual verification (reproduce the closed escape)

After `make build`, confirm the two crits are closed end-to-end:

```bash
# S2: a host secret is NOT inherited into the sandbox (declare-or-drop).
export AWS_ACCESS_KEY_ID=AKIA-SHOULD-NOT-LEAK
mkdir -p /tmp/scrub && printf 'package safeslop\nsafeslop: { version: 1, profiles: { d: {agent: "claude", environment: "sandbox", network: "deny"} } }\n' > /tmp/scrub/safeslop.cue
( cd /tmp/scrub && /Users/jojo/workspace/safeslop/safeslop trust && /Users/jojo/workspace/safeslop/safeslop run d --dry-run )
#   the dry-run resolves; a real run's child env must NOT contain AWS_ACCESS_KEY_ID (verify in the agent).

# S1a: an untrusted policy is refused by the cockpit path (unit-level proof is TestResolveSessionRefusesUntrustedPolicy).
```

## Deferred (follow-on slices)

- **Codesign/audit-token peer verification** — verify the peer's `audit_token` →
  `SecCodeCheckValidity` (or a `csops`/`codesign` shellout to stay CGO-free) so arbitrary same-uid
  *non-descendant* malware (a malicious npm postinstall in the user's shell) also can't drive the
  control plane. Needs a second signed binary to test; that's why it's its own slice. (specs/0012 §2.)
- **GUI "review & trust" screen** — when `OpenSession` returns the fail-closed trust error, the
  SwiftUI app shows an approval screen that calls a new `Trust` RPC (or shells `safeslop trust`),
  so Audience A never needs the CLI. TouchID-gate it (ayo M7).
- **Socket identity (M2)** — have the GUI verify the *server's* identity too, and refuse to start
  `serve` if `~/.safeslop/s.sock`'s dir is attacker-writable (socket-squatting).
```
