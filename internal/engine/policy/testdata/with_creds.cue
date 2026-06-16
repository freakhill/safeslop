package slop

slop: profiles: {
	work: {
		agent:   "shell"
		network: "allow"
		secrets: {
			ANTHROPIC_API_KEY: "op://dev/anthropic/key"
			FOO:               "env:FOO_SRC"
		}
		credentials: pnpm: [
			{host: "registry.npmjs.org", token: "op://dev/npm/token"},
			{host: "npm.pkg.github.com", token: "env:GH_NPM_TOKEN", scope: "@myorg"},
		]
	}
}
