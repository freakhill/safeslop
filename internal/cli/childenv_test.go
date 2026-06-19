package cli

import (
	"strings"
	"testing"
)

func TestChildEnvScrubsAmbientAuthority(t *testing.T) {
	// host ambient credentials that must NOT cross into the sandbox/host tiers
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

	env := childEnv(
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
