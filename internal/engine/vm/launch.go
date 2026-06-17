package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/freakhill/agentic_tactical_boots/internal/engine/exec"
)

// Launch clones+boots a disposable session VM, copies the staged dir in, runs the agent over
// ssh -t (sourcing secrets remotely), and destroys the VM on exit. secretEnv (resolved profile
// secrets) is written to secrets.env in stageDir; the whole stageDir is scp'd to ~/.slop-runtime.
// network "deny" requires SLOP_VM_PROXY_URL (advisory egress); "allow" is full VM network.
func Launch(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (int, error) {
	if !Available() {
		return 1, fmt.Errorf("vm environment requires tart (Apple-Silicon macOS) — run: slop doctor")
	}
	if len(agentArgv) == 0 {
		return 1, exec.ErrNoArgv
	}
	proxyURL := ""
	if network == "deny" {
		proxyURL = os.Getenv("SLOP_VM_PROXY_URL")
		if proxyURL == "" {
			return 1, fmt.Errorf("network:%q needs SLOP_VM_PROXY_URL (a squid/proxy URL); set it or use network:\"allow\"", network)
		}
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return 1, err
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return 1, err
	}
	if err := EnsureBase(ctx); err != nil {
		return 1, err
	}
	_ = Reconcile(ctx, profile) // reclaim an orphaned session from a prior crash
	ip, err := CloneAndBoot(ctx, profile)
	if err != nil {
		return 1, err
	}
	defer func() { _ = Destroy(context.Background(), profile) }() // disposable: always tear down

	if err := provisionToolchain(ctx, ip, toolchainKind); err != nil {
		return 1, err
	}
	if err := runScp(ctx, ip, stageDir, "~/.slop-runtime"); err != nil {
		return 1, err
	}
	remote := remoteAgentCmd(agentArgv, proxyURL)
	inner := exec.LaunchSpec{Argv: sshArgv(ip, true, "zsh", "-lc", remote)}
	return exec.RunInTerminal(ctx, inner)
}

func runScp(ctx context.Context, ip, src, dst string) error {
	if err := osCommand(ctx, scpArgv(ip, src, dst)).Run(); err != nil {
		return fmt.Errorf("scp stage into vm: %w", err)
	}
	return nil
}

// writeSecretsEnv writes shell-escaped KEY='VAL' lines (0600) to stageDir/secrets.env.
func writeSecretsEnv(stageDir string, secretEnv []string) (string, error) {
	if len(secretEnv) == 0 {
		return "", nil
	}
	var b []byte
	for _, kv := range secretEnv {
		eq := indexByte(kv, '=')
		if eq < 0 {
			continue
		}
		b = append(b, kv[:eq+1]...)
		b = append(b, shellQuote(kv[eq+1:])...)
		b = append(b, '\n')
	}
	p := filepath.Join(stageDir, "secrets.env")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
