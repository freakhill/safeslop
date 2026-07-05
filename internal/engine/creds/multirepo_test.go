package creds

import (
	"strings"
	"testing"
)

func TestRepoSlug(t *testing.T) {
	if got := repoSlug("acme/repo1"); got != "acme-repo1" {
		t.Fatalf("repoSlug = %q", got)
	}
}

func TestSplitOwnerRepoRejectsInjection(t *testing.T) {
	bad := []string{
		"acme/repo\"\nProxyCommand=sh",
		"acme\nHost */repo",
		"acme/re po",
		"acme/",
	}
	for _, in := range bad {
		if _, _, err := splitOwnerRepo(in); err == nil {
			t.Fatalf("splitOwnerRepo(%q) unexpectedly succeeded", in)
		}
	}
	owner, repo, err := splitOwnerRepo("acme/repo.1_test-2")
	if err != nil || owner != "acme" || repo != "repo.1_test-2" {
		t.Fatalf("valid repo rejected: %q/%q err=%v", owner, repo, err)
	}
}

func TestRenderAliasSSHConfigRejectsUnsafeEntry(t *testing.T) {
	entries := []aliasEntry{{slug: "acme-repo\nProxyCommand", owner: "acme", repo: "repo", keyPath: "/stage/id"}}
	if _, err := renderAliasSSHConfig("codeberg.org", "22", "/stage/known_hosts", entries, func(p string) string { return p }); err == nil {
		t.Fatal("unsafe alias entry must be rejected")
	}
	entries = []aliasEntry{{slug: "acme-repo", owner: "acme", repo: "repo", keyPath: "/stage/id"}}
	cfg, err := renderAliasSSHConfig("codeberg.org", "22", "/stage/known_hosts", entries, func(p string) string { return p })
	if err != nil {
		t.Fatalf("safe alias entry rejected: %v", err)
	}
	if strings.Contains(cfg, "ProxyCommand") || !strings.Contains(cfg, "Host codeberg.org-acme-repo") {
		t.Fatalf("unexpected rendered ssh config: %q", cfg)
	}
}
