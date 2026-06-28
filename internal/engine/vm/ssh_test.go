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
	cmd := remoteAgentCmd([]string{"claude", "--flag with space"}, "", false, "", false, false)
	if !strings.Contains(cmd, ". ~/.safeslop-runtime/secrets.env") || !strings.Contains(cmd, "set -a;") {
		t.Fatalf("missing secrets sourcing: %q", cmd)
	}
	// W2: a truecolor terminal is forced first, before any cred/secret setup.
	if !strings.HasPrefix(cmd, "export TERM=xterm-256color COLORTERM=truecolor; ") {
		t.Fatalf("vm remote cmd must export TERM/COLORTERM first: %q", cmd)
	}
	if !strings.Contains(cmd, `exec 'claude' '--flag with space'`) {
		t.Fatalf("agent argv not quoted: %q", cmd)
	}
	if !strings.Contains(remoteAgentCmd([]string{"zsh"}, "http://p:3128", false, "", false, false), "export HTTP_PROXY='http://p:3128'") {
		t.Fatal("proxy export missing when proxyURL set")
	}
	// No staged path-creds → no GIT_SSH_COMMAND / KUBECONFIG exports.
	if strings.Contains(cmd, "GIT_SSH_COMMAND") || strings.Contains(cmd, "KUBECONFIG") {
		t.Fatalf("unexpected cred export with no creds staged: %q", cmd)
	}
}

func TestRemoteAgentCmdExportsGitConfig(t *testing.T) {
	cmd := remoteAgentCmd([]string{"claude"}, "", true, ".gitconfig", true, false)
	if !strings.Contains(cmd, "export GIT_CONFIG_GLOBAL=~/.safeslop-runtime/.gitconfig GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("GIT_CONFIG_GLOBAL export missing/wrong: %q", cmd)
	}
	if !strings.Contains(cmd, "export GIT_SSH_COMMAND='ssh -F ~/.safeslop-runtime/.ssh/config.vm'") {
		t.Fatalf("GIT_SSH_COMMAND export missing/wrong: %q", cmd)
	}
	// scp does not preserve 0600; tighten every staged private key before git-over-ssh uses it.
	if !strings.Contains(cmd, "find ~/.safeslop-runtime/.ssh -type f -name 'id_*' -exec chmod 600 {} \\;") {
		t.Fatalf("missing chmod of scp'd private keys: %q", cmd)
	}
	// Exports precede the secrets-source + exec.
	if strings.Index(cmd, "export GIT_CONFIG_GLOBAL") > strings.LastIndex(cmd, "; exec") {
		t.Fatalf("export must come before exec: %q", cmd)
	}
	if strings.Contains(cmd, "KUBECONFIG") {
		t.Fatalf("kube export leaked with only git config staged: %q", cmd)
	}
}

func TestRemoteAgentCmdExportsKubeconfig(t *testing.T) {
	cmd := remoteAgentCmd([]string{"claude"}, "", false, "", false, true)
	if !strings.Contains(cmd, "export KUBECONFIG=~/.safeslop-runtime/kubeconfig") {
		t.Fatalf("KUBECONFIG export missing: %q", cmd)
	}
	if strings.Contains(cmd, "GIT_SSH_COMMAND") {
		t.Fatalf("ssh export leaked with only kube staged: %q", cmd)
	}
}
