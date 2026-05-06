// Strict baseline every other preset unifies on top of.
//
// Field defaults (`*X | T` pattern) let derived presets replace any value
// while keeping the schema constraint. Concrete values (no `*`) would force
// every derived preset to repeat them.

package presets

import "slop.dev/isolation/schema"

#AnyAgentBase: schema.#Isolation & {
	name:        string | *"any-agent"
	description: string | *"Strict generic baseline. Deny by default; presets layer their needs on top."

	network: {
		policy:           schema.#NetworkPolicy | *"strict-egress"
		"allow-domains": [...string] | *[]
		"allow-loopback": bool | *false
		"allow-ports":   [...int] | *[80, 443]
		dns:              string | *"proxy"
	}

	filesystem: {
		"path-scope":   string | *"repo-root"
		"allow-read":  [...string] | *["./"]
		"allow-write": [...string] | *["./"]
		"deny-read":   [...string] | *[
			"~/.ssh/**",
			"~/.aws/**",
			"~/.gnupg/**",
			"~/.config/gcloud/**",
			"~/.npmrc",
			"~/.pypirc",
			"**/.env*",
		]
		"deny-write": [...string] | *[
			"~/**",
			"/etc/**",
			"/private/**",
		]
		"read-only-root": bool | *true
	}

	process: {
		"cap-drop":          [...string] | *["ALL"]
		"no-new-privileges": bool | *true
		"max-processes":     int | *256
		ulimits: {
			nofile: int | *4096
			nproc:  int | *256
		}
		"exec-allow": [...string] | *[]
	}

	adapters: enabled: [...schema.#AdapterName] | *[
		"sandbox-exec",
		"docker-compose",
		"squid",
	]

	tool: {
		pf: "domain-fallback": schema.#PfFallback | *"resolve-once-then-pin"
		envoy: {
			tls:      schema.#EnvoyTLSMode | *"sni"
			notifier: schema.#EnvoyNotifier | *"terminal-notifier"
		}
	}
}

// The bare `any-agent` preset — picks all defaults.
#AnyAgent: #AnyAgentBase & {
	name:        "any-agent"
	description: "Strict generic baseline. Deny by default; presets layer their needs on top."
}
