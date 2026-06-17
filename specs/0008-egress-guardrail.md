# Egress guardrail + hardening — credential-exfil surface

**Goal:** Add a non-fatal lint that flags the one egress combination the research found genuinely
dangerous and genuinely unguarded — a profile that stages **credentials/secrets** under
**`environment: sandbox` + `network: allow`** (the Seatbelt boundary has no egress topology, so a
prompt-injected agent can exfiltrate them) — plus two small regression/hardening closures, without
re-implementing the container egress baseline (which already exists and is tested).

**Architecture:** The container path is already correctly hardened — `internal/engine/container`
denies `127/8` + `169.254/16` + RFC1918 deny-first in `squid.conf.tmpl`, the agent runs on an
`internal: true` Docker net with squid as the sole egress bridge, and `policy_test.go` already pins
`169.254.169.254` blocked in both modes. The gap is **the other two boundaries and the missing
guardrail**: Seatbelt's `network: allow` is `(allow network*)` — wide open, zero filtering (it
*cannot* do per-IP; that is SP8's job), and nothing warns a user who stages cloud creds into that
combo. The VM's `network: deny` egress is *advisory* (`SLOP_VM_PROXY_URL`, HTTP_PROXY-style), which
the research calls bypassable. This plan adds a pure-Go `policy.Lint` advisory surfaced by
`slop validate` and `slop run`, a regression test pinning that no host-gateway/loopback bridge can
leak into the container, and honest documentation of the sandbox/VM egress limits. All changes are
Go + Markdown — no `scripts/*.fish`/`scripts/_py/*.py` (so the `script-doc-sync-check` CI gate does
not fire) and no embedded-asset edits (so `make check-assets` is unaffected).

**Tech stack:** Go 1.26 (`internal/engine/policy` + `internal/cli` + `internal/engine/container`),
the existing white-box test style.

**File structure:**
- `internal/engine/policy/lint.go` (create) — `Warning` type + `Lint(*Config) []Warning`.
- `internal/engine/policy/lint_test.go` (create) — the lint truth table.
- `internal/cli/cli.go` (modify) — `slop validate` loads + lints (prints + `--json`); `dispatchRun` prints warnings before launch.
- `internal/cli/cli_lint_test.go` (create) — the validate-surfaces-warnings helper test.
- `internal/engine/container/compose_test.go` (modify) — host-gateway / loopback leak regression test.
- `internal/engine/container/policy.go` (modify) — extend the `Decide` oracle to also reject metadata hostnames (pure Go; squid already blocks the resolved IP — this makes the oracle honest).
- `internal/engine/container/policy_test.go` (modify) — metadata-hostname oracle assertion.
- `README.md` (modify) — honest egress note (sandbox `allow` = no filtering; VM deny = advisory).
- `specs/research/2026-06-17-startup-usecase-prior-art.md` (modify) — tick actionable #3/#9 as started.

---

## Key design decisions

1. **Advisory, not fatal.** The lint warns; it never blocks `validate` or `run`. Escalating the
   sandbox+creds+open-egress combo to a refuse-without-`--force` is a deliberate follow-up decision,
   not this plan — keep v1 non-breaking.
2. **Don't re-pin what's pinned.** The container deny-baseline + `internal:true` topology are already
   tested; this plan adds only the *missing* guards (host-gateway leak regression, metadata-hostname
   oracle honesty) and the cross-boundary guardrail.
3. **The lint encodes the research's load-bearing insight** ("egress is the control; Seatbelt has no
   topology") as a concrete, hermetic, always-on check — the highest-value, lowest-risk slice of the
   egress actionables.

---

### Task 1: `policy.Lint` — the sandbox + creds + open-egress advisory

**Files:** Create `internal/engine/policy/lint.go`, `internal/engine/policy/lint_test.go`.

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/policy/lint_test.go
package policy

import "testing"

func TestLintFlagsSandboxOpenEgressWithCreds(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"risky":          {Environment: "sandbox", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
		"risky_pnpm":     {Environment: "sandbox", Network: "allow", Credentials: &Credentials{Pnpm: []PnpmRegistry{{Host: "registry.npmjs.org", Token: "env:T"}}}},
		"safe_deny":      {Environment: "sandbox", Network: "deny", Secrets: map[string]string{"A": "env:X"}},
		"safe_container": {Environment: "container", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
		"safe_nocreds":   {Environment: "sandbox", Network: "allow"},
	}}
	ws := Lint(cfg)
	got := map[string]string{}
	for _, w := range ws {
		got[w.Profile] = w.Code
	}
	if len(ws) != 2 {
		t.Fatalf("want 2 warnings, got %d: %+v", len(ws), ws)
	}
	for _, p := range []string{"risky", "risky_pnpm"} {
		if got[p] != "sandbox-open-egress-with-creds" {
			t.Fatalf("profile %q not flagged: %+v", p, ws)
		}
	}
	for _, p := range []string{"safe_deny", "safe_container", "safe_nocreds"} {
		if _, bad := got[p]; bad {
			t.Fatalf("profile %q should NOT be flagged: %+v", p, ws)
		}
	}
}

func TestLintIsDeterministicAndStable(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"b": {Environment: "sandbox", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
		"a": {Environment: "sandbox", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
	}}
	ws := Lint(cfg)
	if len(ws) != 2 || ws[0].Profile != "a" || ws[1].Profile != "b" {
		t.Fatalf("warnings must be sorted by profile: %+v", ws)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/policy/ -run TestLint -v
```
Expected: FAIL — `undefined: Lint` / `Warning`.

- [ ] **Step 3: Write the implementation**

```go
// internal/engine/policy/lint.go

package policy

import "sort"

// Warning is a non-fatal advisory about a dangerous profile combination,
// surfaced by `slop validate` and `slop run` (never blocks).
type Warning struct {
	Profile string `json:"profile"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Lint reports profiles whose configuration is legal but risky. Today it flags
// the one egress combination with no compensating control: credentials/secrets
// staged under environment:sandbox + network:allow. The Seatbelt boundary has no
// egress topology (it cannot do a per-IP/URL allowlist — that is the container's
// or SP8's job), so a prompt-injected agent can exfiltrate the staged creds.
func Lint(cfg *Config) []Warning {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []Warning
	for _, n := range names {
		p := cfg.Profiles[n]
		hasCreds := len(p.Secrets) > 0 || p.Credentials != nil
		if p.Environment == "sandbox" && p.Network == "allow" && hasCreds {
			out = append(out, Warning{
				Profile: n,
				Code:    "sandbox-open-egress-with-creds",
				Message: "stages credentials/secrets under environment:sandbox with network:allow — " +
					"the Seatbelt boundary has no egress filtering, so a compromised agent can exfiltrate them; " +
					"use environment:container/vm, or set network:deny",
			})
		}
	}
	return out
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
gofmt -w internal/engine/policy/ && go test ./internal/engine/policy/ -run TestLint -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/policy/lint.go internal/engine/policy/lint_test.go
git commit -m "feat(policy): lint sandbox+creds+open-egress as a credential-exfil risk"
```

---

### Task 2: surface warnings in `slop validate` and `slop run`

**Files:** Modify `internal/cli/cli.go`; Create `internal/cli/cli_lint_test.go`.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/cli_lint_test.go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

const riskyCue = `package slop
slop: {
	version: 1
	profiles: risky: {
		agent: "claude"
		environment: "sandbox"
		network: "allow"
		secrets: {ANTHROPIC_API_KEY: "env:ANTHROPIC_API_KEY"}
	}
}
`

func TestValidateAndLintSurfacesWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(path, []byte(riskyCue), 0o644); err != nil {
		t.Fatal(err)
	}
	warns, err := validateAndLint(path)
	if err != nil {
		t.Fatalf("validateAndLint: %v", err)
	}
	if len(warns) != 1 || warns[0].Code != "sandbox-open-egress-with-creds" || warns[0].Profile != "risky" {
		t.Fatalf("expected the exfil warning, got %+v", warns)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/cli/ -run TestValidateAndLintSurfacesWarning -v
```
Expected: FAIL — `undefined: validateAndLint`.

- [ ] **Step 3: Add `validateAndLint`, wire it into `cmdValidate` and `dispatchRun`**

Add the helper to `internal/cli/cli.go`:

```go
// validateAndLint loads + validates the config (returning any fatal error) and
// returns non-fatal lint warnings. Shared by `slop validate` and `slop run`.
func validateAndLint(path string) ([]policy.Warning, error) {
	cfg, err := policy.Load(path)
	if err != nil {
		return nil, err
	}
	return policy.Lint(cfg), nil
}

// printWarnings writes lint advisories to stderr (human mode only; JSON callers
// embed them in their payload).
func printWarnings(ws []policy.Warning) {
	for _, w := range ws {
		fmt.Fprintf(os.Stderr, "warning: profile %q %s\n", w.Profile, w.Message)
	}
}
```

Replace `cmdValidate`'s `RunE` body (`internal/cli/cli.go:65-79`) with:

```go
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig(arg0(args))
			if err != nil {
				return err
			}
			warns, err := validateAndLint(path)
			if err != nil {
				return err
			}
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "path": path, "warnings": warns})
			} else {
				fmt.Printf("ok: %s is valid\n", path)
				printWarnings(warns)
			}
			return nil
		},
```

In `cmdRun`'s `RunE` (`internal/cli/cli.go:170-238`), print warnings before launching. Insert these
lines immediately after `selectProfile` returns `name, prof, err` (and the `err` check), before
`agentArgv` — non-JSON only, so a `--dry-run --json` payload is unchanged:

```go
				if !jsonOut {
					printWarnings(policy.Lint(&policy.Config{Profiles: map[string]policy.Profile{name: prof}}))
				}
```

(There is no `dispatchRun` helper today — `cmdRun` holds the launch flow inline — so this is the one
insertion point.)

- [ ] **Step 4: Run, verify it passes**

```bash
gofmt -w internal/cli/ && go test ./internal/cli/ -v
```
Expected: PASS (new test + all existing cli tests).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_lint_test.go
git commit -m "feat(cli): surface lint warnings in validate (+json) and run"
```

---

### Task 3: container — host-gateway leak regression + honest metadata-hostname oracle

**Files:** Modify `internal/engine/container/compose_test.go`, `internal/engine/container/policy.go`, `internal/engine/container/policy_test.go`.

- [ ] **Step 1: Write the failing tests**

```go
// append to internal/engine/container/compose_test.go

// The agent must never gain a host bridge — OrbStack/Docker Desktop can otherwise
// route an internal container to the host loopback (host.docker.internal), defeating
// the squid-only egress topology. Pin that none of these ever leak into the compose.
func TestComposeHasNoHostBridgeLeak(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"host.docker.internal", "host-gateway", "extra_hosts", "network_mode: host", "network_mode: \"host\""} {
		if strings.Contains(yml, bad) {
			t.Fatalf("compose leaks a host bridge (%q):\n%s", bad, yml)
		}
	}
	// the agent service must still be internal-only (belt with the existing test's braces).
	if !strings.Contains(yml, "internal: true") {
		t.Fatalf("agent net no longer internal-only:\n%s", yml)
	}
}
```

```go
// append to internal/engine/container/policy_test.go

// Decide is the test-oracle for squid; make it honest about metadata HOSTNAMES,
// not just the resolved link-local IP (squid blocks the IP after DNS; the oracle
// should refuse the hostname too so policy reasoning matches enforcement).
func TestDecideBlocksMetadataHostnames(t *testing.T) {
	for _, host := range []string{"metadata.google.internal", "metadata", "instance-data.ec2.internal"} {
		if Decide(host, "allow") {
			t.Fatalf("metadata hostname %q must be denied even in allow mode", host)
		}
	}
}
```

- [ ] **Step 2: Run, verify they fail**

```bash
go test ./internal/engine/container/ -run 'TestComposeHasNoHostBridgeLeak|TestDecideBlocksMetadataHostnames' -v
```
Expected: the compose test PASSES already (regression guard — template has no bridge); the Decide
test FAILS (`Decide` only checks IP prefixes today).

- [ ] **Step 3: Extend the `Decide` oracle (pure Go; no asset edit)**

In `internal/engine/container/policy.go`, add a metadata-hostname blocklist and check it in `Decide`
before the allow logic:

```go
// blockedHosts are denied in BOTH modes: the cloud metadata hostnames that resolve
// to the link-local IP squid already blocks. Listed explicitly so the oracle (and a
// future non-IP enforcer) refuses the name, not only the resolved address.
var blockedHosts = []string{"metadata.google.internal", "metadata", "instance-data.ec2.internal"}
```

Then in `Decide`, after the `blockedNets` loop and before `if network == "allow"`:

```go
	for _, h := range blockedHosts {
		if domain == h {
			return false
		}
	}
```

- [ ] **Step 4: Run, verify both pass**

```bash
gofmt -w internal/engine/container/ && go test ./internal/engine/container/ -v
```
Expected: PASS (whole package, including the pre-existing squid/metadata tests).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/container/compose_test.go internal/engine/container/policy.go internal/engine/container/policy_test.go
git commit -m "test(container): pin no host-bridge leak; deny metadata hostnames in the oracle"
```

---

### Task 4: honest egress docs, research cross-ref, verify, PR

**Files:** Modify `README.md`, `specs/research/2026-06-17-startup-usecase-prior-art.md`.

- [ ] **Step 1: Add an honest egress-limits note to the README**

Find the sandbox/network section:

```bash
grep -n 'network: allow\|sandbox-exec\|coarse network\|network policy' README.md | head
```

Add a short note (in that section) stating plainly:
- `environment: sandbox` + `network: allow` does **no** egress filtering (Seatbelt is all-or-nothing) — stage no credentials into that combo; `slop validate`/`slop run` will warn. Use `container`/`vm` for credentialed or untrusted work; per-process egress filtering arrives with SP8.
- `environment: vm` + `network: deny` egress is **advisory** (an `SLOP_VM_PROXY_URL` HTTP-proxy), not topological — point it at a filtering proxy; topological VM egress is future work.

(Confirm with `git diff --name-only` that only `README.md`, `specs/`, `internal/` changed — no `scripts/` — so the `script-doc-sync-check` gate stays dormant.)

- [ ] **Step 2: Tick the research actionables**

In `specs/research/2026-06-17-startup-usecase-prior-art.md`, append to actionables #3 and #9 a note:
`(started: specs/0008 — lint + host-bridge regression + honest docs; metadata/DNS/loopback container baseline confirmed already enforced+tested).`

- [ ] **Step 3: Full verification**

```bash
cd /Users/jojo/workspace/safeslop
make check          # go vet + gofmt + go test ./...
make build
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green (no scripts touched; fish gates unaffected but run per the done-checklist).

- [ ] **Step 4: Manual smoke**

```bash
printf 'package slop\nslop: {version: 1, profiles: risky: {agent: "claude", environment: "sandbox", network: "allow", secrets: {K: "env:K"}}}\n' > /tmp/slop-risky.cue
./slop validate /tmp/slop-risky.cue            # prints "ok" + the exfil warning on stderr
./slop validate --json /tmp/slop-risky.cue     # warnings[] in the JSON payload
```
Expected: the warning names profile `risky` with code `sandbox-open-egress-with-creds`.

- [ ] **Step 5: Branch, push, PR**

```bash
git push -u origin egress-guardrail
gh pr create --title "Egress guardrail: lint sandbox+creds+open-egress; pin no host-bridge leak" \
  --body "Implements the high-value slice of the egress research actionables (specs/research/2026-06-17). Adds a non-fatal lint flagging credentials staged under environment:sandbox + network:allow (Seatbelt has no egress topology -> exfil-ready), surfaced by validate (+json) and run; a regression test pinning no host.docker.internal/host-gateway bridge can leak into the container; an honest metadata-hostname oracle; and honest README notes on the sandbox/VM egress limits. The container deny-baseline (169.254/16 + RFC1918 + loopback, internal:true topology) was already implemented and tested — confirmed, not duplicated. Go + docs only."
```

---

## Verification (what "done" means)

- `make check` + `make build` green; the four fish gates green.
- `policy.Lint` flags exactly the sandbox+`network:allow`+creds combo, deterministically; `validate`
  (human + `--json`) and `run` surface it without ever blocking.
- A regression test fails if any future edit adds a host-gateway/loopback bridge to the container.
- The `Decide` oracle refuses metadata hostnames, matching squid's IP-level enforcement.
- README states the sandbox-`allow` (no filtering) and VM-`deny` (advisory) limits honestly.

## Deliberately deferred (not here)

- **Escalating the lint to a hard refuse** (`--force` to override) — a UX/safety decision for later.
- **Topological VM egress** (route the Tart vNIC through a filtering bridge instead of advisory
  `SLOP_VM_PROXY_URL`) — real work, its own plan.
- **Per-process egress filtering for the sandbox boundary** — that is SP8 (NetworkExtension); the
  lint is the interim guardrail.
- **DNS-tunneling hardening beyond topology** — the `internal:true` net already blocks raw external
  DNS for the container; a dedicated logged resolver is future egress work.
- **The AWS/GCP credential provider** — the larger, separate plan (the genuine #1 gap); this plan
  only guards the *exfil surface* that a missing-but-soon cloud-cred story will widen.
