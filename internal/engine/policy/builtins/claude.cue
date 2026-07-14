// Claude Code in a contained container with deny-by-default network access.
package safeslop

safeslop: {
	version: 1
	profiles: {
		claude: {agent: "claude", environment: "container", network: "deny", bundles: ["personal"]}
	}
}
