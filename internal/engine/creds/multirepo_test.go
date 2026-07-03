package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestRepoSlug(t *testing.T) {
	if got := repoSlug("acme/repo1"); got != "acme-repo1" {
		t.Fatalf("repoSlug = %q", got)
	}
}

func TestStageSSHMultiRepo(t *testing.T) {
	binDir := t.TempDir()
	stage := t.TempDir()
	ghCalls := filepath.Join(stage, "gh-calls")
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB safeslop" > "$p.pub"`)
	// Record each gh invocation so we can assert per-repo read_only, and hand back a distinct id.
	fakeStub(t, binDir, "gh", `echo "$*" >> `+ghCalls+`; if echo "$*" | grep -q repo2; then echo '{"id":9002}'; else echo '{"id":9001}'; fi`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	creds := &policy.Credentials{Github: &policy.GithubCreds{Repos: []policy.RepoCred{
		{Repo: "acme/repo1"},
		{Repo: "acme/repo2", Write: true},
	}}}
	env, err := StageSSH(context.Background(), creds, stage)
	if err != nil {
		t.Fatalf("StageSSH multi: %v", err)
	}

	// One 0600 key per repo, pubs removed.
	for _, slug := range []string{"acme-repo1", "acme-repo2"} {
		kp := filepath.Join(stage, ".ssh", "id_"+slug)
		if fi, _ := os.Stat(kp); fi == nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("key %s not staged 0600", slug)
		}
		if _, err := os.Stat(kp + ".pub"); !os.IsNotExist(err) {
			t.Fatalf(".pub for %s must be removed", slug)
		}
	}

	// Per-repo SSH aliases in the staged config.
	cfg, _ := os.ReadFile(filepath.Join(stage, ".ssh", "config"))
	for _, want := range []string{
		"Host github.com-acme-repo1",
		"Host github.com-acme-repo2",
		"HostName github.com",
		"IdentityFile " + filepath.Join(stage, ".ssh", "id_acme-repo1"),
		"IdentitiesOnly yes",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("ssh config missing %q:\n%s", want, cfg)
		}
	}

	// Env points git at the staged config + global with insteadOf rules.
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -F "+filepath.Join(stage, ".ssh", "config")) {
		t.Fatalf("GIT_SSH_COMMAND not set to staged config: %v", env)
	}
	if !strings.Contains(joined, "GIT_CONFIG_GLOBAL="+filepath.Join(stage, ".gitconfig")) {
		t.Fatalf("GIT_CONFIG_GLOBAL not set: %v", env)
	}

	// insteadOf rewrites both repos onto their aliases.
	gc, _ := os.ReadFile(filepath.Join(stage, ".gitconfig"))
	for _, want := range []string{
		`[url "git@github.com-acme-repo1:acme/repo1.git"]`,
		"insteadOf = git@github.com:acme/repo1.git",
		"insteadOf = git@github.com:acme/repo2.git",
	} {
		if !strings.Contains(string(gc), want) {
			t.Fatalf("gitconfig missing %q:\n%s", want, gc)
		}
	}

	// repo1 read-only, repo2 read-write.
	calls, _ := os.ReadFile(ghCalls)
	if !strings.Contains(string(calls), "repos/acme/repo1/keys") || !strings.Contains(string(calls), "repos/acme/repo2/keys") {
		t.Fatalf("gh not called per repo:\n%s", calls)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if strings.Contains(line, "repo1") && !strings.Contains(line, "read_only=true") {
			t.Fatalf("repo1 should be ro: %s", line)
		}
		if strings.Contains(line, "repo2") && !strings.Contains(line, "read_only=false") {
			t.Fatalf("repo2 should be rw: %s", line)
		}
	}

	// revoke-info: one line per key.
	ri, _ := os.ReadFile(filepath.Join(stage, ".ssh", "revoke-info"))
	if !strings.Contains(string(ri), "acme/repo1 9001") || !strings.Contains(string(ri), "acme/repo2 9002") {
		t.Fatalf("revoke-info = %q", ri)
	}
}

func TestRevokeSSHMultiRevokesAll(t *testing.T) {
	binDir := t.TempDir()
	stage := t.TempDir()
	marker := filepath.Join(stage, "gh-deletes")
	fakeStub(t, binDir, "gh", `echo "$@" >> `+marker)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if err := os.MkdirAll(filepath.Join(stage, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, ".ssh", "revoke-info"),
		[]byte("acme/repo1 9001\nacme/repo2 9002\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RevokeSSH(context.Background(), stage)
	b, _ := os.ReadFile(marker)
	for _, want := range []string{
		"DELETE repos/acme/repo1/keys/9001",
		"DELETE repos/acme/repo2/keys/9002",
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("revoke missing %q:\n%s", want, b)
		}
	}
}
