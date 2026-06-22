package runtime

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/freakhill/safeslop/internal/engine/install"
)

// SystemBackend uses whatever docker engine is already on the host (OrbStack, Docker Desktop, a
// hand-installed daemon). safeslop neither installs nor removes it — it is surfaced as untouched,
// unmanaged system state (specs/0042). Ensure returns "" (use the ambient docker context).
type SystemBackend struct{}

func (*SystemBackend) Name() string { return "system" }

func (*SystemBackend) Ensure(ctx context.Context, _ string, emit func(string)) (Engine, error) {
	if !systemDockerAvailable() {
		return nil, fmt.Errorf("no docker engine on PATH; install OrbStack/Docker Desktop, or use the lima backend")
	}
	if emit != nil {
		emit("using the docker engine already on your PATH (unmanaged by safeslop)")
	}
	return HostDockerEngine{}, nil // the ambient docker context
}

func (*SystemBackend) Teardown(context.Context) error { return nil }
func (*SystemBackend) Pins() []install.Pin            { return nil }
func (*SystemBackend) StateDir() string               { return "" }

// systemDockerAvailable mirrors container.Available(): docker on PATH AND `docker compose` v2 usable.
func systemDockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "compose", "version").Run() == nil
}
