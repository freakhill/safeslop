package slop

// Example slop.cue for the Go engine (SP1). Drop a file like this at a repo
// root and run `slop run <profile>`.
//
//   slop validate          # check this file against the embedded schema
//   slop list              # show the profiles
//   slop run dev           # launch a sandboxed shell in the cwd
//   slop run review        # launch sandboxed Claude Code
//
slop: profiles: {
	// A sandboxed shell for package work (pnpm/uv). network: "deny" blocks
	// egress; set "allow" when you need the registry (sandbox-exec has no URL
	// allowlist — use environment: "container" for that, landing in SP3).
	dev: {agent: "shell", network: "allow"}

	// Sandboxed Claude Code, file-confined to the repo, egress denied.
	review: {agent: "claude", environment: "sandbox", network: "deny"}
}
