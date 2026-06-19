package cli

import (
	"os"
	"strings"
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

// childEnv builds the environment for an isolated child (the sandbox + host tiers, which share the
// host process namespace) from the allowlist above plus the staged secretEnv/pathEnv. It must NOT
// inherit os.Environ() wholesale: that is the ambient-authority leak specs/0024 S2 closes. LC_* are
// carried by prefix (locale, not authority). secretEnv/pathEnv are appended last so a staged value
// wins over any allowlisted host value of the same name.
func childEnv(secretEnv, pathEnv []string) []string {
	out := make([]string, 0, len(secretEnv)+len(pathEnv)+16)
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		name := kv[:i]
		if allowlistEnv[name] || strings.HasPrefix(name, "LC_") {
			out = append(out, kv)
		}
	}
	out = append(out, secretEnv...)
	out = append(out, pathEnv...)
	return out
}
