// Demo end-user config. Pick a preset, extend via the `extras` struct.
// Compile with:
//   slop-isolate compile library/user-config.cue --adapter <name>

package isolation

import "slop.dev/isolation/presets"

isolation: presets.#ClaudeCode & {
	// Add a private GitHub host. The compiler strips `extras` before emit.
	extras: "allow-domains": ["github.example.internal"]

	// Tell pf to fail compile if it has to fall back to resolved IPs.
	tool: pf: "domain-fallback": "fail"
}
