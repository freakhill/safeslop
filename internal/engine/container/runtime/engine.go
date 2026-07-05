package runtime

import (
	"context"
	"os/exec"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
)

// Engine runs container subcommands (run/build/compose/ps) for the detected ambient runtime. The
// container tier invokes the engine instead of a hardcoded `docker`, so the same tier code drives
// docker, podman, or a user-managed lima. Command returns a ready *exec.Cmd so the caller wires its own
// stdio (streaming for `compose up`, captured for `ps`). Engines returned by Detect carry the absolute
// CLI path resolved from safeslop's sanitized host PATH, so detect/probe and later execution cannot
// drift to a different binary.
type Engine interface {
	// Name is the engine's runtime name ("docker" | "podman" | "lima") — used in messages, the recorded
	// Session.Backend, and the deny-tier egress-verification gate.
	Name() string
	// Argv is the full argv for a container subcommand — used where the caller drives its own process
	// (e.g. running the agent through a PTY). For lima this includes the `lima nerdctl` wrapper.
	Argv(args ...string) []string
	// Command builds the full invocation of a container subcommand, environment included.
	Command(ctx context.Context, args ...string) *exec.Cmd
	// InternalNetwork is the name of an externally pre-created `--internal` network the compose must use
	// for the agent's no-egress network, or "" to declare `internal: true` inline. It is "" for docker
	// (rootful/VM-backed docker + OrbStack honor compose's inline internal:true) and a fixed name for
	// podman + lima (rootless RootlessKit/pasta + rootless nerdctl do NOT honor inline internal:true — the
	// agent would otherwise reach the internet directly, bypassing squid).
	InternalNetwork() string
}

// internalNetworkName is the engine-managed `--internal` network the rootless runtimes (podman, lima)
// pre-create so the agent container has no default route (its only egress is the squid proxy).
const internalNetworkName = "safeslop-internal"

// HostDockerEngine runs `docker <args>` / `docker compose <args>` on the host — today's behaviour,
// against an ambient OrbStack / Docker Desktop / any docker-compatible CLI on PATH. safeslop neither
// installs nor manages that daemon. It is the only runtime currently verified for the deny tier
// (rootful/VM-backed, so compose's inline `internal: true` truly isolates egress).
type HostDockerEngine struct{ Path string }

func (HostDockerEngine) Name() string { return "docker" }

func (e HostDockerEngine) bin() string {
	if e.Path != "" {
		return e.Path
	}
	return "docker"
}

func (e HostDockerEngine) Argv(args ...string) []string { return append([]string{e.bin()}, args...) }

// InternalNetwork is "" — rootful docker honors compose's inline `internal: true`.
func (HostDockerEngine) InternalNetwork() string { return "" }

func (e HostDockerEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	return command(ctx, e.Argv(args...)...)
}

// PodmanEngine runs `podman <args>` / `podman compose <args>` against the user's ambient podman.
// safeslop neither installs nor manages it. Rootless podman uses RootlessKit + pasta/slirp4netns, so
// compose's inline `internal: true` does NOT isolate egress (same failure class as rootless nerdctl);
// the agent must join a pre-created `--internal` network instead, hence a non-empty InternalNetwork().
type PodmanEngine struct{ Path string }

func (PodmanEngine) Name() string { return "podman" }

func (e PodmanEngine) bin() string {
	if e.Path != "" {
		return e.Path
	}
	return "podman"
}

func (e PodmanEngine) Argv(args ...string) []string { return append([]string{e.bin()}, args...) }

// InternalNetwork is the pre-created `--internal` network name — rootless podman does not honor
// compose's inline internal:true, so the agent must join a real --internal network to have no egress.
func (PodmanEngine) InternalNetwork() string { return internalNetworkName }

func (e PodmanEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	return command(ctx, e.Argv(args...)...)
}

// LimaEngine runs `lima nerdctl <args>` against the user's OWN default lima instance — no LIMA_HOME
// override, no pinned limactl, no VM boot by safeslop. Rootless nerdctl does not honor compose's inline
// internal:true either (validated 2026-06-22), so InternalNetwork() is the pre-created `--internal`
// network name.
type LimaEngine struct{ Path string }

func (LimaEngine) Name() string { return "lima" }

func (e LimaEngine) bin() string {
	if e.Path != "" {
		return e.Path
	}
	return "lima"
}

// Argv is `lima nerdctl <args>` — `lima` shells into the user's default instance and runs nerdctl there.
func (e LimaEngine) Argv(args ...string) []string {
	return append([]string{e.bin(), "nerdctl"}, args...)
}

// InternalNetwork is the pre-created `--internal` network name — rootless nerdctl does not honor
// compose's inline internal:true, so the agent must join a real --internal network to have no egress.
func (LimaEngine) InternalNetwork() string { return internalNetworkName }

func (e LimaEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	return command(ctx, e.Argv(args...)...)
}

func command(ctx context.Context, argv ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Path = argv[0]
	cmd.Args[0] = argv[0]
	cmd.Env = hostexec.Default().EnvFor(hostexec.EnvRuntime)
	return cmd
}

func engineWithPath(eng Engine, path string) Engine {
	switch e := eng.(type) {
	case HostDockerEngine:
		e.Path = path
		return e
	case PodmanEngine:
		e.Path = path
		return e
	case LimaEngine:
		e.Path = path
		return e
	default:
		return eng
	}
}
