// Fish shell in a contained container with deny-by-default network access.
package safeslop

safeslop: {
	version: 1
	profiles: {
		fish: {agent: "fish", environment: "container", network: "deny", bundles: ["personal"]}
	}
}
