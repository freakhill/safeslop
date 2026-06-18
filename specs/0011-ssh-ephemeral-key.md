# `ssh` credential provider — ephemeral repo-scoped deploy key Implementation Plan

**Goal:** Implement the FLO-decided §7.1 design — deliver Git/SSH auth into the boundary as a per-run, repo-scoped **ephemeral deploy key** (no 1Password agent socket), and **remove** the existing agent-socket bind-mount.

**Architecture:** A new provider `internal/engine/creds/ssh.go` mirroring `kube.go`/`aws.go`/`gcp.go`: a pure render/parse core (argv builders for `ssh-keygen`/`gh`/`git`, owner/repo parse, `GIT_SSH_COMMAND` + pinned `known_hosts` render) plus a thin `StageSSH` that mints a fresh ed25519 keypair into the stageDir, registers the public key as a repo-scoped GitHub deploy key (read-only by default), stages only the `0600` private key + `known_hosts` + a revoke-info file, and returns `GIT_SSH_COMMAND` as a non-secret path env (delivered per-environment like `KUBECONFIG`/`.npmrc`). A `RevokeSSH` does best-effort on-exit revoke. The container path drops the agent-socket bind-mount entirely.

**Why reimplement minting in Go (not shell `slop-gh-key`):** the Go binary is the single signed artifact and must stay fish-free. The provider shells only host CLIs (`ssh-keygen`, `gh`, `git`) with PATH-mocked tests — the same pattern as the cloud providers. `slop-gh-key` (fish) remains the legacy stack's lifecycle; this is its native Go equivalent.

**Decay model:** GitHub deploy keys have no native TTL, so cleanup is layered: best-effort on-exit revoke (`RevokeSSH`, not relied upon — SIGKILL skips it) + the stageDir wipe destroying the private key. A standalone daily reaper is deferred (see "Deferred").

**Tech stack:** Go (`os/exec`, `encoding/json`, `os`, `strings`), embedded CUE schema, `text/template` compose. No new modules.

**Base branch:** off current `main` (has #14 kube + #15 the §7.1 decision). Feature branch `ssh-credentials`.

**File structure:**
- `internal/engine/policy/schema/schema.cue` (modify) — add `#SshCreds`; add `ssh?: #SshCreds` to `#Credentials`.
- `internal/engine/policy/policy.go` (modify) — add `SshCreds` struct; add `Ssh *SshCreds` to `Credentials`.
- `internal/engine/policy/policy_test.go` (modify) — parse an `ssh` profile.
- `internal/engine/creds/ssh.go` (create) — argv builders, parsers, render, `StageSSH`, `RevokeSSH`.
- `internal/engine/creds/ssh_test.go` (create) — pure-core unit tests + PATH-mocked `StageSSH`/`RevokeSSH`.
- `internal/engine/container/compose.go` (modify) — drop `SSHAuthSock`; add `SshKey bool`.
- `internal/engine/container/assets/compose.yml.tmpl` (modify) — drop the agent-socket env+volume; add `GIT_SSH_COMMAND` when staged.
- `internal/engine/container/launch.go` (modify) — drop `SSHAuthSock` setter; detect a staged ssh key.
- `internal/engine/container/compose_test.go` (modify) — assert no agent socket; `GIT_SSH_COMMAND` present iff staged.
- `internal/cli/cli.go` (modify) — `StageSSH` + `RevokeSSH` in `runProfile`; vm guard; `gh` in `doctorReport`.
- `internal/cli/cli_test.go` (modify) — wiring/guard/doctor assertions.
- `internal/engine/policy/lint.go` (modify) — `ssh.write` + `network:allow` → error.
- `internal/engine/policy/lint_test.go` (modify) — the new lint case.
- `specs/0011-ssh-ephemeral-key.md` — this plan.

---

## Design decisions flagged for veto (decide before/with execution)

1. **Mint via `ssh-keygen` + `gh api` in Go (read-only default).** Not shelling `slop-gh-key`. **Recommendation:** as written.
2. **owner/repo from the process cwd's `git remote get-url origin`.** slop runs from the repo root, so the workspace IS the git repo; no schema field for the repo. **Recommendation:** as written; error clearly if no `origin`.
3. **`gh` auth = the host's `gh auth login` (like `aws` relies on `aws sso login`); headless via `GH_TOKEN`.** v1 does not implement the `op read` pre-provisioned-key path. **Recommendation:** as written; defer op-read.
4. **vm deferred behind a guard** (mirrors kube). `ssh` + `environment: vm` errors, pointing at container. **Recommendation:** guard now.
5. **Pinned `known_hosts` = GitHub's published ed25519 host key constant, `StrictHostKeyChecking=yes`.** If GitHub rotates it, update the constant. **Recommendation:** as written (no TOFU).
6. **Best-effort on-exit revoke included; standalone reaper deferred.** **Recommendation:** as written.
7. **Lint v1 = `ssh.write` + `network:allow` → hard error.** The "egress allowlist must be forge-only" assertion is deferred (needs allowlist plumbing). **Recommendation:** as written.

---

### Task 1: schema + Go structs for `ssh` credentials

**Files:**
- Modify: `internal/engine/policy/schema/schema.cue`
- Modify: `internal/engine/policy/policy.go`
- Test: `internal/engine/policy/policy_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/engine/policy/policy_test.go`:

```go
func TestLoadSshCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package slop
slop: profiles: deploy: {
	agent: "claude"
	environment: "container"
	network: "deny"
	credentials: ssh: {write: true, ttl: "30m"}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Profiles["deploy"].Credentials.Ssh
	if s == nil || !s.Write || s.Ttl != "30m" {
		t.Fatalf("ssh creds = %+v", s)
	}
}

func TestLoadSshDefaultsReadOnly(t *testing.T) {
	cfg, err := loadStr(t, `package slop
slop: profiles: review: {
	agent: "claude"
	environment: "sandbox"
	credentials: ssh: {}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Profiles["review"].Credentials.Ssh
	if s == nil || s.Write {
		t.Fatalf("ssh write must default false: %+v", s)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/policy/ -run TestLoadSsh -v
```
Expected: FAIL — `Ssh` undefined / CUE rejects `ssh`.

- [ ] **Step 3: Add the schema.** In `internal/engine/policy/schema/schema.cue`, insert before `#Credentials:` (after the `#KubeCluster`/`#GkeCluster` block):

```cue
// SSH/Git auth into the boundary as a per-run, repo-scoped ephemeral deploy key — the
// 1Password agent socket is never passed in (specs/0001 §7.1, specs/0010-decision). The
// host mints the key (read-only by default); write:true is lint-gated on network:deny.
#SshCreds: {
	write?: bool | *false
	ttl?:   string | *"1h"
}
```

Add `ssh` to `#Credentials`:

```cue
#Credentials: {
	pnpm?: [...#PnpmRegistry]
	aws?:  #AwsSso
	gcp?:  #GcpAdc
	kube?: #KubeCluster
	ssh?:  #SshCreds
}
```

- [ ] **Step 4: Add the Go struct.** In `internal/engine/policy/policy.go`, insert before `type Credentials struct`:

```go
// SshCreds stages a per-run repo-scoped ephemeral SSH deploy key (read-only unless Write).
// The host mints it; only the private key crosses the boundary (specs/0001 §7.1, specs/0011).
type SshCreds struct {
	Write bool   `json:"write,omitempty"`
	Ttl   string `json:"ttl,omitempty"`
}
```

Add `Ssh` to `Credentials`:

```go
type Credentials struct {
	Pnpm []PnpmRegistry `json:"pnpm,omitempty"`
	Aws  *AwsSso        `json:"aws,omitempty"`
	Gcp  *GcpAdc        `json:"gcp,omitempty"`
	Kube *KubeCluster   `json:"kube,omitempty"`
	Ssh  *SshCreds      `json:"ssh,omitempty"`
}
```

- [ ] **Step 5: Run the test, verify it passes**

```bash
go test ./internal/engine/policy/ -run TestLoadSsh -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine/policy/policy.go internal/engine/policy/policy_test.go
git add internal/engine/policy/schema/schema.cue internal/engine/policy/policy.go internal/engine/policy/policy_test.go
git commit -m "feat(creds): #SshCreds schema + Go struct (read-only default, specs/0011)"
```

---

### Task 2: ssh provider — pure core (argv, parse, render)

**Files:**
- Create: `internal/engine/creds/ssh.go`
- Test: `internal/engine/creds/ssh_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/creds/ssh_test.go`:

```go
package creds

import (
	"strings"
	"testing"
)

func TestKeygenArgv(t *testing.T) {
	got := strings.Join(keygenArgv("/stage/.ssh/id", "slop-acme/repo-run1"), " ")
	want := `ssh-keygen -t ed25519 -N  -C slop-acme/repo-run1 -f /stage/.ssh/id`
	if got != want {
		t.Fatalf("keygen argv = %q", got)
	}
}

func TestGhRegisterArgv(t *testing.T) {
	ro := strings.Join(ghRegisterArgv("acme", "repo", "slop-run1", "ssh-ed25519 AAAA", false), " ")
	if ro != "gh api repos/acme/repo/keys -f title=slop-run1 -f key=ssh-ed25519 AAAA -F read_only=true" {
		t.Fatalf("ro argv = %q", ro)
	}
	rw := strings.Join(ghRegisterArgv("acme", "repo", "slop-run1", "ssh-ed25519 AAAA", true), " ")
	if !strings.Contains(rw, "-F read_only=false") {
		t.Fatalf("rw argv = %q", rw)
	}
}

func TestGhRevokeArgv(t *testing.T) {
	got := strings.Join(ghRevokeArgv("acme", "repo", "42"), " ")
	if got != "gh api --method DELETE repos/acme/repo/keys/42" {
		t.Fatalf("revoke argv = %q", got)
	}
}

func TestParseOwnerRepo(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:acme/repo.git\n":        {"acme", "repo"},
		"https://github.com/acme/repo.git\n":    {"acme", "repo"},
		"https://github.com/acme/repo\n":        {"acme", "repo"},
		"ssh://git@github.com/acme/repo.git\n":  {"acme", "repo"},
	}
	for in, want := range cases {
		o, r, err := parseOwnerRepo([]byte(in))
		if err != nil || o != want[0] || r != want[1] {
			t.Fatalf("parseOwnerRepo(%q) = %q/%q err=%v", in, o, r, err)
		}
	}
	if _, _, err := parseOwnerRepo([]byte("/local/path\n")); err == nil {
		t.Fatal("expected error on non-github remote")
	}
}

func TestParseKeyID(t *testing.T) {
	id, err := parseKeyID([]byte(`{"id":1234567,"key":"ssh-ed25519 AAAA","read_only":true}`))
	if err != nil || id != "1234567" {
		t.Fatalf("parseKeyID = %q err=%v", id, err)
	}
	if _, err := parseKeyID([]byte(`{}`)); err == nil {
		t.Fatal("expected error on missing id")
	}
}

func TestRenderGitSSHCommand(t *testing.T) {
	got := renderGitSSHCommand("/slop/runtime/.ssh/id", "/slop/runtime/.ssh/known_hosts")
	for _, want := range []string{
		"ssh -i /slop/runtime/.ssh/id",
		"-o IdentitiesOnly=yes",
		"-o IdentityAgent=none",
		"-o StrictHostKeyChecking=yes",
		"-o UserKnownHostsFile=/slop/runtime/.ssh/known_hosts",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("GIT_SSH_COMMAND missing %q: %s", want, got)
		}
	}
}

func TestKnownHostsIsGithubEd25519(t *testing.T) {
	if !strings.HasPrefix(githubKnownHosts, "github.com ssh-ed25519 ") {
		t.Fatalf("known_hosts must pin github.com ed25519: %q", githubKnownHosts)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/creds/ -run 'TestKeygenArgv|TestGhRegisterArgv|TestGhRevokeArgv|TestParseOwnerRepo|TestParseKeyID|TestRenderGitSSHCommand|TestKnownHostsIsGithubEd25519' -v
```
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Write the pure core** — create `internal/engine/creds/ssh.go`:

```go
package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// githubKnownHosts pins github.com's published ed25519 host key (StrictHostKeyChecking=yes,
// no TOFU). Update this constant if GitHub rotates the key.
const githubKnownHosts = "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"

// ---- argv builders ----

func keygenArgv(keyPath, comment string) []string {
	return []string{"ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-f", keyPath}
}

func ghRegisterArgv(owner, repo, title, pubkey string, write bool) []string {
	ro := "true"
	if write {
		ro = "false"
	}
	return []string{"gh", "api", "repos/" + owner + "/" + repo + "/keys",
		"-f", "title=" + title, "-f", "key=" + pubkey, "-F", "read_only=" + ro}
}

func ghRevokeArgv(owner, repo, id string) []string {
	return []string{"gh", "api", "--method", "DELETE", "repos/" + owner + "/" + repo + "/keys/" + id}
}

// ---- parsers ----

// parseOwnerRepo extracts owner/repo from a github.com remote URL (ssh, scp-like, or https).
func parseOwnerRepo(out []byte) (owner, repo string, err error) {
	u := strings.TrimSpace(string(out))
	if !strings.Contains(u, "github.com") {
		return "", "", fmt.Errorf("origin remote is not github.com (%q); ssh creds support GitHub only", u)
	}
	// normalize to the "owner/repo" tail after "github.com[:/]"
	i := strings.Index(u, "github.com")
	tail := u[i+len("github.com"):]
	tail = strings.TrimLeft(tail, ":/")
	tail = strings.TrimSuffix(tail, ".git")
	parts := strings.Split(tail, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("could not parse owner/repo from %q", u)
	}
	return parts[0], parts[1], nil
}

func parseKeyID(out []byte) (string, error) {
	var k struct {
		ID json.Number `json:"id"`
	}
	if err := json.Unmarshal(out, &k); err != nil {
		return "", fmt.Errorf("parse gh deploy-key response: %w", err)
	}
	if k.ID.String() == "" || k.ID.String() == "0" {
		return "", fmt.Errorf("gh deploy-key response had no id")
	}
	return k.ID.String(), nil
}

// ---- render ----

func renderGitSSHCommand(keyPath, knownHostsPath string) string {
	return "ssh -i " + keyPath +
		" -o IdentitiesOnly=yes -o IdentityAgent=none" +
		" -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + knownHostsPath
}

// runSSHCmd executes argv and returns stdout, wrapping failures with a hint.
func runSSHCmd(ctx context.Context, argv []string, hint string) ([]byte, error) {
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		label := argv[0]
		if len(argv) > 1 {
			label += " " + argv[1]
		}
		return nil, fmt.Errorf("%s (%s): %w", label, hint, err)
	}
	return out, nil
}

// placeholders satisfied in Task 3 (StageSSH/RevokeSSH use os/filepath/policy).
var _ = os.MkdirAll
var _ = filepath.Join
var _ = policy.SshCreds{}
```

> The `var _ =` lines keep imports satisfied until Task 3 adds `StageSSH`/`RevokeSSH`; Task 3 deletes them. (Or write Task 3 now and run its tests together.)

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/creds/ -run 'TestKeygenArgv|TestGhRegisterArgv|TestGhRevokeArgv|TestParseOwnerRepo|TestParseKeyID|TestRenderGitSSHCommand|TestKnownHostsIsGithubEd25519' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/creds/ssh.go internal/engine/creds/ssh_test.go
git add internal/engine/creds/ssh.go internal/engine/creds/ssh_test.go
git commit -m "feat(creds): ssh pure core — keygen/gh/git argv + parse + GIT_SSH_COMMAND render"
```

---

### Task 3: ssh provider — `StageSSH` (PATH-mocked mint + register)

**Files:**
- Modify: `internal/engine/creds/ssh.go` (replace placeholder tail)
- Test: `internal/engine/creds/ssh_test.go`

- [ ] **Step 1: Write the failing test** — first extend `ssh_test.go`'s imports to:

```go
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)
```

Then append:

```go
// fakeStub writes an executable /bin/sh stub with an arbitrary body.
func fakeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStageSSHMintsReadOnly(t *testing.T) {
	binDir := t.TempDir()
	// git remote get-url origin
	fakeStub(t, binDir, "git", `echo "git@github.com:acme/repo.git"`)
	// ssh-keygen writes the priv + .pub files at the -f path (last arg)
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB slop" > "$p.pub"`)
	// gh api ... returns a deploy-key JSON with an id
	fakeStub(t, binDir, "gh", `echo '{"id":4242,"read_only":true}'`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	stage := t.TempDir()
	env, err := StageSSH(context.Background(), &policy.Credentials{Ssh: &policy.SshCreds{}}, stage)
	if err != nil {
		t.Fatalf("StageSSH: %v", err)
	}
	keyPath := filepath.Join(stage, ".ssh", "id")
	khPath := filepath.Join(stage, ".ssh", "known_hosts")
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -i "+keyPath) || !strings.Contains(joined, "UserKnownHostsFile="+khPath) {
		t.Fatalf("env = %v", env)
	}
	if fi, _ := os.Stat(keyPath); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("private key not staged 0600")
	}
	if b, _ := os.ReadFile(khPath); !strings.HasPrefix(string(b), "github.com ssh-ed25519 ") {
		t.Fatalf("known_hosts not pinned: %q", b)
	}
	// revoke-info captured for best-effort on-exit revoke
	if b, _ := os.ReadFile(filepath.Join(stage, ".ssh", "revoke-info")); strings.TrimSpace(string(b)) != "acme/repo 4242" {
		t.Fatalf("revoke-info = %q", b)
	}
}

func TestStageSSHNilIsNoop(t *testing.T) {
	env, err := StageSSH(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil ssh creds must be a no-op: env=%v err=%v", env, err)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/creds/ -run TestStageSSH -v
```
Expected: FAIL — `StageSSH` undefined.

- [ ] **Step 3: Write `StageSSH`.** In `internal/engine/creds/ssh.go`, delete the three `var _ =` lines and append:

```go
// StageSSH mints a fresh ed25519 keypair into stageDir/.ssh, registers the public key as
// a repo-scoped GitHub deploy key (read-only unless creds.Write), stages ONLY the 0600
// private key + a pinned known_hosts + a revoke-info file, and returns GIT_SSH_COMMAND as
// a non-secret path env (host path; the container path is set in the compose env). The
// owner/repo come from the process cwd's `origin` remote. No revoke is relied upon (best
// effort via RevokeSSH); the stageDir wipe destroys the private key (decay-first).
func StageSSH(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Ssh == nil {
		return nil, nil
	}
	rOut, err := runSSHCmd(ctx, []string{"git", "remote", "get-url", "origin"}, "run slop from a repo with a github.com origin")
	if err != nil {
		return nil, err
	}
	owner, repo, err := parseOwnerRepo(rOut)
	if err != nil {
		return nil, err
	}

	sshDir := filepath.Join(stageDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(sshDir, "id")
	khPath := filepath.Join(sshDir, "known_hosts")

	title := "slop-" + owner + "-" + repo
	if _, err := runSSHCmd(ctx, keygenArgv(keyPath, title), "is ssh-keygen on PATH?"); err != nil {
		return nil, err
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("read generated public key: %w", err)
	}
	regOut, err := runSSHCmd(ctx, ghRegisterArgv(owner, repo, title, strings.TrimSpace(string(pub)), creds.Ssh.Write), "is `gh auth login` current with repo admin?")
	if err != nil {
		return nil, err
	}
	keyID, err := parseKeyID(regOut)
	if err != nil {
		return nil, err
	}
	_ = os.Remove(keyPath + ".pub") // only the private key crosses the boundary
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(khPath, []byte(githubKnownHosts), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(sshDir, "revoke-info"), []byte(owner+"/"+repo+" "+keyID+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []string{"GIT_SSH_COMMAND=" + renderGitSSHCommand(keyPath, khPath)}, nil
}

// RevokeSSH best-effort revokes the staged deploy key (reads stageDir/.ssh/revoke-info).
// Never relied upon for security; errors are swallowed (decay-first cleanup is the wipe).
func RevokeSSH(ctx context.Context, stageDir string) {
	b, err := os.ReadFile(filepath.Join(stageDir, ".ssh", "revoke-info"))
	if err != nil {
		return
	}
	f := strings.Fields(strings.TrimSpace(string(b)))
	if len(f) != 2 {
		return
	}
	or := strings.SplitN(f[0], "/", 2)
	if len(or) != 2 {
		return
	}
	_, _ = runSSHCmd(ctx, ghRevokeArgv(or[0], or[1], f[1]), "best-effort revoke")
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/creds/ -run TestStageSSH -v && go test ./internal/engine/creds/ -v
```
Expected: PASS (StageSSH tests + whole `creds` package green).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/creds/ssh.go internal/engine/creds/ssh_test.go
git add internal/engine/creds/ssh.go internal/engine/creds/ssh_test.go
git commit -m "feat(creds): StageSSH — mint repo-scoped deploy key, stage 0600 privkey, decay-first"
```

---

### Task 4: `RevokeSSH` best-effort revoke (PATH-mocked)

**Files:**
- Test: `internal/engine/creds/ssh_test.go`

- [ ] **Step 1: Write the test** — append to `internal/engine/creds/ssh_test.go`:

```go
func TestRevokeSSHCallsGhDelete(t *testing.T) {
	binDir := t.TempDir()
	stage := t.TempDir()
	// record the gh invocation to a marker file
	marker := filepath.Join(stage, "gh-called")
	fakeStub(t, binDir, "gh", `echo "$@" > `+marker)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if err := os.MkdirAll(filepath.Join(stage, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, ".ssh", "revoke-info"), []byte("acme/repo 4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RevokeSSH(context.Background(), stage)
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("gh was not called: %v", err)
	}
	if !strings.Contains(string(b), "DELETE repos/acme/repo/keys/4242") {
		t.Fatalf("gh args = %q", b)
	}
}

func TestRevokeSSHNoInfoIsSilent(t *testing.T) {
	// no revoke-info file => no panic, no-op
	RevokeSSH(context.Background(), t.TempDir())
}
```

- [ ] **Step 2: Run the test, verify it passes** (RevokeSSH was written in Task 3)

```bash
go test ./internal/engine/creds/ -run TestRevokeSSH -v
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/engine/creds/ssh_test.go
git commit -m "test(creds): RevokeSSH best-effort gh delete + silent no-op without info"
```

---

### Task 5: container — drop the agent socket, deliver `GIT_SSH_COMMAND` at the bind-mount path

**Files:**
- Modify: `internal/engine/container/compose.go`
- Modify: `internal/engine/container/assets/compose.yml.tmpl`
- Modify: `internal/engine/container/launch.go`
- Test: `internal/engine/container/compose_test.go`

- [ ] **Step 1: Write the failing test** — add to `internal/engine/container/compose_test.go`:

```go
func TestComposeNoAgentSocketAndSshKey(t *testing.T) {
	with, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", SshKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(with, "SSH_AUTH_SOCK") || strings.Contains(with, "ssh-agent.sock") {
		t.Fatalf("agent socket must be gone from compose:\n%s", with)
	}
	if !strings.Contains(with, "GIT_SSH_COMMAND: ssh -i /slop/runtime/.ssh/id") {
		t.Fatalf("compose missing GIT_SSH_COMMAND:\n%s", with)
	}
	without, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", SshKey: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "GIT_SSH_COMMAND") {
		t.Fatalf("GIT_SSH_COMMAND must be absent when no ssh key staged:\n%s", without)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/container/ -run TestComposeNoAgentSocketAndSshKey -v
```
Expected: FAIL — `SshKey` unknown; agent socket still present.

- [ ] **Step 3: Drop the socket, add the ssh key.**

In `internal/engine/container/compose.go`, in `composeParams`, **remove** the `SSHAuthSock string` field and **add**:

```go
	SshKey      bool // true when a staged ssh deploy key exists (GIT_SSH_COMMAND -> bind-mount path)
```

In `internal/engine/container/assets/compose.yml.tmpl`, **remove** both agent-socket lines:
- the `{{if .SSHAuthSock}}      SSH_AUTH_SOCK: /slop/ssh-agent.sock` env line and its `{{end}}`,
- the `{{if .SSHAuthSock}}      - {{.SSHAuthSock}}:/slop/ssh-agent.sock` volume line and its `{{end}}`.

And **add** a `GIT_SSH_COMMAND` env line next to the `Kubeconfig` one (the `known_hosts` path is `/slop/runtime/.ssh/known_hosts`):

```
{{if .SshKey}}      GIT_SSH_COMMAND: ssh -i /slop/runtime/.ssh/id -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/slop/runtime/.ssh/known_hosts
{{end}}
```

In `internal/engine/container/launch.go`, **remove** the `SSHAuthSock: os.Getenv("SSH_AUTH_SOCK"),` line from the `composeParams` literal, and add a detection next to the kube one:

```go
	_, sshErr := os.Stat(filepath.Join(stageDir, ".ssh", "id"))
```

then set in the literal:

```go
		SshKey:      sshErr == nil,
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/container/ -run TestComposeNoAgentSocketAndSshKey -v && go test ./internal/engine/container/ -v
```
Expected: PASS (new test + container package green; the pre-existing `SSH_AUTH_SOCK` env handling is gone and no test set it).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/container/compose.go internal/engine/container/launch.go internal/engine/container/compose_test.go
git add internal/engine/container/compose.go internal/engine/container/launch.go internal/engine/container/compose_test.go internal/engine/container/assets/compose.yml.tmpl
git commit -m "feat(container): drop the 1Password agent-socket bind-mount; deliver GIT_SSH_COMMAND"
```

---

### Task 6: wire `StageSSH`/`RevokeSSH` into the run lifecycle + vm guard + doctor

**Files:**
- Modify: `internal/cli/cli.go`
- Test: `internal/cli/cli_test.go`

- [ ] **Step 1: Write the failing test** — add to `internal/cli/cli_test.go`:

```go
func TestDoctorReportsGh(t *testing.T) {
	if _, ok := doctorReport()["gh"]; !ok {
		t.Fatalf("doctor must probe gh")
	}
}

func TestRunProfileSshVMGuarded(t *testing.T) {
	prof := policy.Profile{
		Agent:       "claude",
		Environment: "vm",
		Network:     "deny",
		Credentials: &policy.Credentials{Ssh: &policy.SshCreds{}},
	}
	_, err := runProfile("deploy", prof, []string{"claude"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "vm") {
		t.Fatalf("expected vm guard error for ssh creds, got: %v", err)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/cli/ -run 'TestDoctorReportsGh|TestRunProfileSshVMGuarded' -v
```
Expected: FAIL — `gh` not probed; vm guard absent.

- [ ] **Step 3: Wire it in.**

In `internal/cli/cli.go` `doctorReport`, add `gh` to the tools slice (next to `git`):

```go
	tools := []string{"git", "gh", "docker", "op", "claude", "opencode", "tart", "mise", "nix", "aws", "gcloud", "gke-gcloud-auth-plugin"}
```

In `runProfile`, extend the early vm guard (added for kube) to also reject ssh — change:

```go
	if prof.Environment == "vm" && prof.Credentials != nil && prof.Credentials.Kube != nil {
		return 1, fmt.Errorf("kube credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0010)", prof.Environment)
	}
```

to additionally cover ssh:

```go
	if prof.Environment == "vm" && prof.Credentials != nil {
		if prof.Credentials.Kube != nil {
			return 1, fmt.Errorf("kube credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0010)", prof.Environment)
		}
		if prof.Credentials.Ssh != nil {
			return 1, fmt.Errorf("ssh credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0011)", prof.Environment)
		}
	}
```

After the kube staging block (`kubeEnv, err := creds.StageKube(...)`), add ssh staging + the best-effort revoke. `sshEnv` is kept OUT of `secretEnv` (it is a non-secret path command, delivered per-environment like `.npmrc`/`KUBECONFIG`):

```go
	sshEnv, err := creds.StageSSH(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
	if prof.Credentials != nil && prof.Credentials.Ssh != nil {
		defer creds.RevokeSSH(context.Background(), stageDir) // runs before the stageDir wipe (LIFO)
	}
```

Then thread `sshEnv` into the host/sandbox env (next to `kubeEnv`), in both the `sandbox` and `host` cases:

```go
	case "sandbox":
		env := append(append(append(append(os.Environ(), secretEnv...), npmrcEnv...), kubeEnv...), sshEnv...)
		return sandbox.Launch(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env}, ws, prof.Network)
	case "host":
		env := append(append(append(append(os.Environ(), secretEnv...), npmrcEnv...), kubeEnv...), sshEnv...)
		return engexec.RunInTerminal(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env})
```

> `sshEnv` is nil for non-ssh profiles, so the append is a no-op there. The container case is unchanged — `container.Launch` detects the staged key (Task 5) and sets the compose `GIT_SSH_COMMAND`. The `defer RevokeSSH` is registered after the top-of-function `defer os.RemoveAll(stageDir)`, so by LIFO it runs first (revoke, then wipe).

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/cli/ -run 'TestDoctorReportsGh|TestRunProfileSshVMGuarded' -v && go test ./internal/cli/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cli/cli.go internal/cli/cli_test.go
git add internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat(cli): stage ssh deploy key + best-effort revoke; vm guarded; doctor probes gh"
```

---

### Task 7: lint — `ssh.write` + `network:allow` is a credential-exfil risk

**Files:**
- Modify: `internal/engine/policy/lint.go`
- Test: `internal/engine/policy/lint_test.go`

- [ ] **Step 1: Write the failing test** — add to `internal/engine/policy/lint_test.go`:

```go
func TestLintSshWriteOpenEgress(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"push_open":  {Environment: "container", Network: "allow", Credentials: &Credentials{Ssh: &SshCreds{Write: true}}},
		"push_deny":  {Environment: "container", Network: "deny", Credentials: &Credentials{Ssh: &SshCreds{Write: true}}},
		"ro_open":    {Environment: "container", Network: "allow", Credentials: &Credentials{Ssh: &SshCreds{Write: false}}},
	}}
	codes := map[string]string{}
	for _, w := range Lint(cfg) {
		if w.Code == "ssh-write-open-egress" {
			codes[w.Profile] = w.Code
		}
	}
	if codes["push_open"] != "ssh-write-open-egress" {
		t.Fatalf("write+allow must be flagged: %+v", codes)
	}
	if _, bad := codes["push_deny"]; bad {
		t.Fatal("write+deny must NOT be flagged")
	}
	if _, bad := codes["ro_open"]; bad {
		t.Fatal("read-only+allow must NOT be flagged")
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/policy/ -run TestLintSshWriteOpenEgress -v
```
Expected: FAIL — no `ssh-write-open-egress` warning emitted.

- [ ] **Step 3: Add the lint rule.** In `internal/engine/policy/lint.go`, inside the per-profile loop in `Lint` (after the existing `sandbox-open-egress-with-creds` check), add:

```go
		if p.Credentials != nil && p.Credentials.Ssh != nil && p.Credentials.Ssh.Write && p.Network == "allow" {
			out = append(out, Warning{
				Profile: name,
				Code:    "ssh-write-open-egress",
				Message: "a write-capable ssh deploy key with network:allow can be exfiltrated and used off-host — " +
					"set network:deny with a forge-only egress allowlist, or use a read-only key (specs/0011)",
			})
		}
```

> Match the field names in `lint.go` — the loop variable, the warning slice (`out` here), and `Warning{Profile, Code, Message}`. Verify with `grep -n 'range\|out :=\|var out\|append(' internal/engine/policy/lint.go` and adjust the slice/loop names if they differ.

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/policy/ -run TestLintSshWriteOpenEgress -v && go test ./internal/engine/policy/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/policy/lint.go internal/engine/policy/lint_test.go
git add internal/engine/policy/lint.go internal/engine/policy/lint_test.go
git commit -m "feat(policy): lint ssh.write + network:allow as a key-exfil risk"
```

---

### Task 8: full verification + PR

**Files:** none (verification + PR).

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green. (Go-only + schema; no `scripts/` touched. `slop-pinning` scans `*.cue` — `#SshCreds` has no image refs. The compose template's pre-existing `image: ...:latest` is unchanged and already exempt.)

- [ ] **Step 2: Sanity-check the socket is truly gone.**

```bash
grep -rn 'SSH_AUTH_SOCK\|ssh-agent.sock\|SSHAuthSock' internal/ || echo "agent socket fully removed"
```
Expected: prints "agent socket fully removed" (no matches outside comments).

- [ ] **Step 3: Commit (if gofmt/anything changed) + push + PR.**

```bash
git push -u origin ssh-credentials
gh pr create --title "ssh credential provider — ephemeral repo-scoped deploy key (implements §7.1)" --body "$(cat <<'EOF'
## Summary
Implements the FLO-decided §7.1 design (specs/0011, decision record specs/research/2026-06-18-ssh-auth-flo-decision.md): deliver Git/SSH auth into the boundary as a per-run, repo-scoped **ephemeral deploy key**, and **remove** the 1Password agent-socket bind-mount.

- `internal/engine/creds/ssh.go`: mints a fresh ed25519 keypair (`ssh-keygen`), registers the pubkey as a repo-scoped GitHub deploy key (`gh api`, **read-only by default**), stages ONLY the 0600 private key + pinned `known_hosts` + revoke-info, returns `GIT_SSH_COMMAND` (IdentitiesOnly + IdentityAgent=none + StrictHostKeyChecking=yes). owner/repo from the cwd `origin` remote. PATH-mocked tests.
- `RevokeSSH`: best-effort on-exit revoke (not relied upon — decay-first cleanup is the stageDir wipe).
- container: **agent socket bind-mount removed**; `GIT_SSH_COMMAND` set at `/slop/runtime/.ssh/id` when a key is staged.
- cli: wired into `runProfile` (sshEnv per-env like KUBECONFIG; revoke before wipe); vm guarded; `slop doctor` probes `gh`.
- lint: `ssh.write` + `network:allow` → warning (key-exfil risk).

## Deferred
Standalone daily reaper; forge-only egress-allowlist lint assertion; Forgejo; the `op read` pre-provisioned-key headless path; vm support.

## Test
`make check && make build` green; four fish gates green; `grep` confirms no agent socket remains.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` green; the four fish gates green.
- A `credentials: {ssh: {}}` profile mints a repo-scoped **read-only** deploy key, stages a `0600` private key + pinned `known_hosts`, and exposes `GIT_SSH_COMMAND` at the boundary-correct path (host path for sandbox/host; `/slop/runtime/.ssh/id` for container).
- The 1Password agent socket is **never** bind-mounted (grep-confirmed gone).
- `ssh.write:true` + `network:allow` trips the `ssh-write-open-egress` lint.
- `ssh` + `environment: vm` returns a clear guard error.
- `slop doctor` reports `gh`.

## Deliberately deferred (not here)

- **Standalone daily reaper** for orphaned `slop-*` deploy keys (v1 = best-effort on-exit revoke + stageDir wipe).
- **Forge-only egress-allowlist assertion** in lint (beyond `write` + `network:allow`).
- **Forgejo** support (GitHub only v1).
- **`op read` pre-provisioned-key** headless path (v1 mints via `gh`; headless via `GH_TOKEN`).
- **vm** support for `ssh` (guarded).
