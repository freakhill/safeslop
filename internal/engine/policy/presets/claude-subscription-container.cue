// Claude Code in a container using your CLAUDE SUBSCRIPTION (not an API key).
// Setup once: `claude setup-token`, store the token in 1Password, then point the op:// ref below at it.
package safeslop

safeslop: {
	version: 1
	profiles: {
		claude_box: {
			agent:       "claude"
			environment: "container"
			network:     "deny"
			secrets: {CLAUDE_CODE_OAUTH_TOKEN: "op://vault/claude-code-oauth/credential"}
		}
	}
}
