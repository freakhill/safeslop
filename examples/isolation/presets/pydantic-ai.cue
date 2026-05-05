// Preset for PydanticAI. Code Mode runs untrusted generated code through
// Monty's Rust sandbox; the compiler emits the corresponding flag via
// tool."pydantic-ai"."code-mode" = "monty".

package presets

import "list"

#PydanticAI: #AnyAgentBase & {
	name:        "pydantic-ai"
	description: "PydanticAI — strict-egress; Code Mode runs through Monty Rust sandbox."

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

	tool: {
		"pydantic-ai": "code-mode": "monty"
		lulu: binary: "/opt/homebrew/bin/python3"
	}
}
