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

// remoteAgentCmd returns the zsh -lc argument that sources the staged secrets (if present) then
// execs the agent. Each agent arg is single-quote escaped so values with spaces survive the
// remote shell. proxyURL != "" prepends HTTP(S)_PROXY exports (advisory egress for network=deny).
func remoteAgentCmd(agentArgv []string, proxyURL string) string {
	var b strings.Builder
	if proxyURL != "" {
		p := shellQuote(proxyURL)
		b.WriteString("export HTTP_PROXY=" + p + " HTTPS_PROXY=" + p + " http_proxy=" + p + " https_proxy=" + p + "; ")
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
