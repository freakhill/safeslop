package policy

import (
	"path/filepath"
	"testing"
)

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
