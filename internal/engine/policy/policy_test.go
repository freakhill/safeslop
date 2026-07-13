package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadStr writes src to a temp safeslop.cue and Loads it (the package's load path is file-based).
func loadStr(t *testing.T, src string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "safeslop.cue")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadValidAppliesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "valid.cue"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1 (schema default)", cfg.Version)
	}
	dev, ok := cfg.Profiles["dev"]
	if !ok {
		t.Fatal("missing profile 'dev'")
	}
	if dev.Agent != "shell" {
		t.Errorf("dev.agent = %q, want shell", dev.Agent)
	}
	if dev.Environment != "host" {
		t.Errorf("dev.environment = %q, want host", dev.Environment)
	}
	if dev.Network != "deny" {
		t.Errorf("dev.network = %q, want deny (schema default)", dev.Network)
	}
	if got := cfg.Profiles["review"].Agent; got != "claude" {
		t.Errorf("review.agent = %q, want claude", got)
	}
}

func TestLoadAcceptsClaudeCodeAlias(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: review: {agent: "claude-code", environment: "host"}`)
	if err != nil {
		t.Fatalf("Load claude-code alias: %v", err)
	}
	if got := cfg.Profiles["review"].Agent; got != "claude" {
		t.Fatalf("agent = %q, want canonical claude", got)
	}
}

func TestLoadRejectsUnknownAgent(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "invalid_agent.cue")); err == nil {
		t.Fatal("expected a validation error for an unknown agent")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "does-not-exist.cue")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestLoadDecodesSecretsAndCredentials(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "with_creds.cue"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	work, ok := cfg.Profiles["work"]
	if !ok {
		t.Fatal("missing profile 'work'")
	}
	if got := work.Secrets["ANTHROPIC_API_KEY"]; got != "op://dev/anthropic/key" {
		t.Errorf("secret ANTHROPIC_API_KEY = %q", got)
	}
	if got := work.Secrets["FOO"]; got != "env:FOO_SRC" {
		t.Errorf("secret FOO = %q", got)
	}
	if work.Credentials == nil || len(work.Credentials.Pnpm) != 2 {
		t.Fatalf("expected 2 pnpm registries, got %+v", work.Credentials)
	}
	gh := work.Credentials.Pnpm[1]
	if gh.Host != "npm.pkg.github.com" || gh.Token != "env:GH_NPM_TOKEN" || gh.Scope != "@myorg" {
		t.Errorf("pnpm[1] = %+v", gh)
	}
	// Multi-repo github credential decodes through the embedded schema (specs/0047 P2, specs/0069).
	if work.Credentials.Github == nil || len(work.Credentials.Github.Repos) != 2 {
		t.Fatalf("expected 2 github repos, got %+v", work.Credentials.Github)
	}
	if r := work.Credentials.Github.Repos; r[0].Repo != "acme/web" || r[1].Repo != "acme/api" || r[0].Write || !r[1].Write {
		t.Errorf("github repos = %+v", r)
	}
}

func TestLoadRejectsBadSecretRef(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "bad_secretref.cue")); err == nil {
		t.Fatal("expected validation error for a non-op://, non-env: secret ref")
	}
}

func TestToolchainDecodes(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {agent: "claude", environment: "host", toolchain: {kind: "mise", run: "build"}}`)
	if err != nil {
		t.Fatal(err)
	}
	tc := cfg.Profiles["dev"].Toolchain
	if tc == nil || tc.Kind != "mise" || tc.Run != "build" {
		t.Fatalf("toolchain decoded wrong: %+v", tc)
	}
}

func TestLoadRejectsUserAuthoredProjection(t *testing.T) {
	// specs/0096 T1 FLO verdict: projection is engine-owned in MVP. A safeslop.cue that sets a
	// non-empty projection is rejected at load with a spec-cited error; only embedded builtins
	// (which populate the Go struct directly, bypassing LoadBytes) may carry projection.
	_, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {
	agent: "pi", environment: "container",
	projection: {enabled: true, items: [{source: "~/.zshrc"}]}
}`)
	if err == nil {
		t.Fatal("expected user-authored projection to be rejected in MVP")
	}
	if !strings.Contains(err.Error(), "projection") || !strings.Contains(err.Error(), "0096") {
		t.Errorf("error must cite projection + specs/0096 (engine-owned), got:\n%s", err.Error())
	}
}

func TestLoadRejectsMalformedProjectionKind(t *testing.T) {
	// Even though projection is engine-owned, a structurally invalid projection must fail schema
	// validation (the field is typed in the schema), not silently decode.
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {
	agent: "pi", environment: "container",
	projection: {enabled: true, items: [{source: "x", kind: "weird"}]}
}`); err == nil {
		t.Fatal("expected schema rejection of an invalid projection kind")
	}
}

func TestToolchainRejectsBadKind(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {agent: "claude", environment: "host", toolchain: {kind: "cargo"}}`); err == nil {
		t.Fatal("expected validation error for kind \"cargo\"")
	}
}

func TestLoadDecodesAwsGcpCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: cloud: {
	agent: "claude"
	environment: "container"
	credentials: {aws: {profile: "dev-admin", region: "eu-west-1"}, gcp: {}}
}`)
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

func TestLoadKubeEksCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {
	agent: "claude"
	environment: "container"
	credentials: kube: eks: {name: "prod", region: "eu-west-1", profile: "dev-admin"}
}`)
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
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {
	agent: "claude"
	environment: "container"
	credentials: kube: gke: {name: "prod", location: "europe-west1", project: "acme-prod"}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	g := cfg.Profiles["deploy"].Credentials.Kube.Gke
	if g == nil || g.Name != "prod" || g.Location != "europe-west1" || g.Project != "acme-prod" {
		t.Fatalf("gke fields = %+v", g)
	}
}

func TestLoadGithubCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {
	agent: "claude"
	environment: "container"
	network: "deny"
	credentials: github: {write: true, ttl: "30m"}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Profiles["deploy"].Credentials.Github
	if s == nil || !s.Write || s.Ttl != "30m" {
		t.Fatalf("github creds = %+v", s)
	}
}

func TestLoadDecodesPATCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {
	agent: "claude"
	environment: "container"
	network: "deny"
	credentials: {
		github: {mode: "pat", pat: "env:GITHUB_PAT", repos: [{repo: "acme/web"}, {repo: "acme/api"}]}
	}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Profiles["deploy"].Credentials
	if c.Github.Mode != "pat" || c.Github.Pat != "env:GITHUB_PAT" || len(c.Github.Repos) != 2 {
		t.Fatalf("github PAT creds = %+v", c.Github)
	}
}

// The forgejo token/mode/pat fields were removed in specs/0069 (token moved to accounts.cue); the
// closed schema must reject them.
func TestLoadRejectsRemovedForgejoFields(t *testing.T) {
	for _, field := range []string{
		`forgejo: {token: "env:T"}`,
		`forgejo: {mode: "deploy-key", token: "env:T"}`,
		`forgejo: {pat: "env:T"}`,
	} {
		if _, err := loadStr(t, "package safeslop\nsafeslop: profiles: deploy: {agent: \"claude\", credentials: "+field+"}"); err == nil {
			t.Fatalf("removed forgejo field must be rejected: %s", field)
		}
	}
}

func TestLoadRejectsBadCredentialMode(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: github: {mode: "oauth"}}`); err == nil {
		t.Fatal("expected bad github mode to fail validation")
	}
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: forgejo: {mode: "oauth", token: "env:T"}}`); err == nil {
		t.Fatal("expected bad forgejo mode to fail validation")
	}
}

func TestLoadRejectsIncompletePATCredentials(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: github: {mode: "pat", repos: [{repo: "acme/web"}]}}`); err == nil {
		t.Fatal("expected github PAT mode without pat to fail validation")
	}
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: github: {mode: "pat", pat: "env:GITHUB_PAT"}}`); err == nil {
		t.Fatal("expected github PAT mode without repos to fail validation")
	}
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: forgejo: {mode: "deploy-key", url: "https://codeberg.org"}}`); err == nil {
		t.Fatal("expected forgejo deploy-key mode without token to fail validation")
	}
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: forgejo: {mode: "pat", pat: "env:FJ", repos: [{repo: "acme/web"}]}}`); err == nil {
		t.Fatal("expected forgejo PAT mode without url to fail validation")
	}
}

func TestLoadGithubDefaultsReadOnly(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: review: {
	agent: "claude"
	environment: "container"
	credentials: github: {}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Profiles["review"].Credentials.Github
	if s == nil || s.Write {
		t.Fatalf("github write must default false: %+v", s)
	}
}

func TestLoadRejectsGithubDeployKeyMode(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: github: {mode: "deploy-key"}}`); err == nil {
		t.Fatal("expected github mode:deploy-key to be rejected (removed in specs/0069)")
	}
}

func TestLoadRejectsForgejoApiWithoutAck(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", environment: "container", network: "deny", credentials: forgejo: {api: {enabled: true}}}`); err == nil {
		t.Fatal("expected forgejo api.enabled without ackAccountWide to be rejected (specs/0068 F5)")
	}
}

func TestLoadAcceptsForgejoApiWithAck(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", environment: "container", network: "deny", credentials: forgejo: {api: {enabled: true, ackAccountWide: true}}}`); err != nil {
		t.Fatalf("forgejo api with ack must load: %v", err)
	}
}

func TestLoadRejectsLegacySshWithHint(t *testing.T) {
	_, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", environment: "container", credentials: ssh: {write: true}}`)
	if err == nil {
		t.Fatal("expected legacy credentials.ssh to be rejected")
	}
	if !strings.Contains(err.Error(), "renamed to credentials.github") {
		t.Fatalf("expected specs/0069 rename hint, got: %v", err)
	}
}

func TestEnvTier(t *testing.T) {
	cases := map[string]string{
		"host":      "none",
		"":          "none", // environment is required (specs/0053); empty/unknown implies no boundary
		"container": "egress-allowlisted",
	}
	for env, wantTier := range cases {
		tier, note := EnvTier(env)
		if tier != wantTier {
			t.Errorf("EnvTier(%q) tier = %q, want %q", env, tier, wantTier)
		}
		if env != "host" && note == "" {
			t.Errorf("EnvTier(%q) must carry an honest note", env)
		}
	}
	if _, note := EnvTier("host"); note == "" {
		t.Error("host tier must still carry a note (no isolation)")
	}
}

func TestIsLaunchableAgent(t *testing.T) {
	for _, a := range []string{"claude", "pi", "fish", "zsh"} {
		if !IsLaunchableAgent(a) {
			t.Errorf("IsLaunchableAgent(%q) = false, want true", a)
		}
	}
	// "shell" is a profile-only value (handled by agentArgv) but not
	// session-creatable; "claude-code" must be normalized to "claude" before this
	// gate; everything else is unsupported.
	for _, a := range []string{"shell", "claude-code", "cursor", "notanagent", ""} {
		if IsLaunchableAgent(a) {
			t.Errorf("IsLaunchableAgent(%q) = true, want false", a)
		}
	}
}
