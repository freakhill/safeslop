package container

import "testing"

// Regression for specs/0073: the T7 CredsEgress union (specs/0069) adds bare
// github.com/codeload.github.com/objects.githubusercontent.com, each already
// covered by a base wildcard. Squid FATALs on a bare domain that collides with a
// `.wildcard` of the same base, crash-looping the proxy. composeAllowlist must
// drop the covered bare entries while leaving wildcards (incl. nested wildcards)
// intact.
func TestComposeAllowlistDropsWildcardCoveredBareDomains(t *testing.T) {
	base := []byte(".github.com\n.githubusercontent.com\n.raw.githubusercontent.com\n")
	extra := []string{"github.com", "codeload.github.com", "objects.githubusercontent.com"}
	got := string(composeAllowlist(base, extra))
	want := ".github.com\n.githubusercontent.com\n.raw.githubusercontent.com\n"
	if got != want {
		t.Fatalf("covered bare domains not dropped\n got: %q\nwant: %q", got, want)
	}
}

func TestComposeAllowlistKeepsUncoveredExtras(t *testing.T) {
	base := []byte(".github.com\n")
	// example.org has no covering wildcard -> kept; .npmjs.org is a fresh wildcard -> kept.
	got := string(composeAllowlist(base, []string{"example.org", ".npmjs.org", "github.com"}))
	want := ".github.com\nexample.org\n.npmjs.org\n"
	if got != want {
		t.Fatalf("uncovered extras mishandled\n got: %q\nwant: %q", got, want)
	}
}
