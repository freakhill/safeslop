// Claude Code — sandboxed & offline. The safe default for reviewing untrusted code.
package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {agent: "claude", environment: "sandbox", network: "deny"}
	}
}
