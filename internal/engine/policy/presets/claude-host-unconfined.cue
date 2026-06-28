// Claude Code on the host — NO isolation: full account + full network. Convenient, not contained.
package safeslop

safeslop: {
	version: 1
	profiles: {
		host: {agent: "claude", environment: "host", network: "allow"}
	}
}
