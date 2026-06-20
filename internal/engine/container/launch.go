package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freakhill/safeslop/internal/engine/exec"
)

// materializeRun writes the per-run runtime dir (squid.conf, allowlist.domains, compose.yml,
// entrypoint.sh, and a .safeslop-stage marker for the reconcile sweep) and returns the compose
// file path. dir is p.RuntimeDir, which is also the bind-mounted stage dir.
func materializeRun(p composeParams, open bool) (string, error) {
	dir := p.RuntimeDir
	squid, err := RenderSquidConf(open)
	if err != nil {
		return "", err
	}
	allow, err := readAsset("allowlist.domains")
	if err != nil {
		return "", err
	}
	yml, err := renderCompose(p)
	if err != nil {
		return "", err
	}
	if err := writeEntrypoint(dir); err != nil {
		return "", err
	}
	files := map[string][]byte{
		"squid.conf":        []byte(squid),
		"allowlist.domains": allow,
		"compose.yml":       []byte(yml),
		".safeslop-stage":   nil,
	}
	for name, content := range files {
		if werr := os.WriteFile(filepath.Join(dir, name), content, 0o600); werr != nil {
			return "", werr
		}
	}
	return filepath.Join(dir, "compose.yml"), nil
}

// provision materializes the per-run runtime dir and starts the compose stack, returning the
// interactive argv that runs the agent (`docker compose run --rm agent <argv>`) plus the compose
// file path (for teardown). Shared by Launch (safeslop run) and PrepareSession (the embedded cockpit).
// secretEnv is written to secrets.env and sourced by the entrypoint; SP7c-2 cockpit sessions pass
// nil (inherited-host-env parity with SP7c-1; full staging is a separate deferred unit).
func provision(ctx context.Context, agentArgv []string, workspace, network string, secretEnv []string, stageDir string) (argv []string, composeFile string, err error) {
	if !Available() {
		return nil, "", fmt.Errorf("container environment requires docker + docker compose v2 (run: safeslop doctor)")
	}
	if len(agentArgv) == 0 {
		return nil, "", exec.ErrNoArgv
	}
	if agentArgv[0] == "nix" {
		return nil, "", fmt.Errorf("toolchain:nix is not supported in environment:container yet (read-only container vs writable /nix store); use environment:vm or host, or toolchain:mise")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, "", err
	}
	_ = withRepoLock(workspace, func() error { return Reconcile(ctx, workspace, time.Hour) })
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return nil, "", err
	}
	_, npmErr := os.Stat(filepath.Join(stageDir, ".npmrc"))
	_, kubeErr := os.Stat(filepath.Join(stageDir, "kubeconfig"))
	_, sshErr := os.Stat(filepath.Join(stageDir, ".ssh", "id"))
	p := composeParams{
		RuntimeDir: stageDir,
		Workspace:  workspace,
		StageDir:   stageDir,
		Term:       os.Getenv("TERM"),
		NpmConfig:  npmErr == nil,
		Kubeconfig: kubeErr == nil,
		SshKey:     sshErr == nil,
		OpenEgress: network == "allow",
	}
	composeFile, err = materializeRun(p, network == "allow")
	if err != nil {
		return nil, "", err
	}
	if err := Up(ctx, stageDir, composeFile); err != nil {
		return nil, "", err
	}
	return composeRunArgv(composeFile, agentArgv), composeFile, nil
}

// Launch runs spec.Argv in the agent container. secretEnv (the resolved profile secrets) is
// written to secrets.env and sourced by the entrypoint — never passed via -e, so it stays out
// of host `ps` and `docker inspect`. stageDir is the host .safeslop/runtime/<profile> dir (already
// holds .npmrc when pnpm creds were staged); it is bind-mounted ro at /safeslop/runtime and wiped
// on exit by the caller. The agent runs interactively through a PTY (design §6.2).
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, secretEnv []string, stageDir string) (int, error) {
	argv, _, err := provision(ctx, spec.Argv, workspace, network, secretEnv, stageDir)
	if err != nil {
		return 1, err
	}
	return exec.RunInPTY(ctx, exec.LaunchSpec{Argv: argv})
}

// PrepareSession provisions the agent container for an embedded-cockpit session (SP7c-2): it
// returns the interactive argv to run on the engine's PTY plus a cleanup that tears the stack
// down (compose down + stageDir wipe) when the session closes. Cockpit sessions pass secretEnv
// nil (inherited-host-env parity with SP7c-1). cleanup is always non-nil and safe to call once.
func PrepareSession(ctx context.Context, agentArgv []string, workspace, network string, secretEnv []string, stageDir string) (argv []string, cleanup func(), err error) {
	argv, composeFile, err := provision(ctx, agentArgv, workspace, network, secretEnv, stageDir)
	if err != nil {
		return nil, func() {}, err
	}
	return argv, func() {
		_ = Teardown(context.Background(), composeFile)
		_ = os.RemoveAll(stageDir)
	}, nil
}

// ComposeForDown writes a throwaway runtime dir + compose file so `safeslop down` can target
// `docker compose down` without a live run's dir (the agent container is --rm; only squid
// persists). Caller removes the returned dir.
func ComposeForDown() (dir, composeFile string, err error) {
	dir, err = os.MkdirTemp("", "safeslop-down-*")
	if err != nil {
		return "", "", err
	}
	cf, err := materializeRun(composeParams{RuntimeDir: dir, Workspace: "/", StageDir: dir}, false)
	if err != nil {
		return "", "", err
	}
	return dir, cf, nil
}
