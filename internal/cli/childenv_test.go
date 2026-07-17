package cli

import (
	"strings"
	"testing"
)

func TestChildEnvScrubsAmbientAuthority(t *testing.T) {
	// Keep this test hermetic: no real shell reconstruction, so the os.Environ scrub is what's exercised.
	d := defaultDependencies()
	d.hostDiscoveryEnv = func() map[string]string { return nil }

	// host ambient credentials that must NOT cross into the host tier
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA-HOST")
	t.Setenv("OP_SESSION_my", "op-host-token")
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "ops_host")
	t.Setenv("SSH_AUTH_SOCK", "/private/tmp/com.apple.launchd/agent.sock")
	t.Setenv("GITHUB_TOKEN", "ghp_host")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-host") // dropped by design — declare in secrets:
	// safe basics + locale that must be carried
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/Users/test")
	t.Setenv("LC_CTYPE", "UTF-8")

	env := childEnvWithDeps(d,
		[]string{"AWS_ACCESS_KEY_ID=AKIA-EPHEMERAL"},    // a staged (ephemeral) cred
		[]string{"NPM_CONFIG_USERCONFIG=/stage/.npmrc"}, // a staged path env
	)
	has := func(s string) bool {
		for _, e := range env {
			if e == s {
				return true
			}
		}
		return false
	}
	hasName := func(name string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, name+"=") {
				return true
			}
		}
		return false
	}

	for _, leaked := range []string{"OP_SESSION_my", "OP_SERVICE_ACCOUNT_TOKEN", "SSH_AUTH_SOCK", "GITHUB_TOKEN", "ANTHROPIC_API_KEY"} {
		if hasName(leaked) {
			t.Errorf("ambient %s leaked into child env (must be dropped)", leaked)
		}
	}
	if has("AWS_ACCESS_KEY_ID=AKIA-HOST") {
		t.Error("host AWS key leaked into child env")
	}
	if !has("AWS_ACCESS_KEY_ID=AKIA-EPHEMERAL") {
		t.Error("staged ephemeral AWS key must be present")
	}
	if !hasName("PATH") || !hasName("HOME") || !hasName("LC_CTYPE") {
		t.Error("PATH/HOME/LC_CTYPE must be carried")
	}
	if !has("NPM_CONFIG_USERCONFIG=/stage/.npmrc") {
		t.Error("staged pathEnv must be carried")
	}
}

// TestChildEnvFirewallDropsRichHostDiscoveryEnv is the two-environment firewall guard (specs research
// 2026-06-21, actionable 6). hostenv reconstructs a RICH host_discovery_env (a login shell exports
// cloud tokens) so the engine can find brew/agents under a Finder launch. That rich env must reach the
// sandbox ONLY as its allowlisted, non-credential members (PATH/SHELL): the reconstructed PATH crosses
// (it is location, not authority), but AWS_*/tokens/SSH_AUTH_SOCK from the same capture must not.
func TestChildEnvFirewallDropsRichHostDiscoveryEnv(t *testing.T) {
	d := defaultDependencies()
	d.hostDiscoveryEnv = func() map[string]string {
		return map[string]string{
			"PATH":                  "/opt/homebrew/bin:/usr/bin:/bin", // allowlisted → must cross
			"SHELL":                 "/opt/homebrew/bin/fish",          // allowlisted → must cross
			"AWS_SECRET_ACCESS_KEY": "rich-secret",                     // credential → must be dropped
			"GITHUB_TOKEN":          "ghp_rich",
			"ANTHROPIC_API_KEY":     "sk-ant-rich",
			"SSH_AUTH_SOCK":         "/tmp/agent.sock",
			"OP_SESSION_x":          "op-rich",
		}
	}

	// The process env is the Finder-stripped one; the reconstructed PATH must win over it.
	t.Setenv("PATH", "/usr/bin:/bin")

	env := childEnvWithDeps(d, nil, nil)
	get := func(name string) (string, bool) {
		for _, e := range env {
			if v, found := strings.CutPrefix(e, name+"="); found {
				return v, true
			}
		}
		return "", false
	}

	if v, ok := get("PATH"); !ok || v != "/opt/homebrew/bin:/usr/bin:/bin" {
		t.Errorf("reconstructed PATH must cross and win over the stripped process PATH, got %q (%v)", v, ok)
	}
	if v, ok := get("SHELL"); !ok || v != "/opt/homebrew/bin/fish" {
		t.Errorf("reconstructed SHELL must cross, got %q (%v)", v, ok)
	}
	for _, cred := range []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "ANTHROPIC_API_KEY", "SSH_AUTH_SOCK", "OP_SESSION_x"} {
		if _, ok := get(cred); ok {
			t.Errorf("credential %s leaked from the rich host_discovery_env into the child (firewall breached)", cred)
		}
	}
}

// W2: the host-tier child must always get a truecolor terminal, even under a Finder/launchd launch
// where the process env has no TERM. childEnv forces TERM/COLORTERM regardless of the host env.
func TestChildEnvForcesTruecolorTerm(t *testing.T) {
	d := defaultDependencies()
	d.hostDiscoveryEnv = func() map[string]string { return nil }
	t.Setenv("TERM", "")      // simulate the Finder/launchd strip
	t.Setenv("COLORTERM", "") // ditto

	env := childEnvWithDeps(d, nil, nil)
	has := func(s string) bool {
		for _, e := range env {
			if e == s {
				return true
			}
		}
		return false
	}
	if !has("TERM=xterm-256color") {
		t.Errorf("childEnv must force TERM=xterm-256color even with no host TERM, got %v", env)
	}
	if !has("COLORTERM=truecolor") {
		t.Errorf("childEnv must force COLORTERM=truecolor, got %v", env)
	}
}
