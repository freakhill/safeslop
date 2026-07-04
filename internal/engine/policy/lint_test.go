package policy

import "testing"

func TestLintIsDeterministicAndStable(t *testing.T) {
	mk := func() Profile {
		return Profile{Environment: "container", Network: "allow", Credentials: &Credentials{Github: &GithubCreds{Write: true}}}
	}
	cfg := &Config{Profiles: map[string]Profile{"b": mk(), "a": mk()}}
	ws := Lint(cfg)
	if len(ws) != 2 || ws[0].Profile != "a" || ws[1].Profile != "b" {
		t.Fatalf("warnings must be sorted by profile: %+v", ws)
	}
}

func TestLintSshWriteOpenEgress(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"push_open": {Environment: "container", Network: "allow", Credentials: &Credentials{Github: &GithubCreds{Write: true}}},
		"push_deny": {Environment: "container", Network: "deny", Credentials: &Credentials{Github: &GithubCreds{Write: true}}},
		"ro_open":   {Environment: "container", Network: "allow", Credentials: &Credentials{Github: &GithubCreds{Write: false}}},
	}}
	codes := map[string]string{}
	for _, w := range Lint(cfg) {
		if w.Code == "github-write-open-egress" {
			codes[w.Profile] = w.Code
		}
	}
	if codes["push_open"] != "github-write-open-egress" {
		t.Fatalf("write+allow must be flagged: %+v", codes)
	}
	if _, bad := codes["push_deny"]; bad {
		t.Fatal("write+deny must NOT be flagged")
	}
	if _, bad := codes["ro_open"]; bad {
		t.Fatal("read-only+allow must NOT be flagged")
	}
}

func lintCodes(cfg *Config) map[string]bool {
	m := map[string]bool{}
	for _, w := range Lint(cfg) {
		m[w.Code] = true
	}
	return m
}

// TestLintEgressIgnored: an `egress:` list is honored only on environment:container with
// network:deny; anywhere else it is silently ignored, so lint warns (specs/0046).
func TestLintEgressIgnored(t *testing.T) {
	ok := &Config{Profiles: map[string]Profile{
		"p": {Agent: "pi", Environment: "container", Network: "deny", Egress: []string{".x.com"}},
	}}
	if lintCodes(ok)["egress-ignored"] {
		t.Error("container+deny with egress must NOT warn — it is honored there")
	}
	allow := &Config{Profiles: map[string]Profile{
		"p": {Agent: "pi", Environment: "container", Network: "allow", Egress: []string{".x.com"}},
	}}
	if !lintCodes(allow)["egress-ignored"] {
		t.Error("container+allow with egress must warn (allowlist bypassed)")
	}
	hostEgress := &Config{Profiles: map[string]Profile{
		"p": {Agent: "pi", Environment: "host", Network: "deny", Egress: []string{".x.com"}},
	}}
	if !lintCodes(hostEgress)["egress-ignored"] {
		t.Error("non-container env with egress must warn (host has no domain allowlist)")
	}
	none := &Config{Profiles: map[string]Profile{
		"p": {Agent: "pi", Environment: "host", Network: "deny"},
	}}
	if lintCodes(none)["egress-ignored"] {
		t.Error("a profile without egress must not warn")
	}
}
