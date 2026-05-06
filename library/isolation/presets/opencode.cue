// Preset for the OpenCode npm CLI.
// Mirrors library/opencode.restrictive.json semantics.
//
// Extend via the `extras` struct (see library/user-config.cue).

package presets

import "list"

#OpenCode: #AnyAgentBase & {
	name:        "opencode"
	description: "OpenCode (npm CLI) — strict-egress, project-scoped fs, configurable model provider."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: "allow-domains": list.Concat([[
		"opencode.ai",
		"registry.npmjs.org",
		"github.com",
		"raw.githubusercontent.com",
	], extras."allow-domains"])

	filesystem: "allow-write": list.Concat([[
		"./",
		"./tmp",
	], extras."allow-write"])

	process: "exec-allow": list.Concat([[
		"node",
		"npm",
		"git",
		"rg",
	], extras."exec-allow"])

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
		"opencode-settings",
	]

	tool: lulu: binary: "/opt/homebrew/bin/node"
}
