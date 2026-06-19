package safeslop

safeslop: {
	profiles: {
		dev: {agent: "shell"}
		review: {agent: "claude", environment: "sandbox", network: "deny"}
	}
}
