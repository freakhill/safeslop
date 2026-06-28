package safeslop

// Worked example: drop a file like this (named safeslop.cue) at a repo root and
// run `safeslop run <profile>`. Validate it first, then approve it once — `run`
// is fail-closed on an untrusted or edited policy.
//
//   safeslop validate          # check against the embedded schema
//   safeslop list              # show the profiles
//   safeslop trust             # approve this file's exact bytes (one-time)
//   safeslop run review        # launch Claude Code in a container
//
// environment is required (specs/0053 removed the sandbox tier): host | container.
// Network-bound agents (claude, pi) belong in container; host runs them unconfined.
safeslop: {
	version: 1
	profiles: {
		// Claude Code in a container, workspace-mounted, egress limited to the
		// default allowlist (github, npm, pypi, anthropic, …).
		review: {agent: "claude", environment: "container", network: "deny"}

		// A container shell for package work (pnpm/uv). network: "allow" opens
		// egress; prefer network: "deny" + egress: [...] for per-domain control.
		dev: {agent: "shell", environment: "container", network: "allow"}

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
