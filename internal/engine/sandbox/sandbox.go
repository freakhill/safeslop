// Package sandbox launches commands under the macOS sandbox-exec (Seatbelt)
// boundary — the first-class local boundary of the design (specs/0001 §6.2).
//
// The generated .sb profile is ported faithfully from the proven
// scripts/slop-macos-sandbox.fish generator: it builds on Apple's system.sb,
// allows the system reads a shell/binary needs, confines file writes to the
// workspace plus temp dirs, and applies a coarse network policy (deny/allow —
// sandbox-exec cannot do a URL allowlist; that is the container's job).
package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/freakhill/agentic_tactical_boots/internal/engine/exec"
)

// SandboxExecPath is the macOS Seatbelt CLI.
const SandboxExecPath = "/usr/bin/sandbox-exec"

// systemReadPaths are read-allowed so binaries, dylibs, and shell startup work.
var systemReadPaths = []string{
	"/System", "/usr", "/bin", "/sbin", "/Library",
	"/private/etc", "/etc", "/dev", "/var/db/timezone",
}

// tempPaths are read+write allowed; commands and shells need temp dirs even
// under a tight workspace scope.
var tempPaths = []string{"/tmp", "/private/tmp", "/private/var/tmp"}

// toolchainReadPaths are read-allowed so a mise/nix toolchain wrapper (mise exec / nix develop)
// can resolve its store + binaries under the seatbelt. Read-only; harmless when no toolchain is
// used. Home-relative paths are resolved at profile-render time.
func toolchainReadPaths() []string {
	paths := []string{"/nix", "/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".local", "share", "mise"),
			filepath.Join(home, ".local", "state", "mise"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	return paths
}

// Profile renders a Seatbelt profile confining writes to workspace (+ temp) and
// applying the network policy ("allow" or, by default, "deny").
func Profile(workspace, network string) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	line("(version 1)")
	line(`(import "system.sb")`)
	line("(allow process-exec)")
	line("(allow process-fork)")
	line("(allow signal (target self))")

	for _, p := range systemReadPaths {
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, escape(p)))
	}
	for _, p := range toolchainReadPaths() {
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, escape(p)))
	}
	line(`(allow file-read* (literal "/private/var/run/resolv.conf"))`)
	line(`(allow file-read* (literal "/private/var/run/utmpx"))`)

	ws := escape(workspace)
	line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, ws))
	line(fmt.Sprintf(`(allow file-write* (subpath "%s"))`, ws))

	for _, p := range tempPaths {
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, escape(p)))
		line(fmt.Sprintf(`(allow file-write* (subpath "%s"))`, escape(p)))
	}

	if network == "allow" {
		line("(allow network*)")
	} else {
		line("(deny network*)")
	}
	return b.String()
}

// escape quotes a path for inclusion in a Seatbelt double-quoted string.
func escape(p string) string {
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `"`, `\"`)
	return p
}

// Available reports whether this host can run the sandbox boundary.
func Available() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat(SandboxExecPath)
	return err == nil
}

// Launch runs spec.Argv under sandbox-exec with a profile generated for the
// given workspace and network policy.
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string) (int, error) {
	if !Available() {
		return 1, fmt.Errorf("sandbox environment requires macOS sandbox-exec at %s", SandboxExecPath)
	}
	if len(spec.Argv) == 0 {
		return 1, exec.ErrNoArgv
	}
	// Seatbelt matches resolved paths, so canonicalize the workspace (e.g.
	// macOS /var -> /private/var) or writes inside it would be denied.
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}

	f, err := os.CreateTemp("", "slop-*.sb")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(Profile(workspace, network)); err != nil {
		_ = f.Close()
		return 1, err
	}
	if err := f.Close(); err != nil {
		return 1, err
	}

	inner := spec
	inner.Argv = append([]string{SandboxExecPath, "-f", f.Name(), "--"}, spec.Argv...)
	return exec.RunInTerminal(ctx, inner)
}
