package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// loadStr writes src to a temp slop.cue and Loads it (the package's load path is file-based).
func loadStr(t *testing.T, src string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "slop.cue")
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
}

func TestLoadRejectsBadSecretRef(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "bad_secretref.cue")); err == nil {
		t.Fatal("expected validation error for a non-op://, non-env: secret ref")
	}
}

func TestToolchainDecodes(t *testing.T) {
	cfg, err := loadStr(t, `package slop
slop: profiles: dev: {agent: "claude", toolchain: {kind: "mise", run: "build"}}`)
	if err != nil {
		t.Fatal(err)
	}
	tc := cfg.Profiles["dev"].Toolchain
	if tc == nil || tc.Kind != "mise" || tc.Run != "build" {
		t.Fatalf("toolchain decoded wrong: %+v", tc)
	}
}

func TestToolchainRejectsBadKind(t *testing.T) {
	if _, err := loadStr(t, `package slop
slop: profiles: dev: {agent: "claude", toolchain: {kind: "cargo"}}`); err == nil {
		t.Fatal("expected validation error for kind \"cargo\"")
	}
}
