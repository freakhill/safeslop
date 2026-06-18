# `kube` (EKS / GKE) credential provider — decay-first Implementation Plan

**Goal:** Let a profile declare a single Kubernetes cluster (`credentials: {kube: {eks|gke: …}}`) and have `slop` pre-mint a short-lived bearer token on the **host** (using the host's SSO/ADC) and stage a scoped one-cluster kubeconfig into the run's `stageDir`, so the agent's `kubectl` works inside the boundary with **no cloud credentials and no cloud CLI** present.

**Architecture:** A third provider in `internal/engine/creds` (`kube.go`) that mirrors `aws.go`/`gcp.go`: a pure render/parse core (argv builders, `ExecCredential` token parse, EKS/GKE describe parse, kubeconfig render) plus a thin `StageKube(ctx, *policy.Credentials, stageDir) ([]string, error)`. The bearer token is the secret and lives **inside** the staged `kubeconfig` (0600, wiped with `stageDir` on exit — decay-first, no revoke). `KUBECONFIG` is a *non-secret path* delivered exactly like the existing `.npmrc`/`NPM_CONFIG_USERCONFIG` pair: the host path for `sandbox`/`host`, and `/slop/runtime/kubeconfig` set in the compose env for `container`. `environment: vm` is deferred behind a clear guard error (single-quoted `secrets.env` + unknown guest `$HOME` make a correct in-VM path out of scope for v1).

**Why the kubeconfig is a file (not env-only):** AWS/GCP ship pure env vars because the SDKs read env. `kubectl` has no env-only mode — it needs a kubeconfig file with `server` + `certificate-authority-data` + `token`. So a file is unavoidable; the `.npmrc` pattern (token-in-file, path-via-env, per-environment path) is the established precedent for exactly this shape.

**Why GKE uses `gke-gcloud-auth-plugin` + `gcloud … describe`:** the plugin emits the same `ExecCredential{status.token}` JSON shape as `aws eks get-token`, so one parser serves both. `gcloud container clusters describe --format json` yields the endpoint + base64 CA needed to make the kubeconfig standalone.

**Why kubeconfig is rendered as JSON:** JSON is valid YAML, so `kubectl` reads a JSON-content `kubeconfig` file fine. `encoding/json` (already used by `aws.go`) gives us deterministic, escaping-safe rendering with **no new dependency** and trivially re-parseable test assertions.

**Tech stack:** Go (`encoding/json`, `os/exec`), embedded CUE schema (`cuelang.org/go`), `text/template` compose rendering. No new modules.

**Base branch:** Ideally execute off `main` **after #11→#12→#13 merge**, so #12's `policy.Lint` (`internal/engine/policy/lint.go`) is present for the Task 7 lint test and history is clean. The lint needs **no code change** — it already keys on `p.Credentials != nil`, which a `kube` profile satisfies. If starting before those merges, branch off `aws-gcp-credentials` (#13) and add the lint test after rebasing onto post-#12 `main`. Feature branch: `kube-credentials`.

**File structure:**
- `internal/engine/policy/schema/schema.cue` (modify) — add `#KubeCluster`/`#EksCluster`/`#GkeCluster`; add `kube?: #KubeCluster` to `#Credentials`.
- `internal/engine/policy/policy.go` (modify) — add `EksCluster`/`GkeCluster`/`KubeCluster` structs; add `Kube *KubeCluster` to `Credentials`.
- `internal/engine/policy/policy_test.go` (modify) — parse a `kube` profile from CUE.
- `internal/engine/creds/kube.go` (create) — argv builders, `ExecCredential`/describe parsers, kubeconfig render, `StageKube`.
- `internal/engine/creds/kube_test.go` (create) — pure-core unit tests + PATH-mocked `StageKube` for EKS and GKE + guards.
- `internal/engine/container/compose.go` (modify) — add `Kubeconfig bool` to `composeParams`.
- `internal/engine/container/assets/compose.yml.tmpl` (modify) — emit `KUBECONFIG: /slop/runtime/kubeconfig` when staged.
- `internal/engine/container/launch.go` (modify) — detect `<stageDir>/kubeconfig`, set `Kubeconfig: true`.
- `internal/engine/container/compose_test.go` (modify) — assert the compose env line appears iff a kubeconfig is staged.
- `internal/cli/cli.go` (modify) — call `StageKube`; thread `kubeEnv` into host/sandbox; guard `kube`+`vm`; add `gke-gcloud-auth-plugin` to `doctorReport`.
- `internal/cli/cli_test.go` (modify) — wiring/guard/doctor assertions.
- `internal/cli/cli_lint_test.go` (modify, **needs #12**) — a `kube` profile trips `sandbox-open-egress-with-creds`.
- `library/layer/policy/samples/slop/slop.cue` (modify) — a `kube` sample profile.
- `README.md` (modify, outside AUTOGEN blocks) — document the `kube` provider.
- `specs/0001-go-rewrite-design.md` (modify) — mark §7.5 kubectl done, point to specs/0010.

---

## Design decisions flagged for veto (decide before/with execution)

1. **vm deferred behind a guard error.** `kube` + `environment: vm` returns a clear error pointing at `container`. Rationale: `secrets.env` values are single-quoted (no `~`/`$HOME` expansion) and the tart guest's home (`/Users/admin` vs `/home/admin`) is not pinned, so a correct in-VM `KUBECONFIG` path is guesswork. Alternative: hardcode `/Users/admin/.slop-runtime/kubeconfig` for the macOS guest now. **Recommendation:** guard now, support vm in a follow-up once the guest home is pinned.
2. **One cluster per profile.** `#KubeCluster` holds exactly one of `eks`/`gke` (enforced at runtime). The user asked for a "scoped one-cluster kubeconfig". Alternative: `kube: [...#KubeCluster]` for multi-cluster contexts. **Recommendation:** single cluster for v1.
3. **GKE token = `gke-gcloud-auth-plugin` (ExecCredential), not a raw ADC token.** Both reach the same Google OAuth token, but the plugin is the canonical, version-stable path and shares the EKS parser. **Recommendation:** as written.
4. **`--location` (not `--zone`/`--region`) for `gcloud container clusters describe`.** `--location` accepts both zonal and regional clusters, so the schema needs one `location` field instead of a zone/region split. **Recommendation:** as written.
5. **`doctor` adds `gke-gcloud-auth-plugin` only** (the host tool GKE token-minting shells out to). `kubectl` runs *inside* the boundary (it's in the container image), so it is intentionally not a host-doctor probe. **Recommendation:** as written.

---

### Task 1: schema + Go structs for `kube` credentials

**Files:**
- Modify: `internal/engine/policy/schema/schema.cue:44-52`
- Modify: `internal/engine/policy/policy.go:39-46`
- Test: `internal/engine/policy/policy_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/engine/policy/policy_test.go`:

```go
func TestLoadKubeEksCredentials(t *testing.T) {
	dir := t.TempDir()
	cue := `package slop
profiles: {
	deploy: {
		agent:       "claude"
		environment: "container"
		credentials: kube: eks: {name: "prod", region: "eu-west-1", profile: "dev-admin"}
	}
}`
	p := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(p, []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Profiles["deploy"].Credentials
	if c == nil || c.Kube == nil || c.Kube.Eks == nil {
		t.Fatalf("kube.eks not parsed: %+v", c)
	}
	if c.Kube.Eks.Name != "prod" || c.Kube.Eks.Region != "eu-west-1" || c.Kube.Eks.Profile != "dev-admin" {
		t.Fatalf("eks fields = %+v", c.Kube.Eks)
	}
	if c.Kube.Gke != nil {
		t.Fatalf("gke must be nil when only eks set")
	}
}

func TestLoadKubeGkeCredentials(t *testing.T) {
	dir := t.TempDir()
	cue := `package slop
profiles: {
	deploy: {
		agent:       "claude"
		environment: "container"
		credentials: kube: gke: {name: "prod", location: "europe-west1", project: "acme-prod"}
	}
}`
	p := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(p, []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	g := cfg.Profiles["deploy"].Credentials.Kube.Gke
	if g == nil || g.Name != "prod" || g.Location != "europe-west1" || g.Project != "acme-prod" {
		t.Fatalf("gke fields = %+v", g)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/policy/ -run TestLoadKube -v
```
Expected: FAIL — compile error (`Kube` field undefined) or CUE rejects the unknown `kube` field.

- [ ] **Step 3: Add the schema definitions.** In `internal/engine/policy/schema/schema.cue`, insert after the `#GcpAdc` block (currently ends at line 45) and before `// Credentials groups…`:

```cue
// A single Kubernetes cluster to pre-authenticate for (specs/0010). Set exactly one
// of eks/gke. The host mints a short-lived bearer token (aws eks get-token /
// gke-gcloud-auth-plugin) and stages a one-cluster kubeconfig, so the agent's kubectl
// needs neither cloud creds nor the cloud CLI inside the boundary. Decay-first: the
// token expires (~15m EKS / ~1h GKE); cleanup is the stageDir wipe.
#KubeCluster: {
	eks?: #EksCluster
	gke?: #GkeCluster
}

// An EKS cluster. The host resolves it with `aws eks get-token` + `aws eks
// describe-cluster`, using the named SSO profile (or the default) — run `aws sso
// login` first.
#EksCluster: {
	name:     string
	region?:  string
	profile?: string
}

// A GKE cluster. The host mints the token with `gke-gcloud-auth-plugin` (ADC) and
// resolves the endpoint/CA with `gcloud container clusters describe`. `location` is
// the cluster's zone or region (e.g. "europe-west1" or "europe-west1-b").
#GkeCluster: {
	name:     string
	location: string
	project?: string
}
```

Then add `kube` to `#Credentials` (currently lines 48-52):

```cue
#Credentials: {
	pnpm?: [...#PnpmRegistry]
	aws?:  #AwsSso
	gcp?:  #GcpAdc
	kube?: #KubeCluster
}
```

- [ ] **Step 4: Add the Go structs.** In `internal/engine/policy/policy.go`, insert after `type GcpAdc struct{}` (line 39) and before `// Credentials groups…`:

```go
// EksCluster pre-authenticates an EKS cluster: the host runs `aws eks get-token`
// (bearer) + `aws eks describe-cluster` (endpoint/CA) under Profile's SSO (specs/0010).
type EksCluster struct {
	Name    string `json:"name"`
	Region  string `json:"region,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// GkeCluster pre-authenticates a GKE cluster: `gke-gcloud-auth-plugin` (bearer, via
// ADC) + `gcloud container clusters describe` (endpoint/CA). Location is a zone or
// region (specs/0010).
type GkeCluster struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Project  string `json:"project,omitempty"`
}

// KubeCluster stages a scoped one-cluster kubeconfig (token inside, 0600) so the
// agent's kubectl needs no cloud CLI/creds inside the boundary. Exactly one of
// Eks/Gke (specs/0010).
type KubeCluster struct {
	Eks *EksCluster `json:"eks,omitempty"`
	Gke *GkeCluster `json:"gke,omitempty"`
}
```

Then add `Kube` to `Credentials` (currently lines 42-46):

```go
type Credentials struct {
	Pnpm []PnpmRegistry `json:"pnpm,omitempty"`
	Aws  *AwsSso        `json:"aws,omitempty"`
	Gcp  *GcpAdc        `json:"gcp,omitempty"`
	Kube *KubeCluster   `json:"kube,omitempty"`
}
```

- [ ] **Step 5: Run the test, verify it passes**

```bash
go test ./internal/engine/policy/ -run TestLoadKube -v
```
Expected: PASS (both EKS and GKE).

- [ ] **Step 6: Commit**

```bash
git add internal/engine/policy/schema/schema.cue internal/engine/policy/policy.go internal/engine/policy/policy_test.go
git commit -m "feat(creds): #KubeCluster schema + Go structs (eks/gke, specs/0010)"
```

---

### Task 2: kube provider — pure core (argv, parse, render)

**Files:**
- Create: `internal/engine/creds/kube.go`
- Test: `internal/engine/creds/kube_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/creds/kube_test.go`:

```go
package creds

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEksArgv(t *testing.T) {
	tok := strings.Join(eksGetTokenArgv("prod", "eu-west-1", "dev-admin"), " ")
	if tok != "aws eks get-token --cluster-name prod --output json --region eu-west-1 --profile dev-admin" {
		t.Fatalf("get-token argv = %q", tok)
	}
	desc := strings.Join(eksDescribeArgv("prod", "", ""), " ")
	if desc != "aws eks describe-cluster --name prod --output json" {
		t.Fatalf("describe argv = %q", desc)
	}
}

func TestGkeArgv(t *testing.T) {
	if got := strings.Join(gkeTokenArgv(), " "); got != "gke-gcloud-auth-plugin" {
		t.Fatalf("gke token argv = %q", got)
	}
	desc := strings.Join(gkeDescribeArgv("prod", "europe-west1", "acme"), " ")
	if desc != "gcloud container clusters describe prod --location europe-west1 --format json --project acme" {
		t.Fatalf("gke describe argv = %q", desc)
	}
}

func TestParseExecToken(t *testing.T) {
	out := `{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1beta1","status":{"expirationTimestamp":"2026-06-18T12:00:00Z","token":"k8s-aws-v1.aHR0cHM"}}`
	tok, err := parseExecToken([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if tok != "k8s-aws-v1.aHR0cHM" {
		t.Fatalf("token = %q", tok)
	}
	if _, err := parseExecToken([]byte(`{"status":{}}`)); err == nil {
		t.Fatal("expected error on empty token")
	}
}

func TestParseEksDescribe(t *testing.T) {
	out := `{"cluster":{"name":"prod","endpoint":"https://ABC.gr7.eu-west-1.eks.amazonaws.com","certificateAuthority":{"data":"Q0FEQVRB"},"status":"ACTIVE"}}`
	server, ca, err := parseEksDescribe([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://ABC.gr7.eu-west-1.eks.amazonaws.com" || ca != "Q0FEQVRB" {
		t.Fatalf("server=%q ca=%q", server, ca)
	}
	if _, _, err := parseEksDescribe([]byte(`{"cluster":{"endpoint":""}}`)); err == nil {
		t.Fatal("expected error on missing endpoint/ca")
	}
}

func TestParseGkeDescribe(t *testing.T) {
	out := `{"endpoint":"34.79.12.34","masterAuth":{"clusterCaCertificate":"Q0FEQVRB"}}`
	server, ca, err := parseGkeDescribe([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://34.79.12.34" || ca != "Q0FEQVRB" {
		t.Fatalf("server=%q ca=%q", server, ca)
	}
	if _, _, err := parseGkeDescribe([]byte(`{"endpoint":"","masterAuth":{}}`)); err == nil {
		t.Fatal("expected error on missing endpoint/ca")
	}
}

func TestRenderKubeconfig(t *testing.T) {
	raw := renderKubeconfig("eks:prod", "https://srv", "Q0FEQVRB", "k8s-aws-v1.tok")
	var kc map[string]any
	if err := json.Unmarshal(raw, &kc); err != nil {
		t.Fatalf("rendered kubeconfig is not valid JSON/YAML: %v", err)
	}
	if kc["current-context"] != "eks:prod" {
		t.Fatalf("current-context = %v", kc["current-context"])
	}
	clusters := kc["clusters"].([]any)
	cl := clusters[0].(map[string]any)["cluster"].(map[string]any)
	if cl["server"] != "https://srv" || cl["certificate-authority-data"] != "Q0FEQVRB" {
		t.Fatalf("cluster = %v", cl)
	}
	users := kc["users"].([]any)
	usr := users[0].(map[string]any)["user"].(map[string]any)
	if usr["token"] != "k8s-aws-v1.tok" {
		t.Fatalf("user token = %v", usr["token"])
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/creds/ -run 'TestEksArgv|TestGkeArgv|TestParseExecToken|TestParseEksDescribe|TestParseGkeDescribe|TestRenderKubeconfig' -v
```
Expected: FAIL — undefined identifiers (`eksGetTokenArgv`, `parseExecToken`, …).

- [ ] **Step 3: Write the pure core** — create `internal/engine/creds/kube.go`:

```go
package creds

import (
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"
)

// ---- argv builders ----

func eksGetTokenArgv(name, region, profile string) []string {
	a := []string{"aws", "eks", "get-token", "--cluster-name", name, "--output", "json"}
	if region != "" {
		a = append(a, "--region", region)
	}
	if profile != "" {
		a = append(a, "--profile", profile)
	}
	return a
}

func eksDescribeArgv(name, region, profile string) []string {
	a := []string{"aws", "eks", "describe-cluster", "--name", name, "--output", "json"}
	if region != "" {
		a = append(a, "--region", region)
	}
	if profile != "" {
		a = append(a, "--profile", profile)
	}
	return a
}

func gkeTokenArgv() []string { return []string{"gke-gcloud-auth-plugin"} }

func gkeDescribeArgv(name, location, project string) []string {
	a := []string{"gcloud", "container", "clusters", "describe", name, "--location", location, "--format", "json"}
	if project != "" {
		a = append(a, "--project", project)
	}
	return a
}

// ---- parsers ----

// parseExecToken extracts status.token from a client.authentication.k8s.io
// ExecCredential — the shape emitted by both `aws eks get-token` and
// `gke-gcloud-auth-plugin`.
func parseExecToken(out []byte) (string, error) {
	var ec struct {
		Status struct {
			Token string `json:"token"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &ec); err != nil {
		return "", fmt.Errorf("parse ExecCredential token: %w", err)
	}
	if ec.Status.Token == "" {
		return "", fmt.Errorf("no k8s bearer token returned (cloud session expired? run: aws sso login / gcloud auth application-default login)")
	}
	return ec.Status.Token, nil
}

func parseEksDescribe(out []byte) (server, caData string, err error) {
	var d struct {
		Cluster struct {
			Endpoint             string `json:"endpoint"`
			CertificateAuthority struct {
				Data string `json:"data"`
			} `json:"certificateAuthority"`
		} `json:"cluster"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return "", "", fmt.Errorf("parse aws eks describe-cluster: %w", err)
	}
	if d.Cluster.Endpoint == "" || d.Cluster.CertificateAuthority.Data == "" {
		return "", "", fmt.Errorf("aws eks describe-cluster returned no endpoint/CA")
	}
	return d.Cluster.Endpoint, d.Cluster.CertificateAuthority.Data, nil
}

func parseGkeDescribe(out []byte) (server, caData string, err error) {
	var d struct {
		Endpoint   string `json:"endpoint"`
		MasterAuth struct {
			ClusterCaCertificate string `json:"clusterCaCertificate"`
		} `json:"masterAuth"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return "", "", fmt.Errorf("parse gcloud container clusters describe: %w", err)
	}
	if d.Endpoint == "" || d.MasterAuth.ClusterCaCertificate == "" {
		return "", "", fmt.Errorf("gcloud container clusters describe returned no endpoint/CA")
	}
	return "https://" + d.Endpoint, d.MasterAuth.ClusterCaCertificate, nil
}

// ---- kubeconfig render ----

// kubeconfig is a minimal one-cluster kubeconfig. Rendered as JSON (valid YAML, so
// kubectl reads it directly); the bearer token is embedded, making this whole file the
// secret — staged 0600, wiped with the run on exit.
type kubeconfig struct {
	APIVersion     string          `json:"apiVersion"`
	Kind           string          `json:"kind"`
	Clusters       []kcCluster     `json:"clusters"`
	Users          []kcUser        `json:"users"`
	Contexts       []kcContext     `json:"contexts"`
	CurrentContext string          `json:"current-context"`
}

type kcCluster struct {
	Name    string `json:"name"`
	Cluster struct {
		Server                   string `json:"server"`
		CertificateAuthorityData string `json:"certificate-authority-data"`
	} `json:"cluster"`
}

type kcUser struct {
	Name string `json:"name"`
	User struct {
		Token string `json:"token"`
	} `json:"user"`
}

type kcContext struct {
	Name    string `json:"name"`
	Context struct {
		Cluster string `json:"cluster"`
		User    string `json:"user"`
	} `json:"context"`
}

func renderKubeconfig(ctxName, server, caData, token string) []byte {
	var cl kcCluster
	cl.Name = ctxName
	cl.Cluster.Server = server
	cl.Cluster.CertificateAuthorityData = caData

	var us kcUser
	us.Name = ctxName
	us.User.Token = token

	var cx kcContext
	cx.Name = ctxName
	cx.Context.Cluster = ctxName
	cx.Context.User = ctxName

	kc := kubeconfig{
		APIVersion:     "v1",
		Kind:           "Config",
		Clusters:       []kcCluster{cl},
		Users:          []kcUser{us},
		Contexts:       []kcContext{cx},
		CurrentContext: ctxName,
	}
	b, _ := json.MarshalIndent(kc, "", "  ")
	return b
}

// runKubeCmd executes argv and returns stdout, wrapping failures with a hint. The error
// label is argv[0] (+ argv[1] when present) so single-token commands like
// `gke-gcloud-auth-plugin` don't index out of range.
func runKubeCmd(ctx context.Context, argv []string, hint string) ([]byte, error) {
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
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/creds/ -run 'TestEksArgv|TestGkeArgv|TestParseExecToken|TestParseEksDescribe|TestParseGkeDescribe|TestRenderKubeconfig' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/creds/kube.go internal/engine/creds/kube_test.go
git commit -m "feat(creds): kube pure core — argv/ExecCredential/describe parse + kubeconfig render"
```

---

### Task 3: kube provider — `StageKube` (PATH-mocked, EKS + GKE)

**Files:**
- Modify: `internal/engine/creds/kube.go` (replace the Task-2 placeholder tail with `StageKube`)
- Test: `internal/engine/creds/kube_test.go`

- [ ] **Step 1: Write the failing test.** First extend the import block at the top of `internal/engine/creds/kube_test.go` (created in Task 2 with `encoding/json`, `strings`, `testing`) so it reads:

```go
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)
```

Then append the tests:

```go
// fakeMultiBin writes an executable stub that dispatches on $2 (the subcommand) to one
// of several heredoc outputs. Used because `aws` is invoked twice (get-token vs
// describe-cluster) with different required stdout.
func fakeMultiBin(t *testing.T, dir, name string, bySubcmd map[string]string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\ncase \"$2\" in\n")
	for sub, out := range bySubcmd {
		sb.WriteString(sub + ") cat <<'EOF'\n" + out + "\nEOF\n;;\n")
	}
	sb.WriteString("esac\n")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sb.String()), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStageKubeEks(t *testing.T) {
	binDir := t.TempDir()
	fakeMultiBin(t, binDir, "aws", map[string]string{
		"get-token":        `{"kind":"ExecCredential","status":{"token":"k8s-aws-v1.TOK"}}`,
		"describe-cluster": `{"cluster":{"endpoint":"https://EKS.example","certificateAuthority":{"data":"Q0FEQVRB"}}}`,
	})
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH")) // fake `aws` wins; stub's `cat` resolves from real PATH

	stage := t.TempDir()
	env, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "prod", Region: "eu-west-1"}}}, stage)
	if err != nil {
		t.Fatalf("StageKube: %v", err)
	}
	kcPath := filepath.Join(stage, "kubeconfig")
	if got := strings.Join(env, " "); got != "KUBECONFIG="+kcPath {
		t.Fatalf("env = %v", env)
	}
	fi, err := os.Stat(kcPath)
	if err != nil {
		t.Fatalf("kubeconfig not staged: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("kubeconfig perm = %v, want 0600", fi.Mode().Perm())
	}
	body, _ := os.ReadFile(kcPath)
	for _, want := range []string{`"server": "https://EKS.example"`, `"token": "k8s-aws-v1.TOK"`, `"certificate-authority-data": "Q0FEQVRB"`, `"current-context": "eks:prod"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("kubeconfig missing %q:\n%s", want, body)
		}
	}
}

func TestStageKubeGke(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "gke-gcloud-auth-plugin", `{"kind":"ExecCredential","status":{"token":"ya29.TOK"}}`) // fakeBin from aws_test.go
	fakeBin(t, binDir, "gcloud", `{"endpoint":"34.79.12.34","masterAuth":{"clusterCaCertificate":"Q0FEQVRB"}}`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	stage := t.TempDir()
	env, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Gke: &policy.GkeCluster{Name: "prod", Location: "europe-west1"}}}, stage)
	if err != nil {
		t.Fatalf("StageKube: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(stage, "kubeconfig"))
	for _, want := range []string{`"server": "https://34.79.12.34"`, `"token": "ya29.TOK"`, `"current-context": "gke:prod"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("kubeconfig missing %q:\n%s", want, body)
		}
	}
	_ = env
}

func TestStageKubeNilIsNoop(t *testing.T) {
	env, err := StageKube(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil kube creds must be a no-op: env=%v err=%v", env, err)
	}
}

func TestStageKubeRequiresExactlyOne(t *testing.T) {
	// both set
	if _, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "a"}, Gke: &policy.GkeCluster{Name: "b"}}}, t.TempDir()); err == nil {
		t.Fatal("expected error when both eks and gke set")
	}
	// neither set
	if _, err := StageKube(context.Background(), &policy.Credentials{Kube: &policy.KubeCluster{}}, t.TempDir()); err == nil {
		t.Fatal("expected error when neither eks nor gke set")
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/creds/ -run TestStageKube -v
```
Expected: FAIL — `StageKube` undefined.

- [ ] **Step 3: Write `StageKube`.** In `internal/engine/creds/kube.go`, extend the Task-2 import block to add the three imports `StageKube` needs:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/freakhill/safeslop/internal/engine/policy"
)
```

Then append:

```go
// StageKube pre-mints a short-lived k8s bearer token on the host (aws eks get-token /
// gke-gcloud-auth-plugin, using the host's SSO/ADC), resolves the cluster endpoint+CA,
// and writes a scoped one-cluster kubeconfig (token inside, 0600) into stageDir. It
// returns KUBECONFIG pointing at that file — the host path, correct for host/sandbox;
// the container path is set in the compose env (see container.Launch). No revoke: the
// token decays and the stageDir wipe removes the file (decay-first).
func StageKube(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Kube == nil {
		return nil, nil
	}
	k := creds.Kube
	if (k.Eks == nil) == (k.Gke == nil) {
		return nil, fmt.Errorf("kube credentials: set exactly one of eks/gke")
	}

	var server, caData, token, ctxName string
	switch {
	case k.Eks != nil:
		tOut, err := runKubeCmd(ctx, eksGetTokenArgv(k.Eks.Name, k.Eks.Region, k.Eks.Profile), "is `aws sso login` current?")
		if err != nil {
			return nil, err
		}
		if token, err = parseExecToken(tOut); err != nil {
			return nil, err
		}
		dOut, err := runKubeCmd(ctx, eksDescribeArgv(k.Eks.Name, k.Eks.Region, k.Eks.Profile), "can the SSO profile describe the cluster?")
		if err != nil {
			return nil, err
		}
		if server, caData, err = parseEksDescribe(dOut); err != nil {
			return nil, err
		}
		ctxName = "eks:" + k.Eks.Name
	case k.Gke != nil:
		tOut, err := runKubeCmd(ctx, gkeTokenArgv(), "is ADC set up? run: gcloud auth application-default login")
		if err != nil {
			return nil, err
		}
		if token, err = parseExecToken(tOut); err != nil {
			return nil, err
		}
		dOut, err := runKubeCmd(ctx, gkeDescribeArgv(k.Gke.Name, k.Gke.Location, k.Gke.Project), "can gcloud describe the cluster?")
		if err != nil {
			return nil, err
		}
		if server, caData, err = parseGkeDescribe(dOut); err != nil {
			return nil, err
		}
		ctxName = "gke:" + k.Gke.Name
	}

	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	kcPath := filepath.Join(stageDir, "kubeconfig")
	if err := os.WriteFile(kcPath, renderKubeconfig(ctxName, server, caData, token), 0o600); err != nil {
		return nil, err
	}
	return []string{"KUBECONFIG=" + kcPath}, nil
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/creds/ -run TestStageKube -v && go test ./internal/engine/creds/ -v
```
Expected: PASS (StageKube tests + the whole `creds` package still green).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/creds/kube.go internal/engine/creds/kube_test.go
git commit -m "feat(creds): StageKube — host-minted k8s token, scoped 0600 kubeconfig, decay-first"
```

---

### Task 4: container — deliver `KUBECONFIG` at the bind-mount path

**Files:**
- Modify: `internal/engine/container/compose.go:13-20` (composeParams)
- Modify: `internal/engine/container/assets/compose.yml.tmpl:13-18` (env block)
- Modify: `internal/engine/container/launch.go` (detect staged kubeconfig)
- Test: `internal/engine/container/compose_test.go`

- [ ] **Step 1: Write the failing test** — add to `internal/engine/container/compose_test.go`:

```go
func TestRenderComposeKubeconfig(t *testing.T) {
	with, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", Kubeconfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with, "KUBECONFIG: /slop/runtime/kubeconfig") {
		t.Fatalf("compose missing KUBECONFIG env:\n%s", with)
	}
	without, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", Kubeconfig: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "KUBECONFIG") {
		t.Fatalf("KUBECONFIG must be absent when no kubeconfig staged:\n%s", without)
	}
}
```

> If `compose_test.go` does not already import `strings`, add it.

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/container/ -run TestRenderComposeKubeconfig -v
```
Expected: FAIL — `Kubeconfig` field unknown on `composeParams`.

- [ ] **Step 3: Add the field, template line, and detection.**

In `internal/engine/container/compose.go`, add to `composeParams` (after `NpmConfig`):

```go
	NpmConfig   bool // true when a staged .npmrc exists
	Kubeconfig  bool // true when a staged kubeconfig exists (KUBECONFIG -> bind-mount path)
```

In `internal/engine/container/assets/compose.yml.tmpl`, in the `environment:` block (next to the existing `{{if .NpmConfig}}…{{end}}`), add:

```
{{if .Kubeconfig}}      KUBECONFIG: /slop/runtime/kubeconfig
{{end}}
```

> Match the existing indentation/newline style exactly — the `.npmrc` line is the template to copy. The bind mount `{{.StageDir}}:/slop/runtime:ro` already exposes the staged file; no new volume needed.

In `internal/engine/container/launch.go`, where it stats `.npmrc` (around line 72), add a parallel stat and set the param. Current:

```go
	_, npmErr := os.Stat(filepath.Join(stageDir, ".npmrc"))
```

Add immediately after:

```go
	_, kubeErr := os.Stat(filepath.Join(stageDir, "kubeconfig"))
```

and in the `composeParams{…}` literal that follows, set:

```go
		Kubeconfig:  kubeErr == nil,
```

(next to the existing `NpmConfig: npmErr == nil`).

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/container/ -run TestRenderComposeKubeconfig -v && go test ./internal/engine/container/ -v
```
Expected: PASS (new test + container package green).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/container/compose.go internal/engine/container/assets/compose.yml.tmpl internal/engine/container/launch.go internal/engine/container/compose_test.go
git commit -m "feat(container): set KUBECONFIG to the /slop/runtime bind-mount path when staged"
```

---

### Task 5: wire `StageKube` into the run lifecycle + vm guard + doctor

**Files:**
- Modify: `internal/cli/cli.go` (runProfile: ~305-340; doctorReport: 126)
- Test: `internal/cli/cli_test.go`

- [ ] **Step 1: Write the failing test** — add to `internal/cli/cli_test.go`:

```go
func TestDoctorReportsGkeAuthPlugin(t *testing.T) {
	report := doctorReport()
	if _, ok := report["gke-gcloud-auth-plugin"]; !ok {
		t.Fatalf("doctor must probe gke-gcloud-auth-plugin: %v keys", report)
	}
}

func TestRunProfileKubeVMGuarded(t *testing.T) {
	prof := policy.Profile{
		Agent:       "claude",
		Environment: "vm",
		Network:     "allow",
		Credentials: &policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "prod"}}},
	}
	_, err := runProfile("deploy", prof, []string{"claude"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "vm") {
		t.Fatalf("expected a vm-unsupported guard error for kube creds, got: %v", err)
	}
}
```

> Confirm `cli_test.go` imports `policy` and `strings`; add them if missing. `runProfile`'s signature is `runProfile(name string, prof policy.Profile, argv []string, ws string) (int, error)`.

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/cli/ -run 'TestDoctorReportsGkeAuthPlugin|TestRunProfileKubeVMGuarded' -v
```
Expected: FAIL — `gke-gcloud-auth-plugin` not in report; vm guard not present (the vm branch would try to launch).

- [ ] **Step 3: Wire it in.**

In `internal/cli/cli.go` `doctorReport` (line 126), add `gke-gcloud-auth-plugin`:

```go
	tools := []string{"git", "docker", "op", "claude", "opencode", "tart", "mise", "nix", "aws", "gcloud", "gke-gcloud-auth-plugin"}
```

In `runProfile`, add the vm guard **at the top, before any staging** — it must fire before `StageKube` shells out, otherwise a staging failure would mask it. Insert it right after `defer os.RemoveAll(stageDir)` (currently line 289):

```go
	// kube creds need a kubeconfig at a boundary-stable path; vm's scp'd stage path
	// (unknown guest $HOME, single-quoted secrets.env) isn't wired yet. Fail fast,
	// before minting any token (specs/0010).
	if prof.Environment == "vm" && prof.Credentials != nil && prof.Credentials.Kube != nil {
		return 1, fmt.Errorf("kube credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0010)", prof.Environment)
	}
```

Then, after the `gcpEnv` staging block (currently ends ~line 322 with `secretEnv = append(secretEnv, gcpEnv...)`), add the kube staging — note it is **kept out of `secretEnv`**, exactly like `npmrcEnv`, because `KUBECONFIG` is a non-secret path and the container path differs from the host path:

```go
	// kubeconfig (with the bearer token inside) is staged 0600; KUBECONFIG is a
	// non-secret path delivered per-environment like .npmrc/NPM_CONFIG_USERCONFIG —
	// host path for host/sandbox; /slop/runtime/kubeconfig via compose for container.
	kubeEnv, err := creds.StageKube(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
```

Then thread `kubeEnv` into the host/sandbox branches and guard vm. Replace the `switch prof.Environment` cases:

```go
	switch prof.Environment {
	case "sandbox":
		env := append(append(append(os.Environ(), secretEnv...), npmrcEnv...), kubeEnv...)
		return sandbox.Launch(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env}, ws, prof.Network)
	case "host":
		env := append(append(append(os.Environ(), secretEnv...), npmrcEnv...), kubeEnv...)
		return engexec.RunInTerminal(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env})
	case "container":
		// secrets go in secrets.env; .npmrc and kubeconfig are staged in stageDir and
		// reached via the /slop/runtime bind mount (KUBECONFIG set in the compose env).
		return container.Launch(ctx, engexec.LaunchSpec{Argv: argv}, ws, prof.Network, secretEnv, stageDir)
	case "vm":
		// kube+vm is rejected by the fast-fail guard at the top of runProfile.
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		return vm.Launch(ctx, argv, prof.Network, secretEnv, stageDir, name, tk)
	default:
		return 1, fmt.Errorf("unknown environment %q", prof.Environment)
	}
```

> `kubeEnv` is `nil` for non-kube profiles, so `append(..., kubeEnv...)` is a no-op there. The vm guard fires *before* `vm.Launch`, so no VM is booted for a misconfigured kube+vm profile.

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/cli/ -run 'TestDoctorReportsGkeAuthPlugin|TestRunProfileKubeVMGuarded' -v && go test ./internal/cli/ -v
```
Expected: PASS (new tests + cli package green).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat(cli): stage kube creds in the run lifecycle; vm guarded; doctor probes gke plugin"
```

---

### Task 6: leak/decoy pins — kubeconfig stays 0600 and out of `secrets.env`

**Files:**
- Test: `internal/engine/creds/kube_test.go` (add) and `internal/engine/container/launch_test.go` (add, if the package has a launch test; else add to `compose_test.go`)

- [ ] **Step 1: Write the failing test** — append to `internal/engine/creds/kube_test.go` a test that the bearer token is never returned as a plain env var (it must live only in the file):

```go
func TestStageKubeTokenNotInEnv(t *testing.T) {
	binDir := t.TempDir()
	fakeMultiBin(t, binDir, "aws", map[string]string{
		"get-token":        `{"kind":"ExecCredential","status":{"token":"k8s-aws-v1.SECRET"}}`,
		"describe-cluster": `{"cluster":{"endpoint":"https://e","certificateAuthority":{"data":"Q0E"}}}`,
	})
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	env, err := StageKube(context.Background(),
		&policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "prod"}}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// the only env entry is the KUBECONFIG path; the token must never be an env value.
	for _, kv := range env {
		if strings.Contains(kv, "SECRET") {
			t.Fatalf("bearer token leaked into env: %q", kv)
		}
	}
	if len(env) != 1 || !strings.HasPrefix(env[0], "KUBECONFIG=") {
		t.Fatalf("env must be exactly [KUBECONFIG=...]: %v", env)
	}
}
```

- [ ] **Step 2: Run the test, verify it passes** (this asserts behavior already built in Tasks 3–5; it is a regression pin)

```bash
go test ./internal/engine/creds/ -run TestStageKubeTokenNotInEnv -v
```
Expected: PASS. (If it fails, the wiring regressed — fix before continuing.)

- [ ] **Step 3: Pin that the container never writes KUBECONFIG into secrets.env.** Because `kubeEnv` is not part of `secretEnv` (Task 5) and the container sets `KUBECONFIG` via the compose env, `secrets.env` must not contain a `KUBECONFIG` line. Add to `internal/engine/container/compose_test.go`:

```go
func TestSecretsEnvExcludesKubeconfig(t *testing.T) {
	// secretEnv carries only true secrets; KUBECONFIG (a path) is never in it.
	dir := t.TempDir()
	if _, err := writeSecretsEnv(dir, []string{"AWS_ACCESS_KEY_ID=AKIA"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "secrets.env"))
	if strings.Contains(string(body), "KUBECONFIG") {
		t.Fatalf("KUBECONFIG must never ride secrets.env:\n%s", body)
	}
}
```

> Add `os`/`path/filepath`/`strings` imports to `compose_test.go` if absent.

- [ ] **Step 4: Run the tests, verify they pass**

```bash
go test ./internal/engine/creds/ ./internal/engine/container/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/creds/kube_test.go internal/engine/container/compose_test.go
git commit -m "test(creds): pin kube token stays in 0600 file, never in env/secrets.env"
```

---

### Task 7: lint pin (kube trips open-egress) — **requires #12 merged**

**Files:**
- Test: `internal/cli/cli_lint_test.go` (modify) or `internal/engine/policy/lint_test.go`

> Skip this task if building off `aws-gcp-credentials` before #12 merges; add it after rebasing onto post-#12 `main`. No production code change — the lint already keys on `p.Credentials != nil`.

- [ ] **Step 1: Write the test** — add a case asserting a sandbox + network:allow + kube profile yields the `sandbox-open-egress-with-creds` warning. Mirror the existing aws/gcp case in `lint_test.go`:

```go
func TestLintKubeTripsOpenEgress(t *testing.T) {
	cfg := &policy.Config{Profiles: map[string]policy.Profile{
		"deploy": {
			Agent:       "claude",
			Environment: "sandbox",
			Network:     "allow",
			Credentials: &policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "prod"}}},
		},
	}}
	ws := policy.Lint(cfg)
	found := false
	for _, w := range ws {
		if w.Code == "sandbox-open-egress-with-creds" {
			found = true
		}
	}
	if !found {
		t.Fatalf("kube creds under sandbox+allow must trip open-egress lint: %+v", ws)
	}
}
```

> Match the actual `policy.Config`/`policy.Warning` field names in #12's `lint.go` (verify with `grep -n 'type Warning\|type Config' internal/engine/policy/*.go`).

- [ ] **Step 2: Run the test, verify it passes**

```bash
go test ./internal/engine/policy/ -run TestLintKubeTripsOpenEgress -v
```
Expected: PASS (no code change needed — generic `Credentials != nil` already covers it).

- [ ] **Step 3: Commit**

```bash
git add internal/engine/policy/lint_test.go
git commit -m "test(policy): kube creds trip the sandbox-open-egress lint"
```

---

### Task 8: docs, schema sample, §7.5 record, verify, PR

**Files:**
- Modify: `library/layer/policy/samples/slop/slop.cue`
- Modify: `README.md` (prose only, outside AUTOGEN markers)
- Modify: `specs/0001-go-rewrite-design.md:204-206`

- [ ] **Step 1: Add a `kube` sample profile.** In `library/layer/policy/samples/slop/slop.cue`, add inside `profiles:` (mirror the existing block style; no `:latest` anywhere):

```cue
	// "deploy" — a container session pre-authenticated to one EKS cluster. The
	// host mints a short-lived k8s token from your SSO profile and stages a scoped
	// kubeconfig; the agent's kubectl works with no AWS CLI/creds in the container.
	// Run `aws sso login --profile dev-admin` on the host first.
	"deploy": schema.#Profile & {
		agent:       "claude"
		environment: "container"
		network:     "deny"
		credentials: kube: eks: {name: "prod", region: "eu-west-1", profile: "dev-admin"}
	}
```

- [ ] **Step 2: Document the provider in `README.md`.** Find the AWS/GCP credentials section (search `credentials: {aws` or `CLOUDSDK_AUTH_ACCESS_TOKEN`) and add, **outside any `<!-- AUTOGEN -->`…`<!-- /AUTOGEN -->` block**, a `kube` subsection:

```markdown
#### Kubernetes (EKS / GKE) — `credentials: {kube: …}`

Pre-authenticate one cluster so the agent's `kubectl` works with **no cloud CLI or
cloud credentials inside the boundary**. On the host, `slop` mints a short-lived
bearer token (`aws eks get-token` for EKS, `gke-gcloud-auth-plugin` for GKE — using
your existing SSO/ADC) and stages a scoped one-cluster kubeconfig (mode 0600) into the
run's stage dir, exposed via `KUBECONFIG`. The token lives only inside that file and is
wiped with the run on exit. Decay-first: the token expires on its own; there is no
revoke step.

```cue
// EKS — run `aws sso login --profile dev-admin` first
credentials: kube: eks: {name: "prod", region: "eu-west-1", profile: "dev-admin"}

// GKE — run `gcloud auth application-default login` first
credentials: kube: gke: {name: "prod", location: "europe-west1", project: "acme-prod"}
```

Supported in `host`, `sandbox`, and `container` environments. `environment: vm` is not
yet supported for `kube` (use `container`).
```

- [ ] **Step 3: Mark §7.5 done.** In `specs/0001-go-rewrite-design.md`, replace the kubectl bullet (lines 204-206) with:

```markdown
- **kubectl** (EKS/GKE) is implemented as a composing provider (specs/0010): the host
  pre-mints a short-lived k8s bearer token (`aws eks get-token` / `gke-gcloud-auth-plugin`)
  and stages a scoped one-cluster kubeconfig (token inside, 0600), so the agent's `kubectl`
  needs neither cloud creds nor the cloud CLI inside the boundary. `KUBECONFIG` rides the
  per-environment path channel like `.npmrc` (host path for host/sandbox; `/slop/runtime`
  bind mount for container). vm support is deferred.
```

- [ ] **Step 4: Full verification — all gates green.**

```bash
make check && make build
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: `make check`/`make build` pass; the four fish gates pass. (`slop-pinning` scans the new sample `*.cue` — there are no `:latest`/`@latest`/`==latest` tokens in it. `slop-sync-help` only diffs README AUTOGEN blocks against `--help`, which are untouched.)

- [ ] **Step 5: Commit + open PR.**

```bash
git add library/layer/policy/samples/slop/slop.cue README.md specs/0001-go-rewrite-design.md
git commit -m "docs(creds): document the kube (EKS/GKE) provider; record specs/0001 §7.5 done"
git push -u origin kube-credentials
gh pr create --title "kube (EKS/GKE) credential provider — decay-first" --body "$(cat <<'EOF'
## Summary
Adds a `kube` credential provider: a profile declares one EKS or GKE cluster, and the
host pre-mints a short-lived k8s bearer token (`aws eks get-token` / `gke-gcloud-auth-plugin`,
using the host SSO/ADC) and stages a scoped one-cluster kubeconfig (token inside, 0600) into
the run stage dir. The agent's `kubectl` works inside the boundary with **no cloud creds or
cloud CLI**. Decay-first: token expires; cleanup is the stageDir wipe (no revoke).

`KUBECONFIG` is delivered per-environment like `.npmrc`/`NPM_CONFIG_USERCONFIG`: host path for
host/sandbox; `/slop/runtime/kubeconfig` via the compose env for container. `environment: vm`
is guarded with a clear error (deferred).

## Changes
- schema `#KubeCluster`/`#EksCluster`/`#GkeCluster` + `Credentials.Kube`.
- `internal/engine/creds/kube.go`: pure core (argv, ExecCredential/describe parse, JSON
  kubeconfig render) + `StageKube` (PATH-mocked tests, EKS + GKE).
- container: `KUBECONFIG` set to the bind-mount path when a kubeconfig is staged.
- cli: wired into `runProfile`; vm guard; `slop doctor` probes `gke-gcloud-auth-plugin`.
- leak pins: token stays in the 0600 file, never in env/`secrets.env`.
- docs: README provider section, sample profile, specs/0001 §7.5 recorded, specs/0010 plan.

## Test
`make check && make build` green; all four fish gates green.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` (`go vet` + `gofmt` + `go test ./...`) and `make build` both green.
- `fish -n scripts/*.fish`, `fish tests/run.fish`, `fish scripts/slop-sync-help.fish check`, `fish scripts/slop-pinning.fish` all green.
- A `credentials: {kube: {eks|gke: …}}` profile parses, stages a 0600 `kubeconfig` with the right `server`/`certificate-authority-data`/`token`/`current-context`, and exposes `KUBECONFIG` at the boundary-correct path (host path for host/sandbox; `/slop/runtime/kubeconfig` for container).
- The bearer token never appears in `env`, `secrets.env`, `docker inspect`, or `ps`.
- `kube` + `environment: vm` returns a clear guard error instead of launching.
- `slop doctor` reports `gke-gcloud-auth-plugin`.

## Deliberately deferred (not here)

- **vm support** for `kube` (guarded). Needs a pinned guest `$HOME` so `KUBECONFIG` can point at the scp'd stage path.
- **Multi-cluster** kubeconfigs (one cluster per profile in v1).
- **GKE via raw ADC token** instead of `gke-gcloud-auth-plugin` (the plugin is canonical and shares the EKS parser).
- **Keyless WIF/OIDC** federation (schema shape reserved, per specs/0001 §7.5).
