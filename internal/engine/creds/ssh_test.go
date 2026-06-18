package creds

import (
	"strings"
	"testing"
)

func TestKeygenArgv(t *testing.T) {
	got := strings.Join(keygenArgv("/stage/.ssh/id", "slop-acme/repo-run1"), " ")
	want := `ssh-keygen -t ed25519 -N  -C slop-acme/repo-run1 -f /stage/.ssh/id`
	if got != want {
		t.Fatalf("keygen argv = %q", got)
	}
}

func TestGhRegisterArgv(t *testing.T) {
	ro := strings.Join(ghRegisterArgv("acme", "repo", "slop-run1", "ssh-ed25519 AAAA", false), " ")
	if ro != "gh api repos/acme/repo/keys -f title=slop-run1 -f key=ssh-ed25519 AAAA -F read_only=true" {
		t.Fatalf("ro argv = %q", ro)
	}
	rw := strings.Join(ghRegisterArgv("acme", "repo", "slop-run1", "ssh-ed25519 AAAA", true), " ")
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
	got := renderGitSSHCommand("/slop/runtime/.ssh/id", "/slop/runtime/.ssh/known_hosts")
	for _, want := range []string{
		"ssh -i /slop/runtime/.ssh/id",
		"-o IdentitiesOnly=yes",
		"-o IdentityAgent=none",
		"-o StrictHostKeyChecking=yes",
		"-o UserKnownHostsFile=/slop/runtime/.ssh/known_hosts",
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
