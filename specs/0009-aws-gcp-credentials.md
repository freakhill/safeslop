# AWS (SSO) + GCP (ADC) credential providers — decay-first

**Goal:** Give an agent under isolation short-lived, scoped AWS and GCP credentials, minted
just-in-time from the engineer's existing auth and staged into the run's wiped-on-exit dir — so a
prompt-injected agent can act as the user briefly but **never** sees `~/.aws/credentials`,
`~/.config/gcloud`, or any long-lived/refresh material. AWS comes from **IAM Identity Center / SSO**;
GCP from **gcloud ADC** with the `refresh_token` stripped. This closes the research's #1 gap
(`specs/research/2026-06-17-startup-usecase-prior-art.md`, actionables #1/#2).

**Architecture:** Two new providers in `internal/engine/creds`, each following the exact `StagePnpm`
shape proven in SP2: a **pure** render/parse core + a thin `Stage*` that shells out to the host CLI,
writes a `0600` artifact into `stageDir`, and returns an env pointer. **Decay-first:** the staged
creds are inherently short-lived (SSO role creds / ADC access token, ~1h) and there is **no revoke
step** — the only cleanup is the existing `stageDir` wipe, which is correct precisely because
`SIGKILL`/force-quit can skip hooks (the research's lesson). The host CLIs (`aws`, `gcloud`) are run
on the **host before launch**; the staged artifact is what crosses the boundary, never the host
config. Hermetic tests use pure functions for argv/parse + a PATH-injected fake `aws`/`gcloud` for
the stage path — **no live API calls** (the repo rule). The container path already mounts only
`stageDir` (ro) + workspace, so `~/.aws`/`~/.config/gcloud` are never bind-mounted; a leak test pins
that.

**Tech stack:** Go 1.26 (`internal/engine/{policy,creds}`, `internal/cli`), the embedded CUE schema,
the existing white-box + PATH-mock test style. `aws` CLI v2 (`configure export-credentials`) and
`gcloud` are host prerequisites surfaced by `slop doctor`.

**File structure:**
- `internal/engine/policy/schema/schema.cue` (modify) — `#AwsSso`, `#GcpAdc`, extend `#Credentials`.
- `internal/engine/policy/policy.go` (modify) — `AwsSso`/`GcpAdc` structs + `Credentials.Aws`/`.Gcp`.
- `internal/engine/creds/aws.go` (create) — pure `awsExportArgv`/`parseAWSProcessCreds`/`renderAWSCredsFile` + `StageAWS`.
- `internal/engine/creds/aws_test.go` (create) — pure tests + PATH-mock stage test.
- `internal/engine/creds/gcp.go` (create) — pure `gcpTokenArgv` + `StageGCP`.
- `internal/engine/creds/gcp_test.go` (create) — pure tests + PATH-mock stage test.
- `internal/cli/cli.go` (modify) — call `StageAWS`/`StageGCP` in `runProfile`; doctor reports aws/gcloud.
- `internal/cli/cli_creds_test.go` (create) — end-to-end stage-then-wipe (PATH-mocked) + the `~/.aws` leak/decoy assertion.
- `internal/engine/container/compose_test.go` (modify) — pin compose never references host cloud config.
- `README.md`, `specs/0001-go-rewrite-design.md` (modify) — document + record the providers.

---

## Design decisions flagged for veto (decide before/with execution)

1. **AWS staging = a credentials FILE, not env vars.** `StageAWS` writes a `0600`
   `[default]` profile into `stageDir/aws-credentials` and returns
   `AWS_SHARED_CREDENTIALS_FILE=<path>` + `AWS_PROFILE=default` — mirroring pnpm's
   `.npmrc`+`NPM_CONFIG_USERCONFIG`, keeping the secret values out of `docker inspect`/`ps`. (Alt:
   return the 3 keys as env vars routed through `secrets.env`. **Default: file.**)
2. **GCP delivery = `CLOUDSDK_AUTH_ACCESS_TOKEN` + a staged token file; SDK-direct is a documented
   caveat.** `gcloud auth application-default print-access-token` yields a bare ~1h access token.
   gcloud CLI honors `CLOUDSDK_AUTH_ACCESS_TOKEN`; the google client libraries have **no** bare-token
   env and no standard "access-token-only" ADC file (an `authorized_user` file *requires* the
   `refresh_token` we are deliberately dropping). v1 supports the **gcloud-CLI** path robustly and
   documents the SDK limitation. (Alt: impersonated/downscoped tokens — heavier, later. **Default:
   CLOUDSDK_AUTH_ACCESS_TOKEN + caveat.**)
3. **AWS profile identity = a named SSO profile.** `#AwsSso.profile` names a profile already
   configured for SSO in the user's `~/.aws/config`; `aws configure export-credentials --profile P`
   resolves SSO → short-lived creds. (Alt: explicit `startUrl`/`account`/`role`. **Default:
   profile-name** — least config, reuses what `aws sso login` set up.)
4. **No revoke; reserve a federation shape.** Decay-first means cleanup = the `stageDir` wipe only.
   The schema reserves room for a future keyless `#Federation`/OIDC provider so it isn't retrofitted
   as "another static key."

---

### Task 1: schema + Go structs for `aws` / `gcp` credentials

**Files:** Modify `internal/engine/policy/schema/schema.cue`, `internal/engine/policy/policy.go`; Test `internal/engine/policy/policy_test.go`.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/engine/policy/policy_test.go
func TestLoadDecodesAwsGcpCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	src := `package slop
slop: {
	version: 1
	profiles: cloud: {
		agent: "claude"
		environment: "container"
		network: "allow"
		credentials: {
			aws: {profile: "dev-admin", region: "eu-west-1"}
			gcp: {}
		}
	}
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Profiles["cloud"].Credentials
	if c == nil || c.Aws == nil || c.Aws.Profile != "dev-admin" || c.Aws.Region != "eu-west-1" {
		t.Fatalf("aws creds not decoded: %+v", c)
	}
	if c.Gcp == nil {
		t.Fatalf("gcp creds not decoded: %+v", c)
	}
}
```
(Ensure `policy_test.go` imports `os` and `path/filepath`.)

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/engine/policy/ -run TestLoadDecodesAwsGcp -v
```
Expected: FAIL — schema rejects `aws`/`gcp`, or struct has no `Aws`/`Gcp`.

- [ ] **Step 3: Extend the schema** (`internal/engine/policy/schema/schema.cue`)

After `#PnpmRegistry` / before `#Credentials`, add:

```cue
// AWS creds minted from an IAM Identity Center (SSO) profile. `aws configure
// export-credentials --profile <profile>` resolves SSO to short-lived role creds;
// the user runs `aws sso login --profile <profile>` on the host first.
#AwsSso: {
	profile: string        // a named profile configured for SSO in ~/.aws/config
	region?: string
}

// GCP creds from Application Default Credentials. A short-lived access token is
// minted via `gcloud auth application-default print-access-token`; the long-lived
// refresh token is never staged.
#GcpAdc: {
}
```

Extend `#Credentials`:

```cue
#Credentials: {
	pnpm?: [...#PnpmRegistry]
	aws?:  #AwsSso
	gcp?:  #GcpAdc
}
```

- [ ] **Step 4: Extend the Go structs** (`internal/engine/policy/policy.go`)

```go
// AwsSso mints short-lived AWS creds from an SSO-configured profile (SP/0009).
type AwsSso struct {
	Profile string `json:"profile"`
	Region  string `json:"region,omitempty"`
}

// GcpAdc stages a short-lived GCP access token from ADC, refresh token stripped.
type GcpAdc struct{}
```

In `Credentials`:

```go
type Credentials struct {
	Pnpm []PnpmRegistry `json:"pnpm,omitempty"`
	Aws  *AwsSso        `json:"aws,omitempty"`
	Gcp  *GcpAdc        `json:"gcp,omitempty"`
}
```

- [ ] **Step 5: Run, verify it passes**

```bash
gofmt -w internal/engine/policy/ && go test ./internal/engine/policy/ -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/policy/schema/schema.cue internal/engine/policy/policy.go internal/engine/policy/policy_test.go
git commit -m "feat(policy): aws(sso)/gcp(adc) credential schema + structs"
```

---

### Task 2: AWS provider — pure core (argv, parse, render)

**Files:** Create `internal/engine/creds/aws.go`, `internal/engine/creds/aws_test.go`.

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/creds/aws_test.go
package creds

import (
	"strings"
	"testing"
)

func TestAwsExportArgv(t *testing.T) {
	got := awsExportArgv("dev-admin")
	want := []string{"aws", "configure", "export-credentials", "--profile", "dev-admin", "--format", "process"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v", got)
	}
}

func TestParseAWSProcessCreds(t *testing.T) {
	// the documented shape of `aws configure export-credentials --format process`
	out := `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok","Expiration":"2026-06-17T12:00:00Z"}`
	c, err := parseAWSProcessCreds([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessKeyID != "AKIA" || c.SecretAccessKey != "sek" || c.SessionToken != "tok" {
		t.Fatalf("parsed = %+v", c)
	}
}

func TestRenderAWSCredsFileHasSessionToken(t *testing.T) {
	got := renderAWSCredsFile(awsCreds{AccessKeyID: "AKIA", SecretAccessKey: "sek", SessionToken: "tok"}, "eu-west-1")
	for _, want := range []string{"[default]", "aws_access_key_id = AKIA", "aws_secret_access_key = sek", "aws_session_token = tok", "region = eu-west-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("creds file missing %q:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/engine/creds/ -run 'TestAwsExportArgv|TestParseAWSProcessCreds|TestRenderAWSCredsFile' -v
```
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement the pure core** (`internal/engine/creds/aws.go`)

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

type awsCreds struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
}

// awsExportArgv builds the `aws configure export-credentials` call that resolves
// an SSO profile to short-lived role creds (process JSON on stdout).
func awsExportArgv(profile string) []string {
	return []string{"aws", "configure", "export-credentials", "--profile", profile, "--format", "process"}
}

func parseAWSProcessCreds(out []byte) (awsCreds, error) {
	var c awsCreds
	if err := json.Unmarshal(out, &c); err != nil {
		return awsCreds{}, fmt.Errorf("parse aws export-credentials: %w", err)
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return awsCreds{}, fmt.Errorf("aws export-credentials returned no key (SSO session expired? run: aws sso login)")
	}
	return c, nil
}

func renderAWSCredsFile(c awsCreds, region string) string {
	var b strings.Builder
	b.WriteString("[default]\n")
	fmt.Fprintf(&b, "aws_access_key_id = %s\n", c.AccessKeyID)
	fmt.Fprintf(&b, "aws_secret_access_key = %s\n", c.SecretAccessKey)
	if c.SessionToken != "" {
		fmt.Fprintf(&b, "aws_session_token = %s\n", c.SessionToken)
	}
	if region != "" {
		fmt.Fprintf(&b, "region = %s\n", region)
	}
	return b.String()
}
```

- [ ] **Step 4: Run, verify it passes**

```bash
gofmt -w internal/engine/creds/ && go test ./internal/engine/creds/ -run 'TestAws|TestParseAWS|TestRenderAWS' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/creds/aws.go internal/engine/creds/aws_test.go
git commit -m "feat(creds): aws sso provider — pure argv/parse/render core"
```

---

### Task 3: AWS provider — `StageAWS` (PATH-mocked shell-out)

**Files:** Modify `internal/engine/creds/aws.go`, `internal/engine/creds/aws_test.go`.

- [ ] **Step 1: Write the failing test (hermetic — fake `aws` on PATH)**

```go
// append to internal/engine/creds/aws_test.go
import (
	"context"
	"os"
	"path/filepath"
)

// fakeBin writes an executable shell stub named `name` into dir that prints `stdout`.
func fakeBin(t *testing.T, dir, name, stdout string) {
	t.Helper()
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStageAWSWritesScopedFileAndEnv(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "aws", `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok"}`)
	t.Setenv("PATH", binDir)

	stage := t.TempDir()
	env, err := StageAWS(context.Background(), &policy.Credentials{Aws: &policy.AwsSso{Profile: "dev", Region: "eu-west-1"}}, stage)
	if err != nil {
		t.Fatalf("StageAWS: %v", err)
	}
	credFile := filepath.Join(stage, "aws-credentials")
	fi, err := os.Stat(credFile)
	if err != nil {
		t.Fatalf("staged file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v want 0600", fi.Mode().Perm())
	}
	body, _ := os.ReadFile(credFile)
	if !strings.Contains(string(body), "aws_session_token = tok") {
		t.Fatalf("staged creds wrong:\n%s", body)
	}
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "AWS_SHARED_CREDENTIALS_FILE="+credFile) || !strings.Contains(joined, "AWS_PROFILE=default") {
		t.Fatalf("env = %v", env)
	}
}

func TestStageAWSNilIsNoop(t *testing.T) {
	env, err := StageAWS(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil aws creds must be a no-op: env=%v err=%v", env, err)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/engine/creds/ -run TestStageAWS -v
```
Expected: FAIL — `undefined: StageAWS`.

- [ ] **Step 3: Implement `StageAWS`** (append to `internal/engine/creds/aws.go`)

```go
// StageAWS resolves the profile's SSO creds on the host (short-lived), writes a
// 0600 credentials file into stageDir, and returns env pointing the agent at it.
// No revoke: the creds expire (~1h) and stageDir is wiped on exit (decay-first).
func StageAWS(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Aws == nil {
		return nil, nil
	}
	argv := awsExportArgv(creds.Aws.Profile)
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("aws export-credentials (profile %q; is `aws sso login` current?): %w", creds.Aws.Profile, err)
	}
	c, err := parseAWSProcessCreds(out)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	credFile := filepath.Join(stageDir, "aws-credentials")
	if err := os.WriteFile(credFile, []byte(renderAWSCredsFile(c, creds.Aws.Region)), 0o600); err != nil {
		return nil, err
	}
	return []string{"AWS_SHARED_CREDENTIALS_FILE=" + credFile, "AWS_PROFILE=default"}, nil
}
```

- [ ] **Step 4: Run, verify it passes**

```bash
gofmt -w internal/engine/creds/ && go test ./internal/engine/creds/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/creds/aws.go internal/engine/creds/aws_test.go
git commit -m "feat(creds): StageAWS — short-lived SSO creds staged 0600, decay-first"
```

---

### Task 4: GCP provider — `StageGCP` (token mint, refresh_token never staged)

**Files:** Create `internal/engine/creds/gcp.go`, `internal/engine/creds/gcp_test.go`.

- [ ] **Step 1: Write the failing test (PATH-mocked `gcloud`)**

```go
// internal/engine/creds/gcp_test.go
package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestGcpTokenArgv(t *testing.T) {
	got := gcpTokenArgv()
	want := []string{"gcloud", "auth", "application-default", "print-access-token"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v", got)
	}
}

func TestStageGCPStagesAccessTokenOnly(t *testing.T) {
	binDir := t.TempDir()
	// fakeBin from aws_test.go (same package); print a bare access token.
	fakeBin(t, binDir, "gcloud", "ya29.ACCESS_TOKEN_VALUE")
	t.Setenv("PATH", binDir)

	stage := t.TempDir()
	env, err := StageGCP(context.Background(), &policy.Credentials{Gcp: &policy.GcpAdc{}}, stage)
	if err != nil {
		t.Fatalf("StageGCP: %v", err)
	}
	tokFile := filepath.Join(stage, "gcp-access-token")
	body, err := os.ReadFile(tokFile)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if strings.TrimSpace(string(body)) != "ya29.ACCESS_TOKEN_VALUE" {
		t.Fatalf("token body = %q", body)
	}
	if strings.Contains(string(body), "refresh_token") {
		t.Fatal("refresh_token must never be staged")
	}
	if fi, _ := os.Stat(tokFile); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file not 0600")
	}
	if !strings.Contains(strings.Join(env, " "), "CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.ACCESS_TOKEN_VALUE") {
		t.Fatalf("env = %v", env)
	}
}

func TestStageGCPNilIsNoop(t *testing.T) {
	env, err := StageGCP(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil gcp creds must be a no-op: env=%v err=%v", env, err)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/engine/creds/ -run 'TestGcp|TestStageGCP' -v
```
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement** (`internal/engine/creds/gcp.go`)

```go
package creds

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func gcpTokenArgv() []string {
	return []string{"gcloud", "auth", "application-default", "print-access-token"}
}

// StageGCP mints a short-lived ADC access token on the host and stages ONLY that
// token (the long-lived refresh token is never read or written), exposing it via
// CLOUDSDK_AUTH_ACCESS_TOKEN for the gcloud CLI. No revoke (token expires ~1h).
func StageGCP(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Gcp == nil {
		return nil, nil
	}
	argv := gcpTokenArgv()
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("gcloud print-access-token (is ADC set up? run: gcloud auth application-default login): %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return nil, fmt.Errorf("gcloud returned an empty access token")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	tokFile := filepath.Join(stageDir, "gcp-access-token")
	if err := os.WriteFile(tokFile, []byte(tok+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []string{"CLOUDSDK_AUTH_ACCESS_TOKEN=" + tok}, nil
}
```

- [ ] **Step 4: Run, verify it passes**

```bash
gofmt -w internal/engine/creds/ && go test ./internal/engine/creds/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/creds/gcp.go internal/engine/creds/gcp_test.go
git commit -m "feat(creds): StageGCP — ADC access token only, refresh token never staged"
```

---

### Task 5: wire `StageAWS`/`StageGCP` into the run lifecycle

**Files:** Modify `internal/cli/cli.go`; Create `internal/cli/cli_creds_test.go`.

- [ ] **Step 1: Write the failing test (end-to-end stage + wipe + leak/decoy)**

```go
// internal/cli/cli_creds_test.go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func fakeBin(t *testing.T, dir, name, stdout string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ncat <<'EOF'\n"+stdout+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A host-mode profile with aws+gcp creds stages both, the child sees the pointers,
// and the stage (with the secrets) is wiped on exit. A planted host ~/.aws decoy is
// NOT what the child reads — the staged SSO creds are.
func TestRunProfileStagesCloudCredsThenWipes(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "aws", `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok"}`)
	fakeBin(t, binDir, "gcloud", "ya29.TOKEN")
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	ws := t.TempDir()
	out := filepath.Join(ws, "seen")
	// child records the staged AWS file path + the GCP token env it was given.
	script := `printf '%s\n%s\n' "$AWS_SHARED_CREDENTIALS_FILE" "$CLOUDSDK_AUTH_ACCESS_TOKEN" > "` + out + `"`

	prof := policy.Profile{
		Agent: "shell", Environment: "host", Network: "deny",
		Credentials: &policy.Credentials{Aws: &policy.AwsSso{Profile: "dev"}, Gcp: &policy.GcpAdc{}},
	}
	code, err := runProfile("cloud", prof, []string{"/bin/sh", "-c", script}, ws)
	if err != nil || code != 0 {
		t.Fatalf("runProfile code=%d err=%v", code, err)
	}
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), filepath.Join(ws, ".slop", "runtime", "cloud", "aws-credentials")) {
		t.Fatalf("child did not get staged AWS file: %q", got)
	}
	if !strings.Contains(string(got), "ya29.TOKEN") {
		t.Fatalf("child did not get GCP token: %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".slop", "runtime", "cloud")); !os.IsNotExist(err) {
		t.Fatalf("stage (with creds) not wiped: %v", err)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/cli/ -run TestRunProfileStagesCloudCreds -v
```
Expected: FAIL — `runProfile` does not stage AWS/GCP yet.

- [ ] **Step 3: Wire into `runProfile`** (`internal/cli/cli.go`)

After the `npmrcEnv, err := creds.StagePnpm(...)` block, add:

```go
	awsEnv, err := creds.StageAWS(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
	gcpEnv, err := creds.StageGCP(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
	cloudEnv := append(awsEnv, gcpEnv...)
```

Then add `cloudEnv` to each environment's env assembly. For `host` and `sandbox`:

```go
		env := append(append(append(os.Environ(), secretEnv...), npmrcEnv...), cloudEnv...)
```

For `container` and `vm`, the AWS file rides the staged `stageDir` (already bind-mounted ro); the
pointer env vars (`AWS_SHARED_CREDENTIALS_FILE`, `AWS_PROFILE`, `CLOUDSDK_AUTH_ACCESS_TOKEN`) must
travel with the secrets channel, so append `cloudEnv` to `secretEnv` before those calls:

```go
	secretEnv = append(secretEnv, cloudEnv...) // delivered via secrets.env (container) / scp'd env (vm)
```

(Place this right after `cloudEnv` is built, before the `switch prof.Environment`. The host/sandbox
branches read `cloudEnv` directly; container/vm read it from `secretEnv`. Pick ONE path per branch —
do not double-inject. Simplest: build `cloudEnv`, append to `secretEnv` once, and have host/sandbox
also use `secretEnv` — verify the host test still passes.)

- [ ] **Step 4: Run the full cli + creds suites**

```bash
gofmt -w internal/cli/ && go test ./internal/cli/ ./internal/engine/creds/ -v
```
Expected: PASS (new test + all existing — `TestRunProfileStagesSecretsAndNpmrcThenWipes` still green).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_creds_test.go
git commit -m "feat(cli): stage aws/gcp creds in the run lifecycle, wiped on exit"
```

---

### Task 6: leak/decoy pins + `slop doctor` reports aws/gcloud

**Files:** Modify `internal/engine/container/compose_test.go`, `internal/cli/cli.go`.

- [ ] **Step 1: Write the failing tests**

```go
// append to internal/engine/container/compose_test.go
// The container must NEVER bind-mount host cloud config — only stageDir + workspace.
func TestComposeNeverMountsHostCloudConfig(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{".aws", "application_default_credentials", ".config/gcloud"} {
		if strings.Contains(yml, bad) {
			t.Fatalf("compose references host cloud config (%q):\n%s", bad, yml)
		}
	}
}
```

```go
// append to internal/cli/cli_creds_test.go — doctor must surface aws + gcloud presence.
func TestDoctorReportsCloudTools(t *testing.T) {
	report := doctorReport()
	for _, tool := range []string{"aws", "gcloud"} {
		if _, ok := report[tool]; !ok {
			t.Fatalf("doctor omits %q", tool)
		}
	}
}
```

- [ ] **Step 2: Run, verify the doctor test fails**

```bash
go test ./internal/cli/ ./internal/engine/container/ -run 'TestComposeNeverMounts|TestDoctorReportsCloud' -v
```
Expected: compose test PASSES (regression guard); doctor test FAILS (no `doctorReport`/no aws,gcloud).

- [ ] **Step 3: Extract `doctorReport()` and add aws/gcloud** (`internal/cli/cli.go`)

Refactor `cmdDoctor` so its tool-probe map is built by a testable `doctorReport() map[string]any`,
and add `"aws"` and `"gcloud"` to the probed `tools` slice:

```go
	tools := []string{"git", "docker", "op", "claude", "opencode", "tart", "mise", "nix", "aws", "gcloud"}
```

Move the report-building loop into `func doctorReport() map[string]any { ... }`; `cmdDoctor`'s `RunE`
calls it. (Keep the existing sandbox/op/container/vm rows.)

- [ ] **Step 4: Run, verify pass**

```bash
gofmt -w internal/cli/ && go test ./internal/cli/ ./internal/engine/container/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_creds_test.go internal/engine/container/compose_test.go
git commit -m "test(creds): pin no host cloud-config mount; doctor reports aws/gcloud"
```

---

### Task 7: docs, schema sample, roadmap record, verify, PR

**Files:** Modify `README.md`, `specs/0001-go-rewrite-design.md`; optionally `library/layer/policy/samples/slop/slop.cue`.

- [ ] **Step 1: Document the providers in the README**

In the credentials section, add: AWS via SSO (`credentials: {aws: {profile: "<sso-profile>"}}` — run
`aws sso login --profile <p>` first; creds are short-lived, never `~/.aws/credentials`); GCP via ADC
(`credentials: {gcp: {}}` — `gcloud auth application-default login` first; only the access token is
staged, `refresh_token` is never exposed; gcloud-CLI access via `CLOUDSDK_AUTH_ACCESS_TOKEN`, SDK
caveat noted). State the **decay-first** model: short TTL is the control; there is no revoke.

- [ ] **Step 2: Record in specs/0001**

Add an AWS/GCP credential-provider line to §7 (the credentials section) noting decay-first + the
reserved federation shape, and a one-line provenance pointer to `specs/0009`.

- [ ] **Step 3: Full verification**

```bash
cd /Users/jojo/workspace/safeslop
make check
make build
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green (no `scripts/` touched; the embedded schema is Go-side only — confirm
`make check-assets` is unaffected, or run it if present).

- [ ] **Step 4: Manual smoke (with real `aws`/`gcloud` if available, else skip)**

```bash
# requires a current SSO session + ADC; otherwise the errors are the honest expected output
printf 'package slop\nslop: {version:1, profiles: cloud: {agent:"shell", environment:"sandbox", network:"deny", credentials:{aws:{profile:"<your-sso-profile>"}, gcp:{}}}}\n' > /tmp/cloud.cue
./slop run --dry-run cloud   # shows the resolved profile; staging happens on real run
```

- [ ] **Step 5: Branch, push, PR**

```bash
git push -u origin aws-gcp-credentials
gh pr create --title "AWS(SSO)+GCP(ADC) credential providers — decay-first" \
  --body "Implements specs/0009. Short-lived AWS (IAM Identity Center) + GCP (ADC, refresh_token stripped) creds minted just-in-time, staged 0600 into the wiped-on-exit run dir, never exposing ~/.aws/credentials or ~/.config/gcloud. Decay-first: short TTL is the control, no revoke (SIGKILL-safe). Hermetic PATH-mocked tests; leak test pins no host cloud-config mount; doctor reports aws/gcloud. Closes the research #1 gap (specs/research/2026-06-17)."
```

---

## Verification (what "done" means)

- `make check` + `make build` green; the four fish gates green.
- A profile with `credentials: {aws:{profile:…}, gcp:{}}` validates, and a real run stages a `0600`
  AWS credentials file + a GCP access token into `.slop/runtime/<profile>/`, points the agent at
  them, and wipes the stage on exit.
- The `refresh_token` is never read or staged; `~/.aws/credentials`/`~/.config/gcloud` never cross
  any boundary (pinned by the no-host-cloud-mount test).
- No revoke path exists — cleanup is the stage wipe (decay-first); `slop doctor` reports `aws` +
  `gcloud`.

## Deliberately deferred (not here)

- **Keyless Workload Identity Federation** for GCP (and AWS OIDC role-assumption) — the reserved
  `#Federation` shape; the best ephemeral cred is none on disk, but it needs more setup.
- **SDK-direct GCP token delivery** beyond `CLOUDSDK_AUTH_ACCESS_TOKEN` (impersonated/downscoped
  tokens) — documented caveat now, hardening later.
- **aws-vault / static-key AWS paths** — out of scope; this targets IAM Identity Center / SSO.
- **Auto `aws sso login` / `gcloud auth ... login`** from the engine — v1 expects the host session to
  be current and surfaces a clear error if not; driving the interactive login is later (and a natural
  fit for the SP7 installer/portal).
