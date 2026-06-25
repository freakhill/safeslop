package policy

import (
	"os"
	"path/filepath"
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
	if dev.Environment != "sandbox" {
		t.Errorf("dev.environment = %q, want sandbox (schema default)", dev.Environment)
	}
	if dev.Network != "deny" {
		t.Errorf("dev.network = %q, want deny (schema default)", dev.Network)
	}
	if got := cfg.Profiles["review"].Agent; got != "claude" {
		t.Errorf("review.agent = %q, want claude", got)
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
	// Multi-repo ssh credential decodes through the embedded schema (specs/0047 P2).
	if work.Credentials.Ssh == nil || len(work.Credentials.Ssh.Repos) != 2 {
		t.Fatalf("expected 2 ssh repos, got %+v", work.Credentials.Ssh)
	}
	if r := work.Credentials.Ssh.Repos; r[0].Repo != "acme/web" || r[1].Repo != "acme/api" || r[0].Write || !r[1].Write {
		t.Errorf("ssh repos = %+v", r)
	}
}

func TestLoadRejectsBadSecretRef(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "bad_secretref.cue")); err == nil {
		t.Fatal("expected validation error for a non-op://, non-env: secret ref")
	}
}

func TestToolchainDecodes(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {agent: "claude", toolchain: {kind: "mise", run: "build"}}`)
	if err != nil {
		t.Fatal(err)
	}
	tc := cfg.Profiles["dev"].Toolchain
	if tc == nil || tc.Kind != "mise" || tc.Run != "build" {
		t.Fatalf("toolchain decoded wrong: %+v", tc)
	}
}

func TestToolchainRejectsBadKind(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {agent: "claude", toolchain: {kind: "cargo"}}`); err == nil {
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

func TestLoadSshCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {
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

func TestLoadDecodesPATCredentials(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {
	agent: "claude"
	environment: "container"
	network: "deny"
	credentials: {
		ssh: {mode: "pat", pat: "env:GITHUB_PAT", repos: [{repo: "acme/web"}, {repo: "acme/api"}]}
		forgejo: {mode: "pat", url: "https://codeberg.org", pat: "env:CODEBERG_PAT", token: "env:CODEBERG_ADMIN", repos: [{repo: "jojo/web"}]}
	}
}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Profiles["deploy"].Credentials
	if c.Ssh.Mode != "pat" || c.Ssh.Pat != "env:GITHUB_PAT" || len(c.Ssh.Repos) != 2 {
		t.Fatalf("ssh PAT creds = %+v", c.Ssh)
	}
	if c.Forgejo.Mode != "pat" || c.Forgejo.Pat != "env:CODEBERG_PAT" || c.Forgejo.URL != "https://codeberg.org" {
		t.Fatalf("forgejo PAT creds = %+v", c.Forgejo)
	}
}

func TestLoadRejectsBadCredentialMode(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: ssh: {mode: "oauth"}}`); err == nil {
		t.Fatal("expected bad ssh mode to fail validation")
	}
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: forgejo: {mode: "oauth", token: "env:T"}}`); err == nil {
		t.Fatal("expected bad forgejo mode to fail validation")
	}
}

func TestLoadRejectsIncompletePATCredentials(t *testing.T) {
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: ssh: {mode: "pat", repos: [{repo: "acme/web"}]}}`); err == nil {
		t.Fatal("expected ssh PAT mode without pat to fail validation")
	}
	if _, err := loadStr(t, `package safeslop
safeslop: profiles: deploy: {agent: "claude", credentials: ssh: {mode: "pat", pat: "env:GITHUB_PAT"}}`); err == nil {
		t.Fatal("expected ssh PAT mode without repos to fail validation")
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

func TestLoadSshDefaultsReadOnly(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: review: {
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

func TestEnvTier(t *testing.T) {
	cases := map[string]string{
		"host":      "none",
		"sandbox":   "mistake-guard",
		"":          "mistake-guard", // unspecified defaults to sandbox
		"container": "egress-allowlisted",
		"vm":        "adversary-grade",
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
