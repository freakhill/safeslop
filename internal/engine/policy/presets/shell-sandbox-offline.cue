// Sandboxed offline shell — poke around safely, no network, workspace-confined.
package safeslop

safeslop: {
	version: 1
	profiles: {
		safe: {agent: "shell", environment: "sandbox", network: "deny"}
	}
}
