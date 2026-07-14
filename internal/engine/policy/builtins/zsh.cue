// Zsh shell in a contained container with deny-by-default network access.
package safeslop

safeslop: {
	version: 1
	profiles: {
		zsh: {agent: "zsh", environment: "container", network: "deny", bundles: ["personal"]}
	}
}
