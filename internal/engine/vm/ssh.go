package vm

import (
	"os"
	"strings"
)

func sshBaseOpts() []string {
	opts := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=5",
	}
	if key := os.Getenv("SAFESLOP_VM_SSH_KEY"); key != "" {
		opts = append(opts, "-i", key)
	}
	return opts
}

// sshArgv builds `ssh [opts] [-t] admin@ip -- <remote...>`. tty requests a remote PTY (for the
// interactive agent); ssh itself owns the local terminal (exec.RunInTerminal).
func sshArgv(ip string, tty bool, remote ...string) []string {
	a := append([]string{"ssh"}, sshBaseOpts()...)
	if tty {
		a = append(a, "-t")
	}
	a = append(a, sshUser+"@"+ip, "--")
	return append(a, remote...)
}

// scpArgv builds `scp [opts] -r <src> admin@ip:<dst>`.
func scpArgv(ip, src, dst string) []string {
	a := append([]string{"scp"}, sshBaseOpts()...)
	a = append(a, "-r", src, sshUser+"@"+ip+":"+dst)
	return a
}

// remoteAgentCmd returns the zsh -lc argument that exports any staged path-creds, sources the
// staged secrets (if present), then execs the agent. Each agent arg is single-quote escaped so
// values with spaces survive the remote shell. proxyURL != "" prepends HTTP(S)_PROXY exports
// (advisory egress for network=deny). hasSSHKey/hasKubeconfig mirror the container compose env
// (compose.yml.tmpl): the same GIT_SSH_COMMAND / KUBECONFIG, but pointed at the scp'd stage under
// the guest's ~/.safeslop-runtime instead of the bind-mount path.
func remoteAgentCmd(agentArgv []string, proxyURL string, hasSSHKey, hasKubeconfig bool) string {
	var b strings.Builder
	if proxyURL != "" {
		p := shellQuote(proxyURL)
		b.WriteString("export HTTP_PROXY=" + p + " HTTPS_PROXY=" + p + " http_proxy=" + p + " https_proxy=" + p + "; ")
	}
	if hasSSHKey {
		// scp -r does not reliably preserve the host's 0600 on the private key; ssh refuses an
		// over-permissive key ("UNPROTECTED PRIVATE KEY FILE"). Re-tighten before use. The ssh
		// option string mirrors compose.yml.tmpl exactly (pinned known_hosts, no agent); ssh
		// expands the ~ in -i / UserKnownHostsFile itself, so the guest $HOME need not be known.
		b.WriteString("chmod 700 ~/.safeslop-runtime/.ssh 2>/dev/null; chmod 600 ~/.safeslop-runtime/.ssh/id 2>/dev/null; ")
		b.WriteString("export GIT_SSH_COMMAND='ssh -i ~/.safeslop-runtime/.ssh/id -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=yes -o UserKnownHostsFile=~/.safeslop-runtime/.ssh/known_hosts'; ")
	}
	if hasKubeconfig {
		// kubectl does not expand ~, so the shell must — zsh expands the tilde in this assignment.
		b.WriteString("export KUBECONFIG=~/.safeslop-runtime/kubeconfig; ")
	}
	b.WriteString("set -a; [ -f ~/.safeslop-runtime/secrets.env ] && . ~/.safeslop-runtime/secrets.env; set +a; exec")
	for _, a := range agentArgv {
		b.WriteString(" " + shellQuote(a))
	}
	return b.String()
}

// shellQuote wraps s in single quotes, escaping embedded single quotes POSIX-style ('\”).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
