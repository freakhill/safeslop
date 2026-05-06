// Preset for running Nous Hermes locally via ollama or llama.cpp.
// Zero outbound LLM-API egress: the agent reaches the model via loopback.
// Canonical example of the "no third-party LLM provider" deployment shape.

package presets

import "list"

#NousHermesLocal: #AnyAgentBase & {
	name:        "nous-hermes-local"
	description: "Nous Hermes via local ollama/llama.cpp. No outbound LLM egress; loopback only."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: {
		"allow-domains": list.Concat([[], extras."allow-domains"])
		"allow-loopback": true
		"allow-ports":   [11434, 8080] // ollama default + llama-server default
	}

	filesystem: {
		"allow-read": [
			"./",
			"~/.ollama/models",
		]
		"allow-write": list.Concat([["./", "./out"], extras."allow-write"])
	}

	process: {
		"exec-allow": list.Concat([[
			"ollama",
			"llama-server",
			"llama-cli",
			"python",
			"python3",
		], extras."exec-allow"])
		ulimits: nofile: 8192 // mmap'd weights
	}

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
	]

	tool: lulu: binary: "/opt/homebrew/bin/ollama"
}
