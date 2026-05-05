// Preset for ZeroClaw (Rust binary, supervised autonomy).
// Default egress is empty: medium-risk requires approval, high-risk blocked.
// Pair with `slop-isolate proxy start` so the operator approves flows live.

package presets

import "list"

#ZeroClaw: #AnyAgentBase & {
	name:        "zeroclaw"
	description: "ZeroClaw — supervised autonomy. Empty allow-domains; approver mediates egress."

	extras: {
		"allow-domains": [...string] | *[]
		"allow-write":   [...string] | *[]
		"exec-allow":    [...string] | *[]
	}

	network: {
		"allow-domains": list.Concat([[], extras."allow-domains"])
		"allow-loopback": false
	}

	filesystem: "allow-write": list.Concat([["./out"], extras."allow-write"])

	process: "exec-allow": list.Concat([["zeroclaw"], extras."exec-allow"])

	adapters: enabled: [
		"sandbox-exec",
		"docker-compose",
		"squid",
		"envoy",
	]

	tool: {
		"sandbox-exec": "deny-network": "all"
		lulu: binary: "/usr/local/bin/zeroclaw"
	}
}
