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

// ---------------------------------------------------------------------------
// Orchestrator schema (slop.cue)
// ---------------------------------------------------------------------------
//
// An author drops a slop.cue at the root of a repo (or any directory). The
// slop runtime reads it and starts the agents declared, doing setup
// (containers, ephemeral creds, proxy) and cleanup automatically. Authors
// extend an existing #Isolation preset rather than rewriting one from
// scratch — see library/layer/policy/samples/isolation/user-config.cue for the
// pattern. The #Slop schema is the contract; the runtime lives in
// scripts/_py/slop_orchestrator.py (next phase).

// Agent names mirror the preset filenames under library/layer/policy/presets/.
#Agent: "claude" | "opencode" | "ag2" | "openclaw" | "zeroclaw" |
	"crewai" | "pydantic-ai" | "nous-hermes-local" | "nous-hermes-remote"

// Where the agent runs.
//   host:      directly on the host (e.g. `slop-agents claude` flow).
//   container: inside the agent-tools Docker stack.
//   vm:        inside a disposable Tart VM (slop-brew-vm-style).
#Environment: "host" | "container" | "vm"

// Per-host credential mode. The runtime maps each non-"none" value to the
// matching `slop-<host>-key here ...` (or `slop-radicle ...`) flow on
// launch and to the matching cleanup on exit.
#GitHostCredential:  *"none" | "ephemeral-ro" | "ephemeral-rw" | "ephemeral-pair"
#RadicleCredential:  *"none" | "ephemeral"

#Credentials: {
	github?:  #GitHostCredential
	forgejo?: #GitHostCredential
	radicle?: #RadicleCredential
}

// Hooks the runtime executes on profile exit, in declaration order.
//   revoke-credentials: revoke any ephemeral keys created on launch
//                       (gh `cleanup` + `revoke-all` for ephemeral-pair, etc.).
//   stop-container:     `slop-agent-sandbox-tools down`-equivalent.
//   stop-proxy:         `slop-isolate proxy stop`.
//   destroy-vm:         `slop-brew-vm destroy`.
//   snapshot-state:     write the resolved profile + resource ids to
//                       .slop/snapshots/<utc-stamp>.json before tearing down.
#OnExitHook: "revoke-credentials" | "stop-container" | "stop-proxy" |
	"snapshot-state" | "destroy-vm"

// Optional image override for environment="container".
//
// `base` overrides the source tag; defaults to local/agent-sandbox-tools:latest
// when omitted. Must be a tag the local Docker daemon already has or
// can pull.
//
// `extra-apt` / `extra-pip` / `extra-npm` declare extra packages to
// layer on top of the base. The runtime hashes the spec, generates a
// per-profile Dockerfile under <state-dir>/runtime/<profile>/, builds
// the tailored image once, and tags it `local/agent-sandbox-tools:slop-<hash>`.
// Subsequent runs with the same spec reuse the cached tag.
//
// Pin every package by exact version (e.g. "rad==1.2.3"). The `slop-pinning`
// gate flags `=latest` in slop.cue at build time; deferring pin checks to
// the slop runtime would let drift slip into shipped recipes.
#ImageSpec: {
	base?:        string
	"extra-apt"?: [...string]
	"extra-pip"?: [...string]
	"extra-npm"?: [...string]
}

#Profile: {
	agent:       #Agent
	environment: #Environment
	isolation:   #Isolation
	credentials?: #Credentials
	"on-exit"?:  [...#OnExitHook]
	image?:      #ImageSpec
}

// Top-level slop.cue shape. `default` (when set) names the profile that
// runs when the user types bare `slop` in a repo containing this file.
// `state-dir` overrides the runtime's per-repo state directory; the
// default ".slop" is gitignored at the repo root.
#Slop: {
	profiles: [string]: #Profile
	default?:    string
	"state-dir": *".slop" | string
}
