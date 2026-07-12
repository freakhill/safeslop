package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/exec"
)

// detectRuntime is a test seam. Production uses runtime.Detect; unit tests must not invoke an
// ambient Docker/OrbStack daemon merely to model runtime unavailability.
var detectRuntime = runtime.Detect

// policyFromNetwork maps a profile's `network:` field to the runtime.Detect egress posture. Anything
// other than the explicit "allow" is the safe default (deny), which makes Detect fail-close on an
// egress-unverified rootless runtime (specs/0066 D6).
func policyFromNetwork(network string) runtime.NetworkPolicy {
	if network == "allow" {
		return runtime.PolicyAllow
	}
	return runtime.PolicyDeny
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
//
// A bare entry covered by a wildcard entry (`.example.com`, squid's dstdomain
// "domain and all subdomains" form) is dropped: squid FATALs when its dstdomain
// ACL holds both a wildcard `.D` and a domain equal-to-or-under D
// ("'.github.com' is a subdomain of 'github.com'"), which crash-loops the proxy
// and takes all egress down. The T7 CredsEgress union (specs/0069) returns bare
// `github.com`/`codeload.github.com`/`objects.githubusercontent.com`, each already
// covered by a base wildcard, so before this they collided the moment a github-creds
// profile ran (specs/0073). Wildcard entries are left untouched — squid tolerates a
// wildcard nested under a wildcard (`.raw.githubusercontent.com` under
// `.githubusercontent.com`), so the base list's own nesting is preserved. Dropping a
// covered bare entry loses no reachability: the covering wildcard already grants it.
func composeAllowlist(base []byte, extra []string) []byte {
	var all []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		all = append(all, s)
	}
	for _, l := range strings.Split(string(base), "\n") {
		add(l)
	}
	for _, e := range extra {
		add(e)
	}
	// Collect wildcard base domains (".D" => "D"), then drop any BARE entry equal to
	// or under some wildcard's D — the exact overlap squid rejects.
	var wildcards []string
	for _, s := range all {
		if strings.HasPrefix(s, ".") {
			wildcards = append(wildcards, s[1:])
		}
	}
	covered := func(bare string) bool {
		for _, w := range wildcards {
			if bare == w || strings.HasSuffix(bare, "."+w) {
				return true
			}
		}
		return false
	}
	lines := make([]string, 0, len(all))
	for _, s := range all {
		if !strings.HasPrefix(s, ".") && covered(s) {
			continue
		}
		lines = append(lines, s)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// provision materializes the per-run runtime dir and starts the compose stack, returning the
// interactive argv that runs the agent (`docker compose run --rm agent <argv>`) plus the compose
// file path (for teardown).
// secretEnv is written to secrets.env and sourced by the entrypoint.
func provision(ctx context.Context, sessionID string, agentArgv []string, workspace, network string, egress []string, secretEnv []string, stageDir string, enabled []string) (argv []string, composeFile string, eng runtime.Engine, err error) {
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

	// Detect the ambient container runtime (docker → podman → lima) and drive it; safeslop never
	// provisions one (specs/0066 D3). The deny-tier fail-closed egress gate lives inside Detect, so a
	// network:deny profile cannot launch on an egress-unverified rootless runtime (podman/lima) without an
	// explicit opt-in — Detect returns an actionable error instead.
	eng, err = detectRuntime(policyFromNetwork(network))
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
		SessionID:     sessionID,
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
// of host `ps` and `docker inspect`. stageDir is the host stage dir (under the user cache dir,
// outside the agent-writable workspace — specs/0072 F2; already holds .npmrc when pnpm creds were
// staged); it is bind-mounted ro at /safeslop/runtime and wiped on exit by the caller. The agent runs interactively through a PTY (design §6.2).
// enabled is the profile's resolved package identity set (specs/0058): it selects the agent
// image (ENABLE_<pkg> build args) so a profile gets exactly the tools it declared.
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, egress []string, secretEnv []string, stageDir string, enabled []string) (int, error) {
	argv, _, _, err := provision(ctx, SessionIDFromStageDir(stageDir), spec.Argv, workspace, network, egress, secretEnv, stageDir, enabled)
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
