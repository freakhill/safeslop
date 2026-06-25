package cli

import (
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// agent: "shell" must use the host's $SHELL for host/sandbox (the agent runs on the
// host), but a guest-resident shell for container/vm — the host path (e.g. /bin/zsh)
// does not exist inside the image and exec would fail with "/bin/zsh: not found".
func TestAgentArgvShellIsTierAware(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh") // a host shell absent from the container/vm image

	cases := []struct {
		env  string
		want string
	}{
		{"host", "/bin/zsh"},
		{"sandbox", "/bin/zsh"},
		{"container", "bash"},
		{"vm", "/bin/sh"},
	}
	for _, c := range cases {
		argv, err := agentArgv(policy.Profile{Agent: "shell", Environment: c.env})
		if err != nil {
			t.Fatalf("env=%s: %v", c.env, err)
		}
		if len(argv) != 1 || argv[0] != c.want {
			t.Errorf("env=%s: argv=%v, want [%q]", c.env, argv, c.want)
		}
	}
}

func TestAgentArgvShellFallsBackWhenNoShellEnv(t *testing.T) {
	t.Setenv("SHELL", "")
	argv, err := agentArgv(policy.Profile{Agent: "shell", Environment: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 1 || argv[0] != "/bin/sh" {
		t.Errorf("empty $SHELL host fallback: argv=%v, want [/bin/sh]", argv)
	}
}

func TestAgentArgvAcceptsPi(t *testing.T) {
	argv, err := agentArgv(policy.Profile{Agent: "pi"})
	if err != nil {
		t.Fatalf("agentArgv(pi): %v", err)
	}
	if len(argv) != 1 || argv[0] != "pi" {
		t.Errorf("pi argv = %v, want [pi]", argv)
	}
}

func TestAgentArgvAcceptsClaudeCode(t *testing.T) {
	argv, err := agentArgv(policy.Profile{Agent: "claude"})
	if err != nil {
		t.Fatalf("agentArgv(claude): %v", err)
	}
	if len(argv) != 1 || argv[0] != "claude" {
		t.Errorf("claude argv = %v, want [claude]", argv)
	}
}

func TestAgentArgvRejectsOpenCode(t *testing.T) {
	_, err := agentArgv(policy.Profile{Agent: "opencode"})
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("agentArgv(opencode) error = %v, want unknown agent", err)
	}
}

func TestAgentArgvRejectsVSCode(t *testing.T) {
	_, err := agentArgv(policy.Profile{Agent: "vscode"})
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("agentArgv(vscode) error = %v, want unknown agent", err)
	}
}

func TestAgentArgvRejectsUnknownAgent(t *testing.T) {
	_, err := agentArgv(policy.Profile{Agent: "cursor"})
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("agentArgv(cursor) error = %v, want unknown agent", err)
	}
}
