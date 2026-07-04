package safeslop

// Default dogfood profiles for the safeslop repo. Review and `safeslop trust`
// before launching; all routine starters stay in container + deny-by-default
// network posture.
safeslop: {
	version: 1
	profiles: {
		default: {
			agent:       "claude"
			environment: "container"
			network:     "deny"
			workspace:   "."
		}

		pi: {
			agent:       "pi"
			environment: "container"
			network:     "deny"
			workspace:   "."
		}

		shell: {
			agent:       "fish"
			environment: "container"
			network:     "deny"
			workspace:   "."
		}
	}
}
