// Schema for slop-isolate unified isolation policy.
//
// Author an isolation.cue that imports a preset, unify with deltas, and
// run `slop-isolate compile <file>.cue --adapter <name>` to emit per-tool
// configuration. `cue vet` validates against #Isolation.

package schema

#AdapterName: "docker-compose" | "squid" | "envoy" | "coredns" |
	"sandbox-exec" | "lulu" | "pf" |
	"claude-code-settings" | "opencode-settings" | "ag2-executor" |
	"tart" | "orbstack"

#NetworkPolicy: "strict-egress" | "proxy-only" | "off"

#Network: {
	policy:           #NetworkPolicy
	"allow-domains": [...string]
	"allow-loopback": bool | *false
	"allow-ports":   [...int] | *[80, 443]
	"deny-cidrs":    [...string] | *[
		"169.254.0.0/16",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	]
	dns: "proxy" | "system" | =~"^doh:" | *"proxy"
}

#Filesystem: {
	"path-scope":     "cwd" | "repo-root" | "explicit" | *"repo-root"
	"allow-read":    [...string]
	"allow-write":   [...string]
	"deny-read":     [...string]
	"deny-write":    [...string]
	"read-only-root": bool | *true
}

#Process: {
	"cap-drop":          [...string] | *["ALL"]
	"no-new-privileges": bool | *true
	"max-processes":     int | *256
	ulimits: {
		nofile?: int
		nproc?:  int
	}
	"exec-allow": [...string]
}

#PfFallback: "resolve-once-then-pin" | "skip" | "fail"

#EnvoyTLSMode: "sni" | "mitm"

#EnvoyNotifier: "terminal-notifier" | "alerter" | "log-only"

#TcpAllow: {
	port:  int
	hosts: [...string]
}

#AdapterOverrides: {
	"docker-compose"?: {...}
	squid?:            {...}
	envoy?: {
		tls?:      #EnvoyTLSMode
		notifier?: #EnvoyNotifier
		"tcp-allow"?: [...#TcpAllow]
		"udp-allow"?: [...#TcpAllow]
		...
	}
	coredns?:      {...}
	"sandbox-exec"?: {
		"deny-network"?: "all" | "default"
		...
	}
	lulu?: {
		binary?: string
		...
	}
	pf?: {
		"domain-fallback"?: #PfFallback
		...
	}
	"claude-code-settings"?: {...}
	"opencode-settings"?:    {...}
	"ag2-executor"?: {
		executor?: "docker" | "local"
		...
	}
	tart?:     {...}
	orbstack?: {...}
	"pydantic-ai"?: {
		"code-mode"?: "monty" | "off"
		...
	}
}

// `extras` is a free-form extension surface presets use to expose
// concatenable list fields (allow-domains, allow-write, exec-allow). The
// compiler strips this struct before emitting adapter outputs — it is
// purely a CUE-side convenience for user deltas.
#Extras: [string]: [...string]

#Isolation: {
	name?:        string
	description?: string
	network:    #Network
	filesystem: #Filesystem
	process:    #Process
	adapters: enabled: [...#AdapterName]
	tool?:    #AdapterOverrides
	extras?:  #Extras
}
