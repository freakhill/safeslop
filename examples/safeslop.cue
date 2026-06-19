package safeslop

// Worked example: drop a file like this (named safeslop.cue) at a repo root and
// run `safeslop run <profile>`. Validate it first, then approve it once — `run`
// is fail-closed on an untrusted or edited policy.
//
//   safeslop validate          # check against the embedded schema
//   safeslop list              # show the profiles
//   safeslop trust             # approve this file's exact bytes (one-time)
//   safeslop run review        # launch sandboxed Claude Code
//
safeslop: {
	version: 1
	profiles: {
		// Sandboxed Claude Code, file-confined to the repo, egress denied.
		review: {agent: "claude", environment: "sandbox", network: "deny"}

		// A sandboxed shell for package work (pnpm/uv). network: "allow" lets the
		// registry through; sandbox-exec has no URL allowlist — use
		// environment: "container" for real per-domain egress control.
		dev: {agent: "shell", environment: "sandbox", network: "allow"}

		// A container profile with a private-registry pnpm token and an injected
		// API key, both sourced from 1Password and wiped on exit. The secret
		// values and the staged .npmrc never persist outside the run.
		work: {
			agent:       "shell"
			environment: "container"
			network:     "allow"
			secrets: {ANTHROPIC_API_KEY: "op://Private/Anthropic/credential"}
			credentials: pnpm: [
				{host: "npm.pkg.github.com", token: "op://Private/GH Packages/token", scope: "@myorg"},
			]
		}
	}
}
