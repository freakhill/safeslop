// Preset for accessing a hosted Nous Hermes endpoint (e.g. via OpenRouter).
// Mirrors crewai/pydantic-ai but pins the upstream host.

package presets

import "list"

#NousHermesRemote: #AnyAgentBase & {
	name:        "nous-hermes-remote"
	description: "Hosted Nous Hermes endpoint (e.g. OpenRouter routing to NousResearch)."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: "allow-domains": list.Concat([[
		"openrouter.ai",
		"pypi.org",
		"files.pythonhosted.org",
	], extras."allow-domains"])

	filesystem: "allow-write": list.Concat([[
		"./",
		"./tmp",
	], extras."allow-write"])

	process: "exec-allow": list.Concat([[
		"uv",
		"python",
		"python3",
		"git",
	], extras."exec-allow"])

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
	]

	tool: lulu: binary: "/opt/homebrew/bin/python3"
}
