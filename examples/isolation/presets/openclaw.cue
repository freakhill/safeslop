// Preset for OpenClaw (messaging-gateway agent). Broader threat surface than
// code-only agents: SOUL.md is treated as untrusted input, channel creds are
// scoped, output writes are confined to ./out.

package presets

import "list"

#OpenClaw: #AnyAgentBase & {
	name:        "openclaw"
	description: "OpenClaw messaging-gateway — channel-scoped creds, ./out is the only writable target."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: "allow-domains": list.Concat([[
		"slack.com",
		"discord.com",
		"api.telegram.org",
	], extras."allow-domains"])

	filesystem: {
		"allow-write": list.Concat([["./out"], extras."allow-write"])
		"deny-read": [
			"~/.ssh/**",
			"~/.aws/**",
			"~/.gnupg/**",
			"~/.config/gcloud/**",
			"~/.npmrc",
			"~/.pypirc",
			"**/.env*",
			"**/credentials*",
			"**/tokens*",
			"**/secrets*",
		]
	}

	process: "exec-allow": list.Concat([["openclaw"], extras."exec-allow"])

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
	]

	tool: lulu: binary: "/usr/local/bin/openclaw"
}
