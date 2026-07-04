// A plain fish shell in a container — a sandboxed shell (default-allowlist egress, no coding agent).
package safeslop

safeslop: {
	version: 1
	profiles: {
		shell: {agent: "fish", environment: "container", network: "deny"}
	}
}
