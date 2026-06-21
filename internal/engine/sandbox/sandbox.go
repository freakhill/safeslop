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

	"github.com/freakhill/safeslop/internal/engine/exec"
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

// Scope adds paths to the sandbox boundary beyond the workspace (from policy.FileScope): Read/Write
// are extra allowed paths; Deny is emitted last so it overrides any allow (Seatbelt is last-match).
type Scope struct {
	Read  []string
	Write []string
	Deny  []string
}

// expandHome turns a leading "~" into the user's home directory; other paths pass through.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// canonicalizeScope expands ~ and resolves symlinks for every scope path that exists, so Seatbelt's
// resolved-path matching (e.g. /var -> /private/var) holds. Non-existent paths pass through expanded.
func canonicalizeScope(s Scope) Scope {
	resolve := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			ep := expandHome(p)
			if real, err := filepath.EvalSymlinks(ep); err == nil {
				ep = real
			}
			out = append(out, ep)
		}
		return out
	}
	return Scope{Read: resolve(s.Read), Write: resolve(s.Write), Deny: resolve(s.Deny)}
}

// Profile renders a Seatbelt profile confining writes to workspace (+ temp), applying the network
// policy ("allow" or, by default, "deny"), plus any extra file Scope (read/write add allowances;
// deny is emitted last and wins).
func Profile(workspace, network string, scope Scope) string {
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

	// Interactive job control: a shell/agent on a PTY must issue tty ioctls on its
	// controlling terminal — tcsetpgrp (TIOCSPGRP) to claim the foreground process
	// group, plus window-size ioctls (TIOCGWINSZ/TIOCSWINSZ). Seatbelt treats these
	// as `file-ioctl`, which is NOT implied by file-read* on /dev — so without this
	// rule a sandboxed zsh prints "can't set tty pgrp: operation not permitted",
	// becomes a background process group, and its commands suspend on SIGTTIN/SIGTTOU
	// (the cockpit "shell opens but runs nothing" bug). Scoped to the BSD pty slaves
	// (/dev/ttysNNN). Seatbelt can't filter by ioctl request code, so the residual
	// TIOCSTI surface stays within this tier's honest threat model (mistake-guard:
	// guards agent mistakes + accidental exfil, not a malicious-code escape).
	line(`(allow file-ioctl (regex #"^/dev/ttys"))`)

	ws := escape(workspace)
	line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, ws))
	line(fmt.Sprintf(`(allow file-write* (subpath "%s"))`, ws))

	for _, p := range tempPaths {
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, escape(p)))
		line(fmt.Sprintf(`(allow file-write* (subpath "%s"))`, escape(p)))
	}

	// Extra allowed scope (read first, then write-implies-read).
	for _, p := range scope.Read {
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, escape(expandHome(p))))
	}
	for _, p := range scope.Write {
		ep := escape(expandHome(p))
		line(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, ep))
		line(fmt.Sprintf(`(allow file-write* (subpath "%s"))`, ep))
	}

	if network == "allow" {
		line("(allow network*)")
	} else {
		line("(deny network*)")
	}

	// Deny LAST so it overrides any allow above (Seatbelt = last matching rule wins) — the explicit
	// subtractive scope (e.g. carve ~/.ssh back out of a broad read).
	for _, p := range scope.Deny {
		ep := escape(expandHome(p))
		line(fmt.Sprintf(`(deny file-read* (subpath "%s"))`, ep))
		line(fmt.Sprintf(`(deny file-write* (subpath "%s"))`, ep))
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

// WrapArgv writes a Seatbelt profile for (workspace, network) to a temp file and returns
// the argv that runs agentArgv under it, plus a cleanup that removes the file. The caller
// runs the argv (e.g. on a PTY) and calls cleanup when the process exits.
func WrapArgv(agentArgv []string, workspace, network string, scope Scope) (argv []string, cleanup func(), err error) {
	if _, statErr := os.Stat(SandboxExecPath); statErr != nil {
		return nil, func() {}, fmt.Errorf("sandbox environment requires macOS sandbox-exec at %s", SandboxExecPath)
	}
	f, err := os.CreateTemp("", "safeslop-sb-*.sb")
	if err != nil {
		return nil, func() {}, err
	}
	if _, err := f.WriteString(Profile(workspace, network, scope)); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, func() {}, err
	}
	_ = f.Close()
	argv = append([]string{SandboxExecPath, "-f", f.Name(), "--"}, agentArgv...)
	return argv, func() { _ = os.Remove(f.Name()) }, nil
}

// Launch runs spec.Argv under sandbox-exec with a profile generated for the
// given workspace, network policy, and extra file scope.
func Launch(ctx context.Context, spec exec.LaunchSpec, workspace, network string, scope Scope) (int, error) {
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
	scope = canonicalizeScope(scope) // resolve ~ + symlinks so Seatbelt path matching holds

	argv, cleanup, err := WrapArgv(spec.Argv, workspace, network, scope)
	if err != nil {
		return 1, err
	}
	defer cleanup()

	inner := spec
	inner.Argv = argv
	return exec.RunInTerminal(ctx, inner)
}
