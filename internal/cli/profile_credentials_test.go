package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

const profileCredentialsCue = `package safeslop

safeslop: {
	version: 1
	profiles: {
		work: {
			agent: "pi"
			environment: "container"
			network: "deny"
			workspace: "project"
			egress: [".internal.example.com"]
			secrets: {ANTHROPIC_API_KEY: "env:ANTHROPIC_API_KEY"}
			bundles: ["go"]
			packages: ["ripgrep"]
			toolchain: {kind: "mise", run: "test"}
			credentials: {
				pnpm: [{host: "npm.pkg.github.com", token: "op://vault/npm/token", scope: "@acme"}]
				aws: {profile: "dev", region: "us-east-1"}
				gcp: {scopes: ["https://www.googleapis.com/auth/devstorage.read_only"]}
				kube: {eks: {name: "prod", region: "eu-west-1"}}
				forgejo: {url: "https://forgejo.example.com", repos: [{repo: "old/repo"}]}
			}
		}
		onlygit: {
			agent: "pi"
			environment: "container"
			network: "deny"
			credentials: {github: {repos: [{repo: "acme/old"}]}}
		}
	}
}
`

func writeProfileCredentialsCue(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(profileCredentialsCue), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadProfileCredentialsConfig(t *testing.T, path string) *policy.Config {
	t.Helper()
	cfg, err := policy.Load(path)
	if err != nil {
		t.Fatalf("load rendered policy: %v", err)
	}
	return cfg
}

func TestProfileCredentialsSetGithubOriginInference(t *testing.T) {
	ws := t.TempDir()
	path := writeProfileCredentialsCue(t, ws)

	out, err := runRootForTest(t, ws, "profile", "credentials", "set", "work", "--provider", "github", "--use-origin", "--output", "json")
	if err != nil {
		t.Fatalf("profile credentials set github origin: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	if got := credentialScopeStringsForTest(t, env.Data); !containsCredentialScope(got, "github origin app ro") {
		t.Fatalf("credential_scopes missing github origin app ro: %v", got)
	}
	cfg := loadProfileCredentialsConfig(t, path)
	prof := cfg.Profiles["work"]
	if prof.Credentials == nil || prof.Credentials.Github == nil {
		t.Fatalf("github creds not written: %+v", prof.Credentials)
	}
	if len(prof.Credentials.Github.Repos) != 0 {
		t.Fatalf("--use-origin must write no repos, got %+v", prof.Credentials.Github.Repos)
	}
	if prof.Credentials.Forgejo != nil {
		t.Fatalf("setting github must clear forgejo only, got %+v", prof.Credentials.Forgejo)
	}
	if prof.Secrets["ANTHROPIC_API_KEY"] != "env:ANTHROPIC_API_KEY" || prof.Toolchain == nil || prof.Toolchain.Run != "test" || len(prof.Egress) != 1 {
		t.Fatalf("unrelated profile fields were not preserved: %+v", prof)
	}
	if prof.Credentials.Pnpm == nil || prof.Credentials.Aws == nil || prof.Credentials.Gcp == nil || prof.Credentials.Kube == nil {
		t.Fatalf("non-forge credential providers were not preserved: %+v", prof.Credentials)
	}
}

func TestProfileCredentialsSetGithubExplicitRepos(t *testing.T) {
	ws := t.TempDir()
	path := writeProfileCredentialsCue(t, ws)

	out, err := runRootForTest(t, ws, "profile", "credentials", "set", "work", "--provider", "github", "--repo", "acme/web", "--write-repo", "acme/api", "--output", "json")
	if err != nil {
		t.Fatalf("profile credentials set github repos: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	got := strings.Join(credentialScopeStringsForTest(t, env.Data), "\n")
	for _, want := range []string{"github acme/web app ro", "github acme/api app rw"} {
		if !strings.Contains(got, want) {
			t.Fatalf("credential_scopes =\n%s\nmissing %s", got, want)
		}
	}
	for _, bad := range []string{"op://vault/npm/token", "env:ANTHROPIC_API_KEY"} {
		if strings.Contains(got, bad) {
			t.Fatalf("credential_scopes leaked ref %q: %s", bad, got)
		}
	}
	cfg := loadProfileCredentialsConfig(t, path)
	repos := cfg.Profiles["work"].Credentials.Github.Repos
	if len(repos) != 2 || repos[0].Repo != "acme/web" || repos[0].Write || repos[1].Repo != "acme/api" || !repos[1].Write {
		t.Fatalf("github repos wrong: %+v", repos)
	}
}

func TestProfileCredentialsSetForgejoExplicitReposRequiresURL(t *testing.T) {
	ws := t.TempDir()
	writeProfileCredentialsCue(t, ws)

	out, err := runRootForTest(t, ws, "profile", "credentials", "set", "work", "--provider", "forgejo", "--repo", "acme/web", "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("expected contract error, got err=%v out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || !strings.Contains(env.Errors[0].Message, "--url") {
		t.Fatalf("expected --url error envelope, got %+v", env)
	}
}

func TestProfileCredentialsSetForgejoExplicitRepos(t *testing.T) {
	ws := t.TempDir()
	path := writeProfileCredentialsCue(t, ws)

	out, err := runRootForTest(t, ws, "profile", "credentials", "set", "work", "--provider", "forgejo", "--url", "https://forgejo.example.com", "--ssh-port", "2222", "--repo", "acme/web", "--write-repo", "acme/api", "--output", "json")
	if err != nil {
		t.Fatalf("profile credentials set forgejo repos: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	got := strings.Join(credentialScopeStringsForTest(t, env.Data), "\n")
	for _, want := range []string{"forgejo acme/web deploy-key ro", "forgejo acme/api deploy-key rw"} {
		if !strings.Contains(got, want) {
			t.Fatalf("credential_scopes =\n%s\nmissing %s", got, want)
		}
	}
	cfg := loadProfileCredentialsConfig(t, path)
	creds := cfg.Profiles["work"].Credentials
	if creds.Github != nil || creds.Forgejo == nil {
		t.Fatalf("forgejo set must clear github and write forgejo: %+v", creds)
	}
	if creds.Forgejo.URL != "https://forgejo.example.com" || creds.Forgejo.SSHPort != 2222 {
		t.Fatalf("forgejo URL/ssh port wrong: %+v", creds.Forgejo)
	}
}

func TestProfileCredentialsClearPreservesOtherProvidersAndDeletesEmptyCredentials(t *testing.T) {
	ws := t.TempDir()
	path := writeProfileCredentialsCue(t, ws)

	out, err := runRootForTest(t, ws, "profile", "credentials", "clear", "work", "--output", "json")
	if err != nil {
		t.Fatalf("profile credentials clear work: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	cfg := loadProfileCredentialsConfig(t, path)
	creds := cfg.Profiles["work"].Credentials
	if creds == nil || creds.Github != nil || creds.Forgejo != nil || creds.Pnpm == nil || creds.Aws == nil || creds.Gcp == nil || creds.Kube == nil {
		t.Fatalf("clear must remove only forge creds from mixed credentials: %+v", creds)
	}
	if cfg.Profiles["work"].Secrets["ANTHROPIC_API_KEY"] != "env:ANTHROPIC_API_KEY" {
		t.Fatalf("clear must preserve secrets: %+v", cfg.Profiles["work"].Secrets)
	}

	out, err = runRootForTest(t, ws, "profile", "credentials", "clear", "onlygit", "--output", "json")
	if err != nil {
		t.Fatalf("profile credentials clear onlygit: %v\nout=%s", err, out)
	}
	env = parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	cfg = loadProfileCredentialsConfig(t, path)
	if cfg.Profiles["onlygit"].Credentials != nil {
		t.Fatalf("empty credentials object should be deleted, got %+v", cfg.Profiles["onlygit"].Credentials)
	}
	if scopes := credentialScopeStringsForTest(t, env.Data); len(scopes) != 0 {
		t.Fatalf("clear-only profile should return credential_scopes: [], got %v", scopes)
	}
}

func TestProfileCredentialsRejectsMalformedAndConflictingRepos(t *testing.T) {
	ws := t.TempDir()
	writeProfileCredentialsCue(t, ws)

	out, err := runRootForTest(t, ws, "profile", "credentials", "set", "work", "--provider", "github", "--repo", "bad/repo/name", "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("expected malformed repo contract error, got err=%v out=%s", err, out)
	}
	if env := parseEnvelopeForTest(t, out); env.OK || !strings.Contains(env.Errors[0].Message, "owner/repo") {
		t.Fatalf("expected malformed repo error envelope, got %+v", env)
	}

	out, err = runRootForTest(t, ws, "profile", "credentials", "set", "work", "--provider", "github", "--repo", "acme/web", "--write-repo", "acme/web", "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("expected conflicting repo contract error, got err=%v out=%s", err, out)
	}
	if env := parseEnvelopeForTest(t, out); env.OK || !strings.Contains(env.Errors[0].Message, "conflicting") {
		t.Fatalf("expected conflicting repo error envelope, got %+v", env)
	}
}

func containsCredentialScope(rows []string, want string) bool {
	for _, row := range rows {
		if row == want {
			return true
		}
	}
	return false
}
