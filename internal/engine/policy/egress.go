package policy

// AgentEgress returns the built-in extra egress allowlist domains a given agent needs
// beyond the shared base allowlist. The base already carries the common providers
// (.anthropic.com + .openrouter.ai) plus the clone/dep-install infra, so only agents
// whose provider reach goes beyond the base return entries here; everything else returns
// nil. These are unioned with the base + any per-profile `egress:` when the container
// allowlist is materialized at launch (specs/0046). A leading dot is a squid subdomain
// suffix match.
//
// pi is BYOK / multi-provider, so it carries the ZDR-clean provider set safeslop allows.
// OpenAI/xAI are a privacy hard line and deliberately absent. Hosts verified against each
// provider's API base URL (2026-06-24): .z.ai is the GLM endpoint (ai-router GLM_BASE_URL);
// .moonshot.ai is the international Kimi endpoint (the .cn endpoint 401s for intl keys).
func AgentEgress(agent string) []string {
	switch agent {
	case "pi":
		return []string{
			".pi.dev",
			".generativelanguage.googleapis.com", // Gemini
			".moonshot.ai",                       // Kimi (international)
			".z.ai",                              // GLM / z.ai
			".deepseek.com",
			".mistral.ai",
			".sakana.ai",
			".exa.ai",
		}
	default:
		return nil
	}
}

// CredsEgress returns the egress hosts a profile needs because its git credentials are staged.
// GitHub over HTTPS reaches github.com plus the CDN hosts that clones and LFS objects redirect to
// (specs/0068 FIX-b); api.github.com is intentionally excluded in P1 because API-token staging is
// P2. These are exact-host entries (no leading dot). They are unioned with AgentEgress + the
// per-profile egress: at allowlist materialization (network:deny profiles only, specs/0046).
// Forgejo rides SSH deploy keys whose egress is handled elsewhere (specs/0008/0046) and is
// unchanged here; Forgejo host:443 also waits for P2 API staging.
func CredsEgress(prof *Profile) []string {
	if prof == nil || prof.Credentials == nil || prof.Credentials.Github == nil {
		return nil
	}
	return []string{"github.com", "codeload.github.com", "objects.githubusercontent.com"}
}
