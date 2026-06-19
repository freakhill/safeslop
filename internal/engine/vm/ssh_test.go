package vm

import (
	"strings"
	"testing"
)

func TestSSHArgvTTYAndUser(t *testing.T) {
	got := strings.Join(sshArgv("10.0.0.9", true, "zsh", "-lc", "x"), " ")
	for _, want := range []string{"-t", "admin@10.0.0.9", "BatchMode=yes", "-- zsh -lc x"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sshArgv missing %q in %q", want, got)
		}
	}
	if strings.Contains(strings.Join(sshArgv("10.0.0.9", false, "x"), " "), " -t ") {
		t.Fatal("no -t expected when tty=false")
	}
}

func TestScpArgv(t *testing.T) {
	got := strings.Join(scpArgv("10.0.0.9", "/stage", "~/.safeslop-runtime"), " ")
	if !strings.Contains(got, "-r /stage admin@10.0.0.9:~/.safeslop-runtime") {
		t.Fatalf("scpArgv wrong: %q", got)
	}
}

func TestRemoteAgentCmdSourcesSecretsAndEscapes(t *testing.T) {
	cmd := remoteAgentCmd([]string{"claude", "--flag with space"}, "")
	if !strings.Contains(cmd, ". ~/.safeslop-runtime/secrets.env") || !strings.HasPrefix(cmd, "set -a;") {
		t.Fatalf("missing secrets sourcing: %q", cmd)
	}
	if !strings.Contains(cmd, `exec 'claude' '--flag with space'`) {
		t.Fatalf("agent argv not quoted: %q", cmd)
	}
	if !strings.Contains(remoteAgentCmd([]string{"zsh"}, "http://p:3128"), "export HTTP_PROXY='http://p:3128'") {
		t.Fatal("proxy export missing when proxyURL set")
	}
}
