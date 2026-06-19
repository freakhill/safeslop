package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestKeygenArgv(t *testing.T) {
	got := strings.Join(keygenArgv("/stage/.ssh/id", "safeslop-acme/repo-run1"), " ")
	want := `ssh-keygen -t ed25519 -N  -C safeslop-acme/repo-run1 -f /stage/.ssh/id`
	if got != want {
		t.Fatalf("keygen argv = %q", got)
	}
}

func TestGhRegisterArgv(t *testing.T) {
	ro := strings.Join(ghRegisterArgv("acme", "repo", "safeslop-run1", "ssh-ed25519 AAAA", false), " ")
	if ro != "gh api repos/acme/repo/keys -f title=safeslop-run1 -f key=ssh-ed25519 AAAA -F read_only=true" {
		t.Fatalf("ro argv = %q", ro)
	}
	rw := strings.Join(ghRegisterArgv("acme", "repo", "safeslop-run1", "ssh-ed25519 AAAA", true), " ")
	if !strings.Contains(rw, "-F read_only=false") {
		t.Fatalf("rw argv = %q", rw)
	}
}

func TestGhRevokeArgv(t *testing.T) {
	got := strings.Join(ghRevokeArgv("acme", "repo", "42"), " ")
	if got != "gh api --method DELETE repos/acme/repo/keys/42" {
		t.Fatalf("revoke argv = %q", got)
	}
}

func TestParseOwnerRepo(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:acme/repo.git\n":       {"acme", "repo"},
		"https://github.com/acme/repo.git\n":   {"acme", "repo"},
		"https://github.com/acme/repo\n":       {"acme", "repo"},
		"ssh://git@github.com/acme/repo.git\n": {"acme", "repo"},
	}
	for in, want := range cases {
		o, r, err := parseOwnerRepo([]byte(in))
		if err != nil || o != want[0] || r != want[1] {
			t.Fatalf("parseOwnerRepo(%q) = %q/%q err=%v", in, o, r, err)
		}
	}
	if _, _, err := parseOwnerRepo([]byte("/local/path\n")); err == nil {
		t.Fatal("expected error on non-github remote")
	}
}

func TestParseKeyID(t *testing.T) {
	id, err := parseKeyID([]byte(`{"id":1234567,"key":"ssh-ed25519 AAAA","read_only":true}`))
	if err != nil || id != "1234567" {
		t.Fatalf("parseKeyID = %q err=%v", id, err)
	}
	if _, err := parseKeyID([]byte(`{}`)); err == nil {
		t.Fatal("expected error on missing id")
	}
}

func TestRenderGitSSHCommand(t *testing.T) {
	got := renderGitSSHCommand("/safeslop/runtime/.ssh/id", "/safeslop/runtime/.ssh/known_hosts")
	for _, want := range []string{
		"ssh -i /safeslop/runtime/.ssh/id",
		"-o IdentitiesOnly=yes",
		"-o IdentityAgent=none",
		"-o StrictHostKeyChecking=yes",
		"-o UserKnownHostsFile=/safeslop/runtime/.ssh/known_hosts",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("GIT_SSH_COMMAND missing %q: %s", want, got)
		}
	}
}

func TestKnownHostsIsGithubEd25519(t *testing.T) {
	if !strings.HasPrefix(githubKnownHosts, "github.com ssh-ed25519 ") {
		t.Fatalf("known_hosts must pin github.com ed25519: %q", githubKnownHosts)
	}
}

// fakeStub writes an executable /bin/sh stub with an arbitrary body.
func fakeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStageSSHMintsReadOnly(t *testing.T) {
	binDir := t.TempDir()
	fakeStub(t, binDir, "git", `echo "git@github.com:acme/repo.git"`)
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB safeslop" > "$p.pub"`)
	fakeStub(t, binDir, "gh", `echo '{"id":4242,"read_only":true}'`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	stage := t.TempDir()
	env, err := StageSSH(context.Background(), &policy.Credentials{Ssh: &policy.SshCreds{}}, stage)
	if err != nil {
		t.Fatalf("StageSSH: %v", err)
	}
	keyPath := filepath.Join(stage, ".ssh", "id")
	khPath := filepath.Join(stage, ".ssh", "known_hosts")
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -i "+keyPath) || !strings.Contains(joined, "UserKnownHostsFile="+khPath) {
		t.Fatalf("env = %v", env)
	}
	if fi, _ := os.Stat(keyPath); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("private key not staged 0600")
	}
	if _, err := os.Stat(keyPath + ".pub"); !os.IsNotExist(err) {
		t.Fatalf(".pub must not remain staged")
	}
	if b, _ := os.ReadFile(khPath); !strings.HasPrefix(string(b), "github.com ssh-ed25519 ") {
		t.Fatalf("known_hosts not pinned: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(stage, ".ssh", "revoke-info")); strings.TrimSpace(string(b)) != "acme/repo 4242" {
		t.Fatalf("revoke-info = %q", b)
	}
}

func TestStageSSHNilIsNoop(t *testing.T) {
	env, err := StageSSH(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil ssh creds must be a no-op: env=%v err=%v", env, err)
	}
}

func TestRevokeSSHCallsGhDelete(t *testing.T) {
	binDir := t.TempDir()
	stage := t.TempDir()
	marker := filepath.Join(stage, "gh-called")
	fakeStub(t, binDir, "gh", `echo "$@" > `+marker)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if err := os.MkdirAll(filepath.Join(stage, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, ".ssh", "revoke-info"), []byte("acme/repo 4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RevokeSSH(context.Background(), stage)
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("gh was not called: %v", err)
	}
	if !strings.Contains(string(b), "DELETE repos/acme/repo/keys/4242") {
		t.Fatalf("gh args = %q", b)
	}
}

func TestRevokeSSHNoInfoIsSilent(t *testing.T) {
	RevokeSSH(context.Background(), t.TempDir())
}
