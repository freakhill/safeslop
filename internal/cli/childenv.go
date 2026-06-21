package cli

import (
	"os"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/hostenv"
)

// allowlistEnv is the set of host environment variable NAMES safe to carry into an isolated
// (sandbox/host) child. Everything else — cloud tokens, the 1Password session, the ssh-agent
// socket, forge tokens, even ANTHROPIC_API_KEY — is dropped, so host ambient authority never
// crosses the boundary (specs/0024 S2). Credentials reach the agent ONLY via the policy's
// secrets:/credentials: blocks (the secretEnv/pathEnv channels), never by inheritance.
var allowlistEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "COLORTERM": true,
	"TMPDIR": true, "LANG": true, "TZ": true,
}

// carryName reports whether an env var name is safe to carry into an isolated child: the static
// allowlist plus the LC_* locale prefix (locale, not authority).
func carryName(name string) bool {
	return allowlistEnv[name] || strings.HasPrefix(name, "LC_")
}

// hostDiscoveryEnv returns the rich, reconstructed host environment (the host_discovery_env from
// internal/engine/hostenv) as a name→value map. Under a Finder/launchd launch the process env is
// stripped (PATH≈/usr/bin:/bin, no $SHELL); this recovers the real PATH/SHELL the user's login shell
// builds. It is a package var so the firewall test can substitute a rich credential-laden map.
//
// SECURITY: this map is RICH (AWS_*, GITHUB_TOKEN, SSH_AUTH_SOCK, …). childEnv pulls ONLY allowlisted
// names from it, so those credentials are structurally excluded from the child — the rich env never
// crosses the sandbox boundary. This is the two-environment firewall: rich env for host-side discovery
// and binary resolution, scrubbed allowlist for what reaches the agent.
var hostDiscoveryEnv = func() map[string]string {
	out := map[string]string{}
	for _, kv := range hostenv.Reconstruct().Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// childEnv builds the environment for an isolated child (the sandbox + host tiers, which share the
// host process namespace) from the allowlist above plus the staged secretEnv/pathEnv. It must NOT
// inherit os.Environ() wholesale: that is the ambient-authority leak specs/0024 S2 closes.
//
// The reconstructed host_discovery_env is overlaid on top of the allowlisted os.Environ values, so a
// Finder-launched app still hands the agent the user's real PATH/SHELL (otherwise the agent would
// inherit the stripped /usr/bin:/bin and find none of its tools). Only allowlisted names are pulled
// from either source, so the rich env's credentials never cross — see hostDiscoveryEnv. secretEnv/
// pathEnv are appended last so a staged value wins over any allowlisted host value of the same name.
func childEnv(secretEnv, pathEnv []string) []string {
	carried := map[string]string{}
	for _, kv := range os.Environ() {
		if name, val, ok := strings.Cut(kv, "="); ok && carryName(name) {
			carried[name] = val
		}
	}
	// Overlay the reconstructed env's allowlisted members (PATH/SHELL/…), which are richer than the
	// possibly-stripped process env. Credentials in the rich map are excluded by carryName.
	for name, val := range hostDiscoveryEnv() {
		if carryName(name) {
			carried[name] = val
		}
	}
	out := make([]string, 0, len(carried)+len(secretEnv)+len(pathEnv))
	for name, val := range carried {
		out = append(out, name+"="+val)
	}
	out = append(out, secretEnv...)
	out = append(out, pathEnv...)
	return out
}
