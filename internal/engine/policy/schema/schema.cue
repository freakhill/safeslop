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

#Profile: {
	agent:       #Agent
	environment: #Environment | *"sandbox"
	// Directory the boundary confines file access to. Empty (default) means the
	// directory slop was invoked from.
	workspace?: string
	network:    #Network | *"deny"
}

#Slop: {
	version:  int | *1
	profiles: {[string]: #Profile}
}

slop: #Slop
