package runtime

import (
	"context"
	"os/exec"
)

// Engine runs container subcommands (run/build/compose/ps) for the detected ambient runtime. The
// container tier invokes the engine instead of a hardcoded `docker`, so the same tier code drives
// docker, podman, or a user-managed lima. Command returns a ready *exec.Cmd so the caller wires its own
// stdio (streaming for `compose up`, captured for `ps`). Every engine is zero-config (built from PATH,
// no pinned paths), so runtime.Detect can reconstruct the right one anywhere — run, `down`, or the sweep.
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
type HostDockerEngine struct{}

func (HostDockerEngine) Name() string { return "docker" }

func (HostDockerEngine) Argv(args ...string) []string { return append([]string{"docker"}, args...) }

// InternalNetwork is "" — rootful docker honors compose's inline `internal: true`.
func (HostDockerEngine) InternalNetwork() string { return "" }

func (e HostDockerEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	argv := e.Argv(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

// PodmanEngine runs `podman <args>` / `podman compose <args>` against the user's ambient podman.
// safeslop neither installs nor manages it. Rootless podman uses RootlessKit + pasta/slirp4netns, so
// compose's inline `internal: true` does NOT isolate egress (same failure class as rootless nerdctl);
// the agent must join a pre-created `--internal` network instead, hence a non-empty InternalNetwork().
type PodmanEngine struct{}

func (PodmanEngine) Name() string { return "podman" }

func (PodmanEngine) Argv(args ...string) []string { return append([]string{"podman"}, args...) }

// InternalNetwork is the pre-created `--internal` network name — rootless podman does not honor
// compose's inline internal:true, so the agent must join a real --internal network to have no egress.
func (PodmanEngine) InternalNetwork() string { return internalNetworkName }

func (e PodmanEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	argv := e.Argv(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

// LimaEngine runs `lima nerdctl <args>` against the user's OWN default lima instance — no LIMA_HOME
// override, no pinned limactl, no VM boot by safeslop. Rootless nerdctl does not honor compose's inline
// internal:true either (validated 2026-06-22), so InternalNetwork() is the pre-created `--internal`
// network name.
type LimaEngine struct{}

func (LimaEngine) Name() string { return "lima" }

// Argv is `lima nerdctl <args>` — `lima` shells into the user's default instance and runs nerdctl there.
func (LimaEngine) Argv(args ...string) []string { return append([]string{"lima", "nerdctl"}, args...) }

// InternalNetwork is the pre-created `--internal` network name — rootless nerdctl does not honor
// compose's inline internal:true, so the agent must join a real --internal network to have no egress.
func (LimaEngine) InternalNetwork() string { return internalNetworkName }

func (e LimaEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	argv := e.Argv(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}
