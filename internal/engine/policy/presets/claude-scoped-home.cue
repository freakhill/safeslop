// Claude sandboxed with extra read access to ~/.config — credential stores are auto-denied.
package safeslop

safeslop: {
	version: 1
	profiles: {
		dev: {
			agent:       "claude"
			environment: "sandbox"
			network:     "deny"
			files: {read: ["~/.config"]}
		}
	}
}
