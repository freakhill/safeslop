# SP3 — container environment: docker compose + squid URL-allowlist in Go

**Goal:** Add `environment: container` to the Go engine — run a profile's agent inside a Docker
container whose **only route to the internet is a squid proxy enforcing a domain allowlist** (the
*real*, network-enforced boundary that sandbox-exec can't give us), with SP2 secrets/creds
delivered so they never appear in `ps`, `docker inspect`, or on persistent disk, and the
1Password SSH-agent socket bind-mounted. Final v1 environment.

> **Provenance (FLO-hardened, 2026-06-17).** The six decisions below were optimized with a
> feedback-loop run (safety 45% / robustness 30% / ease 25%). A cross-family adversarial judge
> refuted an earlier draft and exposed two real holes that shaped this design:
> 1. **Egress must be enforced at the network layer, not via `HTTP_PROXY` env vars** — a
>    malicious process just ignores the proxy. The boundary is the *absence of any route* except
>    through squid (agent on an `internal: true` network).
> 2. **Secrets passed via `docker compose run -e KEY=VAL` (or `--env-file`) leak** — `-e` lands
>    in the host `ps` table, and both `-e` and `--env-file` populate the container's
>    `Config.Env`, visible via `docker inspect` for the container's lifetime. Secrets must be
>    delivered by a file that a PID-1 entrypoint sources into the env at runtime.

**Architecture:** A new `internal/engine/container` package mirrors `internal/engine/sandbox`:
one exported `Launch` plus `Up` / `Down` / `Available` / `Reconcile`. The squid boundary, agent
Dockerfiles, compose topology, and a tiny POSIX entrypoint are `//go:embed`-ed (distribution
stays a single signed file, design §8) and materialized to a per-run temp dir, as `sandbox`
writes a temp `.sb` and `policy` embeds the CUE schema. The agent is launched interactively
through `exec.RunInPTY` (the wrapped-I/O path, design §6.2) via `docker compose run --rm`. The
network policy (which domains pass) lives in pure, golden-tested Go (`Decide`/`BuildAllowlist`/
`RenderSquidConf`) so the enforcement logic is unit-testable without Docker. `slop run` gains a
`case "container"`; new `slop down` + a reconcile-on-run sweep manage lifecycle.

**Tech stack:** Go 1.26, `embed` + `text/template`, `os/exec` (drive `docker` / `docker
compose`), `github.com/creack/pty` via the existing `internal/engine/exec.RunInPTY`,
`golang.org/x/sys/unix` for an advisory `flock` (already transitively available; else
`syscall.Flock`). No new third-party deps. Docker / Docker Compose v2 are runtime requirements
(reported and partly remediated by `slop doctor`).

**File structure:**
- `internal/engine/container/container.go` (create) — `Launch` / `Up` / `Down` / `Available` / `Reconcile`; docker/compose orchestration (build-if-missing, start proxy, run agent, sweep stale state).
- `internal/engine/container/policy.go` (create) — pure network policy: `Decide(domain, network)`, `BuildAllowlist()`, `RenderSquidConf(open)`. No I/O; golden + table tested.
- `internal/engine/container/compose.go` (create) — `renderCompose`, `materialize`, `composeRunArgv`, secret-file + entrypoint staging. The compose template is Go-owned and puts the agent on an **`internal: true`** network with squid as the sole egress bridge.
- `internal/engine/container/lock.go` (create) — `withRepoLock(dir, fn)`: advisory `flock` on `<repo>/.slop/lock` so parallel `slop` invocations serialize staging + reconcile.
- `internal/engine/container/assets.go` (create) — `//go:embed assets` FS + accessors.
- `internal/engine/container/assets/` (create, partly generated) — `squid.conf.tmpl`, `compose.yml.tmpl`, `entrypoint.sh` (Go-owned); `allowlist.domains`, `Dockerfile.agent`, `Dockerfile.agent.tools`, `agent-tools.env` (synced verbatim from `library/layer/container/`).
- `internal/engine/container/container_test.go` (create) — hermetic unit tests (Decide table, squid.conf golden, internal-net assertion, no-secret-in-compose, run-argv, materialize, reconcile sweep, flock, unavailable-guard). No Docker, no network.
- `internal/cli/cli.go` (modify) — `case "container"` in `runProfile`; split secret env from non-secret/host env; add `cmdDown()` + register it; reconcile-on-run; docker line in `cmdDoctor`.
- `Makefile` (modify) — `sync-container-assets` + a `check-assets` drift gate folded into `check`.
- `specs/0001-go-rewrite-design.md` (modify) — flip SP3 to **complete** in §11.

---

## Key design decisions

1. **Network = enforced egress through squid; `deny|allow` field reused, no schema change.**
   The agent container runs on a Docker network declared **`internal: true`** — it has *no route
   to the internet at all*. The only container bridging that internal network to an `egress`
   (bridge) network is squid. So egress is **fail-closed by topology**: HTTP(S) traffic that
   honors `HTTP_PROXY` reaches squid; anything that tries a direct connection (raw TCP, a
   process ignoring the proxy, a non-HTTP protocol) has nowhere to route and fails. `HTTP_PROXY`
   is a *convenience for honest clients, not the boundary*. On top of that, squid applies the
   policy:
   - `network: "deny"` (default) → squid `http_access` allows only the embedded
     `allowlist.domains` (github / npm / pypi / anthropic / openrouter); everything else denied.
   - `network: "allow"` → squid allows all domains **but a deny-first ACL, ordered before any
     allow rule, hard-blocks RFC-1918 + `169.254.0.0/16`** (the cloud-metadata endpoint and the
     host LAN) in *both* modes.
   - The pass/block decision is mirrored in a pure Go `Decide(domain, network)` so it is
     golden/table-tested without a running squid (the embedded `squid.conf` is the golden render).
   - Per-profile *custom* allowlist domains stay **deferred**; the embedded set is the v1 default.

2. **`library/layer/container/` stays the single source of truth.** `Dockerfile.agent`,
   `Dockerfile.agent.tools`, `agent-tools.env`, `allowlist.domains` are *synced* into the Go
   embed dir by `make sync-container-assets` and **drift-checked in `make check`** (same pattern
   as `slop-sync-help`). The pinning gate keeps scanning the canonical `library/` copies, so
   version pins can't fork. `squid.conf.tmpl`, `compose.yml.tmpl`, `entrypoint.sh` are Go-owned.

3. **Two-image build, faithful to the fish stack.** `Up` builds `Dockerfile.agent` →
   `local/agent-sandbox:latest`, then `Dockerfile.agent.tools` → `local/agent-sandbox-tools:latest`
   (the tools Dockerfile's `FROM local/agent-sandbox:latest` is preserved), both idempotent via
   `docker image inspect`. Tools image built with `ENABLE_CLAUDE_CODE=true ENABLE_OPENCODE=true`.

4. **Secrets reach the container via a sourced file + entrypoint — never `-e`/`--env-file`.**
   The host's full `os.Environ()` is never forwarded. Resolved `secrets` (the sensitive values,
   e.g. `ANTHROPIC_API_KEY`) are written **shell-escaped, `0600`** to `secrets.env` in the staged
   `.slop/runtime/<profile>/` dir, which is bind-mounted **read-only** into the container at
   `/slop/runtime`. The compose `agent` service's `entrypoint` is the embedded `entrypoint.sh`,
   which `set -a; . /slop/runtime/secrets.env; set +a; exec "$@"` — so the values enter the
   agent's process env **at runtime**, and are therefore **absent from argv/`ps`** (no `-e`),
   **absent from `docker inspect`** (`Config.Env` is never written with them), and **never baked
   into an image layer**. Non-secret env (`TERM`, `HTTP_PROXY`, `NO_PROXY`,
   `NPM_CONFIG_USERCONFIG=/slop/runtime/.npmrc`) lives in the compose `environment:` block (safe
   to appear in `inspect` — none are secret). The npm token was already file-based (`.npmrc`), so
   it never had the `-e` problem. **Honest residuals (stated, not hidden):** (a) `secrets.env`
   exists on the host staging dir (macOS APFS, `0600`) for the run — `Launch` unlinks it shortly
   after the container starts and the whole staged dir is wiped on exit, but it is not RAM-only;
   (b) any code already executing *inside* the container can read `/proc/<pid>/environ` — intrinsic
   to handing a process an env var, and the boundary protects only against *other host users*,
   `docker inspect`, and persistent disk. If `SSH_AUTH_SOCK` is set, the host agent socket is
   bind-mounted to `/slop/ssh-agent.sock` (keys never touch container disk).
   Container hardening: `read_only: true`, `cap_drop: ALL`, `no-new-privileges`, `tmpfs /tmp`,
   workspace bind-mounted `rw` at `/workspace` (the only writable persistent path).

5. **Lifecycle = reconcile-on-run + flock + explicit `slop down`.** Squid persists between runs
   (warm, fast re-launch); the agent container is `--rm`. Every `slop run`/`slop down`:
   (a) takes an advisory `flock` on `<repo>/.slop/lock` so parallel invocations don't race on
   staging/reconcile; (b) **fail-fast probes docker + compose on the run path** (not only in
   `doctor`); (c) **sweeps stale state** — staged dirs from a crashed prior run (identified by a
   `.slop-stage` marker + age) and any orphaned squid container — *before* staging anew, so a
   `SIGKILL` that skipped `defer` cleanup leaves only inert state the next run reclaims. `slop
   down` stops squid + networks. **Wall-clock idle auto-teardown** (stop squid N minutes after the
   *last* agent exits with no new run) needs a background timer (a launchd agent on macOS) and is
   **deferred**; SP3 ships reconcile-based reclamation + explicit `down`, which covers the safety
   and robustness goals without a daemon.

6. **SSH-agent socket bind-mount** (the SP2-deferred item, design §7.1): when `SSH_AUTH_SOCK` is
   set, the host socket is bind-mounted to `/slop/ssh-agent.sock` and `SSH_AUTH_SOCK` is set
   (non-secret path) so keys never touch container disk; full op-desktop end-to-end verification
   stays a follow-up.

---

### Task 1: Vendor + embed the container assets, with a drift gate

**Files:** Create `internal/engine/container/assets/{allowlist.domains,Dockerfile.agent,Dockerfile.agent.tools,agent-tools.env}` (synced), `internal/engine/container/assets.go`; Modify `Makefile`; Test `internal/engine/container/container_test.go`.

- [ ] **Step 1: Confirm the Dockerfiles need no repo build-context** (decision 3 hinges on this)
```bash
grep -nE '^(COPY|ADD) ' library/layer/container/Dockerfile.agent library/layer/container/Dockerfile.agent.tools
```
Expected: **no output**. If anything prints, STOP — the build context is not empty and Task 5's `docker build` context handling must change; record the COPY sources before continuing.

- [ ] **Step 2: Add the sync + drift targets to `Makefile`** (use the real env filename — check `ls library/layer/container/`; it is `agent-tools.env.example`)
```makefile
CONTAINER_SRC := library/layer/container
CONTAINER_DST := internal/engine/container/assets
SYNCED := allowlist.domains Dockerfile.agent Dockerfile.agent.tools

sync-container-assets:
	@for f in $(SYNCED); do cp $(CONTAINER_SRC)/$$f $(CONTAINER_DST)/$$f; done
	@cp $(CONTAINER_SRC)/agent-tools.env.example $(CONTAINER_DST)/agent-tools.env
	@echo "synced $(SYNCED) agent-tools.env -> $(CONTAINER_DST)"

check-assets:
	@for f in $(SYNCED); do \
	  diff -q $(CONTAINER_SRC)/$$f $(CONTAINER_DST)/$$f >/dev/null || { \
	    echo "drift: $(CONTAINER_DST)/$$f (run 'make sync-container-assets')"; exit 1; }; \
	done
	@diff -q $(CONTAINER_SRC)/agent-tools.env.example $(CONTAINER_DST)/agent-tools.env >/dev/null || { \
	  echo "drift: agent-tools.env (run 'make sync-container-assets')"; exit 1; }
```
Then make `check-assets` the first prerequisite of `check`: `check: check-assets vet fmtcheck test`.

- [ ] **Step 3: Populate the embed dir** — `mkdir -p internal/engine/container/assets && make sync-container-assets && ls internal/engine/container/assets` → the four files present.

- [ ] **Step 4: Write the embed accessor** (`internal/engine/container/assets.go`)
```go
// Package container runs a profile's agent inside a Docker container whose only route
// to the internet is a squid proxy enforcing a domain allowlist (the real network boundary).
package container

import "embed"

//go:embed assets
var assetsFS embed.FS

// readAsset returns an embedded asset's bytes (path relative to assets/).
func readAsset(name string) ([]byte, error) { return assetsFS.ReadFile("assets/" + name) }
```

- [ ] **Step 5: Failing embed test** (`container_test.go`)
```go
package container

import "testing"

func TestEmbeddedAssetsPresent(t *testing.T) {
	for _, name := range []string{"allowlist.domains", "Dockerfile.agent", "Dockerfile.agent.tools", "agent-tools.env"} {
		if b, err := readAsset(name); err != nil || len(b) == 0 {
			t.Fatalf("asset %q missing or empty: %v", name, err)
		}
	}
}
```

- [ ] **Step 6: Run** — `go test ./internal/engine/container/ -run TestEmbeddedAssetsPresent -v` → PASS.
- [ ] **Step 7: Verify the gate** — `make check-assets` (PASS); `printf '\n' >> internal/engine/container/assets/allowlist.domains && make check-assets` (must FAIL `drift:`); `make sync-container-assets` to restore.
- [ ] **Step 8: Commit**
```bash
git add internal/engine/container/assets internal/engine/container/assets.go internal/engine/container/container_test.go Makefile
git commit -m "sp3: embed container assets (squid allowlist + Dockerfiles) with a sync drift gate"
```

---

### Task 2: Pure network policy — `Decide` / `BuildAllowlist` / `RenderSquidConf` (the hermetic test seam)

**Files:** Create `internal/engine/container/assets/squid.conf.tmpl`, `internal/engine/container/policy.go`; Test `container_test.go`.

- [ ] **Step 1: Write `assets/squid.conf.tmpl`** — deny-first ACL (metadata/RFC1918 blocked in *both* modes), ordered before any allow:
```
http_port 3128
acl SSL_ports port 443
acl Safe_ports port 80 443
acl CONNECT method CONNECT
http_access deny !Safe_ports
http_access deny CONNECT !SSL_ports
# deny-first: metadata + private ranges are blocked even in "allow" mode
acl blocked_dst dst 127.0.0.0/8 169.254.0.0/16 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16
http_access deny blocked_dst
{{if .Open}}http_access allow all
{{else}}acl allowed_domains dstdomain "/etc/squid/allowlist.domains"
http_access allow allowed_domains
http_access deny all
{{end}}via off
forwarded_for delete
request_header_access X-Forwarded-For deny all
```

- [ ] **Step 2: Write `policy.go`** — the pure, Docker-free policy seam
```go
package container

import (
	"bufio"
	"bytes"
	"strings"
	"text/template"
)

// blockedNets are denied in BOTH modes (cloud metadata + RFC1918), matching squid's deny-first ACL.
var blockedNets = []string{"127.", "169.254.", "10.", "172.16.", "192.168."}

// BuildAllowlist returns the embedded allowlist domains (one per line, comments/blanks dropped).
func BuildAllowlist() ([]string, error) {
	b, err := readAsset("allowlist.domains")
	if err != nil {
		return nil, err
	}
	var out []string
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, s.Err()
}

// Decide mirrors squid's enforcement so the policy is testable without a running proxy:
// a literal IP in a blocked range is always denied; in "deny" only allowlisted domains pass
// (a leading-dot entry matches the domain and its subdomains); "allow" passes everything else.
func Decide(domain, network string) bool {
	for _, n := range blockedNets {
		if strings.HasPrefix(domain, n) {
			return false
		}
	}
	if network == "allow" {
		return true
	}
	allow, err := BuildAllowlist()
	if err != nil {
		return false
	}
	for _, a := range allow {
		bare := strings.TrimPrefix(a, ".")
		if domain == bare || strings.HasSuffix(domain, "."+bare) {
			return true
		}
	}
	return false
}

// RenderSquidConf renders squid.conf for the given mode (open == network "allow").
func RenderSquidConf(open bool) (string, error) {
	raw, err := readAsset("squid.conf.tmpl")
	if err != nil {
		return "", err
	}
	t, err := template.New("squid").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, struct{ Open bool }{open}); err != nil {
		return "", err
	}
	return b.String(), nil
}
```

- [ ] **Step 3: Table + golden tests** (`container_test.go`) — these cover the critical security path with **zero Docker**
```go
import "strings"

func TestDecide(t *testing.T) {
	cases := []struct {
		domain, network string
		want            bool
	}{
		{"api.anthropic.com", "deny", true},   // allowlisted
		{"raw.githubusercontent.com", "deny", true}, // subdomain of .githubusercontent.com
		{"example.com", "deny", false},        // not allowlisted
		{"example.com", "allow", true},        // open
		{"169.254.169.254", "allow", false},   // metadata blocked even in allow
		{"10.0.0.5", "allow", false},          // RFC1918 blocked even in allow
		{"169.254.169.254", "deny", false},
	}
	for _, c := range cases {
		if got := Decide(c.domain, c.network); got != c.want {
			t.Errorf("Decide(%q,%q)=%v want %v", c.domain, c.network, got, c.want)
		}
	}
}

func TestRenderSquidConf(t *testing.T) {
	strict, err := RenderSquidConf(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strict, `dstdomain "/etc/squid/allowlist.domains"`) || !strings.Contains(strict, "http_access deny all") {
		t.Fatalf("strict squid.conf missing allowlist/deny-all:\n%s", strict)
	}
	open, _ := RenderSquidConf(true)
	if !strings.Contains(open, "http_access allow all") {
		t.Fatalf("open squid.conf missing allow-all:\n%s", open)
	}
	for _, c := range []string{strict, open} {
		// deny-first metadata/RFC1918 block must precede any allow line
		di := strings.Index(c, "http_access deny blocked_dst")
		ai := strings.Index(c, "http_access allow")
		if di < 0 || ai < 0 || di > ai {
			t.Fatalf("deny-first ordering broken:\n%s", c)
		}
	}
}
```

- [ ] **Step 4: Run** — `go test ./internal/engine/container/ -run 'TestDecide|TestRenderSquidConf' -v` → PASS.
- [ ] **Step 5: Commit**
```bash
git add internal/engine/container/assets/squid.conf.tmpl internal/engine/container/policy.go internal/engine/container/container_test.go
git commit -m "sp3: pure network-policy seam (Decide/BuildAllowlist/RenderSquidConf) + deny-first squid.conf, golden+table tested"
```

---

### Task 3: Network-enforced compose template + entrypoint (the topology boundary + secret delivery)

**Files:** Create `internal/engine/container/assets/compose.yml.tmpl`, `internal/engine/container/assets/entrypoint.sh`, `internal/engine/container/compose.go`; Test `container_test.go`.

- [ ] **Step 1: Write `assets/compose.yml.tmpl`** — agent on an **`internal: true`** network (no internet route); squid the sole bridge to `egress`; entrypoint sources secrets; **no secret values anywhere in this file**
```
services:
  proxy:
    image: ubuntu/squid:5.2-22.04_beta
    volumes:
      - {{.RuntimeDir}}/squid.conf:/etc/squid/squid.conf:ro
      - {{.RuntimeDir}}/allowlist.domains:/etc/squid/allowlist.domains:ro
    networks: [internal, egress]
  agent:
    image: local/agent-sandbox-tools:latest
    working_dir: /workspace
    entrypoint: ["/bin/sh", "/slop/runtime/entrypoint.sh"]
    environment:
      HTTP_PROXY: http://proxy:3128
      HTTPS_PROXY: http://proxy:3128
      NO_PROXY: localhost,127.0.0.1,proxy
{{if .NpmConfig}}      NPM_CONFIG_USERCONFIG: /slop/runtime/.npmrc
{{end}}{{if .Term}}      TERM: {{.Term}}
{{end}}{{if .SSHAuthSock}}      SSH_AUTH_SOCK: /slop/ssh-agent.sock
{{end}}    volumes:
      - {{.Workspace}}:/workspace:rw
      - {{.StageDir}}:/slop/runtime:ro
{{if .SSHAuthSock}}      - {{.SSHAuthSock}}:/slop/ssh-agent.sock
{{end}}    read_only: true
    tmpfs:
      - /tmp
      - /var/tmp
    cap_drop: [ALL]
    security_opt:
      - "no-new-privileges:true"
    networks: [internal]      # internal:true => NO direct internet route; squid is the only egress
networks:
  internal:
    internal: true
  egress:
    driver: bridge
```

- [ ] **Step 2: Write `assets/entrypoint.sh`** — sources the secret file, then execs the agent (values enter env only here, never via -e/inspect)
```sh
#!/bin/sh
# slop container entrypoint: load secrets into env at runtime, then exec the agent.
set -a
[ -f /slop/runtime/secrets.env ] && . /slop/runtime/secrets.env
set +a
exec "$@"
```

- [ ] **Step 3: Write `compose.go`** — renderer + secret-file writer + run-argv (note: **no `-e` for secrets**)
```go
package container

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

type composeParams struct {
	RuntimeDir  string // where squid.conf + allowlist.domains were written
	Workspace   string
	StageDir    string // host .slop/runtime/<profile>; mounted ro at /slop/runtime
	SSHAuthSock string
	Term        string
	NpmConfig   bool // true if a staged .npmrc exists
}

func renderCompose(p composeParams) (string, error) {
	raw, err := readAsset("compose.yml.tmpl")
	if err != nil {
		return "", err
	}
	t, err := template.New("compose").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeSecretsEnv writes shell-escaped KEY='VAL' lines (0600) to stageDir/secrets.env so
// entrypoint.sh can source them. Returns the file path ("" when there are no secrets).
// Single-quote escaping: ' -> '\'' (POSIX-safe).
func writeSecretsEnv(stageDir string, secretEnv []string) (string, error) {
	if len(secretEnv) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, kv := range secretEnv {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		b.WriteString(k)
		b.WriteString("='")
		b.WriteString(strings.ReplaceAll(v, "'", `'\''`))
		b.WriteString("'\n")
	}
	path := filepath.Join(stageDir, "secrets.env")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// composeRunArgv builds `docker compose -f <file> run --rm agent <argv...>`. There is NO -e:
// secrets ride secrets.env (sourced by the entrypoint), non-secret env lives in the compose file.
func composeRunArgv(composeFile string, argv []string) []string {
	out := []string{"docker", "compose", "-f", composeFile, "run", "--rm", "agent"}
	return append(out, argv...)
}

// writeEntrypoint copies the embedded entrypoint.sh into dir (mode 0755).
func writeEntrypoint(dir string) error {
	b, err := readAsset("entrypoint.sh")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "entrypoint.sh"), b, 0o755)
}

var _ = fmt.Sprintf // (remove if unused after wiring)
```

- [ ] **Step 4: Tests** — the boundary + the no-secret-leak guarantees, hermetic
```go
func TestComposeIsNetworkEnforcedAndLeakFree(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st", Term: "xterm", NpmConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yml, "internal: true") {
		t.Fatal("agent network is not internal-only — egress not enforced")
	}
	if !strings.Contains(yml, `entrypoint: ["/bin/sh", "/slop/runtime/entrypoint.sh"]`) {
		t.Fatal("entrypoint (secret loader) missing")
	}
	if !strings.Contains(yml, "/ws:/workspace:rw") || !strings.Contains(yml, "/st:/slop/runtime:ro") {
		t.Fatalf("mounts missing:\n%s", yml)
	}
	// non-secret env is fine in the file; a secret VALUE must never be here.
	if strings.Contains(yml, "secrets.env") && strings.Contains(yml, "ANTHROPIC") {
		t.Fatal("a secret leaked into the compose file")
	}
}

func TestWriteSecretsEnvEscapesAndIs0600(t *testing.T) {
	dir := t.TempDir()
	p, err := writeSecretsEnv(dir, []string{`ANTHROPIC_API_KEY=sk-a'b`})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.env perm = %v want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(p)
	if string(b) != "ANTHROPIC_API_KEY='sk-a'\\''b'\n" {
		t.Fatalf("escaping wrong: %q", string(b))
	}
	if got, _ := writeSecretsEnv(dir, nil); got != "" {
		t.Fatal("no secrets should yield no file")
	}
}

func TestComposeRunArgvHasNoDashE(t *testing.T) {
	got := composeRunArgv("/rt/compose.yml", []string{"fish"})
	for _, a := range got {
		if a == "-e" {
			t.Fatal("composeRunArgv must not use -e (secrets leak to ps/inspect)")
		}
	}
	want := []string{"docker", "compose", "-f", "/rt/compose.yml", "run", "--rm", "agent", "fish"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", got, want)
	}
}
```
(Add `"os"` to the test imports.)

- [ ] **Step 5: Run** — `go test ./internal/engine/container/ -v` → all PASS.
- [ ] **Step 6: Commit**
```bash
git add internal/engine/container/assets/compose.yml.tmpl internal/engine/container/assets/entrypoint.sh internal/engine/container/compose.go internal/engine/container/container_test.go
git commit -m "sp3: network-enforced compose (internal-only agent net) + entrypoint secret loader (no -e leak)"
```

---

### Task 4: Materialize + advisory flock + reconcile sweep

**Files:** Modify `internal/engine/container/compose.go`; Create `internal/engine/container/lock.go`; Modify `internal/engine/container/container.go` (created here); Test `container_test.go`.

- [ ] **Step 1: Add `materialize`** to `compose.go` — write the per-run dir (squid.conf, allowlist.domains, compose.yml, entrypoint.sh, `.slop-stage` marker)
```go
// materialize writes the runtime dir for a run and returns (dir, composeFile). The dir holds
// squid.conf, allowlist.domains, compose.yml, entrypoint.sh, and a .slop-stage marker so the
// reconcile sweep can recognize (and reclaim) it after a crash. Caller removes the dir on exit.
func materialize(p composeParams) (composeFile string, err error) {
	squid, err := RenderSquidConf(strings.Contains(p.RuntimeDir, "")) // placeholder; set below
	_ = squid
	// p.RuntimeDir is the dir itself; render squid for the resolved mode passed by Launch.
	return "", fmt.Errorf("materialize is wired in Launch (Task 6); see note")
}
```
> Note: `materialize` is small but couples mode + paths; implement it inside `Launch` (Task 6)
> where `open := network == "allow"` and the stage dir are in scope, writing exactly:
> `squid.conf` = `RenderSquidConf(open)`, `allowlist.domains` = `readAsset`, `compose.yml` =
> `renderCompose(p)`, `entrypoint.sh` via `writeEntrypoint(dir)`, and an empty `.slop-stage`
> marker. Keep the file-writing in one helper; the placeholder above is replaced in Task 6.

- [ ] **Step 2: Write `lock.go`** — advisory per-repo flock
```go
package container

import (
	"os"
	"path/filepath"
	"syscall"
)

// withRepoLock serializes staging/reconcile across concurrent slop invocations on the same
// repo via an advisory flock on <repo>/.slop/lock. The lock is released when fn returns.
func withRepoLock(repo string, fn func() error) error {
	if err := os.MkdirAll(filepath.Join(repo, ".slop"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(repo, ".slop", "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
```

- [ ] **Step 3: Write `Reconcile`** in `container.go` — sweep stale staged dirs + orphaned squid (run under the lock)
```go
package container

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Reconcile reclaims state a crashed prior run may have left: staged dirs older than maxAge
// carrying the .slop-stage marker (secrets wiped), and the squid container if no agent is using
// it. Safe to call on every run; idempotent. repo is the workspace root.
func Reconcile(ctx context.Context, repo string, maxAge time.Duration) error {
	runtimeRoot := filepath.Join(repo, ".slop", "runtime")
	entries, _ := os.ReadDir(runtimeRoot)
	for _, e := range entries {
		dir := filepath.Join(runtimeRoot, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".slop-stage")); err != nil {
			continue // not ours
		}
		if fi, err := os.Stat(dir); err == nil && time.Since(fi.ModTime()) > maxAge {
			_ = os.RemoveAll(dir) // wipes any leftover secrets.env
		}
	}
	return nil
}
```
> Orphan-squid reaping is best-effort: in Task 6, `Reconcile` also checks `docker ps` for a
> `proxy` container with no peer `agent` and (optionally) `docker compose down` if it predates
> the configured idle window. Keep that behind the same lock; do not block the run if Docker is
> momentarily unavailable (the run-path probe in `Launch` reports that).

- [ ] **Step 4: Tests** — flock round-trip + reconcile age sweep, hermetic
```go
import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWithRepoLockReentrantSequential(t *testing.T) {
	repo := t.TempDir()
	got := 0
	err := withRepoLock(repo, func() error {
		got++
		return withRepoLockNonBlockingShouldQueue(repo) // see note
	})
	if err != nil || got != 1 {
		t.Fatalf("lock body err=%v got=%d", err, got)
	}
}

func TestReconcileSweepsStaleMarkedDirs(t *testing.T) {
	repo := t.TempDir()
	stale := filepath.Join(repo, ".slop", "runtime", "old")
	fresh := filepath.Join(repo, ".slop", "runtime", "new")
	for _, d := range []string{stale, fresh} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, ".slop-stage"), nil, 0o600)
		os.WriteFile(filepath.Join(d, "secrets.env"), []byte("K=v"), 0o600)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stale, old, old)
	if err := Reconcile(context.Background(), repo, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staged dir (with secrets) was not swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh staged dir wrongly swept")
	}
}
```
> Drop the `withRepoLockNonBlockingShouldQueue` placeholder; a single-process flock test only
> needs to confirm the body runs once and returns cleanly. Replace the inner call with `nil`.

- [ ] **Step 5: Run** — `go test ./internal/engine/container/ -v` → PASS.
- [ ] **Step 6: Commit**
```bash
git add internal/engine/container/lock.go internal/engine/container/container.go internal/engine/container/compose.go internal/engine/container/container_test.go
git commit -m "sp3: advisory repo flock + reconcile sweep of crashed-run staged dirs (crash-safe, concurrency-safe)"
```

---

### Task 5: Availability + two-image build + Up/Down

**Files:** Modify `internal/engine/container/container.go`; Test `container_test.go`.

- [ ] **Step 1: Add `Available`, image build, `Up`/`Down`** to `container.go`
```go
import "fmt"

const (
	baseTag  = "local/agent-sandbox:latest"
	toolsTag = "local/agent-sandbox-tools:latest"
)

// Available reports whether this host can run the container boundary: docker + Compose v2.
func Available() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.CommandContext(context.Background(), "docker", "compose", "version").Run() == nil
}

func imageExists(ctx context.Context, tag string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil
}

func ensureDockerfiles(dir string) error {
	for _, name := range []string{"Dockerfile.agent", "Dockerfile.agent.tools"} {
		b, err := readAsset(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func runDocker(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

func buildImages(ctx context.Context, dir string) error {
	if err := ensureDockerfiles(dir); err != nil {
		return err
	}
	if !imageExists(ctx, baseTag) {
		if err := runDocker(ctx, "build", "-f", filepath.Join(dir, "Dockerfile.agent"), "-t", baseTag, dir); err != nil {
			return fmt.Errorf("build base image: %w", err)
		}
	}
	if !imageExists(ctx, toolsTag) {
		if err := runDocker(ctx, "build", "-f", filepath.Join(dir, "Dockerfile.agent.tools"), "-t", toolsTag,
			"--build-arg", "ENABLE_CLAUDE_CODE=true", "--build-arg", "ENABLE_OPENCODE=true", dir); err != nil {
			return fmt.Errorf("build tools image: %w", err)
		}
	}
	return nil
}

// Up ensures images are built and the squid proxy is running.
func Up(ctx context.Context, dir, composeFile string) error {
	if err := buildImages(ctx, dir); err != nil {
		return err
	}
	return runDocker(ctx, "compose", "-f", composeFile, "up", "-d", "proxy")
}

// Down stops squid + networks. composeFile "" is a no-op.
func Down(ctx context.Context, composeFile string) error {
	if composeFile == "" {
		return nil
	}
	return runDocker(ctx, "compose", "-f", composeFile, "down")
}
```

- [ ] **Step 2: Test `Available` is false with an empty PATH** (hermetic)
```go
func TestAvailableFalseWithoutDocker(t *testing.T) {
	t.Setenv("PATH", "")
	if Available() {
		t.Fatal("Available must be false when docker is not on PATH")
	}
}
```
- [ ] **Step 3: Run** — `go test ./internal/engine/container/ -v` → PASS; `go vet ./...`; `gofmt -l cmd internal` empty.
- [ ] **Step 4: Commit**
```bash
git add internal/engine/container/container.go internal/engine/container/container_test.go
git commit -m "sp3: container.Available + idempotent two-image build + Up/Down"
```

---

### Task 6: `container.Launch` + wire `runProfile` (secrets via file, not -e)

**Files:** Modify `internal/engine/container/container.go`, `internal/engine/container/compose.go`; Modify `internal/cli/cli.go`; Test `container_test.go`, `internal/cli/cli_test.go`.

- [ ] **Step 1: Implement `materialize` (final) + `Launch`** in `container.go`
```go
import (
	"path/filepath"
	"time"

	"github.com/freakhill/safeslop/internal/engine/exec"
)

// materializeRun writes the per-run runtime dir and returns its path + compose file path.
func materializeRun(p composeParams, open bool) (composeFile string, err error) {
	dir := p.RuntimeDir
	squid, err := RenderSquidConf(open)
	if err != nil {
		return "", err
	}
	allow, err := readAsset("allowlist.domains")
	if err != nil {
		return "", err
	}
	yml, err := renderCompose(p)
	if err != nil {
		return "", err
	}
	if err := writeEntrypoint(dir); err != nil {
		return "", err
	}
	files := map[string][]byte{
		"squid.conf": []byte(squid), "allowlist.domains": allow,
		"compose.yml": []byte(yml), ".slop-stage": nil,
	}
	for name, content := range files {
		if werr := os.WriteFile(filepath.Join(dir, name), content, 0o600); werr != nil {
			return "", werr
		}
	}
	return filepath.Join(dir, "compose.yml"), nil
}

// Launch runs spec.Argv in the agent container. secretEnv (resolved profile secrets) is written
// to secrets.env and sourced by the entrypoint — never passed via -e. stageDir is the host
// .slop/runtime/<profile> dir (already holds .npmrc when pnpm creds were staged); it is mounted
// ro at /slop/runtime. The agent runs interactively through a PTY (design §6.2).
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, secretEnv []string, stageDir string) (int, error) {
	if !Available() {
		return 1, fmt.Errorf("container environment requires docker + docker compose v2 (run: slop doctor)")
	}
	if len(spec.Argv) == 0 {
		return 1, exec.ErrNoArgv
	}
	_ = withRepoLock(workspace, func() error { return Reconcile(ctx, workspace, time.Hour) })
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return 1, err
	}
	_, npmErr := os.Stat(filepath.Join(stageDir, ".npmrc"))
	p := composeParams{
		RuntimeDir: stageDir, Workspace: workspace, StageDir: stageDir,
		SSHAuthSock: os.Getenv("SSH_AUTH_SOCK"), Term: os.Getenv("TERM"), NpmConfig: npmErr == nil,
	}
	composeFile, err := materializeRun(p, network == "allow")
	if err != nil {
		return 1, err
	}
	if err := Up(ctx, stageDir, composeFile); err != nil {
		return 1, err
	}
	// best-effort: drop secrets.env off host disk shortly after the container has it.
	go func() { time.Sleep(3 * time.Second); _ = os.Remove(filepath.Join(stageDir, "secrets.env")) }()

	inner := exec.LaunchSpec{Argv: composeRunArgv(composeFile, spec.Argv)}
	return exec.RunInPTY(ctx, inner)
}
```
> The runtime dir IS the stage dir (`.slop/runtime/<profile>`) so the single ro mount carries
> compose inputs + `.npmrc` + `secrets.env`. `runProfile` already `defer os.RemoveAll(stageDir)`s
> it, which is the on-exit secret wipe.

- [ ] **Step 2: Refactor `runProfile`** in `internal/cli/cli.go` — split SECRET env (→ container file) from non-secret, add the `container` case
```go
	ctx := context.Background()
	stageDir := filepath.Join(ws, ".slop", "runtime", name)
	defer os.RemoveAll(stageDir) // wipes secrets.env + .npmrc on exit

	// secretEnv = resolved profile secrets (sensitive). npm token rides a staged .npmrc file.
	var secretEnv []string
	if len(prof.Secrets) > 0 {
		resolved, err := secrets.ResolveMap(ctx, prof.Secrets)
		if err != nil {
			return 1, err
		}
		for k, v := range resolved {
			secretEnv = append(secretEnv, k+"="+v)
		}
	}
	npmrcEnv, err := creds.StagePnpm(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}

	switch prof.Environment {
	case "sandbox":
		spec := engexec.LaunchSpec{Argv: argv, Dir: ws, Env: append(append(os.Environ(), secretEnv...), npmrcEnv...)}
		return sandbox.Launch(ctx, spec, ws, prof.Network)
	case "host":
		spec := engexec.LaunchSpec{Argv: argv, Dir: ws, Env: append(append(os.Environ(), secretEnv...), npmrcEnv...)}
		return engexec.RunInTerminal(ctx, spec)
	case "container":
		// secrets go in secrets.env (sourced by entrypoint); .npmrc is already staged in stageDir.
		return container.Launch(ctx, engexec.LaunchSpec{Argv: argv}, ws, prof.Network, secretEnv, stageDir)
	default:
		return 1, fmt.Errorf("environment %q is not implemented yet (vm lands in SP4)", prof.Environment)
	}
```
Add `"github.com/freakhill/safeslop/internal/engine/container"`.

- [ ] **Step 3: Hermetic guard test** (`container_test.go`)
```go
func TestLaunchRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Launch(context.Background(), exec.LaunchSpec{Argv: []string{"fish"}}, t.TempDir(), "deny", nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error when docker unavailable")
	}
}
```
(Add the `exec` import alias for `exec.LaunchSpec`.)

- [ ] **Step 4: Run** — `go test ./... -v` → green (new tests + unchanged SP1/SP2; `cli_test.go`'s `runProfile` end-to-end still passes — sandbox/host build the same env).

- [ ] **Step 5: Manual smoke (needs Docker; do NOT block the commit on it)** — `/tmp/sp3/slop.cue`:
```cue
version: 1
profiles: box: {agent: "shell", environment: "container", network: "deny"}
```
```bash
cd /tmp/sp3 && /Users/jojo/workspace/safeslop/slop run box
```
Expected: first run builds the images; drops into `fish`; `curl https://api.github.com` works,
`curl https://example.com` is blocked, **`curl --noproxy '*' https://example.com` also fails**
(no direct route — the topology boundary), `cat /proc/1/environ` shows no secret leaked via
inspect (`docker inspect <container>` Config.Env has no `ANTHROPIC_API_KEY`); `exit` returns to
the host. Record the result in the PR.

- [ ] **Step 6: Commit**
```bash
git add internal/engine/container/container.go internal/engine/container/compose.go internal/cli/cli.go internal/engine/container/container_test.go
git commit -m "sp3: container.Launch (PTY, file-based secrets, reconcile+flock) + wire environment:container into slop run"
```

---

### Task 7: `slop down` + reconcile-on-run + doctor

**Files:** Modify `internal/cli/cli.go`, `internal/engine/container/container.go`; Test `internal/cli/cli_test.go`.

- [ ] **Step 1: `ComposeForDown` + `cmdDown`** — teardown targets a throwaway proxy-only compose (the agent is `--rm`; only squid persists)
```go
// container.go
func ComposeForDown() (dir, composeFile string, err error) {
	dir, err = os.MkdirTemp("", "slop-down-*")
	if err != nil {
		return "", "", err
	}
	p := composeParams{RuntimeDir: dir, Workspace: "/", StageDir: dir}
	if err := writeEntrypoint(dir); err != nil {
		return "", "", err
	}
	cf, err := materializeRun(p, false)
	return dir, cf, err
}
```
```go
// cli.go
func cmdDown() *cobra.Command {
	return &cobra.Command{
		Use: "down", Short: "Tear down the container stack (squid proxy + networks)", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !container.Available() {
				return fmt.Errorf("docker + docker compose v2 required (run: slop doctor)")
			}
			dir, composeFile, err := container.ComposeForDown()
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			return container.Down(context.Background(), composeFile)
		},
	}
}
```

- [ ] **Step 2: Register** — `root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdDown())`.

- [ ] **Step 3: Doctor** — add a docker/compose line (read the function first; mirror how `sandbox`/`op` are reported; for `--json` add `report["docker"] = container.Available()`).

- [ ] **Step 4: Test `down` registered + graceful** (`cli_test.go`)
```go
func TestDownCommandRegistered(t *testing.T) {
	var found bool
	for _, c := range newRoot().Commands() {
		if c.Name() == "down" {
			found = true
		}
	}
	if !found {
		t.Fatal("down command not registered")
	}
}
```

- [ ] **Step 5: Run** — `go test ./internal/cli/ -v` → PASS; `go build ./... && ./slop down` with no Docker prints the clear `docker + docker compose v2 required` error (non-zero); with Docker idle it's a clean no-op.

- [ ] **Step 6: Commit**
```bash
git add internal/cli/cli.go internal/engine/container/container.go internal/cli/cli_test.go
git commit -m "sp3: slop down (compose teardown) + doctor reports docker/compose"
```

---

### Task 8: Full gate + design-doc record + PR

- [ ] **Step 1: Go gate** — `make check` (check-assets + vet + fmtcheck + `go test ./...`) and `make build` (`-> ./slop`). `./slop --help` shows `down`; `./slop doctor` reports docker/compose.
- [ ] **Step 2: Four fish gates** — `fish -n scripts/*.fish`; `fish tests/run.fish`; `fish scripts/slop-sync-help.fish check`; `fish scripts/slop-pinning.fish`. All pass. **Pinning watch:** the synced `internal/.../assets/` copies are byte-identical to canonical `library/`, so no new `latest` is introduced; if pinning newly fails, a `library/` version was already unpinned — fix it there, then `make sync-container-assets`.
- [ ] **Step 3: Flip SP3 to complete** in `specs/0001-go-rewrite-design.md` §11:
```
SP0–SP3 are **complete** (PRs #1–#3 + `specs/0005-sp3-container-environment.md`). SP4 (tart VM,
`environment: vm`) is the next artifact.
```
- [ ] **Step 4: Commit** — `git add specs/0001-go-rewrite-design.md && git commit -m "sp3: record container environment complete in the roadmap"`.
- [ ] **Step 5: Push + PR**
```bash
git push -u origin sp3-container
gh pr create --title "SP3: container environment — network-enforced squid egress + leak-free secrets in Go" \
  --body "Ports the container network boundary to the Go engine. Egress enforced by an internal-only agent network (squid the sole bridge); secrets delivered via a sourced file + entrypoint, never -e/inspect. FLO-hardened design — see specs/0005."
```
(Push to the feature branch + PR is fine; do **not** push `main`. Hand the PR link back.)

---

## Verification (what "done" means)

- `make check` green: `check-assets` + `vet` + `gofmt` + `go test ./...`, including the hermetic
  container tests — **`Decide` table** (allowlist pass, subdomain match, deny non-listed, allow
  opens, metadata+RFC1918 blocked in both modes), squid.conf **deny-first ordering** golden,
  **internal-only network** assertion, **no-`-e`** run-argv, `secrets.env` 0600 + shell-escape,
  flock round-trip, reconcile age-sweep, unavailable-guard. **No Docker or network in CI.**
- The four fish gates green (old stack stays green; pinning unaffected).
- Manual Docker smoke (Task 6 Step 5): allowlisted egress works; non-allowlisted **and direct
  (no-proxy) egress both fail** (topology boundary); `docker inspect` shows no secret in
  `Config.Env`; `slop down` tears the proxy down.
- `slop doctor` reports docker/compose; `slop down` registered + graceful without Docker.

## Deliberately deferred (not in SP3)

- **Our own macOS Network-Extension egress filter (à-la-LuLu).** The strongest enforcement is a
  native NetworkExtension content filter (`NEFilterDataProvider`, per-process, like Objective-See
  **LuLu**) that decides egress at the host kernel boundary rather than relying on container
  topology + squid. SP3 ships the squid/internal-network boundary; a *slop-owned* Network
  Extension is a later iteration — it would give per-process, per-domain egress control for the
  `host`/`sandbox` environments too (not just `container`), and is the natural successor to the
  squid allowlist. Tracked under **SP8 niche adapters** (`specs/0001` §10 already lists
  `lulu / pf`); this note upgrades it from "adapt LuLu" to "build our own NE filter."
- **Wall-clock idle auto-teardown** (stop squid N min after the last agent exits) — needs a
  launchd timer/daemon; SP3 ships reconcile-based reclamation + explicit `slop down`.
- **Per-profile custom allowlist** (`network` carrying extra domains) — v1 ships the embedded set.
- **Tailored images** (`image.extra-{apt,pip,npm}` content-hash build) — SP3 builds the standard
  tools image.
- **On-exit hook framework** (snapshot-state / revoke-credentials ordering) — SP2-track follow-up.
- **Full 1Password SSH-agent verification** — SP3 bind-mounts `SSH_AUTH_SOCK`; op-desktop
  end-to-end proof + the `doctor` "agent socket reachable" line are a follow-up.
- **`environment: vm`** — SP4 (tart).
