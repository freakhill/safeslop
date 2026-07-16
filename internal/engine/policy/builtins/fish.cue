// Fish shell in a contained container with deny-by-default network access.
// Builtin contract: fish-projection-v2-functions-completions-only.
package safeslop

safeslop: {
	version: 1
	profiles: {
		fish: {agent: "fish", environment: "container", network: "deny", bundles: ["personal"]}
	}
}
