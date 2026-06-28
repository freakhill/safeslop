package safeslop

safeslop: {
	profiles: {
		dev: {agent: "shell", environment: "host"}
		review: {agent: "claude", environment: "container", network: "deny"}
	}
}
