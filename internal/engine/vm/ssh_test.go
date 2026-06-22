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
	cmd := remoteAgentCmd([]string{"claude", "--flag with space"}, "", false, false)
	if !strings.Contains(cmd, ". ~/.safeslop-runtime/secrets.env") || !strings.HasPrefix(cmd, "set -a;") {
		t.Fatalf("missing secrets sourcing: %q", cmd)
	}
	if !strings.Contains(cmd, `exec 'claude' '--flag with space'`) {
		t.Fatalf("agent argv not quoted: %q", cmd)
	}
	if !strings.Contains(remoteAgentCmd([]string{"zsh"}, "http://p:3128", false, false), "export HTTP_PROXY='http://p:3128'") {
		t.Fatal("proxy export missing when proxyURL set")
	}
	// No staged path-creds → no GIT_SSH_COMMAND / KUBECONFIG exports.
	if strings.Contains(cmd, "GIT_SSH_COMMAND") || strings.Contains(cmd, "KUBECONFIG") {
		t.Fatalf("unexpected cred export with no creds staged: %q", cmd)
	}
}

func TestRemoteAgentCmdExportsSSHKey(t *testing.T) {
	cmd := remoteAgentCmd([]string{"claude"}, "", true, false)
	// The ssh option string must mirror compose.yml.tmpl (pinned known_hosts, IdentitiesOnly, no agent).
	want := "export GIT_SSH_COMMAND='ssh -i ~/.safeslop-runtime/.ssh/id -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=yes -o UserKnownHostsFile=~/.safeslop-runtime/.ssh/known_hosts'"
	if !strings.Contains(cmd, want) {
		t.Fatalf("GIT_SSH_COMMAND export missing/wrong: %q", cmd)
	}
	// scp does not preserve 0600; the key must be re-tightened before ssh uses it.
	if !strings.Contains(cmd, "chmod 600 ~/.safeslop-runtime/.ssh/id") {
		t.Fatalf("missing chmod of scp'd private key: %q", cmd)
	}
	// Exports precede the secrets-source + exec.
	if strings.Index(cmd, "GIT_SSH_COMMAND") > strings.Index(cmd, "exec") {
		t.Fatalf("export must come before exec: %q", cmd)
	}
	if strings.Contains(cmd, "KUBECONFIG") {
		t.Fatalf("kube export leaked with only ssh staged: %q", cmd)
	}
}

func TestRemoteAgentCmdExportsKubeconfig(t *testing.T) {
	cmd := remoteAgentCmd([]string{"claude"}, "", false, true)
	if !strings.Contains(cmd, "export KUBECONFIG=~/.safeslop-runtime/kubeconfig") {
		t.Fatalf("KUBECONFIG export missing: %q", cmd)
	}
	if strings.Contains(cmd, "GIT_SSH_COMMAND") {
		t.Fatalf("ssh export leaked with only kube staged: %q", cmd)
	}
}
