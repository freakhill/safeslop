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
// (advisory egress for network=deny). hasGitConfig/hasGitSSHConfig/hasKubeconfig mirror the
// container compose env, but pointed at the scp'd stage under the guest's ~/.safeslop-runtime.
func remoteAgentCmd(agentArgv []string, proxyURL string, hasGitConfig bool, gitConfigName string, hasGitSSHConfig, hasKubeconfig bool) string {
	var b strings.Builder
	// Force a truecolor terminal in the VM guest: the agent TUIs (Ink/chalk) need TERM/COLORTERM to
	// emit 24-bit color, and the ssh session would otherwise inherit whatever (if any) the remote
	// login set. Never export LINES/COLUMNS — the PTY winsize is authoritative.
	b.WriteString("export TERM=xterm-256color COLORTERM=truecolor; ")
	if proxyURL != "" {
		p := shellQuote(proxyURL)
		b.WriteString("export HTTP_PROXY=" + p + " HTTPS_PROXY=" + p + " http_proxy=" + p + " https_proxy=" + p + "; ")
	}
	if hasGitConfig || hasGitSSHConfig {
		// scp -r does not reliably preserve the host's 0600 on private keys/token files; ssh refuses
		// over-permissive keys. Re-tighten staged credentials before git reads them.
		b.WriteString("chmod 700 ~/.safeslop-runtime/.ssh 2>/dev/null; find ~/.safeslop-runtime/.ssh -type f -name 'id_*' -exec chmod 600 {} \\; 2>/dev/null; chmod 600 ~/.safeslop-runtime/.git-pat-token 2>/dev/null; ")
	}
	if hasGitConfig {
		if gitConfigName == "" {
			gitConfigName = ".gitconfig"
		}
		b.WriteString("export GIT_CONFIG_GLOBAL=~/.safeslop-runtime/" + gitConfigName + " GIT_TERMINAL_PROMPT=0; ")
	}
	if hasGitSSHConfig {
		b.WriteString("export GIT_SSH_COMMAND='ssh -F ~/.safeslop-runtime/.ssh/config.vm'; ")
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
