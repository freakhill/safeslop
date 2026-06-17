package slop

// Embedded engine schema for slop.cue (specs/0001 §6.1). Compiled into the
// binary via go:embed; the external `cue` binary is never needed.
//
// SP1 scope: enough to launch claude/shell under the sandbox-exec boundary.
// credentials (SP2), container/vm (SP3/SP4), and toolchains (SP5) extend this.

// Where the agent runs. SP1 implements "sandbox" (default) and "host"; the
// others are accepted by the schema and land in later sub-projects.
#Environment: "sandbox" | "container" | "vm" | "host"

// What to launch.
#Agent: "claude" | "shell" | "opencode"

// Coarse egress policy for the sandbox-exec boundary. Not a URL allowlist —
// that is the container's job (specs/0001 §6.2).
#Network: "deny" | "allow"

// A secret reference resolved at launch (specs/0001 §7): a 1Password URI
// ("op://vault/item/field") or "env:NAME" to read from the launching shell.
// Values are never written to disk except in the ephemeral, wiped-on-exit stage.
#SecretRef: string & =~"^(op://|env:).+"

// An npm/pnpm registry to authenticate. The token is sourced from a secret ref
// and staged into a scoped .npmrc, wiped on exit (specs/0001 §7.2).
#PnpmRegistry: {
	host:   string | *"registry.npmjs.org"
	token:  #SecretRef
	scope?: string
}

// Credential providers a profile uses (SP2: pnpm; gh/forgejo/1Password-SSH follow).
#Credentials: {
	pnpm?: [...#PnpmRegistry]
}

// A pinned toolchain layered onto any environment (SP5), orthogonal to `environment`.
//   kind: which provider provisions tools — mise (version manager + task runner) or nix
//         (flakes; pinned inputs = the safe-install story).
//   run:  optional — a mise task name (kind=mise) or a nix app ref like ".#app" (kind=nix)
//         to launch INSTEAD of the profile's agent. Absent => the agent is wrapped so the
//         pinned toolchain is on PATH.
#Toolchain: {
	kind: "mise" | "nix" | "none"
	run?: string
}

#Profile: {
	agent:       #Agent
	environment: #Environment | *"sandbox"
	// Directory the boundary confines file access to. Empty (default) means the
	// directory slop was invoked from.
	workspace?: string
	network:    #Network | *"deny"
	// Env var name -> secret ref; injected into the agent's environment at launch.
	secrets?: {[string]: #SecretRef}
	// Credentials staged before launch and wiped on exit.
	credentials?: #Credentials
	// Optional pinned toolchain, provisioned into the chosen environment (SP5).
	toolchain?: #Toolchain
}

#Slop: {
	version:  int | *1
	profiles: {[string]: #Profile}
}

slop: #Slop
