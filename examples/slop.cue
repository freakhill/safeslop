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

	// A sandboxed shell with a private-registry pnpm token and an injected API
	// key, both sourced from 1Password and wiped on exit (SP2). secrets values
	// and the staged .npmrc never persist outside the run.
	work: {
		agent:   "shell"
		network: "allow" // installs need the registry
		secrets: {ANTHROPIC_API_KEY: "op://Private/Anthropic/credential"}
		credentials: pnpm: [
			{host: "npm.pkg.github.com", token: "op://Private/GH Packages/token", scope: "@myorg"},
		]
	}
}
