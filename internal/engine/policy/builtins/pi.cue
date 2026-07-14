// Pi coding agent in a contained container with deny-by-default network access.
package safeslop

safeslop: {
	version: 1
	profiles: {
		pi: {agent: "pi", environment: "container", network: "deny", bundles: ["personal"]}
	}
}
