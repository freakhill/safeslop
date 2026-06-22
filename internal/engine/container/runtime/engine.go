package runtime

import (
	"context"
	"fmt"
	"os/exec"
)

// Engine runs container subcommands (run/build/compose/ps) for the selected backend. The container tier
// invokes the engine instead of a hardcoded `docker`: HostDockerEngine shells `docker` on the host (the
// ambient, unchanged model used by SystemBackend), while LimaNerdctlEngine shells rootless `nerdctl`
// inside the lima guest. Command returns a ready *exec.Cmd so the caller wires its own stdio (streaming
// for `compose up`, captured for `ps`).
type Engine interface {
	// Name is the engine binary's name ("docker" | "nerdctl") — used in messages and feature checks.
	Name() string
	// Argv is the full argv for a container subcommand — used where the caller drives its own process
	// (e.g. running the agent through a PTY). For lima this includes the `limactl shell <inst> env …`
	// wrapper; the workspace + stage dirs are virtiofs-mounted at their IDENTITY paths, so the host paths
	// the tier passes resolve unchanged in the guest (validated 2026-06-22) — no path translation needed.
	Argv(args ...string) []string
	// Command builds the full invocation of a container subcommand, environment included.
	Command(ctx context.Context, args ...string) *exec.Cmd
}

// HostDockerEngine runs `docker <args>` on the host — today's behaviour, used by SystemBackend against an
// ambient OrbStack/Docker Desktop. safeslop neither installs nor manages that daemon.
type HostDockerEngine struct{}

func (HostDockerEngine) Name() string { return "docker" }

func (HostDockerEngine) Argv(args ...string) []string { return append([]string{"docker"}, args...) }

func (e HostDockerEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	argv := e.Argv(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

// LimaNerdctlEngine runs rootless `nerdctl <args>` inside a lima instance via `limactl shell`. It sets
// the guest PATH (so the staged /usr/local/bin engine is found) and XDG_RUNTIME_DIR (so the rootless
// containerd socket is found), and LIMA_HOME on the host side so limactl targets safeslop's OWNED lima
// home rather than the user's ~/.lima. The exact form was validated live on 2026-06-22.
type LimaNerdctlEngine struct {
	Limactl  string // absolute path to the pinned limactl
	Instance string // lima instance name
	UID      int    // guest uid (= host uid); selects /run/user/<uid>
	LimaHome string // LIMA_HOME — safeslop's owned StateDir
}

func (LimaNerdctlEngine) Name() string { return "nerdctl" }

// Argv is `env LIMA_HOME=… limactl shell <inst> env XDG…=… PATH=… nerdctl <args>` — the outer env points
// limactl at the owned lima home; the inner env sets the rootless guest environment. Shared by Command
// (for the tier's own stdio) and by the backend's Runner-routed readiness probe (so Ensure is testable).
func (e LimaNerdctlEngine) Argv(args ...string) []string {
	return append([]string{
		"env", "LIMA_HOME=" + e.LimaHome,
		e.Limactl, "shell", e.Instance,
		"env",
		fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", e.UID),
		"PATH=/usr/local/bin:/usr/bin:/bin:/sbin:/usr/sbin",
		"nerdctl",
	}, args...)
}

func (e LimaNerdctlEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	argv := e.Argv(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}
