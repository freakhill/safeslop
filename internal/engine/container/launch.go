package container

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/install"
)

// preferLimaBackend reports whether the user opted into the safeslop-managed lima container runtime
// (SAFESLOP_CONTAINER_BACKEND=lima). Unset → use the ambient host docker when present (today's
// behaviour); lima is never an automatic fallback, so it is never a surprise VM boot.
func preferLimaBackend() bool {
	return os.Getenv("SAFESLOP_CONTAINER_BACKEND") == "lima"
}

// confirmLimaBlastRadius is the first-run consent gate (specs/0044 Phase 4.2): before safeslop first
// boots the managed lima VM it itemises the blast radius and requires a typed confirmation. Fails closed
// when stdin is not a terminal (booting a VM unattended without consent is exactly what this prevents).
func confirmLimaBlastRadius(lb *runtime.LimaBackend) error {
	fmt.Fprintln(os.Stderr, lb.BlastRadius())
	if fi, _ := os.Stdin.Stat(); fi == nil || fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("the lima container VM needs first-run consent; run `safeslop run` interactively once to approve it")
	}
	fmt.Fprint(os.Stderr, "\nType 'boot the vm' to proceed (anything else aborts): ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != "boot the vm" {
		return fmt.Errorf("aborted: lima VM consent not given")
	}
	return lb.RecordConsent()
}

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
		"allowlist.domains": composeAllowlist(allow, p.Egress),
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

// composeAllowlist appends extra domains (the agent's built-in providers + the profile's
// egress, already unioned by the caller) to the base allowlist asset, de-duplicating while
// preserving order (base first, then first-seen extras). Empty/whitespace entries are
// dropped. This is the per-run effective container egress allowlist (specs/0046).
func composeAllowlist(base []byte, extra []string) []byte {
	var lines []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		lines = append(lines, s)
	}
	for _, l := range strings.Split(string(base), "\n") {
		add(l)
	}
	for _, e := range extra {
		add(e)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// provision materializes the per-run runtime dir and starts the compose stack, returning the
// interactive argv that runs the agent (`docker compose run --rm agent <argv>`) plus the compose
// file path (for teardown).
// secretEnv is written to secrets.env and sourced by the entrypoint.
func provision(ctx context.Context, agentArgv []string, workspace, network string, egress []string, secretEnv []string, stageDir string, enabled []string) (argv []string, composeFile string, eng runtime.Engine, err error) {
	if len(agentArgv) == 0 {
		return nil, "", nil, exec.ErrNoArgv
	}
	if agentArgv[0] == "nix" {
		return nil, "", nil, fmt.Errorf("toolchain:nix is not supported in environment:container yet (read-only container vs writable /nix store); use environment:vm or host, or toolchain:mise")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, "", nil, err
	}
	_ = withRepoLock(workspace, func() error { return Reconcile(ctx, workspace, time.Hour) })
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}

	// Select + bring up the container engine. Default: the ambient host docker (unchanged behaviour).
	// SAFESLOP_CONTAINER_BACKEND=lima opts into the safeslop-managed, pinned, rootless lima VM; lima is
	// also the fallback when no host docker is present. The lima VM mounts the workspace (writable) so the
	// agent's edits land on the host repo (specs/0044 Phase 4.1). Backend.Ensure fails closed if its
	// engine is unavailable (no docker / no limactl).
	dirs, err := install.DefaultDirs()
	if err != nil {
		return nil, "", nil, err
	}
	// Lima is OPT-IN, never an automatic fallback: without the opt-in we use the ambient host docker
	// (and surface the unchanged "needs docker" error if it is absent), so enabling the managed VM is
	// always a deliberate choice, never a surprise boot.
	var backend runtime.Backend = &runtime.SystemBackend{}
	if preferLimaBackend() {
		lb := runtime.NewLimaBackend(dirs)
		if lb.NeedsConsent() { // first-run blast-radius gate (Phase 4.2)
			if cerr := confirmLimaBlastRadius(lb); cerr != nil {
				return nil, "", nil, cerr
			}
		}
		backend = lb
	}
	eng, err = backend.Ensure(ctx, workspace, func(s string) { fmt.Fprintln(os.Stderr, "safeslop: "+s) })
	if err != nil {
		return nil, "", nil, err
	}
	// Egress isolation: the agent's no-egress network. Host docker honours compose's inline internal:true;
	// lima/rootless-nerdctl does NOT, so the engine names a `--internal` network we pre-create here (before
	// compose up) and the compose references it as external (validated 2026-06-22).
	internalNet := eng.InternalNetwork()
	if internalNet != "" {
		_ = runEngine(ctx, eng, "network", "create", "--internal", internalNet) // idempotent; ignore "exists"
	}

	if _, err := writeSecretsEnv(stageDir, secretEnv); err != nil {
		return nil, "", nil, err
	}
	_, npmErr := os.Stat(filepath.Join(stageDir, ".npmrc"))
	_, kubeErr := os.Stat(filepath.Join(stageDir, "kubeconfig"))
	gitConfigName := ".gitconfig"
	if _, err := os.Stat(filepath.Join(stageDir, ".gitconfig.container")); err == nil {
		gitConfigName = ".gitconfig.container"
	}
	_, gitConfigErr := os.Stat(filepath.Join(stageDir, gitConfigName))
	_, gitSSHConfigErr := os.Stat(filepath.Join(stageDir, ".ssh", "config.container"))
	_, toolsImg, _, err := agentImageTags(enabled)
	if err != nil {
		return nil, "", nil, err
	}
	p := composeParams{
		RuntimeDir:    stageDir,
		Workspace:     workspace,
		StageDir:      stageDir,
		AgentImage:    toolsImg,
		NpmConfig:     npmErr == nil,
		Kubeconfig:    kubeErr == nil,
		GitConfig:     gitConfigErr == nil,
		GitConfigPath: "/safeslop/runtime/" + gitConfigName,
		GitSSHConfig:  gitSSHConfigErr == nil,
		OpenEgress:    network == "allow",
		InternalNet:   internalNet,
		Egress:        egress,
	}
	composeFile, err = materializeRun(p, network == "allow")
	if err != nil {
		return nil, "", nil, err
	}
	if err := Up(ctx, eng, stageDir, composeFile, enabled); err != nil {
		return nil, "", nil, err
	}
	return composeRunArgv(eng, composeFile, agentArgv), composeFile, eng, nil
}

// Launch runs spec.Argv in the agent container. secretEnv (the resolved profile secrets) is
// written to secrets.env and sourced by the entrypoint — never passed via -e, so it stays out
// of host `ps` and `docker inspect`. stageDir is the host .safeslop/runtime/<profile> dir (already
// holds .npmrc when pnpm creds were staged); it is bind-mounted ro at /safeslop/runtime and wiped
// on exit by the caller. The agent runs interactively through a PTY (design §6.2).
// enabled is the profile's resolved package identity set (specs/0058): it selects the agent
// image (ENABLE_<pkg> build args) so a profile gets exactly the tools it declared.
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, egress []string, secretEnv []string, stageDir string, enabled []string) (int, error) {
	argv, _, _, err := provision(ctx, spec.Argv, workspace, network, egress, secretEnv, stageDir, enabled)
	if err != nil {
		return 1, err
	}
	// A detached supervisor passes a PTY slave it owns (spec.Stdin set). `compose
	// run` already allocates the container's tty when its stdin is a tty, so binding
	// the compose process's stdio directly to that PTY (RunInTerminal) bridges the
	// container's tty straight to the supervisor's PTY — single hop, docker forwards
	// resize. The coupled path (no stdio) keeps RunInPTY, which owns the host pty and
	// puts the user's real terminal in raw mode + forwards SIGWINCH (specs/0051).
	if spec.Stdin != nil {
		return exec.RunInTerminal(ctx, exec.LaunchSpec{Argv: argv, Stdin: spec.Stdin, Stdout: spec.Stdout, Stderr: spec.Stderr, ControllingTTY: true})
	}
	return exec.RunInPTY(ctx, exec.LaunchSpec{Argv: argv})
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
