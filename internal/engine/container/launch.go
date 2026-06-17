package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freakhill/agentic_tactical_boots/internal/engine/exec"
)

// materializeRun writes the per-run runtime dir (squid.conf, allowlist.domains, compose.yml,
// entrypoint.sh, and a .slop-stage marker for the reconcile sweep) and returns the compose
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
		".slop-stage":       nil,
	}
	for name, content := range files {
		if werr := os.WriteFile(filepath.Join(dir, name), content, 0o600); werr != nil {
			return "", werr
		}
	}
	return filepath.Join(dir, "compose.yml"), nil
}

// Launch runs spec.Argv in the agent container. secretEnv (the resolved profile secrets) is
// written to secrets.env and sourced by the entrypoint — never passed via -e, so it stays out
// of host `ps` and `docker inspect`. stageDir is the host .slop/runtime/<profile> dir (already
// holds .npmrc when pnpm creds were staged); it is bind-mounted ro at /slop/runtime and wiped
// on exit by the caller. The agent runs interactively through a PTY (design §6.2).
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, secretEnv []string, stageDir string) (int, error) {
	if !Available() {
		return 1, fmt.Errorf("container environment requires docker + docker compose v2 (run: slop doctor)")
	}
	if len(spec.Argv) == 0 {
		return 1, exec.ErrNoArgv
	}
	if spec.Argv[0] == "nix" {
		return 1, fmt.Errorf("toolchain:nix is not supported in environment:container yet (read-only container vs writable /nix store); use environment:vm or host, or toolchain:mise")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return 1, err
	}
	_ = withRepoLock(workspace, func() error { return Reconcile(ctx, workspace, time.Hour) })
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}
	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return 1, err
	}
	_, npmErr := os.Stat(filepath.Join(stageDir, ".npmrc"))
	p := composeParams{
		RuntimeDir:  stageDir,
		Workspace:   workspace,
		StageDir:    stageDir,
		SSHAuthSock: os.Getenv("SSH_AUTH_SOCK"),
		Term:        os.Getenv("TERM"),
		NpmConfig:   npmErr == nil,
	}
	composeFile, err := materializeRun(p, network == "allow")
	if err != nil {
		return 1, err
	}
	if err := Up(ctx, stageDir, composeFile); err != nil {
		return 1, err
	}
	inner := exec.LaunchSpec{Argv: composeRunArgv(composeFile, spec.Argv)}
	return exec.RunInPTY(ctx, inner)
}

// ComposeForDown writes a throwaway runtime dir + compose file so `slop down` can target
// `docker compose down` without a live run's dir (the agent container is --rm; only squid
// persists). Caller removes the returned dir.
func ComposeForDown() (dir, composeFile string, err error) {
	dir, err = os.MkdirTemp("", "slop-down-*")
	if err != nil {
		return "", "", err
	}
	cf, err := materializeRun(composeParams{RuntimeDir: dir, Workspace: "/", StageDir: dir}, false)
	if err != nil {
		return "", "", err
	}
	return dir, cf, nil
}
