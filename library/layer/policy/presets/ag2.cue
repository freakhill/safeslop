// Preset for AG2 (formerly AutoGen). AG2 has its own
// DockerCommandLineCodeExecutor; the ag2-executor adapter emits a stub
// Python file that wires it up with mounts/limits matching this config.

package presets

import "list"

#AG2: #AnyAgentBase & {
	name:        "ag2"
	description: "AG2 (AutoGen) — strict-egress; nested isolation via DockerCommandLineCodeExecutor."

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
		"docker",
		"git",
	], extras."exec-allow"])

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
		"ag2-executor",
	]

	tool: {
		"ag2-executor": executor: "docker"
		lulu: binary: "/opt/homebrew/bin/python3"
	}
}
