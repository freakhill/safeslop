package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestRenderNpmrcScopedAndDefault(t *testing.T) {
	regs := []policy.PnpmRegistry{
		{Host: "registry.npmjs.org", Token: "ignored"},
		{Host: "npm.pkg.github.com", Token: "ignored", Scope: "@myorg"},
	}
	out := RenderNpmrc(regs, []string{"TOK1", "TOK2"})
	for _, want := range []string{
		"//registry.npmjs.org/:_authToken=TOK1",
		"@myorg:registry=https://npm.pkg.github.com/",
		"//npm.pkg.github.com/:_authToken=TOK2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("npmrc missing %q\n---\n%s", want, out)
		}
	}
}

func TestStagePnpmWritesScopedNpmrc(t *testing.T) {
	t.Setenv("SLOP_TEST_NPM_TOKEN", "abc123")
	stage := t.TempDir()
	env, err := StagePnpm(context.Background(), &policy.Credentials{
		Pnpm: []policy.PnpmRegistry{
			{Host: "registry.npmjs.org", Token: "env:SLOP_TEST_NPM_TOKEN"},
		},
	}, stage)
	if err != nil {
		t.Fatalf("StagePnpm: %v", err)
	}
	npmrc := filepath.Join(stage, ".npmrc")
	if len(env) != 1 || env[0] != "NPM_CONFIG_USERCONFIG="+npmrc {
		t.Fatalf("env additions = %v", env)
	}
	info, err := os.Stat(npmrc)
	if err != nil {
		t.Fatalf("stat .npmrc: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".npmrc perms = %o, want 600", info.Mode().Perm())
	}
	b, _ := os.ReadFile(npmrc)
	if !strings.Contains(string(b), "//registry.npmjs.org/:_authToken=abc123") {
		t.Errorf(".npmrc content:\n%s", b)
	}
}

func TestStagePnpmNoCredsIsNoop(t *testing.T) {
	env, err := StagePnpm(context.Background(), nil, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("expected no-op, got env=%v err=%v", env, err)
	}
}
