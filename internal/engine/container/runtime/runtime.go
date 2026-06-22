// Package runtime is safeslop's container-runtime provider seam (specs/0043/0044). The container tier
// (internal/engine/container) runs `docker compose` and today ASSUMES a docker daemon is already on
// PATH; a Backend instead PROVIDES that daemon — a SystemBackend defers to an ambient OrbStack/Docker
// Desktop the user already runs (never installs/removes it), while LimaBackend boots a pinned, rootless,
// hardened vz Linux VM and hands back a docker-compatible socket. The thin interface keeps the
// tart-Linux "north star" a one-impl migration rather than a rewrite (specs/0043 graft #2).
package runtime

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/freakhill/safeslop/internal/engine/install"
)

// Backend provides a container engine for the container tier. SystemBackend hands back a HostDockerEngine
// (the ambient docker the user already runs); LimaBackend boots a pinned, rootless, hardened vz Linux VM
// and hands back a LimaNerdctlEngine that runs nerdctl inside it. The engine in the VM is rootless and the
// socket lives inside the guest — the host's /var/run/docker.sock is never used or exposed (specs/0043).
type Backend interface {
	// Name identifies the backend ("system" | "lima").
	Name() string
	// Ensure idempotently brings the engine up and returns an Engine the container tier runs commands
	// through (host docker vs in-guest rootless nerdctl).
	Ensure(ctx context.Context, emit func(string)) (Engine, error)
	// Teardown stops the engine/VM this backend started (no-op for SystemBackend).
	Teardown(ctx context.Context) error
	// Pins are the Path A artifacts this backend needs installed before Ensure (nil for SystemBackend).
	// These are installed on demand — gated at first container start — NOT folded into the base
	// install.DesiredState() that `install apply` runs for every user.
	Pins() []install.Pin
	// StateDir is the single directory holding this backend's mutable state, for the uninstall receipt
	// ("" for SystemBackend, which owns nothing).
	StateDir() string
}

// Runner runs an argv and returns combined output + exit code; injected so unit tests never boot a VM.
type Runner func(ctx context.Context, argv []string) (output string, exitCode int, err error)

func defaultRunner(ctx context.Context, argv []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code, err = ee.ExitCode(), nil
		} else {
			code = -1
		}
	}
	return buf.String(), code, err
}

// Select chooses the backend: lima when explicitly preferred or when no ambient docker is present;
// otherwise the SystemBackend (use what the user already runs). The container tier asks for lima when a
// profile opts into safeslop-managed containers; SystemBackend keeps today's behaviour for users on
// OrbStack/Docker Desktop.
func Select(preferLima bool, dirs install.Dirs) Backend {
	if !preferLima && systemDockerAvailable() {
		return &SystemBackend{}
	}
	return NewLimaBackend(dirs)
}
