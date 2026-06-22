package runtime

import (
	"os/exec"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/receipt"
)

// Non-touch (specs/0042/0044): when safeslop provisions its own lima runtime, any container engine the
// user ALREADY runs (Docker Desktop, OrbStack, a hand-installed daemon) is left strictly untouched —
// never managed-installed, never uninstalled. It is recorded as negative provenance so a later uninstall
// can explain why it was left in place. There is no flag that overrides this.

// unmanagedRuntimes returns the container runtimes present on the host that safeslop did NOT install,
// keyed by name -> path. Docker is read from the live install.State probe; OrbStack is detected by its
// CLI on PATH (it is not in the install probe set).
func unmanagedRuntimes(st install.State) map[string]string {
	out := map[string]string{}
	for _, t := range st.Runtimes {
		if t.Name == "docker" && t.Present {
			path := t.Path
			if path == "" {
				path = "docker"
			}
			out["docker"] = path
		}
	}
	if p, err := exec.LookPath("orb"); err == nil {
		out["orbstack"] = p
	}
	return out
}

// noteUnmanaged records every present-but-unmanaged runtime into the receipt store's negative-provenance
// map, so uninstall surfaces them as untouched. Recording is best-effort additive; it never installs or
// removes the foreign runtime.
func noteUnmanaged(store *receipt.Store, st install.State) error {
	for tool, path := range unmanagedRuntimes(st) {
		if err := store.NoteUnmanaged(tool, path); err != nil {
			return err
		}
	}
	return nil
}
