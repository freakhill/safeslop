// Preset for the Claude Code npm CLI.
// Mirrors examples/claude-code.settings.json but emits the same intent
// to every other adapter as well.
//
// To extend without rewriting the full list, set fields on `extras`.
// The compiler strips `extras` before emitting adapter outputs.

package presets

import "list"

#ClaudeCode: #AnyAgentBase & {
	name:        "claude-code"
	description: "Claude Code (npm CLI) — strict-egress, project-scoped fs."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: "allow-domains": list.Concat([[
		"api.anthropic.com",
		"registry.npmjs.org",
		"github.com",
		"raw.githubusercontent.com",
		"pypi.org",
		"files.pythonhosted.org",
	], extras."allow-domains"])

	filesystem: "allow-write": list.Concat([[
		"./",
		"./tmp",
		"~/.config/claude-code",
	], extras."allow-write"])

	process: "exec-allow": list.Concat([[
		"node",
		"npm",
		"git",
		"rg",
		"fish",
	], extras."exec-allow"])

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
		"claude-code-settings",
	]

	tool: lulu: binary: "/opt/homebrew/bin/node"
}
