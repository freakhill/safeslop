// Package hostenv reconstructs the user's real shell environment for a process that was started
// with a stripped one — notably a Finder/launchd-launched .app, which inherits PATH≈/usr/bin:/bin
// and usually no $SHELL. Without this the engine can't find brew, git, mise/asdf shims, or the
// agents, so binary resolution and agent launch both break outside a terminal.
//
// It captures rather than parses: it runs the user's login+interactive shell once and reads the
// environment that shell builds (dotfiles have conditional logic and version-manager `eval`s that
// only a real run resolves), with deterministic fallbacks when the capture fails.
//
// SECURITY — the two-environment firewall. The reconstructed env is RICH: a login shell exports
// AWS_*, ANTHROPIC_API_KEY, GITHUB_TOKEN, SSH_AUTH_SOCK, etc. This package's output is for HOST-SIDE
// DISCOVERY AND BINARY RESOLUTION ONLY (LookPath, resolving an absolute binary to spawn). It must
// NEVER be handed wholesale to a sandboxed child: the cli.childEnv allowlist remains the sole gate
// into the sandbox. The only value safe to carry across is PATH (location, not authority) — and even
// that is sanitized here first (DYLD_*/LD_PRELOAD stripped; non-absolute / world-writable / `..`
// PATH entries rejected) so a poisoned dotfile can't turn host binary resolution into host RCE.
package hostenv

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// statFunc resolves a filesystem path to its mode (injected so PATH/sanitize logic is testable
// without touching disk). It returns fs.ErrNotExist for an absent path.
type statFunc func(string) (fs.FileMode, error)

// brewBinDirs are the Homebrew bin prefixes whose presence in PATH marks a "rich" (terminal) env:
// /opt/homebrew/bin on Apple Silicon, /usr/local/bin on Intel.
var brewBinDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// isGUIMinimal reports whether environ looks like a Finder/launchd-stripped environment that needs
// reconstruction: no $SHELL, or a PATH that lacks any Homebrew bin prefix. A normal terminal shell
// has both, so it is left untouched (reconstruction is skipped and os.Environ is used as-is).
func isGUIMinimal(environ []string) bool {
	var shell, path string
	for _, kv := range environ {
		if name, val, ok := strings.Cut(kv, "="); ok {
			switch name {
			case "SHELL":
				shell = val
			case "PATH":
				path = val
			}
		}
	}
	if shell == "" {
		return true
	}
	return !pathHasBrew(path)
}

// pathHasBrew reports whether a colon-separated PATH contains a Homebrew bin prefix.
func pathHasBrew(path string) bool {
	for _, dir := range strings.Split(path, ":") {
		for _, b := range brewBinDirs {
			if dir == b {
				return true
			}
		}
	}
	return false
}

// identRune reports whether r is legal in a POSIX-ish environment variable name.
func identRune(r rune, first bool) bool {
	if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
		return true
	}
	return !first && r >= '0' && r <= '9'
}

// isEnvName reports whether s is a valid environment variable name ([A-Za-z_][A-Za-z0-9_]*).
func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !identRune(r, i == 0) {
			return false
		}
	}
	return true
}

// parseMarkerEnv extracts the `env` dump that the capture shell wrapped between two identical marker
// lines and parses it into a name→value map. Content outside the markers (shell rc MOTD/banners) is
// ignored. A line whose text before the first '=' is a valid env name starts a new variable; any
// other line (no '=', or a noise line like "tip: x = y") folds into the previous variable's value —
// so a genuinely multiline value is captured whole and then dropped by sanitize. Missing markers are
// an error so the caller can fall back to path_helper/hardcoded dirs.
func parseMarkerEnv(output, marker string) (map[string]string, error) {
	lines := strings.Split(output, "\n")
	start, end := -1, -1
	for i, ln := range lines {
		if ln == marker {
			if start == -1 {
				start = i
			} else {
				end = i
				break
			}
		}
	}
	if start == -1 || end == -1 {
		return nil, errors.New("hostenv: capture markers not found in shell output")
	}
	out := map[string]string{}
	var last string
	for _, ln := range lines[start+1 : end] {
		if name, val, ok := strings.Cut(ln, "="); ok && isEnvName(name) {
			out[name] = val
			last = name
			continue
		}
		if last != "" {
			out[last] += "\n" + ln
		}
	}
	return out, nil
}

// filterPATH keeps only PATH entries that are safe to use for host binary resolution: absolute, free
// of any `..` component, and not world-writable (a poisoned dotfile prepending /tmp/evil would
// otherwise make the host run attacker code). Absent dirs are kept (harmless for LookPath); a dir is
// dropped only when stat succeeds AND the world-writable bit is set. Order is preserved and dupes
// removed.
func filterPATH(path string, stat statFunc) string {
	seen := map[string]bool{}
	var keep []string
	for _, dir := range strings.Split(path, ":") {
		if dir == "" || !filepath.IsAbs(dir) || seen[dir] {
			continue
		}
		if hasDotDot(dir) {
			continue
		}
		if mode, err := stat(dir); err == nil && mode.Perm()&0o002 != 0 {
			continue // world-writable
		}
		seen[dir] = true
		keep = append(keep, dir)
	}
	return strings.Join(keep, ":")
}

// hasDotDot reports whether a path contains a `..` component.
func hasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// sanitize strips the dangerous and unusable entries from a captured env before it is used for host
// discovery: the dynamic-linker injection vectors (DYLD_*, LD_PRELOAD — a host RCE vector if a child
// inherits them), any value containing a NUL or newline (multiline values can't round-trip and are a
// smuggling vector), and it replaces PATH with its filtered form.
func sanitize(vars map[string]string, stat statFunc) map[string]string {
	out := make(map[string]string, len(vars))
	for name, val := range vars {
		if strings.HasPrefix(name, "DYLD_") || name == "LD_PRELOAD" {
			continue
		}
		if strings.ContainsAny(val, "\n\x00") {
			continue
		}
		if name == "PATH" {
			val = filterPATH(val, stat)
		}
		out[name] = val
	}
	return out
}

// hardcodedDirs is the zero-latency PATH floor used when (or alongside when) the shell capture fails:
// the Homebrew prefixes, common per-user bin dirs, and the mise/asdf shim dirs. It covers the great
// majority of installs even with no shell run at all.
func hardcodedDirs(home string) []string {
	return []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".asdf", "shims"),
		"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}
}

// isFishShell reports whether the resolved login shell is fish, which needs the capture flags passed
// separately (its getopt rejects a packed "-ilc") and exports no POSIX `export -p` — we dump the
// process env via the external `env` instead.
func isFishShell(shellPath string) bool {
	return filepath.Base(shellPath) == "fish"
}

// shellArgv builds the argv (after the shell path) that runs inner as a login+interactive command.
// Flags are passed SEPARATELY rather than packed as "-ilc": fish's argument parser rejects the
// packed form, and zsh/bash accept the separate form identically.
func shellArgv(inner string) []string {
	return []string{"-l", "-i", "-c", inner}
}
