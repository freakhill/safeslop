package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestStageGitHubPATMultiRepo(t *testing.T) {
	stage := t.TempDir()
	t.Setenv("GH_FINE_GRAINED_PAT", "ghp_dummy_secret")

	env, err := StageGithub(context.Background(), &policy.Credentials{Github: &policy.GithubCreds{
		Mode: "pat",
		Pat:  "env:GH_FINE_GRAINED_PAT",
		Repos: []policy.RepoCred{
			{Repo: "acme/web"},
			{Repo: "acme/api", Write: true},
		},
	}}, stage, nil)
	if err != nil {
		t.Fatalf("StageGithub PAT: %v", err)
	}

	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_CONFIG_GLOBAL="+filepath.Join(stage, ".gitconfig")) {
		t.Fatalf("GIT_CONFIG_GLOBAL not staged: %v", env)
	}
	if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("PAT mode must disable interactive credential prompts: %v", env)
	}
	if strings.Contains(joined, "GIT_SSH_COMMAND") {
		t.Fatalf("PAT mode must use HTTPS git config, not SSH command: %v", env)
	}

	tokenPath := filepath.Join(stage, ".git-pat-token")
	if fi, err := os.Stat(tokenPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file must be staged 0600, stat=%v err=%v", fi, err)
	}
	if b, _ := os.ReadFile(tokenPath); string(b) != "ghp_dummy_secret" {
		t.Fatalf("token file content mismatch")
	}

	gc, _ := os.ReadFile(filepath.Join(stage, ".gitconfig"))
	cfg := string(gc)
	for _, want := range []string{
		"[include]\n\tpath = ~/.gitconfig",
		`[credential "https://github.com/acme/web.git"]`,
		`[credential "https://github.com/acme/api.git"]`,
		`helper = "!f()`,
		"cat '" + tokenPath + "'",
		`[url "https://github.com/acme/web.git"]`,
		"insteadOf = git@github.com:acme/web.git",
		"insteadOf = ssh://git@github.com/acme/web.git",
		`[url "https://github.com/acme/api.git"]`,
		"insteadOf = git@github.com:acme/api.git",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("gitconfig missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "ghp_dummy_secret") {
		t.Fatal("token value must not be embedded in gitconfig")
	}
	if _, err := os.Stat(filepath.Join(stage, ".ssh")); !os.IsNotExist(err) {
		t.Fatalf("PAT mode should not stage .ssh artifacts: %v", err)
	}
}

func TestStagePATRequiresTokenAndRepos(t *testing.T) {
	if _, err := StageGithub(context.Background(), &policy.Credentials{Github: &policy.GithubCreds{Mode: "pat", Repos: []policy.RepoCred{{Repo: "acme/web"}}}}, t.TempDir(), nil); err == nil {
		t.Fatal("GitHub PAT mode without pat ref must fail")
	}
}
