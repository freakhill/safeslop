package creds

import "testing"

func TestRepoSlug(t *testing.T) {
	if got := repoSlug("acme/repo1"); got != "acme-repo1" {
		t.Fatalf("repoSlug = %q", got)
	}
}
