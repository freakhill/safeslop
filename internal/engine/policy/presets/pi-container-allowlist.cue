// pi coding agent in a container with egress limited to the default allowlist (github/npm/pypi/anthropic/openrouter).
package safeslop

safeslop: {
	version: 1
	profiles: {
		pair: {agent: "pi", environment: "container", network: "deny"}
	}
}
