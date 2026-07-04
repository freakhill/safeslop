// Claude Code in a container with egress limited to the default allowlist (github/npm/pypi/anthropic).
package safeslop

safeslop: {
	version: 1
	profiles: {
		build: {agent: "claude", environment: "container", network: "deny"}
	}
}
