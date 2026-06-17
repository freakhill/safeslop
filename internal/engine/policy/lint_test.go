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
