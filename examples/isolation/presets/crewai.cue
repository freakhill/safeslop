// Preset for CrewAI (Python lib). Defaults assume the model provider is
// configured in agent code; add the relevant API host to extras.allow-domains.

package presets

import "list"

#CrewAI: #AnyAgentBase & {
	name:        "crewai"
	description: "CrewAI Python stack — strict-egress, model provider host added per deployment."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: "allow-domains": list.Concat([[
		"pypi.org",
		"files.pythonhosted.org",
	], extras."allow-domains"])

	filesystem: "allow-write": list.Concat([[
		"./",
		"./tmp",
		"~/.cache/uv",
	], extras."allow-write"])

	process: {
		"max-processes": 64
		"exec-allow": list.Concat([[
			"uv",
			"python",
			"python3",
			"git",
		], extras."exec-allow"])
	}

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
	]

	tool: lulu: binary: "/opt/homebrew/bin/python3"
}
