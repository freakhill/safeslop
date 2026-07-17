package cli

import (
	"os"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/hostenv"
)

// carryName reports whether an env var name is safe to carry into an isolated child: the static
// allowlist plus the LC_* locale prefix (locale, not authority). Everything else — cloud tokens,
// 1Password sessions, ssh-agent sockets, and forge tokens — is dropped.
func carryName(name string) bool {
	switch name {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL",
		"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM",
		"TMPDIR", "LANG", "TZ":
		return true
	default:
		return strings.HasPrefix(name, "LC_")
	}
}

// defaultHostDiscoveryEnv returns the rich, reconstructed host environment (the host_discovery_env from
// internal/engine/hostenv) as a name→value map. Under a Finder/launchd launch the process env is
// stripped (PATH≈/usr/bin:/bin, no $SHELL); this recovers the real PATH/SHELL the user's login shell
// builds. Tests substitute it through the root dependency bundle.
//
// SECURITY: this map is RICH (AWS_*, GITHUB_TOKEN, SSH_AUTH_SOCK, …). childEnv pulls ONLY allowlisted
// names from it, so those credentials are structurally excluded from the child — the rich env never
// crosses the boundary. This is the two-environment firewall: rich env for host-side discovery
// and binary resolution, scrubbed allowlist for what reaches the agent.
func defaultHostDiscoveryEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range hostenv.Reconstruct().Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// childEnv builds the environment for the host-tier child (which shares the host process namespace)
// from the allowlist above plus the staged secretEnv/pathEnv. It must NOT inherit os.Environ()
// wholesale: that is the ambient-authority leak specs/0024 S2 closes.
//
// The reconstructed host_discovery_env is overlaid on top of the allowlisted os.Environ values, so a
// Finder-launched app still hands the agent the user's real PATH/SHELL (otherwise the agent would
// inherit the stripped /usr/bin:/bin and find none of its tools). Only allowlisted names are pulled
// from either source, so the rich env's credentials never cross — see defaultHostDiscoveryEnv. secretEnv/
// pathEnv are appended last so a staged value wins over any allowlisted host value of the same name.
func childEnv(secretEnv, pathEnv []string) []string {
	return childEnvWithDeps(defaultDependencies(), secretEnv, pathEnv)
}

func childEnvWithDeps(d *dependencies, secretEnv, pathEnv []string) []string {
	carried := map[string]string{}
	for _, kv := range os.Environ() {
		if name, val, ok := strings.Cut(kv, "="); ok && carryName(name) {
			carried[name] = val
		}
	}
	// Overlay the reconstructed env's allowlisted members (PATH/SHELL/…), which are richer than the
	// possibly-stripped process env. Credentials in the rich map are excluded by carryName.
	for name, val := range d.hostDiscoveryEnv() {
		if carryName(name) {
			carried[name] = val
		}
	}
	// Force a truecolor terminal at the boundary: under a Finder/launchd launch TERM/COLORTERM are
	// absent, yet the agent TUIs (Ink/chalk) need them to emit 24-bit color. Set unconditionally so
	// the child always gets a correct terminal regardless of how safeslop was launched. Never set
	// LINES/COLUMNS — the PTY winsize is authoritative.
	carried["TERM"] = "xterm-256color"
	carried["COLORTERM"] = "truecolor"
	out := make([]string, 0, len(carried)+len(secretEnv)+len(pathEnv))
	for name, val := range carried {
		out = append(out, name+"="+val)
	}
	out = append(out, secretEnv...)
	out = append(out, pathEnv...)
	return out
}
