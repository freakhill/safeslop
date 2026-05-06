// Sample slop.cue — drop a file like this at the root of a repo so the
// `slop` runtime reads it and starts the declared agents with the right
// isolation, credentials, and cleanup on exit.
//
// Validate this file in place:
//
//   cue vet library/layer/policy/samples/slop.cue \
//           library/layer/policy/schema/...
//
// Or, in a real repo, copy it to ./slop.cue and run `slop validate`
// (Phase D).

package slop

import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"

profiles: {
	// "review" — a tightly-isolated Claude Code session in the
	// container stack, with an ephemeral RW deploy key. Cleaned up
	// automatically on exit.
	"review": schema.#Profile & {
		agent:       "claude"
		environment: "container"
		isolation:   presets.#ClaudeCode & {
			extras: "allow-domains": [
				"pypi.org",
				"registry.npmjs.org",
			]
		}
		credentials: github: "ephemeral-rw"
		"on-exit": [
			"revoke-credentials",
			"stop-container",
			"stop-proxy",
		]
	}

	// "explore" — host-side OpenCode with the bundled defaults. No
	// container, no ephemeral creds.
	"explore": schema.#Profile & {
		agent:       "opencode"
		environment: "host"
		isolation:   presets.#OpenCode
	}
}

default: "review"
