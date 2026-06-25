package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/freakhill/safeslop/internal/engine/exec"
)

// provision boots a disposable session VM, provisions its toolchain, scp's the staged dir in,
// and returns the `ssh -t` argv that runs the agent remotely (sourcing secrets.env over ssh).
// Shared by Launch (safeslop run) and PrepareSession (the embedded cockpit). On any failure after the
// VM boots, provision destroys it so no VM leaks; on success the caller owns teardown (Destroy +
// stage wipe). network "deny" requires SAFESLOP_VM_PROXY_URL; "allow" is full VM network.
func provision(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (argv []string, err error) {
	if !Available() {
		return nil, fmt.Errorf("vm environment requires tart (Apple-Silicon macOS) — run: safeslop doctor")
	}
	if len(agentArgv) == 0 {
		return nil, exec.ErrNoArgv
	}
	proxyURL := ""
	if network == "deny" {
		proxyURL = os.Getenv("SAFESLOP_VM_PROXY_URL")
		if proxyURL == "" {
			return nil, fmt.Errorf("network:%q needs SAFESLOP_VM_PROXY_URL (a squid/proxy URL); set it or use network:\"allow\"", network)
		}
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return nil, err
	}
	if err := EnsureBase(ctx); err != nil {
		return nil, err
	}
	_ = Reconcile(ctx, profile) // reclaim an orphaned session from a prior crash
	ip, err := CloneAndBoot(ctx, profile)
	if err != nil {
		return nil, err
	}
	if err := provisionToolchain(ctx, ip, toolchainKind); err != nil {
		_ = Destroy(context.Background(), profile)
		return nil, err
	}
	if err := runScp(ctx, ip, stageDir, "~/.safeslop-runtime"); err != nil {
		_ = Destroy(context.Background(), profile)
		return nil, err
	}
	// Detect staged path-creds the same way the container path does (container/launch.go): their
	// presence flips the matching guest-side env export. StageSSH/StageKube wrote these into
	// stageDir before Launch, and runScp copied the whole tree to ~/.safeslop-runtime.
	gitConfigName := ".gitconfig"
	if _, err := os.Stat(filepath.Join(stageDir, ".gitconfig.vm")); err == nil {
		gitConfigName = ".gitconfig.vm"
	}
	_, gitConfigErr := os.Stat(filepath.Join(stageDir, gitConfigName))
	_, gitSSHConfigErr := os.Stat(filepath.Join(stageDir, ".ssh", "config.vm"))
	_, kubeErr := os.Stat(filepath.Join(stageDir, "kubeconfig"))
	remote := remoteAgentCmd(agentArgv, proxyURL, gitConfigErr == nil, gitConfigName, gitSSHConfigErr == nil, kubeErr == nil)
	return sshArgv(ip, true, "zsh", "-lc", remote), nil
}

// Launch clones+boots a disposable session VM, copies the staged dir in, runs the agent over
// ssh -t (sourcing secrets remotely), and destroys the VM on exit. secretEnv (resolved profile
// secrets) is written to secrets.env in stageDir; the whole stageDir is scp'd to ~/.safeslop-runtime.
// network "deny" requires SAFESLOP_VM_PROXY_URL (advisory egress); "allow" is full VM network.
func Launch(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (int, error) {
	argv, err := provision(ctx, agentArgv, network, secretEnv, stageDir, profile, toolchainKind)
	if err != nil {
		return 1, err
	}
	defer func() { _ = Destroy(context.Background(), profile) }() // disposable: always tear down
	return exec.RunInTerminal(ctx, exec.LaunchSpec{Argv: argv})
}

// PrepareSession provisions a disposable VM for an embedded-cockpit session (SP7c-2): it returns
// the `ssh -t` argv to run on the engine's PTY plus a cleanup that destroys the VM and wipes the
// stage when the session closes. Cockpit sessions pass secretEnv nil (inherited-host-env parity
// with SP7c-1). cleanup is always non-nil and safe to call once.
func PrepareSession(ctx context.Context, agentArgv []string, network string, secretEnv []string, stageDir, profile, toolchainKind string) (argv []string, cleanup func(), err error) {
	argv, err = provision(ctx, agentArgv, network, secretEnv, stageDir, profile, toolchainKind)
	if err != nil {
		return nil, func() {}, err
	}
	return argv, func() {
		_ = Destroy(context.Background(), profile)
		_ = os.RemoveAll(stageDir)
	}, nil
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
