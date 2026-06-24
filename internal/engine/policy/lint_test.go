package policy

import "testing"

func TestLintFlagsSandboxOpenEgressWithCreds(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"risky":          {Environment: "sandbox", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
		"risky_pnpm":     {Environment: "sandbox", Network: "allow", Credentials: &Credentials{Pnpm: []PnpmRegistry{{Host: "registry.npmjs.org", Token: "env:T"}}}},
		"safe_deny":      {Environment: "sandbox", Network: "deny", Secrets: map[string]string{"A": "env:X"}},
		"safe_container": {Environment: "container", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
		"safe_nocreds":   {Environment: "sandbox", Network: "allow"},
	}}
	ws := Lint(cfg)
	got := map[string]string{}
	for _, w := range ws {
		got[w.Profile] = w.Code
	}
	if len(ws) != 2 {
		t.Fatalf("want 2 warnings, got %d: %+v", len(ws), ws)
	}
	for _, p := range []string{"risky", "risky_pnpm"} {
		if got[p] != "sandbox-open-egress-with-creds" {
			t.Fatalf("profile %q not flagged: %+v", p, ws)
		}
	}
	for _, p := range []string{"safe_deny", "safe_container", "safe_nocreds"} {
		if _, bad := got[p]; bad {
			t.Fatalf("profile %q should NOT be flagged: %+v", p, ws)
		}
	}
}

func TestLintIsDeterministicAndStable(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"b": {Environment: "sandbox", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
		"a": {Environment: "sandbox", Network: "allow", Secrets: map[string]string{"A": "env:X"}},
	}}
	ws := Lint(cfg)
	if len(ws) != 2 || ws[0].Profile != "a" || ws[1].Profile != "b" {
		t.Fatalf("warnings must be sorted by profile: %+v", ws)
	}
}

func TestLintKubeTripsOpenEgress(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"deploy": {Environment: "sandbox", Network: "allow", Credentials: &Credentials{Kube: &KubeCluster{Eks: &EksCluster{Name: "prod"}}}},
	}}
	ws := Lint(cfg)
	found := false
	for _, w := range ws {
		if w.Profile == "deploy" && w.Code == "sandbox-open-egress-with-creds" {
			found = true
		}
	}
	if !found {
		t.Fatalf("kube creds under sandbox+allow must trip open-egress lint: %+v", ws)
	}
}

func TestLintSshWriteOpenEgress(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"push_open": {Environment: "container", Network: "allow", Credentials: &Credentials{Ssh: &SshCreds{Write: true}}},
		"push_deny": {Environment: "container", Network: "deny", Credentials: &Credentials{Ssh: &SshCreds{Write: true}}},
		"ro_open":   {Environment: "container", Network: "allow", Credentials: &Credentials{Ssh: &SshCreds{Write: false}}},
	}}
	codes := map[string]string{}
	for _, w := range Lint(cfg) {
		if w.Code == "ssh-write-open-egress" {
			codes[w.Profile] = w.Code
		}
	}
	if codes["push_open"] != "ssh-write-open-egress" {
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
	sb := &Config{Profiles: map[string]Profile{
		"p": {Agent: "pi", Environment: "sandbox", Network: "deny", Egress: []string{".x.com"}},
	}}
	if !lintCodes(sb)["egress-ignored"] {
		t.Error("non-container env with egress must warn (Seatbelt has no domain allowlist)")
	}
	none := &Config{Profiles: map[string]Profile{
		"p": {Agent: "pi", Environment: "sandbox", Network: "deny"},
	}}
	if lintCodes(none)["egress-ignored"] {
		t.Error("a profile without egress must not warn")
	}
}
